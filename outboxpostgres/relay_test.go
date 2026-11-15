package outboxpostgres_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Gui-Rondon/hyrule/outbox"
	"github.com/Gui-Rondon/hyrule/outboxpostgres"
	"github.com/Gui-Rondon/hyrule/platform/config"
)

// recorder is a Publisher that keeps the messages and fails when fail says
// so -- same shape as outbox/relay_test.go's own recorder.
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

func add(t *testing.T, box *outboxpostgres.Outbox, events ...outbox.Event) {
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

func TestRelayDrainPublishesInOrder(t *testing.T) {
	pool := testPool(t)
	table := testTable(t, pool)
	box := &outboxpostgres.Outbox{Pool: pool, Table: table, AllowNoTransaction: true}
	ctx := context.Background()
	add(t, box,
		outbox.Event{Topic: "videos-to-publish", Type: "video.publish_requested", Key: "v1", Body: "1", Attributes: map[string]string{"origin": "edit"}},
		outbox.Event{Topic: "videos-to-unpublish", Key: "v1", Body: "2"},
		outbox.Event{Topic: "contents-to-publish", Body: "3"},
	)
	pub := &recorder{}
	relay := &outboxpostgres.Relay{Outbox: box, Publisher: pub}

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

	if n, _ := relay.Drain(ctx); n != 0 {
		t.Errorf("republished %d", n)
	}
}

// A failed event holds back the later events of its key for this pass
// only -- across workers, C-1 accepts the divergence.
func TestRelayFailureHoldsItsKeyForThePass(t *testing.T) {
	pool := testPool(t)
	table := testTable(t, pool)
	box := &outboxpostgres.Outbox{Pool: pool, Table: table, AllowNoTransaction: true}
	ctx := context.Background()
	add(t, box,
		outbox.Event{Topic: "t", Key: "a", Body: "a1"},
		outbox.Event{Topic: "t", Key: "b", Body: "b1"},
		outbox.Event{Topic: "t", Key: "a", Body: "a2"},
		outbox.Event{Topic: "t", Body: "free"},
	)
	down := true
	pub := &recorder{fail: func(m outbox.Message) bool { return down && string(m.Data) == "a1" }}
	relay := &outboxpostgres.Relay{Outbox: box, Publisher: pub}

	if n, _ := relay.Drain(ctx); n != 2 {
		t.Fatalf("published %d", n)
	}
	if got := pub.bodies(); !equal(got, []string{"b1", "free"}) {
		t.Errorf("first pass = %v", got)
	}
	var attempts int
	var lastError string
	if err := pool.QueryRow(ctx, "SELECT attempts, last_error FROM "+table+" WHERE data = $1", []byte("a1")).Scan(&attempts, &lastError); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 || lastError != "unavailable" {
		t.Errorf("failed event: attempts=%d last_error=%q", attempts, lastError)
	}

	down = false
	// skip the backoff wait (TestRelayBacksOffFailures covers it)
	if _, err := pool.Exec(ctx, "UPDATE "+table+" SET next_attempt_at = NULL"); err != nil {
		t.Fatal(err)
	}
	relay.Drain(ctx)
	if got := pub.bodies(); !equal(got, []string{"b1", "free", "a1", "a2"}) {
		t.Errorf("second pass = %v", got)
	}
}

// R2: a failing event waits (exponential backoff) before the next attempt.
func TestRelayBacksOffFailures(t *testing.T) {
	pool := testPool(t)
	table := testTable(t, pool)
	box := &outboxpostgres.Outbox{Pool: pool, Table: table, AllowNoTransaction: true}
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
	relay := &outboxpostgres.Relay{Outbox: box, Publisher: pub, MaxBackoff: time.Minute}

	relay.Drain(ctx)
	relay.Drain(ctx)
	if calls.Load() != 1 {
		t.Fatalf("attempts during backoff = %d", calls.Load())
	}
	var next time.Time
	if err := pool.QueryRow(ctx, "SELECT next_attempt_at FROM "+table+" WHERE data = $1", []byte("a1")).Scan(&next); err != nil {
		t.Fatal(err)
	}
	if wait := time.Until(next); wait <= 0 || wait > 2*time.Second {
		t.Errorf("first backoff = %v", wait)
	}

	// once the wait is over, the event goes first and the key follows
	if _, err := pool.Exec(ctx, "UPDATE "+table+" SET next_attempt_at = now() - interval '1 second'"); err != nil {
		t.Fatal(err)
	}
	down = false
	if n, _ := relay.Drain(ctx); n != 2 {
		t.Errorf("published after backoff = %d", n)
	}
}

// A publish that hangs is cut by PublishTimeout.
func TestRelayPublishTimeoutCutsHungPublish(t *testing.T) {
	pool := testPool(t)
	table := testTable(t, pool)
	box := &outboxpostgres.Outbox{Pool: pool, Table: table, AllowNoTransaction: true}
	ctx := context.Background()
	add(t, box, outbox.Event{Topic: "hang", Body: "x"})
	hang := outbox.PublisherFunc(func(ctx context.Context, _ outbox.Message) error {
		<-ctx.Done()
		return ctx.Err()
	})
	start := time.Now()
	relay := &outboxpostgres.Relay{Outbox: box, Publisher: hang, PublishTimeout: 100 * time.Millisecond}
	if n, err := relay.Drain(ctx); n != 0 || err != nil {
		t.Errorf("drain = %d %v", n, err)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("hung publish not cut: %v", time.Since(start))
	}
}

// TestRelayConcurrentWorkersEachEventPublishedExactlyOnce is C2's real
// proof: several goroutines draining the same table at the same time
// (no lease, unlike the Mongo relay) never publish the same event
// twice -- SELECT ... FOR UPDATE SKIP LOCKED is what makes that true,
// not any coordination between them.
func TestRelayConcurrentWorkersEachEventPublishedExactlyOnce(t *testing.T) {
	pool := testPool(t)
	table := testTable(t, pool)
	box := &outboxpostgres.Outbox{Pool: pool, Table: table, AllowNoTransaction: true}
	ctx := context.Background()
	const n = 60
	for i := range n {
		if err := box.Add(ctx, outbox.Event{Topic: "t", Body: fmt.Sprintf("%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	pub := &recorder{}
	relay := &outboxpostgres.Relay{Outbox: box, Publisher: pub}

	const workers = 8
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() { relay.Drain(ctx) })
	}
	wg.Wait()

	got := pub.bodies()
	if len(got) != n {
		t.Fatalf("published %d events, want %d", len(got), n)
	}
	seen := map[string]bool{}
	for _, b := range got {
		if seen[b] {
			t.Fatalf("body %q published more than once", b)
		}
		seen[b] = true
	}
}

func TestRelayRun(t *testing.T) {
	pool := testPool(t)
	table := testTable(t, pool)
	box := &outboxpostgres.Outbox{Pool: pool, Table: table, AllowNoTransaction: true}
	pub := &recorder{}
	relay := &outboxpostgres.Relay{Outbox: box, Publisher: pub, Interval: 20 * time.Millisecond, Workers: 3}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- relay.Run(ctx) }()

	add(t, box, outbox.Event{Topic: "t", Body: "now"})
	deadline := time.Now().Add(5 * time.Second)
	for len(pub.bodies()) == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := pub.bodies(); !equal(got, []string{"now"}) {
		t.Fatalf("published = %v", got)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}
}

func TestRelayDeadLetterAfterMaxAttempts(t *testing.T) {
	pool := testPool(t)
	table := testTable(t, pool)
	box := &outboxpostgres.Outbox{Pool: pool, Table: table, AllowNoTransaction: true}
	ctx := context.Background()
	add(t, box, outbox.Event{Topic: "t", Body: "x"})
	pub := &recorder{fail: func(outbox.Message) bool { return true }}
	relay := &outboxpostgres.Relay{Outbox: box, Publisher: pub, MaxAttempts: 2, MaxBackoff: time.Millisecond}

	relay.Drain(ctx) // attempt 1: fails, next_attempt_at set
	if _, err := pool.Exec(ctx, "UPDATE "+table+" SET next_attempt_at = now() - interval '1 second'"); err != nil {
		t.Fatal(err)
	}
	relay.Drain(ctx) // attempt 2: fails, reaches MaxAttempts -> dead_at set

	var deadAt *time.Time
	var attempts int
	if err := pool.QueryRow(ctx, "SELECT dead_at, attempts FROM "+table).Scan(&deadAt, &attempts); err != nil {
		t.Fatal(err)
	}
	if deadAt == nil || attempts != 2 {
		t.Fatalf("dead_at = %v, attempts = %d, want dead_at set, attempts=2", deadAt, attempts)
	}

	stats, err := relay.Stats(ctx)
	if err != nil || stats.Pending != 0 {
		t.Errorf("stats after DLQ = %+v %v", stats, err)
	}
	if n, err := relay.Drain(ctx); err != nil || n != 0 {
		t.Errorf("a dead event must not be picked up again: drain = %d %v", n, err)
	}
}

func TestRelayReplayResurrectsDeadEvent(t *testing.T) {
	pool := testPool(t)
	table := testTable(t, pool)
	box := &outboxpostgres.Outbox{Pool: pool, Table: table, AllowNoTransaction: true}
	ctx := context.Background()
	add(t, box, outbox.Event{Topic: "t", Body: "x"})
	down := true
	pub := &recorder{fail: func(outbox.Message) bool { return down }}
	relay := &outboxpostgres.Relay{Outbox: box, Publisher: pub, MaxAttempts: 1, MaxBackoff: time.Millisecond}

	relay.Drain(ctx) // attempt 1 of 1: fails, dead_at set immediately
	var id int64
	if err := pool.QueryRow(ctx, "SELECT id FROM "+table).Scan(&id); err != nil {
		t.Fatal(err)
	}
	var deadAt *time.Time
	pool.QueryRow(ctx, "SELECT dead_at FROM "+table+" WHERE id = $1", id).Scan(&deadAt)
	if deadAt == nil {
		t.Fatal("event not dead yet")
	}

	if err := relay.Replay(ctx, id); err != nil {
		t.Fatal(err)
	}
	down = false
	n, err := relay.Drain(ctx)
	if err != nil || n != 1 {
		t.Fatalf("drain after replay = %d %v", n, err)
	}

	if err := relay.Replay(ctx, id); !errors.Is(err, outboxpostgres.ErrNotReplayable) {
		t.Errorf("replaying a published (not dead) event: %v", err)
	}
	if err := relay.Replay(ctx, 999999999); !errors.Is(err, outboxpostgres.ErrNotReplayable) {
		t.Errorf("replaying an unknown id: %v", err)
	}
}

// Stats and the healthcheck: pending, retrying and the age of the
// oldest -- outbox.Check/CheckFromConfig work against *outboxpostgres.Relay
// unchanged, proving the StatsProvider generalization (C1) for real.
func TestStatsAndCheck(t *testing.T) {
	pool := testPool(t)
	table := testTable(t, pool)
	box := &outboxpostgres.Outbox{Pool: pool, Table: table, AllowNoTransaction: true}
	ctx := context.Background()
	relay := &outboxpostgres.Relay{Outbox: box, Publisher: &recorder{fail: func(m outbox.Message) bool { return string(m.Data) == "bad" }}}

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
	if _, err := pool.Exec(ctx, "UPDATE "+table+" SET occurred_at = now() - interval '2 minutes' WHERE published_at IS NULL"); err != nil {
		t.Fatal(err)
	}
	if ok, out := check.Check(ctx); ok || !strings.HasPrefix(out, "Outbox Nok, 1 pending (1 retrying), oldest 2m") {
		t.Errorf("check over the limit = %v %q", ok, out)
	}
}

func TestCheckFromConfig(t *testing.T) {
	pool := testPool(t)
	table := testTable(t, pool)
	relay := &outboxpostgres.Relay{Outbox: &outboxpostgres.Outbox{Pool: pool, Table: table}}
	if _, err := outbox.CheckFromConfig(config.Config{"outbox": map[string]any{"max_delay": "x"}}, relay); err == nil {
		t.Error("invalid max_delay accepted")
	}
	if c, err := outbox.CheckFromConfig(config.Config{}, relay); err != nil || c.Name != "outbox_check" {
		t.Errorf("default = %+v %v", c, err)
	}
}
