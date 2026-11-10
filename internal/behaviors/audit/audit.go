// Package audit records who created and last changed a document:
// _created_user and _updated_user with the username of the request token
// (nil when authentication is disabled).
package audit

import (
	"context"

	"github.com/Gui-Rondon/hyrule/internal/evecompat"
	"github.com/Gui-Rondon/hyrule/internal/platform/auth"
)

// Hooks returns the audit hooks.
func Hooks() evecompat.Hooks {
	return evecompat.Hooks{
		OnInsert: []func(context.Context, []evecompat.Doc) error{
			func(ctx context.Context, docs []evecompat.Doc) error {
				user := username(ctx)
				for _, doc := range docs {
					doc["_updated_user"] = user
					if doc["_created_user"] == nil {
						doc["_created_user"] = user
					}
				}
				return nil
			},
		},
		OnUpdate: []func(context.Context, evecompat.Doc, evecompat.Doc) error{
			func(ctx context.Context, updates, original evecompat.Doc) error {
				stamp(ctx, updates, original)
				return nil
			},
		},
		OnReplace: []func(context.Context, evecompat.Doc, evecompat.Doc) error{
			func(ctx context.Context, replacement, original evecompat.Doc) error {
				stamp(ctx, replacement, original)
				// a PUT replaces the document; in Eve _created_user was lost here
				if created := original["_created_user"]; created != nil {
					replacement["_created_user"] = created
				}
				return nil
			},
		},
	}
}

func stamp(ctx context.Context, doc, original evecompat.Doc) {
	user := username(ctx)
	doc["_updated_user"] = user
	if original["_created_user"] == nil {
		doc["_created_user"] = user
	}
}

func username(ctx context.Context) any {
	if u := auth.FromContext(ctx).Username(); u != "" {
		return u
	}
	return nil
}
