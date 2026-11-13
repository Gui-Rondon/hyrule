package rest

// ErrorClassifier tells Service what kind of failure a Store operation
// returned, so the HTTP status it picks does not depend on checking a
// backend-specific error value (mongo.ErrNoDocuments, mongo.IsTimeout...)
// at each call site.
//
// Fase 0 of the Postgres portability work (see
// website/docs/reference/architecture-decisions.md, ADR-0002): extracted
// out of rest/service.go, with mongoAdapter implementing it exactly as
// that code already behaved — no behavior change for Mongo.
type ErrorClassifier interface {
	// IsNotFound reports whether err means a lookup found no document.
	IsNotFound(err error) bool
	// IsDuplicateKey reports whether err means a unique index was
	// violated.
	IsDuplicateKey(err error) bool
	// IsClientError reports whether err was caused by the client's query
	// itself (a bad filter value, an invalid regular expression...), not
	// the server or the connection.
	IsClientError(err error) bool
	// IsTimeout reports whether err means the operation ran out of time.
	IsTimeout(err error) bool
}

// IDCodec generates and parses the `_id` field's values.
type IDCodec interface {
	// NewID generates the id of a document being inserted.
	NewID() any
	// IDString renders id the way it appears in a URL or a response body.
	IDString(id any) string
	// LookupByID parses value, taken from a URL path segment, as an id;
	// ok is false when value does not look like an id at all.
	LookupByID(value string) (id any, ok bool)
	// LookupID adapts an id already stored in a Doc for use inside a
	// filter (e.g. excluding the current document from a uniqueness
	// check).
	LookupID(id any) any
}
