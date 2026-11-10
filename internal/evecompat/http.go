package evecompat

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/Gui-Rondon/hyrule/internal/platform/auth"
	"github.com/Gui-Rondon/hyrule/internal/platform/metrics"
)

// Options configure an API.
type Options struct {
	// Auth validates tokens; nil disables authentication.
	Auth *auth.Authenticator
	// Transactor wraps the writes; nil means no transactions.
	Transactor Transactor
	// Messages are the default error texts; nil means Werkzeug1. Use
	// Werkzeug0 for an API that really runs Werkzeug 0.11.
	Messages Messages
	// CacheControl and CacheExpires (seconds) are Eve's global
	// CACHE_CONTROL and CACHE_EXPIRES, sent by the home endpoint.
	CacheControl string
	CacheExpires int
	// HTMLForbidden answers a missing or invalid token with Werkzeug's HTML
	// 403 page, as Eve 0.7 does when 403 is not in STANDARD_ERRORS
	// (contents-api).
	HTMLForbidden bool
	// QueryTimeout limits each read (find and count); 0 means no limit.
	// When it runs out the response is 503 (D-27).
	QueryTimeout time.Duration
	// DryRun marks every response with X-Dry-Run, logs the writes and skips
	// the post-write hooks. The Transactor must roll back.
	DryRun bool
	// DryRunTimeout limits each request in dry run; 0 means no limit. The
	// mirror of ingress-nginx holds the client's next request on the
	// connection until the mirrored one ends, so a slow Go API would slow
	// the Python one; it also bounds the locks of the aborted transaction
	// and the driver's retries on write conflicts.
	DryRunTimeout time.Duration
}

// API serves a set of resources with Eve's HTTP contract.
type API struct {
	opts     Options
	services []*Service
	byName   map[string]*Service
	mux      *http.ServeMux
}

// New builds an API.
func New(opts Options) *API {
	a := &API{opts: opts, byName: map[string]*Service{}, mux: http.NewServeMux()}
	a.mux.HandleFunc("/{$}", a.home)
	a.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, r, ErrNotFound)
	})
	return a
}

// Register adds a resource and returns its Service.
func (a *API) Register(res *Resource) *Service {
	svc := NewService(res, a.opts.Transactor)
	svc.queryTimeout = a.opts.QueryTimeout
	svc.dryRun = a.opts.DryRun
	a.services = append(a.services, svc)
	a.byName[res.Name] = svc
	h := &handler{api: a, svc: svc}
	a.mux.HandleFunc("/"+res.URL, h.serveResource)
	a.mux.HandleFunc("/"+res.URL+"/{lookup}", h.serveItem)
	return svc
}

// Service returns the Service of a registered resource.
func (a *API) Service(name string) *Service { return a.byName[name] }

// ResourceURLs lists the resource routes in registration order.
func (a *API) ResourceURLs() []string {
	urls := make([]string, len(a.services))
	for i, svc := range a.services {
		urls[i] = svc.res.URL
	}
	return urls
}

// Handle adds a custom route. More specific patterns win over the resource
// routes, e.g. "POST /articles/{id}/publish".
func (a *API) Handle(pattern string, h http.Handler) { a.mux.Handle(pattern, h) }

// RequireAuth protects a custom endpoint with the API authentication.
func (a *API) RequireAuth(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r, ok := a.authorize(w, r, nil); ok {
			h.ServeHTTP(w, r)
		}
	})
}

// ServeHTTP implements http.Handler.
func (a *API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r = withoutTrailingSlash(r)
	msgs := a.opts.Messages
	if msgs == nil {
		msgs = Werkzeug1
	}
	ctx := context.WithValue(r.Context(), messagesKey{}, msgs)
	ctx = context.WithValue(ctx, argsKey{}, r.URL.Query())
	// a custom endpoint that serves through the API again (duplicate, the
	// contents POST) is one request: marked and logged once
	if !a.opts.DryRun || ctx.Value(dryRunKey{}) != nil {
		r = r.WithContext(ctx)
		// the mux sets the matched pattern on r; deferred, so a panic keeps
		// the route
		defer func() { metrics.SetRoute(ctx, r.Pattern) }()
		a.mux.ServeHTTP(w, r)
		return
	}
	w.Header().Set(HeaderDryRun, "true")
	if a.opts.DryRunTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, a.opts.DryRunTimeout)
		defer cancel()
	}
	r = r.WithContext(context.WithValue(ctx, dryRunKey{}, true))
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	start := time.Now()
	defer func() { metrics.SetRoute(ctx, r.Pattern) }()
	a.mux.ServeHTTP(rec, r)
	timedOut := errors.Is(r.Context().Err(), context.DeadlineExceeded)
	msg := "dry run write"
	if !isWrite(r.Method) {
		// a read is logged only when DryRunTimeout cut it: nothing else
		// records it (the ingress has no access log)
		if !timedOut {
			return
		}
		msg = "dry run read timed out"
	}
	// the mux sets the matched pattern on r; timed_out tells a 503 of
	// DryRunTimeout from a real one
	slog.InfoContext(r.Context(), msg, "method", r.Method, "route", r.Pattern, "path", r.URL.Path,
		"status", rec.status, "duration_ms", time.Since(start).Milliseconds(), "timed_out", timedOut)
}

// withoutTrailingSlash serves "/articles/" as "/articles", with no redirect,
// as Eve does with Flask's strict_slashes = False. Only one slash is
// removed: "/articles//" stays a 404, as in Werkzeug.
func withoutTrailingSlash(r *http.Request) *http.Request {
	p := r.URL.Path
	if len(p) < 2 || !strings.HasSuffix(p, "/") {
		return r
	}
	r2 := r.Clone(r.Context())
	r2.URL.Path = p[:len(p)-1]
	r2.URL.RawPath = strings.TrimSuffix(r.URL.RawPath, "/")
	return r2
}

// HeaderDryRun is set to "true" on every response of an API in dry run.
const HeaderDryRun = "X-Dry-Run"

type dryRunKey struct{}

// isWrite tells whether the method may change data.
func isWrite(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return true
}

// statusRecorder keeps the status written by the handler.
type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (s *statusRecorder) WriteHeader(status int) {
	if !s.wrote {
		s.status, s.wrote = status, true
	}
	s.ResponseWriter.WriteHeader(status)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	s.wrote = true
	return s.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the server's writer (Flush,
// deadlines).
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

type argsKey struct{}

// RequestArgs returns the query string of the request being served, for
// hooks that read parameters Eve's parse_request exposed (e.g. a custom
// projection key).
func RequestArgs(ctx context.Context) url.Values {
	args, _ := ctx.Value(argsKey{}).(url.Values)
	return args
}

// home reproduces Eve's root endpoint listing the resources.
func (a *API) home(w http.ResponseWriter, r *http.Request) {
	children := make([]any, 0, len(a.services))
	for _, svc := range a.services {
		children = append(children, link(svc.res.ResourceTitle, svc.res.URL))
	}
	writeCache(w, a.opts.CacheControl, a.opts.CacheExpires)
	WriteJSON(w, http.StatusOK, map[string]any{"_links": map[string]any{"child": children}})
}

// authorize returns false after writing the error response.
func (a *API) authorize(w http.ResponseWriter, r *http.Request, public []string) (*http.Request, bool) {
	authn := a.opts.Auth
	if authn == nil || !authn.Enabled() || r.Method == http.MethodOptions || slices.Contains(public, r.Method) {
		return r, true
	}
	claims, err := authn.Authenticate(r)
	if err != nil {
		if a.opts.HTMLForbidden {
			// Werkzeug's raw page carries none of Eve's headers
			w.Header().Del("Cache-Control")
			w.Header().Del("Expires")
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusForbidden)
			io.WriteString(w, "<!DOCTYPE HTML PUBLIC \"-//W3C//DTD HTML 3.2 Final//EN\">\n<title>403 Forbidden</title>\n"+
				"<h1>Forbidden</h1>\n<p>"+msgForbidden+"</p>\n")
			return r, false
		}
		WriteError(w, r, Abort(http.StatusForbidden, msgForbidden))
		return r, false
	}
	return r.WithContext(auth.WithClaims(r.Context(), claims)), true
}

type handler struct {
	api *API
	svc *Service
}

func (h *handler) res() *Resource { return h.svc.res }

func (h *handler) serveResource(w http.ResponseWriter, r *http.Request) {
	methods := h.res().ResourceMethods
	if r.Method == http.MethodOptions {
		writeAllow(w, methods)
		return
	}
	method := effectiveMethod(r)
	if method == http.MethodGet {
		h.cacheHeaders(w)
	}
	if !slices.Contains(methods, method) {
		WriteError(w, r, Abort(http.StatusMethodNotAllowed, ""))
		return
	}
	r, ok := h.api.authorize(w, r, h.res().PublicMethods)
	if !ok {
		return
	}
	switch method {
	case http.MethodGet:
		h.list(w, r)
	case http.MethodPost:
		h.post(w, r)
	default:
		WriteError(w, r, Abort(http.StatusMethodNotAllowed, ""))
	}
}

func (h *handler) serveItem(w http.ResponseWriter, r *http.Request) {
	// eve.render sets the cache directives on every GET, errors included
	if effectiveMethod(r) == http.MethodGet {
		h.cacheHeaders(w)
	}
	lookup, methods := h.itemLookup(r.PathValue("lookup"))
	if lookup == nil {
		WriteError(w, r, ErrNotFound)
		return
	}
	if r.Method == http.MethodOptions {
		// Eve's Allow for an item also lists the resource methods
		allowed := slices.Clone(methods)
		if len(methods) > 1 {
			for _, m := range h.res().ResourceMethods {
				if !slices.Contains(allowed, m) {
					allowed = append(allowed, m)
				}
			}
		}
		writeAllow(w, allowed)
		return
	}
	method := effectiveMethod(r)
	if !slices.Contains(methods, method) {
		WriteError(w, r, Abort(http.StatusMethodNotAllowed, ""))
		return
	}
	r, ok := h.api.authorize(w, r, h.res().PublicItemMethods)
	if !ok {
		return
	}
	switch method {
	case http.MethodGet:
		h.getItem(w, r, lookup)
	case http.MethodPatch, http.MethodPut:
		h.update(w, r, lookup, method == http.MethodPut)
	case http.MethodDelete:
		h.delete(w, r, lookup)
	}
}

// itemLookup maps the URL value to a filter: an ObjectID, or the additional
// lookup, which Eve registers for reads only.
func (h *handler) itemLookup(value string) (bson.M, []string) {
	if objectIDRe.MatchString(value) {
		oid, _ := bson.ObjectIDFromHex(value)
		return bson.M{"_id": oid}, h.res().ItemMethods
	}
	if al := h.res().AdditionalLookup; al != nil && al.Pattern.MatchString(value) {
		if al.Filter != nil {
			return al.Filter(value), []string{http.MethodGet}
		}
		return bson.M{al.Field: value}, []string{http.MethodGet}
	}
	return nil, nil
}

func (h *handler) list(w http.ResponseWriter, r *http.Request) {
	h.svc.writeList(w, r, nil, h.res().URL)
}

// WriteList answers a custom list route as Eve's get(resource, lookup) with
// send_response: the query string of the request (where, sort, page...)
// plus lookup, with the links built from the request path as Eve's
// resource_link does.
func (s *Service) WriteList(w http.ResponseWriter, r *http.Request, lookup bson.M) {
	writeCache(w, s.res.CacheControl, s.res.CacheExpires)
	s.writeList(w, r, lookup, strings.Trim(r.URL.Path, "/"))
}

// WriteListFallback answers a custom list route with lookup when its page
// has items and with fallback otherwise: two get(resource, lookup) calls on
// the same request, as contents-api's get_content_by_url (the exact link,
// then the slow $regex).
func (s *Service) WriteListFallback(w http.ResponseWriter, r *http.Request, lookup, fallback bson.M) {
	writeCache(w, s.res.CacheControl, s.res.CacheExpires)
	s.writeList(w, r, lookup, strings.Trim(r.URL.Path, "/"), fallback)
}

// writeList lists with each lookup, combined with the client where, until
// a page has items; the last one answers anyway.
func (s *Service) writeList(w http.ResponseWriter, r *http.Request, lookup bson.M, base string, fallbacks ...bson.M) {
	res := s.res
	parsed, err := parseListQuery(r.URL.Query(), res.Pagination, res.QueryDenylist, res.QueryObjectIDAsString)
	if err != nil {
		WriteError(w, r, Abort(http.StatusBadRequest, ""))
		return
	}
	var q *ListQuery
	var result *ListResult
	for i, lookup := range append([]bson.M{lookup}, fallbacks...) {
		lq := *parsed
		q = &lq
		if len(lookup) > 0 {
			if len(q.Filter) > 0 {
				q.Filter = bson.M{"$and": bson.A{q.Filter, lookup}}
			} else {
				q.Filter = lookup
			}
		}
		if result, err = s.List(r.Context(), q); err != nil {
			WriteError(w, r, err)
			return
		}
		if len(result.Docs) > 0 || i == len(fallbacks) {
			break
		}
	}
	items := make([]any, 0, len(result.Docs))
	var lastModified time.Time
	for _, doc := range result.Docs {
		if updated, ok := doc["_updated"].(bson.DateTime); ok && updated.Time().After(lastModified) {
			lastModified = updated.Time()
		}
		item := Render(withEpochDates(doc)).(map[string]any)
		item["_etag"] = etagOf(doc)
		item["_links"] = itemLinks(item["_links"], link(res.ItemTitle, base+"/"+idString(doc["_id"])))
		items = append(items, item)
	}
	// Eve sends the count of the whole query and the newest _updated of the page
	switch {
	case res.Pagination.Disabled:
		w.Header().Set("X-Total-Count", strconv.Itoa(len(items)))
	case result.Total >= 0:
		w.Header().Set("X-Total-Count", strconv.FormatInt(result.Total, 10))
	}
	if !lastModified.IsZero() {
		w.Header().Set("Last-Modified", lastModified.UTC().Format(DateFormat))
	}
	body := map[string]any{"_items": items}
	if res.Pagination.Disabled {
		body["_links"] = s.unpaginatedLinks(q, len(items), base)
	} else {
		meta := map[string]any{"page": q.Page, "max_results": q.MaxResults}
		if result.Total >= 0 {
			meta["total"] = result.Total
		}
		body["_links"] = s.paginationLinks(q, result.Total, base)
		body["_meta"] = meta
	}
	WriteJSON(w, http.StatusOK, body)
}

// paginationLinks reproduces eve.methods.get._pagination_links. total is -1
// when the count is skipped (OPTIMIZE_PAGINATION_FOR_SPEED).
func (s *Service) paginationLinks(q *ListQuery, total int64, base string) map[string]any {
	res := s.res
	self := base
	if total > 0 {
		self += q.queryString(q.Page, res.Pagination.Default)
	}
	links := map[string]any{
		"parent": link("home", "/"),
		"self":   link(res.ResourceTitle, self),
	}
	if int64(q.Page*q.MaxResults) < total || total < 0 {
		links["next"] = link("next page", base+q.queryString(q.Page+1, res.Pagination.Default))
		if total > 0 {
			last := int(math.Ceil(float64(total) / float64(q.MaxResults)))
			links["last"] = link("last page", base+q.queryString(last, res.Pagination.Default))
		}
	}
	if q.Page > 1 {
		links["prev"] = link("previous page", base+q.queryString(q.Page-1, res.Pagination.Default))
	}
	return links
}

// unpaginatedLinks are the links with PAGINATION = False: the query is
// echoed (max_results=0 included, since it differs from the default).
func (s *Service) unpaginatedLinks(q *ListQuery, count int, base string) map[string]any {
	res := s.res
	self := base
	if count > 0 {
		self += q.queryString(1, res.Pagination.Default)
	}
	return map[string]any{
		"parent": link("home", "/"),
		"self":   link(res.ResourceTitle, self),
	}
}

func (h *handler) getItem(w http.ResponseWriter, r *http.Request, lookup bson.M) {
	base := h.res().URL
	if al := h.res().AdditionalLookup; al != nil && al.PathLinks && !objectIDRe.MatchString(r.PathValue("lookup")) {
		base = strings.Trim(r.URL.Path, "/")
	}
	h.svc.writeFound(w, r, lookup, base)
}

// WriteFound answers a custom item route as Eve's getitem(resource, lookup)
// with send_response: 404 when nothing matches, the cache headers on GET and
// HEAD (render.py), ETag and Last-Modified, 304 on If-None-Match, and the
// links built from the request path, also when the route is a POST.
func (s *Service) WriteFound(w http.ResponseWriter, r *http.Request, lookup bson.M) {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		writeCache(w, s.res.CacheControl, s.res.CacheExpires)
	}
	s.writeFound(w, r, lookup, strings.Trim(r.URL.Path, "/"))
}

func (s *Service) writeFound(w http.ResponseWriter, r *http.Request, lookup bson.M, base string) {
	// getitem parses the projection of the request; its pre_event fires
	// on_pre_<method>, so the OnPreGet hooks only run on a GET
	q := &ListQuery{}
	if v := r.URL.Query().Get("projection"); v != "" {
		p, err := parseProjection(v)
		if err != nil {
			WriteError(w, r, err)
			return
		}
		q.Projection = p
	}
	if r.Method == http.MethodGet {
		for _, hook := range s.res.Hooks.OnPreGet {
			if err := hook(r.Context(), q); err != nil {
				WriteError(w, r, err)
				return
			}
		}
	}
	doc, err := s.getIncludingDeleted(r.Context(), lookup, q.Projection)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	etag := etagOf(doc)
	// the 304 repeats the ETag, as in production
	w.Header().Set("ETag", `"`+etag+`"`)
	if match := r.Header.Get("If-None-Match"); match != "" && trimEtag(match) == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if updated, ok := withEpochDates(doc)["_updated"].(bson.DateTime); ok {
		if ims, err := http.ParseTime(r.Header.Get("If-Modified-Since")); err == nil && !updated.Time().After(ims) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Last-Modified", updated.Time().UTC().Format(DateFormat))
	}
	if s.res.SoftDelete && doc["_deleted"] == true {
		// getitem: a soft deleted document answers 404 with its content, the
		// self link and ("ERR",) as _status
		body := Render(withEpochDates(doc)).(map[string]any)
		body["_etag"] = etag
		body["_links"] = map[string]any{"self": link(s.res.ItemTitle, base+"/"+idString(doc["_id"]))}
		body["_status"] = []any{"ERR"}
		body["_error"] = map[string]any{"code": http.StatusNotFound, "message": "The requested URL was not found on this server."}
		WriteJSON(w, http.StatusNotFound, body)
		return
	}
	WriteJSON(w, http.StatusOK, s.itemBody(doc, r.URL.Query(), base))
}

func (h *handler) cacheHeaders(w http.ResponseWriter) {
	writeCache(w, h.res().CacheControl, h.res().CacheExpires)
}

func writeCache(w http.ResponseWriter, control string, expires int) {
	if control != "" {
		w.Header().Set("Cache-Control", control)
	}
	if expires > 0 {
		w.Header().Set("Expires", time.Now().UTC().Add(time.Duration(expires)*time.Second).Format(http.TimeFormat))
	}
}

func (h *handler) post(w http.ResponseWriter, r *http.Request) {
	docs, isList, err := readBody(r, true)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	stored, err := h.svc.Insert(r.Context(), docs, WriteOptions{})
	var insertErr *InsertError
	if errors.As(err, &insertErr) {
		writeInsertionFailure(w, insertErr, isList)
		return
	}
	if err != nil {
		WriteError(w, r, err)
		return
	}
	if !isList {
		WriteJSON(w, http.StatusCreated, h.svc.WriteResult(stored[0]))
		return
	}
	items := make([]any, len(stored))
	for i, doc := range stored {
		items[i] = h.svc.WriteResult(doc)
	}
	WriteJSON(w, http.StatusCreated, map[string]any{"_status": "OK", "_items": items})
}

func (h *handler) update(w http.ResponseWriter, r *http.Request, lookup bson.M, replace bool) {
	opts := WriteOptions{IfMatch: r.Header.Get("If-Match")}
	// Eve answers 404 and 428/412 before reading the body
	original, err := h.svc.Find(r.Context(), lookup)
	if err == nil {
		err = h.svc.checkEtag(original, opts)
	}
	if err != nil {
		WriteError(w, r, err)
		return
	}
	docs, _, err := readBody(r, false)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	var doc Doc
	if replace {
		doc, err = h.svc.Replace(r.Context(), lookup, docs[0], opts)
	} else {
		doc, err = h.svc.Patch(r.Context(), lookup, docs[0], opts)
	}
	if err != nil {
		WriteError(w, r, err)
		return
	}
	w.Header().Set("ETag", `"`+doc["_etag"].(string)+`"`)
	WriteJSON(w, http.StatusOK, h.svc.WriteResult(doc))
}

func (h *handler) delete(w http.ResponseWriter, r *http.Request, lookup bson.M) {
	if err := h.svc.Delete(r.Context(), lookup, WriteOptions{IfMatch: r.Header.Get("If-Match")}); err != nil {
		WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// WriteItem answers as GET /<url>/<id> (Eve's send_response of getitem):
// ETag and Last-Modified headers and the item body; the cache headers only
// for GET and HEAD, as Eve's _prepare_response.
func (s *Service) WriteItem(w http.ResponseWriter, r *http.Request, doc Doc) {
	if m := effectiveMethod(r); m == http.MethodGet || m == http.MethodHead {
		writeCache(w, s.res.CacheControl, s.res.CacheExpires)
	}
	w.Header().Set("ETag", `"`+etagOf(doc)+`"`)
	if updated, ok := withEpochDates(doc)["_updated"].(bson.DateTime); ok {
		w.Header().Set("Last-Modified", updated.Time().UTC().Format(DateFormat))
	}
	WriteJSON(w, http.StatusOK, s.ItemBody(doc))
}

// ItemBody is the body of GET /<url>/<id>.
func (s *Service) ItemBody(doc Doc) map[string]any {
	return s.itemBody(doc, nil, s.res.URL)
}

// itemBody builds the item response; the collection link echoes the query
// as Eve's _pagination_links does (with PAGINATION = False it is
// "?max_results=0"). base is the resource URL, or the request path of a
// custom route.
func (s *Service) itemBody(doc Doc, args url.Values, base string) map[string]any {
	self := base + "/" + idString(doc["_id"])
	collection := base
	q, err := ParseListQuery(args, s.res.Pagination, nil)
	if err == nil {
		collection += q.queryString(q.Page, s.res.Pagination.Default)
	}
	item := Render(withEpochDates(doc)).(map[string]any)
	item["_etag"] = etagOf(doc)
	links := map[string]any{
		"self":       link(s.res.ItemTitle, self),
		"parent":     link("home", "/"),
		"collection": link(s.res.ResourceTitle, collection),
	}
	if s.res.ItemNextLink && err == nil {
		next := self
		if s.res.ItemNextFromCollection {
			next = base
		}
		links["next"] = link("next page", next+q.queryString(2, s.res.Pagination.Default))
	}
	item["_links"] = links
	return item
}

// itemLinks are the _links of a list item, as build_response_document: a
// _links stored in the document (a client that sent back a GET body, with
// allow_unknown) is kept, and self is added only when it lacks one.
func itemLinks(stored any, self map[string]any) map[string]any {
	links, ok := stored.(map[string]any)
	if !ok {
		return map[string]any{"self": self}
	}
	if _, ok := links["self"]; !ok {
		links["self"] = self
	}
	return links
}

// WriteResult is the body Eve returns for a write with BANDWIDTH_SAVER on:
// the auto_fields, with _deleted for soft delete.
func (s *Service) WriteResult(doc Doc) map[string]any {
	out := map[string]any{
		"_id":      idString(doc["_id"]),
		"_created": Render(doc["_created"]),
		"_updated": Render(doc["_updated"]),
		"_etag":    doc["_etag"],
		"_links":   map[string]any{"self": s.selfLink(doc["_id"])},
		"_status":  "OK",
	}
	if s.res.SoftDelete {
		out["_deleted"] = doc["_deleted"] == true
	}
	return out
}

func (s *Service) selfLink(id any) map[string]any {
	return link(s.res.ItemTitle, s.res.URL+"/"+idString(id))
}

// WriteJSON writes body as JSON.
func WriteJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(body); err != nil {
		slog.Error("write response", "error", err)
	}
}

// WriteError writes the Eve response for an error returned by Service or a
// hook; unknown errors become 500 and are logged.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	var (
		e          *Error
		validation *ValidationError
		precond    *PreconditionFailedError
	)
	switch {
	case errors.As(err, &validation):
		// PATCH and PUT answer 422 without _error
		WriteJSON(w, http.StatusUnprocessableEntity, map[string]any{"_status": "ERR", "_issues": validation.Issues})
	case errors.As(err, &precond):
		body := errorBody(http.StatusPreconditionFailed, msgEtagMismatch)
		// a client can use this to resolve editing conflicts: who last
		// changed the document it was trying to write
		if user, ok := precond.Current["_updated_user"]; ok {
			body["_updated_user"] = user
		}
		WriteJSON(w, http.StatusPreconditionFailed, body)
	case errors.As(err, &e):
		msg := e.Message
		if msg == "" {
			msg = messagesFrom(r.Context()).message(e.Status)
		}
		WriteJSON(w, e.Status, errorBody(e.Status, msg))
	default:
		slog.ErrorContext(r.Context(), "request failed", "method", r.Method, "path", r.URL.Path, "error", err)
		WriteJSON(w, http.StatusInternalServerError, errorBody(http.StatusInternalServerError,
			messagesFrom(r.Context()).message(http.StatusInternalServerError)))
	}
}

func errorBody(status int, message string) map[string]any {
	return map[string]any{
		"_status": "ERR",
		"_error":  map[string]any{"code": status, "message": message},
	}
}

func writeInsertionFailure(w http.ResponseWriter, e *InsertError, isList bool) {
	body := errorBody(http.StatusUnprocessableEntity,
		"Insertion failure: "+strconv.Itoa(e.Failed)+" document(s) contain(s) error(s)")
	if isList {
		items := make([]any, len(e.Results))
		for i, issues := range e.Results {
			if issues == nil {
				items[i] = map[string]any{"_status": "OK"}
			} else {
				items[i] = map[string]any{"_status": "ERR", "_issues": issues}
			}
		}
		body["_items"] = items
	} else {
		body["_issues"] = e.Results[0]
	}
	WriteJSON(w, http.StatusUnprocessableEntity, body)
}

// ReadBody decodes a JSON object; custom endpoints use it.
func ReadBody(r *http.Request) (Doc, error) {
	docs, _, err := readBody(r, false)
	if err != nil {
		return nil, err
	}
	return docs[0], nil
}

// readBody decodes a JSON object or, when allowList is true, a non empty list
// of objects.
func readBody(r *http.Request, allowList bool) ([]Doc, bool, error) {
	bad := Abort(http.StatusBadRequest, "")
	// Eve's payload() also takes forms: articles-saver PATCHes with
	// requests.patch(url, dict), a form-urlencoded body. multipart/form-data
	// is left out: no client sends it, no resource takes files, and parsing
	// it on the request clones of ServeHTTP would leave its temporary files
	// behind (the server only removes those of the original request).
	if mediaType(r) == "application/x-www-form-urlencoded" {
		// ParseForm reads at most 10 MB of form body (net/http's limit);
		// more is a 400
		if err := r.ParseForm(); err != nil {
			return nil, false, bad
		}
		return formDoc(r.PostForm, "No form-urlencoded data supplied")
	}
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, false, bad
	}
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, false, bad
	}
	switch t := raw.(type) {
	case map[string]any:
		return []Doc{t}, false, nil
	case []any:
		if !allowList || len(t) == 0 {
			return nil, false, bad
		}
		docs := make([]Doc, 0, len(t))
		for _, item := range t {
			m, ok := item.(map[string]any)
			if !ok {
				return nil, false, bad
			}
			docs = append(docs, m)
		}
		return docs, true, nil
	}
	return nil, false, bad
}

// mediaType is the Content-Type without parameters, as Eve splits it.
func mediaType(r *http.Request) string {
	t, _, _ := strings.Cut(r.Header.Get("Content-Type"), ";")
	return strings.ToLower(strings.TrimSpace(t))
}

// formDoc turns form fields into a document with the first value of each
// key, as Eve's multidict_to_dict with AUTO_COLLAPSE_MULTI_KEYS off. The
// values stay strings, as in Eve; the schema validation sees them as sent.
func formDoc(values map[string][]string, empty string) ([]Doc, bool, error) {
	if len(values) == 0 {
		return nil, false, Abort(http.StatusBadRequest, empty)
	}
	doc := make(Doc, len(values))
	for k, v := range values {
		if len(v) > 0 {
			doc[k] = v[0]
		}
	}
	return []Doc{doc}, false, nil
}

func effectiveMethod(r *http.Request) string {
	if r.Method == http.MethodHead {
		return http.MethodGet
	}
	return r.Method
}

func writeAllow(w http.ResponseWriter, methods []string) {
	all := slices.Clone(methods)
	all = append(all, http.MethodOptions)
	if slices.Contains(methods, http.MethodGet) {
		all = append(all, http.MethodHead)
	}
	w.Header().Set("Allow", strings.Join(all, ", "))
	// Flask's automatic OPTIONS response
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
}

func idString(id any) string {
	if oid, ok := id.(bson.ObjectID); ok {
		return oid.Hex()
	}
	if s, ok := Render(id).(string); ok {
		return s
	}
	b, _ := json.Marshal(Render(id))
	return string(b)
}

func trimEtag(s string) string {
	s = strings.TrimPrefix(strings.TrimSpace(s), "W/")
	return strings.Trim(s, `"`)
}
