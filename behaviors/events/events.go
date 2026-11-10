// Package events records outbox events in the transaction of a resource
// write (D-23). The caller attaches the events to the context of one write;
// the pre-write hooks store them with outbox.Add, so the change and its
// events are committed together:
//
//	res.Hooks.Use(events.Hooks(box))
//	ctx = events.With(ctx, outbox.Event{Topic: "local:videos-schedule", Key: id, Body: entry})
//	svc.Patch(ctx, lookup, changes, opts)
//
// Use a context from With for a single write: the hooks run again when the
// driver retries the transaction, and every run adds the events again inside
// the retried transaction.
//
// ConditionalHooks is the declarative alternative: instead of business code
// deciding when to call With, an outbox.Condition inspects the document
// itself (see outbox.OnFieldChange for the common "field reached one of
// these values" case).
package events

import (
	"context"
	"maps"

	"github.com/Gui-Rondon/hyrule/outbox"
	"github.com/Gui-Rondon/hyrule/rest"
)

type key struct{}

// With returns ctx carrying events for the next write.
func With(ctx context.Context, events ...outbox.Event) context.Context {
	pending, _ := ctx.Value(key{}).([]outbox.Event)
	return context.WithValue(ctx, key{}, append(append([]outbox.Event(nil), pending...), events...))
}

// Hooks store the events of the context in box during every pre-write hook.
func Hooks(box *outbox.Outbox) rest.Hooks {
	add := func(ctx context.Context) error {
		pending, _ := ctx.Value(key{}).([]outbox.Event)
		return box.Add(ctx, pending...)
	}
	return rest.Hooks{
		OnInsert: []func(context.Context, []rest.Doc) error{
			func(ctx context.Context, _ []rest.Doc) error { return add(ctx) },
		},
		OnUpdate: []func(context.Context, rest.Doc, rest.Doc) error{
			func(ctx context.Context, _, _ rest.Doc) error { return add(ctx) },
		},
		OnReplace: []func(context.Context, rest.Doc, rest.Doc) error{
			func(ctx context.Context, _, _ rest.Doc) error { return add(ctx) },
		},
		OnDelete: []func(context.Context, rest.Doc) error{
			func(ctx context.Context, _ rest.Doc) error { return add(ctx) },
		},
	}
}

// ConditionalHooks adds cond's events to box during the pre-write hooks,
// independently of (and composable with) Hooks/With. doc is the full
// document as it will be stored once the write commits; original is nil on
// insert. On update, where the hook only carries the changed fields, doc is
// original shallow-merged with updates — enough for a Condition that reads
// simple top-level fields such as status.
func ConditionalHooks(box *outbox.Outbox, cond outbox.Condition) rest.Hooks {
	onWrite := func(ctx context.Context, doc, original rest.Doc) error {
		events, ok := cond(ctx, doc, original)
		if !ok {
			return nil
		}
		return box.Add(ctx, events...)
	}
	return rest.Hooks{
		OnInsert: []func(context.Context, []rest.Doc) error{
			func(ctx context.Context, docs []rest.Doc) error {
				for _, doc := range docs {
					if err := onWrite(ctx, doc, nil); err != nil {
						return err
					}
				}
				return nil
			},
		},
		OnUpdate: []func(context.Context, rest.Doc, rest.Doc) error{
			func(ctx context.Context, updates, original rest.Doc) error {
				merged := maps.Clone(original)
				maps.Copy(merged, updates)
				return onWrite(ctx, merged, original)
			},
		},
		OnReplace: []func(context.Context, rest.Doc, rest.Doc) error{
			func(ctx context.Context, replacement, original rest.Doc) error {
				return onWrite(ctx, replacement, original)
			},
		},
	}
}
