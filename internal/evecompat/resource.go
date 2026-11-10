// Package evecompat serves Mongo collections with the HTTP contract of Python
// Eve, so the gateways that call the Eve APIs keep working unchanged:
// where/sort/projection/page/max_results, the _items/_links/_meta envelope,
// If-Match/ETag, Eve's error envelope and Cerberus validation messages.
//
// The package has two layers:
//
//   - Service: the resource operations without HTTP (Eve's getitem,
//     post_internal, patch_internal...). Custom endpoints and tests use it.
//   - API: the HTTP handlers that translate requests and responses.
//
// Eve's event hooks are the Hooks of a Resource. Pre-write hooks run in the
// same Mongo transaction as the write, which is where an outbox event must be
// recorded.
package evecompat

import (
	"context"
	"net/http"
	"regexp"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// Resource is an entry of Eve's DOMAIN.
type Resource struct {
	// Name identifies the resource (the DOMAIN key).
	Name string
	// URL is the route, Eve's "url". Defaults to Name.
	URL string
	// ResourceTitle is the title of the collection links. Defaults to URL.
	ResourceTitle string
	// ItemTitle is the title of the document links. Defaults to
	// ResourceTitle without trailing "s", as Eve does.
	ItemTitle string

	Store  Store
	Schema Schema
	// AllowUnknown accepts fields outside Schema (Eve's allow_unknown).
	AllowUnknown bool

	Datasource Datasource

	// AdditionalLookup enables GET /<url>/<value> by another field.
	AdditionalLookup *AdditionalLookup

	// Allowed methods, Eve's RESOURCE_METHODS and ITEM_METHODS.
	ResourceMethods []string
	ItemMethods     []string
	// Methods that do not require authentication, Eve's PUBLIC_METHODS and
	// PUBLIC_ITEM_METHODS.
	PublicMethods     []string
	PublicItemMethods []string

	Pagination Pagination
	// SoftDelete marks documents with _deleted instead of removing them.
	SoftDelete bool
	// QueryDenylist is the list of operators rejected in where, Eve's
	// MONGO_QUERY_BLACKLIST. Nil means DefaultQueryDenylist.
	QueryDenylist []string

	// CacheControl and CacheExpires (seconds) set the Cache-Control and
	// Expires headers of successful GETs (Eve's CACHE_CONTROL/CACHE_EXPIRES).
	CacheControl string
	CacheExpires int

	// QueryObjectIDAsString keeps 24 hex strings of where as strings (Eve's
	// query_objectid_as_string), for ids stored as strings.
	QueryObjectIDAsString bool
	// ItemNextLink adds {"next": "<item>?page=2"} to item responses, as Eve
	// 0.7 does with OPTIMIZE_PAGINATION_FOR_SPEED.
	ItemNextLink bool
	// ItemNextFromCollection points that next link at the collection
	// ("<url>?page=2"), as Eve 2.3 does.
	ItemNextFromCollection bool
	// Validate runs after the schema rules, as the rules of a custom Eve
	// Validator. original is nil on POST; partial is true on PATCH. The
	// returned issues are merged with the schema's.
	Validate func(ctx context.Context, doc, original Doc, partial bool) (Issues, error)
	// CompatibleEtags names a document field with older etags that If-Match
	// still accepts: the hooks put there the etags replaced by writes that
	// did not touch what the client edits (Eve has no such field).
	CompatibleEtags string

	Hooks Hooks
}

// PublicGET is the PUBLIC_METHODS/PUBLIC_ITEM_METHODS of every Eve API.
var PublicGET = []string{http.MethodGet}

// Datasource mirrors Eve's datasource settings.
type Datasource struct {
	// Projection is the default projection (fields returned when the client
	// does not send one).
	Projection bson.M
	// Filter is always applied to reads and writes.
	Filter bson.M
	// DefaultSort is used when the client does not send sort.
	DefaultSort bson.D
}

// AdditionalLookup is Eve's additional_lookup. Eve registers it for reads only.
type AdditionalLookup struct {
	Field   string
	Pattern *regexp.Regexp
	// Filter builds the lookup instead of {Field: value}, for a custom
	// lookup route that matches several fields.
	Filter func(value string) bson.M
	// PathLinks builds the item links from the request path, as Eve's
	// resource_link does for a custom Flask route that calls getitem
	// (videos' find_by_slug): collection "videos/<slug>", self
	// "videos/<slug>/<id>".
	PathLinks bool
}

// Pagination holds Eve's PAGINATION_DEFAULT, PAGINATION_LIMIT,
// OPTIMIZE_PAGINATION_FOR_SPEED (no count, so no total and no last page) and
// PAGINATION = False (Disabled: every document, no _meta, no page links).
type Pagination struct {
	Default          int
	Limit            int
	OptimizeForSpeed bool
	Disabled         bool
}

func (r *Resource) setDefaults() {
	if r.URL == "" {
		r.URL = r.Name
	}
	if r.ResourceTitle == "" {
		r.ResourceTitle = r.URL
	}
	if r.ItemTitle == "" {
		r.ItemTitle = strings.TrimRight(r.ResourceTitle, "s")
	}
	if r.ResourceMethods == nil {
		r.ResourceMethods = []string{http.MethodGet}
	}
	if r.ItemMethods == nil {
		r.ItemMethods = []string{http.MethodGet}
	}
	if r.Pagination.Default == 0 {
		r.Pagination.Default = 25
	}
	if r.Pagination.Limit == 0 {
		r.Pagination.Limit = 50
	}
	if r.QueryDenylist == nil {
		r.QueryDenylist = DefaultQueryDenylist
	}
}

// Hooks mirror Eve's event hooks. Each list runs in order; behaviors that are
// shared between resources (audit, Elasticsearch sync, outbox) are Hooks
// values combined with Use.
//
// Pre-write hooks (OnInsert, OnUpdate, OnReplace, OnDelete) run inside the
// write transaction: an error aborts the write, and an *Error sets the status.
// Post-write hooks run after the commit; their errors are logged and do not
// change the response.
type Hooks struct {
	// OnPreGet may change the query before a GET (Eve's on_pre_GET).
	OnPreGet []func(ctx context.Context, q *ListQuery) error
	// OnFetchedResource and OnFetchedItem may change the documents before
	// they are rendered (Eve's on_fetched_resource / on_fetched_item).
	OnFetchedResource []func(ctx context.Context, docs []Doc) error
	OnFetchedItem     []func(ctx context.Context, doc Doc) error

	// OnPrepare runs once per written document, after validation and before
	// the transaction (original is nil on insert). It is the place for HTTP
	// calls: the hooks below run inside the transaction, which they would
	// hold open, and run again when the driver retries it.
	OnPrepare []func(ctx context.Context, doc, original Doc) error

	OnInsert   []func(ctx context.Context, docs []Doc) error
	OnInserted []func(ctx context.Context, docs []Doc) error

	// updates holds only the changed fields, original the stored document.
	OnUpdate  []func(ctx context.Context, updates, original Doc) error
	OnUpdated []func(ctx context.Context, updates, original Doc) error

	OnReplace  []func(ctx context.Context, replacement, original Doc) error
	OnReplaced []func(ctx context.Context, replacement, original Doc) error

	OnDelete  []func(ctx context.Context, original Doc) error
	OnDeleted []func(ctx context.Context, original Doc) error
}

// Use appends the hooks of each behavior, keeping their order.
func (h *Hooks) Use(behaviors ...Hooks) {
	for _, b := range behaviors {
		h.OnPreGet = append(h.OnPreGet, b.OnPreGet...)
		h.OnFetchedResource = append(h.OnFetchedResource, b.OnFetchedResource...)
		h.OnFetchedItem = append(h.OnFetchedItem, b.OnFetchedItem...)
		h.OnPrepare = append(h.OnPrepare, b.OnPrepare...)
		h.OnInsert = append(h.OnInsert, b.OnInsert...)
		h.OnInserted = append(h.OnInserted, b.OnInserted...)
		h.OnUpdate = append(h.OnUpdate, b.OnUpdate...)
		h.OnUpdated = append(h.OnUpdated, b.OnUpdated...)
		h.OnReplace = append(h.OnReplace, b.OnReplace...)
		h.OnReplaced = append(h.OnReplaced, b.OnReplaced...)
		h.OnDelete = append(h.OnDelete, b.OnDelete...)
		h.OnDeleted = append(h.OnDeleted, b.OnDeleted...)
	}
}

// Transactor runs fn in a transaction. Use mongodb.Transactor with a replica
// set; NoTransaction runs fn directly (standalone Mongo, unit tests).
type Transactor interface {
	WithTransaction(ctx context.Context, fn func(ctx context.Context) error) error
}

// NoTransaction runs fn without a transaction.
type NoTransaction struct{}

// WithTransaction implements Transactor.
func (NoTransaction) WithTransaction(ctx context.Context, fn func(ctx context.Context) error) error {
	return fn(ctx)
}
