package restpostgres

import (
	"fmt"

	"github.com/Gui-Rondon/hyrule/rest"
)

// Projection selects which fields Find/FindOne keep in the returned
// Doc. An empty/nil Projection keeps every field. Postgres always
// hydrates the full Doc from the row before applying this -- read
// hooks (a future consumer, not wired in Fase 2) see the complete
// document, exactly as a Mongo resource's do today; this is a
// deliberate portability rule (postgres-rfc.md's cross-backend hook
// contract), not an optimization left for later.
type Projection map[string]bool

// toProjection normalizes Find/FindOne's projection parameter: nil
// means no projection (keep everything); any non-nil value must be a
// Projection. A wrong type errors loudly here instead of silently
// keeping every field as if none had been given.
func toProjection(projection any) (Projection, error) {
	if projection == nil {
		return nil, nil
	}
	proj, ok := projection.(Projection)
	if !ok {
		return nil, fmt.Errorf("restpostgres: projection must be a Projection, got %T", projection)
	}
	return proj, nil
}

// applyProjection narrows doc to projection's included fields, always
// keeping the meta fields regardless of projection's content -- a
// client can narrow which Schema fields it gets back, never which
// meta fields.
func applyProjection(doc rest.Doc, projection Projection) rest.Doc {
	if len(projection) == 0 {
		return doc
	}
	out := make(rest.Doc, len(projection)+len(metaFields))
	for _, k := range metaFields {
		if v, ok := doc[k]; ok {
			out[k] = v
		}
	}
	for field, include := range projection {
		if !include {
			continue
		}
		if v, ok := doc[field]; ok {
			out[field] = v
		}
	}
	return out
}
