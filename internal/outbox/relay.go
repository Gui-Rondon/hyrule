package outbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/Gui-Rondon/hyrule/internal/platform/mongodb"
)

// Message is an outbox event ready to be sent.
type Message struct {
	// ID is the event id, also sent as the event_id attribute.
	ID          string
	Topic       string
	Data        []byte
	Attributes  map[string]string
	OrderingKey string
}

// Publisher sends a message and returns once the destination accepted it.
type Publisher interface {
	Publish(ctx context.Context, msg Message) error
}

// PublisherFunc adapts a function to Publisher.
type PublisherFunc func(ctx context.Context, msg Message) error

// Publish implements Publisher.
func (f PublisherFunc) Publish(ctx context.Context, msg Message) error { return f(ctx, msg) }

// Dispatcher sends the events of local destinations to a function in this
// process and the others to Next (Pub/Sub). A local destination is a topic
// name that no Pub/Sub topic uses, e.g. "local:articles-schedule" for the
// write to the Redis scheduling hash (D-24): the write leaves the request
// and still happens if and only if the change was committed.
type Dispatcher struct {
	Local map[string]PublisherFunc
	Next  Publisher
}

// Publish implements Publisher.
func (d Dispatcher) Publish(ctx context.Context, msg Message) error {
	if fn, ok := d.Local[msg.Topic]; ok {
		return fn(ctx, msg)
	}
	if d.Next == nil {
		return fmt.Errorf("outbox: no destination for topic %q", msg.Topic)
	}
	return d.Next.Publish(ctx, msg)
}

// Relay publishes the pending events of an outbox, oldest first. Every
// replica of a service may run one; a lease in the outbox_lease collection
// lets only one of them publish at a time, which keeps the order per key.
//
// The pending events are read from the collection (the source of truth);
// a change stream only wakes the relay up early, and Interval bounds the
// delay when it is not available.
//
// The lease is per outbox collection: one service per database, as the Eve
// APIs are deployed (videosdb, specials, contents...).
type Relay struct {
	Outbox    *Outbox
	Publisher Publisher
	// Owner identifies the replica in the lease (default: the hostname).
	Owner string
	// Lease is how long a replica keeps the lease without renewing it
	// (default 30s). A replica that dies is replaced after this time.
	Lease time.Duration
	// Interval between scans when no insert is notified (default 5s).
	Interval time.Duration
	// Batch is the number of events read per query (default 100).
	Batch int
	// Retention keeps published events for inspection before the TTL
	// index removes them (default 7 days).
	Retention time.Duration
	// StaleAfter logs a warning when the oldest pending event is older
	// (default 1 minute).
	StaleAfter time.Duration
	// PublishTimeout bounds each publish (default 10s), so a stuck call
	// cannot outlive the lease.
	PublishTimeout time.Duration
	// MaxBackoff caps the wait between attempts of a failing event: 1s,
	// 2s, 4s... (default 5 minutes).
	MaxBackoff time.Duration

	once sync.Once
}

func (r *Relay) setDefaults() { r.once.Do(r.defaults) }

func (r *Relay) defaults() {
	if r.Owner == "" {
		r.Owner, _ = os.Hostname()
		r.Owner = fmt.Sprintf("%s-%d", r.Owner, os.Getpid())
	}
	if r.Lease <= 0 {
		r.Lease = 30 * time.Second
	}
	if r.Interval <= 0 {
		r.Interval = 5 * time.Second
	}
	if r.Batch <= 0 {
		r.Batch = 100
	}
	if r.Retention <= 0 {
		r.Retention = 7 * 24 * time.Hour
	}
	if r.StaleAfter <= 0 {
		r.StaleAfter = time.Minute
	}
	if r.PublishTimeout <= 0 {
		r.PublishTimeout = 10 * time.Second
	}
	if r.MaxBackoff <= 0 {
		r.MaxBackoff = 5 * time.Minute
	}
}

// EnsureIndexes creates the index of the pending scan and the TTL index of
// the published events.
func (r *Relay) EnsureIndexes(ctx context.Context) error {
	r.setDefaults()
	ttl := int32(r.Retention / time.Second)
	coll := r.Outbox.coll.Name()
	specs := []mongodb.IndexSpec{
		{Collection: coll, Keys: bson.D{{Key: "published_at", Value: 1}, {Key: "_id", Value: 1}}, Name: "pending"},
		{Collection: coll, Keys: bson.D{{Key: "published_at", Value: 1}}, Name: "published_at_ttl", TTL: &ttl},
	}
	_, err := mongodb.EnsureIndexes(ctx, r.Outbox.coll.Database(), specs, mongodb.IndexesCreate)
	return err
}

// Run publishes events until ctx is done.
func (r *Relay) Run(ctx context.Context) error {
	r.setDefaults()
	if err := r.EnsureIndexes(ctx); err != nil {
		return err
	}
	wake := make(chan struct{}, 1)
	go r.watch(ctx, wake)
	ticker := time.NewTicker(r.Interval)
	defer ticker.Stop()
	defer r.release()
	for {
		if _, err := r.Drain(ctx); err != nil && !errors.Is(err, ErrNotLeader) && ctx.Err() == nil {
			slog.Warn("outbox: relay pass failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-wake:
		case <-ticker.C:
		}
	}
}

// ErrNotLeader is returned by Drain when another replica holds the lease.
var ErrNotLeader = errors.New("outbox: lease held by another replica")

// Drain takes the lease and publishes every pending event once, in _id
// order. An event that fails is retried after a backoff; the later events
// with the same key wait for it, the others go on. The lease is renewed
// during the pass, and Drain stops as soon as it is lost.
func (r *Relay) Drain(ctx context.Context) (published int, err error) {
	r.setDefaults()
	blocked := map[string]bool{}
	var after bson.ObjectID
	var renewed time.Time
	// leading renews the lease when a third of it has passed
	leading := func() (bool, error) {
		if time.Since(renewed) < r.Lease/3 {
			return true, nil
		}
		ok, err := r.acquire(ctx)
		if ok {
			renewed = time.Now()
		}
		return ok, err
	}
	for {
		leader, err := leading()
		if err != nil {
			return published, err
		}
		if !leader {
			if published == 0 {
				return 0, ErrNotLeader
			}
			return published, nil
		}
		filter := bson.M{"published_at": nil}
		if !after.IsZero() {
			filter["_id"] = bson.M{"$gt": after}
		}
		cur, err := r.Outbox.coll.Find(ctx, filter,
			options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}).SetLimit(int64(r.Batch)))
		if err != nil {
			return published, err
		}
		var batch []record
		if err := cur.All(ctx, &batch); err != nil {
			return published, err
		}
		for _, rec := range batch {
			after = rec.ID
			if rec.Key != "" && blocked[rec.Key] {
				continue
			}
			if rec.NextAttemptAt != nil && rec.NextAttemptAt.After(time.Now()) {
				if rec.Key != "" {
					blocked[rec.Key] = true
				}
				continue
			}
			if leader, err := leading(); err != nil || !leader {
				return published, err
			}
			if err := r.publish(ctx, rec); err != nil {
				if ctx.Err() != nil {
					return published, ctx.Err()
				}
				if rec.Key != "" {
					blocked[rec.Key] = true
				}
				continue
			}
			published++
		}
		if len(batch) < r.Batch {
			r.warnIfStale(ctx)
			return published, nil
		}
	}
}

func (r *Relay) publish(ctx context.Context, rec record) error {
	attrs := map[string]string{}
	maps.Copy(attrs, rec.Attributes)
	attrs[AttrEventID] = rec.ID.Hex()
	attrs[AttrOccurredAt] = rec.OccurredAt.Format(time.RFC3339Nano)
	if rec.Type != "" {
		attrs[AttrEventType] = rec.Type
	}
	msg := Message{ID: rec.ID.Hex(), Topic: rec.Topic, Data: rec.Data, Attributes: attrs, OrderingKey: rec.Key}

	pctx, cancel := context.WithTimeout(ctx, r.PublishTimeout)
	err := r.Publisher.Publish(pctx, msg)
	cancel()
	if err != nil {
		wait := r.backoff(rec.Attempts + 1)
		slog.Warn("outbox: publish failed", "topic", rec.Topic, "event_id", msg.ID,
			"key", rec.Key, "attempt", rec.Attempts+1, "retry_in", wait.String(), "error", err)
		_, uerr := r.Outbox.coll.UpdateByID(context.WithoutCancel(ctx), rec.ID, bson.M{
			"$inc": bson.M{"attempts": 1},
			"$set": bson.M{"last_error": err.Error(), "next_attempt_at": time.Now().UTC().Add(wait)},
		})
		return errors.Join(err, uerr)
	}
	// a failure here republishes the event later: consumers deduplicate
	_, err = r.Outbox.coll.UpdateByID(context.WithoutCancel(ctx), rec.ID, bson.M{
		"$set": bson.M{"published_at": now()},
	})
	return err
}

// backoff is 1s, 2s, 4s... up to MaxBackoff.
func (r *Relay) backoff(attempt int) time.Duration {
	wait := time.Second
	for i := 1; i < attempt && wait < r.MaxBackoff; i++ {
		wait *= 2
	}
	return min(wait, r.MaxBackoff)
}

// OldestPending returns the age of the oldest event not published yet, or 0
// when there is none.
func (r *Relay) OldestPending(ctx context.Context) (time.Duration, error) {
	var rec record
	err := r.Outbox.coll.FindOne(ctx, bson.M{"published_at": nil},
		options.FindOne().SetSort(bson.D{{Key: "_id", Value: 1}})).Decode(&rec)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return time.Since(rec.OccurredAt), nil
}

func (r *Relay) warnIfStale(ctx context.Context) {
	age, err := r.OldestPending(ctx)
	if err == nil && age > r.StaleAfter {
		slog.Warn("outbox: pending events delayed", "collection", r.Outbox.coll.Name(), "oldest", age.Round(time.Second).String())
	}
}

func (r *Relay) leases() *mongo.Collection {
	return r.Outbox.coll.Database().Collection(r.Outbox.coll.Name() + "_lease")
}

// acquire takes or renews the lease.
func (r *Relay) acquire(ctx context.Context) (bool, error) {
	ts := time.Now().UTC()
	_, err := r.leases().UpdateOne(ctx,
		bson.M{"_id": r.Outbox.coll.Name(), "$or": bson.A{
			bson.M{"until": bson.M{"$lt": ts}},
			bson.M{"owner": r.Owner},
		}},
		bson.M{"$set": bson.M{"owner": r.Owner, "until": ts.Add(r.Lease)}},
		options.UpdateOne().SetUpsert(true))
	if mongo.IsDuplicateKeyError(err) {
		return false, nil
	}
	return err == nil, err
}

// release gives the lease up on shutdown so another replica takes over
// without waiting for it to expire.
func (r *Relay) release() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r.leases().UpdateOne(ctx, bson.M{"_id": r.Outbox.coll.Name(), "owner": r.Owner},
		bson.M{"$set": bson.M{"until": time.Unix(0, 0)}})
}

// watch signals inserts in the outbox. On a standalone Mongo, which has no
// change streams, the relay relies on Interval.
func (r *Relay) watch(ctx context.Context, wake chan<- struct{}) {
	pipeline := mongo.Pipeline{{{Key: "$match", Value: bson.M{"operationType": "insert"}}}}
	for ctx.Err() == nil {
		stream, err := r.Outbox.coll.Watch(ctx, pipeline)
		if err == nil {
			for stream.Next(ctx) {
				select {
				case wake <- struct{}{}:
				default:
				}
			}
			err = stream.Err()
			stream.Close(context.Background())
		}
		var cmdErr mongo.CommandError
		if errors.As(err, &cmdErr) && cmdErr.Code == 40573 { // change streams need a replica set
			return
		}
		if ctx.Err() == nil {
			slog.Debug("outbox: change stream interrupted", "error", err)
			select {
			case <-ctx.Done():
			case <-time.After(r.Interval):
			}
		}
	}
}
