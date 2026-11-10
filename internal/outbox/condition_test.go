package outbox_test

import (
	"context"
	"testing"

	"github.com/Gui-Rondon/hyrule/internal/outbox"
)

func TestOnFieldChange(t *testing.T) {
	cond := outbox.OnFieldChange("status", []string{"published"}, func(doc map[string]any) outbox.Event {
		return outbox.Event{Topic: "t", Key: doc["id"].(string)}
	})
	ctx := context.Background()

	// insert landing directly on the target value fires
	if events, ok := cond(ctx, map[string]any{"id": "a", "status": "published"}, nil); !ok || len(events) != 1 {
		t.Errorf("insert published = %v %v", events, ok)
	}
	// insert on another value does not
	if _, ok := cond(ctx, map[string]any{"id": "a", "status": "draft"}, nil); ok {
		t.Error("insert draft fired")
	}

	// update that changes status into the target value fires
	original := map[string]any{"id": "a", "status": "draft"}
	if events, ok := cond(ctx, map[string]any{"id": "a", "status": "published"}, original); !ok || len(events) != 1 {
		t.Errorf("draft -> published = %v %v", events, ok)
	}
	// update that was already at the target value does not (no transition)
	already := map[string]any{"id": "a", "status": "published"}
	if _, ok := cond(ctx, map[string]any{"id": "a", "status": "published"}, already); ok {
		t.Error("published -> published fired")
	}
	// update that leaves the target value does not
	if _, ok := cond(ctx, map[string]any{"id": "a", "status": "archived"}, already); ok {
		t.Error("published -> archived fired")
	}
}
