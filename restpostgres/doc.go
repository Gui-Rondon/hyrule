package restpostgres

import (
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/Gui-Rondon/hyrule/rest"
)

// metaFields names the Doc keys the adapter itself owns -- never part
// of the jsonb doc column, always derived from the row's other
// columns. _deleted is only ever added by hydrate when the resource
// has SoftDelete on (see Options.SoftDelete) -- businessFieldsOnly's
// delete of it is a no-op otherwise, since deleting an absent map key
// is always safe.
var metaFields = [...]string{"_id", "_etag", "_created", "_updated", "_deleted"}

// hydrate builds the rest.Doc a read returns: fields (the jsonb doc
// column's content) plus the meta fields derived from the row's other
// columns -- id/version/created_at/updated_at/deleted never live
// inside fields, so there's nothing to skip or overwrite. _deleted is
// included only when the resource is SoftDelete, mirroring
// rest/service.go's own metaFields(res) -- a resource without it never
// has a _deleted field in its Doc, on either backend.
func (a *postgresAdapter) hydrate(id uuid.UUID, fields map[string]any, createdAt, updatedAt time.Time, version int64, deleted bool) rest.Doc {
	doc := make(rest.Doc, len(fields)+len(metaFields))
	for k, v := range fields {
		doc[k] = v
	}
	doc["_id"] = id
	doc["_etag"] = strconv.FormatInt(version, 10)
	doc["_created"] = createdAt
	doc["_updated"] = updatedAt
	if a.softDelete {
		doc["_deleted"] = deleted
	}
	return doc
}

// requiredMeta reads doc[key] as T, for InsertMany's meta fields
// (_id/_created/_updated) -- Service.Insert is expected to have set
// them before hooks run (postgres-rfc.md §6); i names which element of
// the batch failed, for a clear error.
func requiredMeta[T any](doc rest.Doc, key string, i int) (T, error) {
	v, ok := doc[key].(T)
	if !ok {
		return v, fmt.Errorf("restpostgres: InsertMany: docs[%d][%q] must be a %T, got %T", i, key, v, doc[key])
	}
	return v, nil
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
