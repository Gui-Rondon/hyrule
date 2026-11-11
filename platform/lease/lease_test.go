package lease

import (
	"context"
	"testing"
	"time"

	"github.com/Gui-Rondon/hyrule/testkit"
)

// One holder at a time: the other replica gets the lease after it expires
// or is released.
func TestLease(t *testing.T) {
	coll := testkit.Mongo(t).Collection("leases")
	ctx := context.Background()
	a := &Lease{Coll: coll, Name: "scheduler", Owner: "a", TTL: time.Second}
	b := &Lease{Coll: coll, Name: "scheduler", Owner: "b", TTL: time.Second}

	if ok, err := a.Acquire(ctx); !ok || err != nil {
		t.Fatalf("a = %v %v", ok, err)
	}
	if ok, err := b.Acquire(ctx); ok || err != nil {
		t.Fatalf("b while a holds = %v %v", ok, err)
	}
	if ok, _ := a.Acquire(ctx); !ok {
		t.Fatal("a cannot renew")
	}
	// another name is another lease
	if ok, _ := (&Lease{Coll: coll, Name: "other", Owner: "b"}).Acquire(ctx); !ok {
		t.Error("other lease")
	}

	time.Sleep(1100 * time.Millisecond)
	if ok, _ := b.Acquire(ctx); !ok {
		t.Fatal("b after expiry")
	}
	if ok, _ := a.Acquire(ctx); ok {
		t.Fatal("a took it back")
	}
	b.Release()
	if ok, _ := a.Acquire(ctx); !ok {
		t.Fatal("a after release")
	}
	// a release by a replica that does not hold it changes nothing
	b.Release()
	if ok, _ := b.Acquire(ctx); ok {
		t.Error("b after its stale release")
	}
}

func TestDefaults(t *testing.T) {
	l := &Lease{}
	l.defaults()
	if l.Owner == "" || l.TTL != 30*time.Second {
		t.Errorf("defaults = %+v", l)
	}
}
