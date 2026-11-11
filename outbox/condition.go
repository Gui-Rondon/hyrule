package outbox

import (
	"context"
	"slices"
)

// Condition decides whether a write should emit events, given the document
// as it will be stored once the write commits and, for an update or a
// replace, the document as it was stored before (nil on insert). A
// condition that does not apply returns ok false.
//
// Plug a Condition into a Resource with
// behaviors/events.ConditionalHooks: unlike events.With, which
// attaches events the business code already decided to send, a Condition
// decides on its own by inspecting the write — the common case (see
// OnFieldChange) needs no hand-written "if" in the service at all.
type Condition func(ctx context.Context, doc, original map[string]any) (events []Event, ok bool)

// OnFieldChange returns a Condition that builds an event when field's
// effective new value (in doc) is one of to, and either the write is an
// insert (original is nil) or field's value actually changed — a write
// that leaves field alone, or one that lands back on a value it already
// had, emits nothing.
func OnFieldChange(field string, to []string, build func(doc map[string]any) Event) Condition {
	return func(_ context.Context, doc, original map[string]any) ([]Event, bool) {
		newVal, _ := doc[field].(string)
		if !slices.Contains(to, newVal) {
			return nil, false
		}
		if original != nil {
			if oldVal, _ := original[field].(string); oldVal == newVal {
				return nil, false
			}
		}
		return []Event{build(doc)}, true
	}
}
