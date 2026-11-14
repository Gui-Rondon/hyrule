package restpostgres

import (
	"testing"

	"github.com/google/uuid"
)

func TestNewIDGeneratesUniqueUUIDs(t *testing.T) {
	a := &postgresAdapter{}
	id1 := a.NewID()
	id2 := a.NewID()
	u1, ok := id1.(uuid.UUID)
	if !ok {
		t.Fatalf("NewID() = %T, want uuid.UUID", id1)
	}
	u2 := id2.(uuid.UUID)
	if u1 == u2 {
		t.Fatal("two calls to NewID() produced the same uuid")
	}
}

func TestIDStringRoundTrip(t *testing.T) {
	a := &postgresAdapter{}
	id := a.NewID()
	s := a.IDString(id)
	parsed, ok := a.LookupByID(s)
	if !ok {
		t.Fatalf("LookupByID(%q) = false, want true", s)
	}
	if parsed != id {
		t.Fatalf("LookupByID(IDString(id)) = %v, want %v", parsed, id)
	}
}

func TestLookupByIDRejectsGarbage(t *testing.T) {
	a := &postgresAdapter{}
	if _, ok := a.LookupByID("not-a-uuid"); ok {
		t.Fatal("expected LookupByID to reject a non-UUID string")
	}
}

func TestLookupIDIsIdentity(t *testing.T) {
	a := &postgresAdapter{}
	id := a.NewID()
	if got := a.LookupID(id); got != id {
		t.Fatalf("LookupID(%v) = %v, want the same value unchanged", id, got)
	}
}

func TestIDStringRejectsNonUUID(t *testing.T) {
	a := &postgresAdapter{}
	if got := a.IDString("not a uuid.UUID"); got != "" {
		t.Fatalf("IDString(wrong type) = %q, want empty string", got)
	}
}
