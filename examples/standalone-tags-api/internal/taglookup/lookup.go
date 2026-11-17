// Package taglookup is an opt-in, application-local read pilot. It does not
// automatically expose an endpoint or trust claims stored in context. Optional
// JWTIdentity and HTTP helpers supply verified identity and local transport.
// Dependencies and resource definitions must be fixed before concurrent use.
package taglookup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/Gui-Rondon/hyrule/rest"
	"github.com/google/uuid"
)

const Name = "tags.lookup"
const Version = "1"
const Permission = "tags:read"
const MaxInput = 1024
const MaxOutput = 16 * 1024
const minSlugLength = 1
const maxSlugLength = 128
const invalidSlugPattern = `[^A-Za-z0-9_-]`
const executionTimeout = 2 * time.Second
const auditTimeout = time.Second

var (
	ErrIdentity    = errors.New("unauthenticated")
	ErrForbidden   = errors.New("forbidden")
	ErrInput       = errors.New("invalid input")
	ErrUnavailable = errors.New("lookup unavailable")
	ErrOutput      = errors.New("invalid lookup output")
	ErrDeadline    = errors.New("deadline exceeded")
	ErrCanceled    = errors.New("canceled")
	ErrAudit       = errors.New("audit unavailable")
	invalidSlug    = regexp.MustCompile(invalidSlugPattern)
)

// Principal must come from a verified application boundary, never tool input.
type Principal struct{ Subject string }
type Action struct{ Tool, Version, Permission, Scope string }
type Record struct {
	Subject, CorrelationID, Tool, Version, Scope, Outcome string
	Authorized                                            bool
	Duration                                              time.Duration
}
type Dependencies struct {
	Service *rest.Service
	Scope   string
	// Verify must reject disabled authentication and verify issuer, audience,
	// expiration and subject. Context claims alone are not evidence of identity.
	Verify    func(context.Context) (Principal, error)
	Authorize func(context.Context, Principal, Action) error
	// Audit must honor its context and be safe for concurrent calls. Only
	// sanitized metadata is supplied, never arguments, credentials or errors.
	Audit func(context.Context, Record) error
}
type Executor struct{ deps Dependencies }
type Tool struct {
	Name, Version string
	ReadOnly      bool
}

func New(d Dependencies) (*Executor, error) {
	if d.Service == nil || strings.TrimSpace(d.Scope) == "" || d.Verify == nil || d.Authorize == nil || d.Audit == nil {
		return nil, errors.New("taglookup: service, scope, verifier, policy and audit are required")
	}
	r := d.Service.Resource()
	if r.Name != "tags" || !slices.Contains(r.ResourceMethods, "GET") || len(r.Hooks.OnPreGet) > 0 {
		return nil, errors.New("taglookup: requires readable tags without query-mutating pre-read hooks")
	}
	for _, name := range []string{"name", "slug"} {
		if r.Schema[name].Type != rest.TypeString {
			return nil, errors.New("taglookup: requires string name and slug")
		}
	}
	if r.Schema["enabled"].Type != rest.TypeBoolean || !r.Schema["slug"].Unique {
		return nil, errors.New("taglookup: requires boolean enabled and unique slug")
	}
	return &Executor{deps: d}, nil
}

// Describe applies the same policy as direct execution; it never reads storage.
func (e *Executor) Describe(ctx context.Context) (tool Tool, err error) {
	ctx, cancel := context.WithTimeout(ctx, executionTimeout)
	defer cancel()
	record := e.record()
	started := time.Now()
	defer func() {
		err = e.finish(ctx, record, started, err)
		if err != nil {
			tool = Tool{}
		}
	}()
	if err = e.authorize(ctx, &record); err != nil {
		return Tool{}, err
	}
	return Tool{Name, Version, true}, nil
}

// Execute accepts one bounded JSON object. Readers must cooperate with context
// cancellation; a transport must also configure its own request/read timeout.
func (e *Executor) Execute(ctx context.Context, input io.Reader) (output []byte, err error) {
	ctx, cancel := context.WithTimeout(ctx, executionTimeout)
	defer cancel()
	record := e.record()
	started := time.Now()
	defer func() {
		err = e.finish(ctx, record, started, err)
		if err != nil {
			output = nil
		}
	}()
	if err = e.authorize(ctx, &record); err != nil {
		return nil, err
	}
	slug, err := parse(input)
	if err != nil {
		return nil, err
	}
	if err = contextError(ctx); err != nil {
		return nil, err
	}
	r := e.deps.Service.Resource()
	raw, _ := json.Marshal(map[string]any{"slug": slug, "enabled": true})
	filter, err := r.Adapter.ParseWhere(string(raw), r)
	if err != nil {
		return nil, ErrUnavailable
	}
	projection, err := r.Adapter.ParseProjection(`{"name":1,"slug":1,"enabled":1}`)
	if err != nil {
		return nil, ErrUnavailable
	}
	result, err := e.deps.Service.List(ctx, &rest.ListQuery{Filter: filter, Projection: projection, Page: 1, MaxResults: 1})
	if ctxErr := contextError(ctx); ctxErr != nil {
		return nil, ctxErr
	}
	if err != nil {
		return nil, ErrUnavailable
	}
	if result == nil || len(result.Docs) > 1 {
		return nil, ErrOutput
	}
	response := struct {
		Found bool `json:"found"`
		Tag   *tag `json:"tag,omitempty"`
	}{Found: len(result.Docs) == 1}
	if response.Found {
		doc := result.Docs[0]
		name, nameOK := doc["name"].(string)
		gotSlug, slugOK := doc["slug"].(string)
		enabled, enabledOK := doc["enabled"].(bool)
		if !nameOK || !slugOK || !enabledOK || gotSlug != slug || !enabled {
			return nil, ErrOutput
		}
		// A bound on raw strings prevents allocating a huge JSON encoding of a hook's
		// output; the encoded bound below also accounts for JSON escaping overhead.
		if len(name) > MaxOutput {
			return nil, ErrOutput
		}
		response.Tag = &tag{name, gotSlug, enabled}
	}
	output, err = json.Marshal(response)
	if err != nil || len(output) > MaxOutput {
		return nil, ErrOutput
	}
	return output, nil
}

type tag struct {
	Name    string `json:"name"`
	Slug    string `json:"slug"`
	Enabled bool   `json:"enabled"`
}

func parse(input io.Reader) (string, error) {
	if input == nil {
		return "", ErrInput
	}
	data, err := io.ReadAll(io.LimitReader(input, MaxInput+1))
	if err != nil || len(data) > MaxInput {
		return "", ErrInput
	}
	d := json.NewDecoder(bytes.NewReader(data))
	first, err := d.Token()
	if err != nil || first != json.Delim('{') {
		return "", ErrInput
	}
	seen := false
	slug := ""
	for d.More() {
		key, err := d.Token()
		if err != nil || key != "slug" || seen {
			return "", ErrInput
		}
		seen = true
		// Decode into an interface so JSON null cannot masquerade as an empty string.
		var value any
		if d.Decode(&value) != nil {
			return "", ErrInput
		}
		var ok bool
		slug, ok = value.(string)
		if !ok || len(slug) < minSlugLength || len(slug) > maxSlugLength || invalidSlug.MatchString(slug) {
			return "", ErrInput
		}
	}
	if _, err = d.Token(); err != nil || !seen {
		return "", ErrInput
	}
	if _, err = d.Token(); err != io.EOF {
		return "", ErrInput
	}
	return slug, nil
}
func (e *Executor) record() Record {
	return Record{CorrelationID: uuid.NewString(), Tool: Name, Version: Version, Scope: e.deps.Scope}
}
func (e *Executor) authorize(ctx context.Context, r *Record) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	p, err := e.deps.Verify(ctx)
	if ctxErr := contextError(ctx); ctxErr != nil {
		return ctxErr
	}
	if err != nil || strings.TrimSpace(p.Subject) == "" || len(p.Subject) > 256 {
		return ErrIdentity
	}
	r.Subject = p.Subject
	if err = e.deps.Authorize(ctx, p, Action{Name, Version, Permission, e.deps.Scope}); err != nil {
		if ctxErr := contextError(ctx); ctxErr != nil {
			return ctxErr
		}
		return ErrForbidden
	}
	if err = contextError(ctx); err != nil {
		return err
	}
	r.Authorized = true
	return nil
}
func (e *Executor) finish(ctx context.Context, r Record, started time.Time, err error) error {
	if err == nil {
		err = contextError(ctx)
	}
	r.Duration = time.Since(started)
	r.Outcome = "ok"
	if err != nil {
		r.Outcome = err.Error()
	}
	// Audit survives canceled queries with its own budget. A fresh context also
	// prevents credentials/claims from being forwarded to the audit dependency.
	auditCtx, cancel := context.WithTimeout(context.Background(), auditTimeout)
	defer cancel()
	auditErr := e.deps.Audit(auditCtx, r)
	if auditErr != nil || auditCtx.Err() != nil {
		return errors.Join(err, ErrAudit)
	}
	if err == nil {
		err = contextError(ctx)
	}
	return err
}
func contextError(ctx context.Context) error {
	switch ctx.Err() {
	case context.DeadlineExceeded:
		return ErrDeadline
	case context.Canceled:
		return ErrCanceled
	}
	return nil
}
