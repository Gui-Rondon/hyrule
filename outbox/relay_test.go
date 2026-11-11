package outbox_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/Gui-Rondon/hyrule/outbox"
	"github.com/Gui-Rondon/hyrule/platform/config"
	"github.com/Gui-Rondon/hyrule/testkit"
)

// recorder is a Publisher that keeps the messages and fails when fail says so.
type recorder struct {
	mu   sync.Mutex
	msgs []outbox.Message
	fail func(outbox.Message) bool
}

func (r *recorder) Publish(_ context.Context, m outbox.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail != nil && r.fail(m) {
		return errors.New("unavailable")
	}
	r.msgs = append(r.msgs, m)
	return nil
}

func (r *recorder) bodies() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, m := range r.msgs {
		out = append(out, string(m.Data))
	}
	return out
}

func newBox(t *testing.T) *outbox.Outbox {
	box := outbox.New(testkit.Mongo(t))
	box.AllowNoTransaction = true
	return box
}

func add(t *testing.T, box *outbox.Outbox, events ...outbox.Event) {
	t.Helper()
	if err := box.Add(context.Background(), events...); err != nil {
		t.Fatal(err)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestRelayPublishesInOrder(t *testing.T) {
	box := newBox(t)
	ctx := context.Background()
	add(t, box,
		outbox.Event{Topic: "videos-to-publish", Type: "video.publish_requested", Key: "v1", Body: "1", Attributes: map[string]string{"origin": "edit"}},
		outbox.Event{Topic: "videos-to-unpublish", Key: "v1", Body: "2"},
		outbox.Event{Topic: "contents-to-publish", Body: "3"},
	)
	pub := &recorder{}
	relay := &outbox.Relay{Outbox: box, Publisher: pub}

	n, err := relay.Drain(ctx)
	if err != nil || n != 3 {
		t.Fatalf("drain = %d %v", n, err)
	}
	if got := pub.bodies(); !equal(got, []string{"1", "2", "3"}) {
		t.Errorf("order = %v", got)
	}
	m := pub.msgs[0]
	if m.Topic != "videos-to-publish" || m.OrderingKey != "v1" || m.Attributes["origin"] != "edit" ||
		m.Attributes[outbox.AttrEventType] != "video.publish_requested" || m.Attributes[outbox.AttrEventID] != m.ID {
		t.Errorf("message = %+v", m)
	}
	if _, err := time.Parse(time.RFC3339Nano, m.Attributes[outbox.AttrOccurredAt]); err != nil {
		t.Errorf("occurred_at = %q", m.Attributes[outbox.AttrOccurredAt])
	}
	if _, ok := pub.msgs[2].Attributes[outbox.AttrEventType]; ok {
		t.Error("event_type sent without a type")
	}

	if pending, _ := box.Collection().CountDocuments(ctx, bson.M{"published_at": nil}); pending != 0 {
		t.Errorf("pending = %d", pending)
	}
	if n, _ := relay.Drain(ctx); n != 0 {
		t.Errorf("republished %d", n)
	}
	if age, err := relay.OldestPending(ctx); err != nil || age != 0 {
		t.Errorf("oldest = %v %v", age, err)
	}
}

// A failed event holds back the later events of its key only, and is
// retried first on the next pass.
func TestRelayFailureHoldsItsKey(t *testing.T) {
	box := newBox(t)
	ctx := context.Background()
	add(t, box,
		outbox.Event{Topic: "t", Key: "a", Body: "a1"},
		outbox.Event{Topic: "t", Key: "b", Body: "b1"},
		outbox.Event{Topic: "t", Key: "a", Body: "a2"},
		outbox.Event{Topic: "t", Body: "free"},
	)
	down := true
	pub := &recorder{fail: func(m outbox.Message) bool { return down && string(m.Data) == "a1" }}
	relay := &outbox.Relay{Outbox: box, Publisher: pub}

	if n, _ := relay.Drain(ctx); n != 2 {
		t.Fatalf("published %d", n)
	}
	if got := pub.bodies(); !equal(got, []string{"b1", "free"}) {
		t.Errorf("first pass = %v", got)
	}
	var failed struct {
		Attempts  int    `bson:"attempts"`
		LastError string `bson:"last_error"`
	}
	box.Collection().FindOne(ctx, bson.M{"data": []byte("a1")}).Decode(&failed)
	if failed.Attempts != 1 || failed.LastError != "unavailable" {
		t.Errorf("failed event = %+v", failed)
	}
	if age, _ := relay.OldestPending(ctx); age <= 0 {
		t.Errorf("oldest pending = %v", age)
	}

	down = false
	// skip the backoff wait (TestRelayBacksOffFailures covers it)
	box.Collection().UpdateMany(ctx, bson.M{}, bson.M{"$unset": bson.M{"next_attempt_at": ""}})
	relay.Drain(ctx)
	if got := pub.bodies(); !equal(got, []string{"b1", "free", "a1", "a2"}) {
		t.Errorf("second pass = %v", got)
	}
}

// Events that keep failing do not starve the ones after them.
func TestRelayScansPastFailures(t *testing.T) {
	box := newBox(t)
	for _, b := range []string{"bad", "bad", "ok1", "ok2", "ok3"} {
		add(t, box, outbox.Event{Topic: "t", Body: b})
	}
	pub := &recorder{fail: func(m outbox.Message) bool { return string(m.Data) == "bad" }}
	relay := &outbox.Relay{Outbox: box, Publisher: pub, Batch: 2}
	if n, err := relay.Drain(context.Background()); err != nil || n != 3 {
		t.Fatalf("drain = %d %v", n, err)
	}
	if got := pub.bodies(); !equal(got, []string{"ok1", "ok2", "ok3"}) {
		t.Errorf("published = %v", got)
	}
}

// Only the replica holding the lease publishes.
func TestRelayLease(t *testing.T) {
	box := newBox(t)
	ctx := context.Background()
	add(t, box, outbox.Event{Topic: "t", Body: "1"})
	one := &outbox.Relay{Outbox: box, Publisher: &recorder{}, Owner: "one", Lease: 300 * time.Millisecond}
	two := &outbox.Relay{Outbox: box, Publisher: &recorder{}, Owner: "two", Lease: 300 * time.Millisecond}

	if n, err := one.Drain(ctx); err != nil || n != 1 {
		t.Fatalf("one = %d %v", n, err)
	}
	add(t, box, outbox.Event{Topic: "t", Body: "2"})
	if _, err := two.Drain(ctx); !errors.Is(err, outbox.ErrNotLeader) {
		t.Fatalf("two while one holds the lease: %v", err)
	}
	if n, err := one.Drain(ctx); err != nil || n != 1 {
		t.Fatalf("one renews: %d %v", n, err)
	}

	time.Sleep(400 * time.Millisecond) // one stopped renewing
	add(t, box, outbox.Event{Topic: "t", Body: "3"})
	if n, err := two.Drain(ctx); err != nil || n != 1 {
		t.Fatalf("two after expiry = %d %v", n, err)
	}
}

func TestDispatcher(t *testing.T) {
	var local []string
	next := &recorder{}
	d := outbox.Dispatcher{
		Local: map[string]outbox.PublisherFunc{
			"local:articles-schedule": func(_ context.Context, m outbox.Message) error {
				local = append(local, string(m.Data))
				return nil
			},
		},
		Next: next,
	}
	ctx := context.Background()
	d.Publish(ctx, outbox.Message{Topic: "local:articles-schedule", Data: []byte("s")})
	d.Publish(ctx, outbox.Message{Topic: "articles-to-publish", Data: []byte("p")})
	if !equal(local, []string{"s"}) || !equal(next.bodies(), []string{"p"}) {
		t.Errorf("local = %v, next = %v", local, next.bodies())
	}
	if err := (outbox.Dispatcher{}).Publish(ctx, outbox.Message{Topic: "x"}); err == nil {
		t.Error("topic without destination must fail")
	}
}

// Run publishes an insert without waiting for Interval (change stream), and
// gives the lease up when it stops.
func TestRelayRun(t *testing.T) {
	db := testkit.Mongo(t)
	transactor(t, db) // change streams need a replica set
	box := outbox.New(db)
	box.AllowNoTransaction = true
	pub := &recorder{}
	relay := &outbox.Relay{Outbox: box, Publisher: pub, Owner: "runner", Interval: time.Minute, Lease: time.Minute}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- relay.Run(ctx) }()

	time.Sleep(300 * time.Millisecond) // first pass and change stream open
	add(t, box, outbox.Event{Topic: "t", Body: "now"})
	deadline := time.Now().Add(5 * time.Second)
	for len(pub.bodies()) == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := pub.bodies(); !equal(got, []string{"now"}) {
		t.Fatalf("published = %v", got)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	other := &outbox.Relay{Outbox: box, Publisher: pub, Owner: "other"}
	if _, err := other.Drain(context.Background()); err != nil {
		t.Errorf("lease not released: %v", err)
	}

	if !(&testkit.DB{Database: db}).HasIndex(t, outbox.Collection, "published_at") {
		t.Error("indexes not created")
	}
	cur, _ := box.Collection().Indexes().List(context.Background())
	var idx []bson.M
	cur.All(context.Background(), &idx)
	ttl := false
	for _, i := range idx {
		if i["name"] == "published_at_ttl" && i["expireAfterSeconds"] != nil {
			ttl = true
		}
	}
	if !ttl {
		t.Errorf("ttl index missing: %v", idx)
	}
}

// R2: a failing event waits (exponential backoff) before the next attempt,
// and still holds back the later events of its key.
func TestRelayBacksOffFailures(t *testing.T) {
	box := newBox(t)
	ctx := context.Background()
	add(t, box, outbox.Event{Topic: "t", Key: "a", Body: "a1"}, outbox.Event{Topic: "t", Key: "a", Body: "a2"})
	var calls atomic.Int32
	down := true
	pub := outbox.PublisherFunc(func(_ context.Context, m outbox.Message) error {
		calls.Add(1)
		if down {
			return errors.New("unavailable")
		}
		return nil
	})
	relay := &outbox.Relay{Outbox: box, Publisher: pub, MaxBackoff: time.Minute}

	relay.Drain(ctx)
	relay.Drain(ctx)
	if calls.Load() != 1 {
		t.Fatalf("attempts during backoff = %d", calls.Load())
	}
	var rec struct {
		Next time.Time `bson:"next_attempt_at"`
	}
	box.Collection().FindOne(ctx, bson.M{"data": []byte("a1")}).Decode(&rec)
	if wait := time.Until(rec.Next); wait <= 0 || wait > 2*time.Second {
		t.Errorf("first backoff = %v", wait)
	}

	// once the wait is over, the event goes first and the key follows
	box.Collection().UpdateMany(ctx, bson.M{}, bson.M{"$set": bson.M{"next_attempt_at": time.Now().Add(-time.Second)}})
	down = false
	if n, _ := relay.Drain(ctx); n != 2 {
		t.Errorf("published after backoff = %d", n)
	}
}

// R1: a publish that hangs is cut by PublishTimeout, and a long pass renews
// the lease so no other replica publishes at the same time.
func TestRelayPublishTimeoutAndLeaseRenewal(t *testing.T) {
	box := newBox(t)
	ctx := context.Background()
	add(t, box, outbox.Event{Topic: "hang", Body: "x"})
	hang := outbox.PublisherFunc(func(ctx context.Context, _ outbox.Message) error {
		<-ctx.Done()
		return ctx.Err()
	})
	start := time.Now()
	relay := &outbox.Relay{Outbox: box, Publisher: hang, PublishTimeout: 100 * time.Millisecond}
	if n, err := relay.Drain(ctx); n != 0 || err != nil {
		t.Errorf("drain = %d %v", n, err)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("hung publish not cut: %v", time.Since(start))
	}

	box2 := newBox(t)
	for _, b := range []string{"1", "2", "3", "4", "5"} {
		add(t, box2, outbox.Event{Topic: "t", Body: b})
	}
	slow := outbox.PublisherFunc(func(context.Context, outbox.Message) error {
		time.Sleep(120 * time.Millisecond)
		return nil
	})
	one := &outbox.Relay{Outbox: box2, Publisher: slow, Owner: "one", Lease: 300 * time.Millisecond}
	two := &outbox.Relay{Outbox: box2, Publisher: slow, Owner: "two", Lease: 300 * time.Millisecond}
	done := make(chan int)
	go func() {
		n, _ := one.Drain(ctx)
		done <- n
	}()
	time.Sleep(450 * time.Millisecond) // past the first lease, while one still publishes
	if _, err := two.Drain(ctx); !errors.Is(err, outbox.ErrNotLeader) {
		t.Errorf("second replica during a long pass: %v", err)
	}
	if n := <-done; n != 5 {
		t.Errorf("published = %d", n)
	}
}

// R3: the defaults are set once, so Run and OldestPending can run together.
func TestRelayConcurrentUse(t *testing.T) {
	box := newBox(t)
	relay := &outbox.Relay{Outbox: box, Publisher: &recorder{}, Interval: 10 * time.Millisecond}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	go relay.Run(ctx)
	for ctx.Err() == nil {
		relay.OldestPending(context.Background())
		time.Sleep(5 * time.Millisecond)
	}
}

// Stats and the healthcheck: pending, retrying and the age of the oldest.
func TestStatsAndCheck(t *testing.T) {
	box := newBox(t)
	ctx := context.Background()
	relay := &outbox.Relay{Outbox: box, Publisher: &recorder{fail: func(m outbox.Message) bool { return string(m.Data) == "bad" }}}

	stats, err := relay.Stats(ctx)
	if err != nil || stats.Pending != 0 || stats.Oldest != 0 {
		t.Fatalf("empty stats = %+v %v", stats, err)
	}
	check := outbox.Check(relay, time.Minute)
	if ok, out := check.Check(ctx); !ok || out != "Outbox OK, 0 pending" {
		t.Errorf("empty check = %v %q", ok, out)
	}

	add(t, box, outbox.Event{Topic: "t", Body: "bad"}, outbox.Event{Topic: "t", Body: "ok"})
	relay.Drain(ctx)
	stats, _ = relay.Stats(ctx)
	if stats.Pending != 1 || stats.Retrying != 1 || stats.Oldest <= 0 {
		t.Errorf("stats = %+v", stats)
	}
	if ok, out := check.Check(ctx); !ok || !strings.HasPrefix(out, "Outbox OK, 1 pending (1 retrying), oldest ") {
		t.Errorf("check within the limit = %v %q", ok, out)
	}
	// older than the limit: the check fails
	box.Collection().UpdateMany(ctx, bson.M{"published_at": nil}, bson.M{"$set": bson.M{"occurred_at": time.Now().Add(-2 * time.Minute)}})
	if ok, out := check.Check(ctx); ok || !strings.HasPrefix(out, "Outbox Nok, 1 pending (1 retrying), oldest 2m") {
		t.Errorf("check over the limit = %v %q", ok, out)
	}
}

func TestCheckFromConfig(t *testing.T) {
	relay := &outbox.Relay{Outbox: newBox(t)}
	if _, err := outbox.CheckFromConfig(config.Config{"outbox": map[string]any{"max_delay": "x"}}, relay); err == nil {
		t.Error("invalid max_delay accepted")
	}
	if c, err := outbox.CheckFromConfig(config.Config{}, relay); err != nil || c.Name != "outbox_check" {
		t.Errorf("default = %+v %v", c, err)
	}
}
