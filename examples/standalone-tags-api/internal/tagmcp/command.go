package tagmcp

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"example.com/standalone-tags-api/internal/taglookup"
	"github.com/Gui-Rondon/hyrule/declarative"
	"github.com/Gui-Rondon/hyrule/platform/sqlite"
	"github.com/Gui-Rondon/hyrule/rest"
	"github.com/Gui-Rondon/hyrule/restsqlite"
)

// RequiredEnvironment returns a fresh list of mandatory launcher settings.
func RequiredEnvironment() []string {
	return []string{
		"TAGS_MCP_RESOURCES_FILE", "TAGS_MCP_DATA_FILE", "TAGS_MCP_AUDIT_FILE",
		"TAGS_MCP_PUBLIC_KEY_FILE", "TAGS_MCP_KEY_ID", "TAGS_MCP_ISSUER",
		"TAGS_MCP_AUDIENCE", "TAGS_MCP_SCOPE", "TAGS_MCP_TOKEN_FILE",
	}
}

type commandConfig struct {
	resources, data, audit, token string
	identity                      taglookup.JWTFileConfig
}

func commandConfiguration(getenv func(string) string) (commandConfig, error) {
	if getenv == nil {
		return commandConfig{}, errors.New("tags-mcp: environment reader is required")
	}
	values := map[string]string{}
	for _, name := range RequiredEnvironment() {
		value := getenv(name)
		if strings.TrimSpace(value) == "" {
			return commandConfig{}, errors.New("tags-mcp: missing " + name)
		}
		values[name] = value
	}
	c := commandConfig{resources: values["TAGS_MCP_RESOURCES_FILE"], data: values["TAGS_MCP_DATA_FILE"], audit: values["TAGS_MCP_AUDIT_FILE"], token: values["TAGS_MCP_TOKEN_FILE"], identity: taglookup.JWTFileConfig{
		Issuer: values["TAGS_MCP_ISSUER"], Audience: values["TAGS_MCP_AUDIENCE"], Scope: values["TAGS_MCP_SCOPE"], KeyID: values["TAGS_MCP_KEY_ID"], PublicKeyFile: values["TAGS_MCP_PUBLIC_KEY_FILE"],
	}}
	for _, path := range []string{c.resources, c.data, c.token, c.identity.PublicKeyFile} {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return commandConfig{}, errors.New("tags-mcp: configured input files must exist and be regular files")
		}
	}
	auditAbs, err := filepath.Abs(c.audit)
	if err != nil {
		return commandConfig{}, errors.New("tags-mcp: invalid audit path")
	}
	c.audit = auditAbs
	auditInfo, auditErr := os.Stat(c.audit)
	if auditErr != nil && !os.IsNotExist(auditErr) {
		return commandConfig{}, errors.New("tags-mcp: cannot inspect audit file")
	}
	for _, path := range []string{c.resources, c.data, c.token, c.identity.PublicKeyFile} {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return commandConfig{}, errors.New("tags-mcp: invalid input path")
		}
		info, err := os.Stat(path)
		if err != nil {
			return commandConfig{}, errors.New("tags-mcp: cannot inspect input file")
		}
		if absolute == c.audit || (auditInfo != nil && os.SameFile(info, auditInfo)) {
			return commandConfig{}, errors.New("tags-mcp: audit must be separate from data, schema and credential files")
		}
	}
	return c, nil
}

// RunCommand composes the opt-in executable from explicit environment settings.
// It reads existing SQLite data only; it never creates a resource database,
// seeds records or changes global environment/configuration. Audit owns its file.
func RunCommand(ctx context.Context, getenv func(string) string) (err error) {
	c, err := commandConfiguration(getenv)
	if err != nil {
		return err
	}
	startup, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	identity, err := taglookup.LoadJWTIdentity(c.identity)
	if err != nil {
		return errors.New("tags-mcp: invalid identity configuration or public key")
	}
	token, err := readCommandToken(startup, c.token)
	if err != nil {
		return errors.New("tags-mcp: cannot load owner token")
	}
	verified := taglookup.WithToken(startup, token)
	principal, err := identity.Verify(verified)
	if err != nil {
		return errors.New("tags-mcp: owner token failed identity validation")
	}
	if err = identity.Authorize(verified, principal, taglookup.Action{Tool: taglookup.Name, Version: taglookup.Version, Permission: taglookup.Permission, Scope: c.identity.Scope}); err != nil {
		return errors.New("tags-mcp: owner token is not authorized for the configured resource")
	}
	service, db, err := openCommandResource(startup, c.resources, c.data)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			err = errors.Join(err, errors.New("tags-mcp: closing data connection failed"))
		}
	}()
	audit, err := taglookup.OpenSQLiteAudit(startup, c.audit)
	if err != nil {
		return errors.New("tags-mcp: cannot provision persistent audit")
	}
	defer func() {
		if closeErr := audit.Close(); closeErr != nil {
			err = errors.Join(err, errors.New("tags-mcp: closing audit connection failed"))
		}
	}()
	cancel()
	err = RunStdio(ctx, Config{Service: service, Identity: identity, Scope: c.identity.Scope, Audit: audit.Write, Token: func(request context.Context) (string, error) { return readCommandToken(request, c.token) }})
	if ctx.Err() != nil && errors.Is(err, context.Canceled) {
		return nil
	}
	if err != nil {
		return errors.New("tags-mcp: MCP transport failed")
	}
	return nil
}
func readCommandToken(ctx context.Context, path string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	data, err := readCommandFile(path, taglookup.MaxToken+2)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(data))
	if token == "" || len(token) > taglookup.MaxToken {
		return "", taglookup.ErrIdentity
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	return token, nil
}
func readCommandFile(path string, limit int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("tags-mcp: cannot read configured file")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("tags-mcp: expected a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, int64(limit+1)))
	if err != nil || len(data) > limit {
		return nil, errors.New("tags-mcp: invalid configured file size")
	}
	return data, nil
}

type commandSQLiteBackend struct{ db *sql.DB }

func (b commandSQLiteBackend) Attach(ctx context.Context, res *rest.Resource, source string) error {
	adapter, err := restsqlite.NewAdapter(ctx, b.db, source, res)
	if err != nil {
		return err
	}
	res.Adapter = adapter
	return nil
}
func openCommandResource(ctx context.Context, schemaPath, dataPath string) (*rest.Service, *sql.DB, error) {
	raw, err := readCommandFile(schemaPath, 64*1024)
	if err != nil {
		return nil, nil, errors.New("tags-mcp: cannot read resource schema")
	}
	file, err := declarative.Parse(strings.NewReader(string(raw)))
	if err != nil || len(file.Resources) != 1 {
		return nil, nil, errors.New("tags-mcp: expected one valid tags resource")
	}
	resource := file.Resources[0]
	if resource.Name != "tags" || resource.Collection != "tags" || (resource.Backend != "" && resource.Backend != "sqlite") {
		return nil, nil, errors.New("tags-mcp: only the local tags SQLite resource is supported")
	}
	// The executable explicitly selects SQLite for the backend-neutral example
	// declaration. An explicitly conflicting backend is rejected above.
	resource.Backend = "sqlite"
	file.Resources[0] = resource
	absolute, err := filepath.Abs(dataPath)
	if err != nil {
		return nil, nil, errors.New("tags-mcp: invalid data path")
	}
	path := filepath.ToSlash(absolute)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	uri := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}
	db, err := sqlite.Open(ctx, uri.String())
	if err != nil {
		return nil, nil, errors.New("tags-mcp: cannot open existing read-only SQLite data")
	}
	built, err := file.Build(ctx, declarative.Registry{"sqlite": commandSQLiteBackend{db}})
	if err != nil {
		db.Close()
		return nil, nil, errors.New("tags-mcp: data schema/indexes must already match the tags resource")
	}
	service := rest.New(rest.Options{Transactor: sqlite.Transactor{DB: db}}).Register(built[0])
	return service, db, nil
}
