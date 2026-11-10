package evecompat

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// Werkzeug's default messages, which Eve returns in _error.message.
const (
	msgBadRequest       = "The browser (or proxy) sent a request that this server could not understand."
	msgNotFound         = "The requested URL was not found on the server. If you entered the URL manually please check your spelling and try again."
	msgMethodNotAllowed = "The method is not allowed for the requested URL."
	msgInternal         = "The server encountered an internal error and was unable to complete your request. Either the server is overloaded or there is an error in the application."
	msgForbidden        = "Please provide proper credentials"
	msgIfMatchRequired  = "To edit a document its etag must be provided using the If-Match header"
	msgEtagMismatch     = "Client and server etags don't match"
	msgConflict         = "A conflict happened while processing the request. The resource might have been modified while the request was being processed."
)

// Doc is a document as stored in Mongo.
type Doc = map[string]any

// Render converts a stored document into Eve's JSON representation:
// ObjectIDs as hex strings and dates in DATE_FORMAT.
func Render(v any) any {
	return render(v, func(t time.Time) any { return t.UTC().Format(DateFormat) })
}

// RenderISO converts a stored document into plain JSON with ISO 8601 dates
// without offset, as Eve-Elastic serializes datetimes.
func RenderISO(v any) any {
	return render(v, func(t time.Time) any { return t.UTC().Format("2006-01-02T15:04:05") })
}

// RenderISOOffset is RenderISO with the +00:00 offset, as the Python
// Elasticsearch client serializes the timezone aware datetimes of Eve
// (tags-api in HLG: "2026-10-26T18:24:29+00:00").
func RenderISOOffset(v any) any {
	return render(v, func(t time.Time) any { return t.UTC().Format("2006-01-02T15:04:05+00:00") })
}

func render(v any, date func(time.Time) any) any {
	switch t := v.(type) {
	case bson.D:
		m := make(map[string]any, len(t))
		for _, e := range t {
			m[e.Key] = render(e.Value, date)
		}
		return m
	case bson.M:
		return render(map[string]any(t), date)
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, val := range t {
			m[k] = render(val, date)
		}
		return m
	case bson.A:
		return render([]any(t), date)
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = render(val, date)
		}
		return out
	case bson.ObjectID:
		return t.Hex()
	case bson.DateTime:
		return date(t.Time())
	case time.Time:
		return date(t)
	case bson.Decimal128:
		return t.String()
	case bson.Binary:
		return base64.StdEncoding.EncodeToString(t.Data)
	}
	return v
}

// computeEtag hashes the document without its meta fields. Eve hashes the
// pymongo json_util dump, so values differ from the ones Eve generated; the
// stored _etag is opaque to clients and is preserved until the next write.
func computeEtag(doc Doc) string {
	clean := make(map[string]any, len(doc))
	for k, v := range doc {
		if k == "_etag" || k == "_links" || k == "_id" {
			continue
		}
		clean[k] = v
	}
	b, _ := json.Marshal(RenderISO(clean))
	sum := sha1.Sum(b)
	return hex.EncodeToString(sum[:])
}

// newEtag is the etag of a write. _updated has second precision, so two
// writes with the same content in the same second would hash the same; a
// nonce guarantees that every write invalidates the previous etag.
func newEtag(doc Doc) string {
	sum := sha1.Sum([]byte(computeEtag(doc) + bson.NewObjectID().Hex()))
	return hex.EncodeToString(sum[:])
}

// etagOf returns the stored _etag or computes it, like Eve does for documents
// written outside the API.
// withEpochDates fills a missing _created or _updated with the epoch, as
// date_created and last_updated do in Eve's build_response_document. The
// stored document is not changed, and the ETag is resolved before (Eve does
// it first too).
func withEpochDates(doc Doc) Doc {
	_, created := doc["_created"]
	_, updated := doc["_updated"]
	if created && updated {
		return doc
	}
	out := make(Doc, len(doc)+2)
	for k, v := range doc {
		out[k] = v
	}
	if !created {
		out["_created"] = bson.DateTime(0)
	}
	if !updated {
		out["_updated"] = bson.DateTime(0)
	}
	return out
}

func etagOf(doc Doc) string {
	if s, ok := doc["_etag"].(string); ok && s != "" {
		return s
	}
	return computeEtag(doc)
}

func link(title, href string) map[string]any {
	return map[string]any{"title": title, "href": href}
}

func now() time.Time {
	// Eve stores dates without microseconds
	return time.Now().UTC().Truncate(time.Second)
}

func parseDate(s string) (time.Time, error) {
	return time.Parse(DateFormat, s)
}
