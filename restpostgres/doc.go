package restpostgres

import (
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/Gui-Rondon/hyrule/rest"
)

// metaFields names the Doc keys the adapter itself owns -- never part
// of the jsonb doc column, always derived from the row's own columns.
var metaFields = [...]string{"_id", "_etag", "_created", "_updated", "_deleted"}

// hydrate builds the rest.Doc a read returns: fields (the jsonb doc
// column's content) plus the meta fields derived from the row's other
// columns -- id/version/created_at/updated_at/deleted never live
// inside fields, so there's nothing to skip or overwrite.
func hydrate(id uuid.UUID, fields map[string]any, createdAt, updatedAt time.Time, version int64, deleted bool) rest.Doc {
	doc := make(rest.Doc, len(fields)+len(metaFields))
	for k, v := range fields {
		doc[k] = v
	}
	doc["_id"] = id
	doc["_etag"] = strconv.FormatInt(version, 10)
	doc["_created"] = createdAt
	doc["_updated"] = updatedAt
	doc["_deleted"] = deleted
	return doc
}

// businessFieldsOnly strips doc's meta fields, leaving only what's
// serialized into the jsonb doc column -- meta fields are never
// duplicated inside it.
func businessFieldsOnly(doc rest.Doc) map[string]any {
	out := make(map[string]any, len(doc))
	for k, v := range doc {
		out[k] = v
	}
	for _, k := range metaFields {
		delete(out, k)
	}
	return out
}
