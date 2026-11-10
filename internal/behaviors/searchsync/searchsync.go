// Package searchsync keeps an Elasticsearch index in sync with a resource,
// as the resource_callbacks.py of the Eve APIs do: every write indexes the
// stored document (without _id) and a delete removes it.
//
// It runs after the commit: Mongo is the source of truth and a failure is
// logged without failing the request (in Eve the document was saved and the
// response was 500).
package searchsync

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/Gui-Rondon/hyrule/internal/platform/search"
	"github.com/Gui-Rondon/hyrule/internal/rest"
)

// Indexer is the part of search.Client used here.
type Indexer interface {
	Index(ctx context.Context, index, id string, doc any, opts ...search.WriteOption) error
	Delete(ctx context.Context, index, id string, opts ...search.WriteOption) error
}

// Option customizes the hooks.
type Option func(*options)

type options struct {
	transform func(rest.Doc) any
	render    func(any) any
	resource  string
	write     []search.WriteOption
}

// EveElastic reproduces Eve-Elastic, used by settings, channels,
// classifications and covers: the body carries _resource, dates go without
// offset and every write refreshes the index.
func EveElastic(resource string) Option {
	return func(o *options) {
		o.resource = resource
		o.render = rest.RenderISO
		o.write = append(o.write, search.Refresh())
	}
}

// WithTransform changes the indexed body; the default is the document
// without _id, with ISO 8601 dates (+00:00, or without offset with
// EveElastic).
func WithTransform(f func(rest.Doc) any) Option {
	return func(o *options) { o.transform = f }
}

// Hooks returns the hooks that sync index.
func Hooks(idx Indexer, index string, opts ...Option) rest.Hooks {
	o := options{render: rest.RenderISOOffset}
	for _, opt := range opts {
		opt(&o)
	}
	if o.transform == nil {
		o.transform = func(doc rest.Doc) any { return defaultBody(doc, o.render) }
	}
	put := func(ctx context.Context, doc rest.Doc) error {
		id := ID(doc)
		body := o.transform(doc)
		if m, ok := body.(map[string]any); ok && o.resource != "" {
			m["_resource"] = o.resource
		}
		if err := idx.Index(ctx, index, id, body, o.write...); err != nil {
			return fmt.Errorf("index %s/%s: %w", index, id, err)
		}
		return nil
	}
	return rest.Hooks{
		OnInserted: []func(context.Context, []rest.Doc) error{
			func(ctx context.Context, docs []rest.Doc) error {
				for _, doc := range docs {
					if err := put(ctx, doc); err != nil {
						return err
					}
				}
				return nil
			},
		},
		OnUpdated: []func(context.Context, rest.Doc, rest.Doc) error{
			func(ctx context.Context, updates, original rest.Doc) error {
				merged := rest.Doc{}
				for k, v := range original {
					merged[k] = v
				}
				for k, v := range updates {
					merged[k] = v
				}
				return put(ctx, merged)
			},
		},
		// Eve merged the replacement into the original, which kept removed
		// fields in the index; the replacement is the stored document.
		OnReplaced: []func(context.Context, rest.Doc, rest.Doc) error{
			func(ctx context.Context, replacement, _ rest.Doc) error {
				return put(ctx, replacement)
			},
		},
		OnDeleted: []func(context.Context, rest.Doc) error{
			func(ctx context.Context, original rest.Doc) error {
				return idx.Delete(ctx, index, ID(original), o.write...)
			},
		},
	}
}

// ID is the Elasticsearch id of a document: the ObjectID hex.
func ID(doc rest.Doc) string {
	if oid, ok := doc["_id"].(bson.ObjectID); ok {
		return oid.Hex()
	}
	return fmt.Sprint(doc["_id"])
}

func defaultBody(doc rest.Doc, render func(any) any) any {
	body := make(rest.Doc, len(doc))
	for k, v := range doc {
		if k != "_id" {
			body[k] = v
		}
	}
	return render(body)
}
