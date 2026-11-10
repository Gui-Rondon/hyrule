// Package outbox implements the transactional outbox (D-10, D-23): a write
// stores its events in the outbox collection in the same Mongo transaction,
// and the Relay publishes them after the commit. A write and its events are
// stored together or not at all; delivery is at least once, so every message
// carries the event_id attribute for the consumers to deduplicate.
//
// Events are added from the pre-write hooks of a resource, which run inside
// the write transaction:
//
//	res.Hooks.OnUpdate = append(res.Hooks.OnUpdate, func(ctx context.Context, updates, original rest.Doc) error {
//		return box.Add(ctx, outbox.Event{Topic: "videos-to-publish", Key: id, Body: msg})
//	})
package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// Collection is the default outbox collection.
const Collection = "outbox"

// Message attributes added to every event. The body stays as the consumers
// read it today; only attributes are new.
const (
	AttrEventID    = "event_id"
	AttrEventType  = "event_type"
	AttrOccurredAt = "occurred_at"
)

// Event is a message to publish once the write commits.
type Event struct {
	// Topic is the Pub/Sub topic, or a local destination registered in the
	// Relay (see Dispatcher).
	Topic string
	// Type goes to the event_type attribute, e.g. "video.publish_requested".
	Type string
	// Key orders the events of one aggregate (the document id): events with
	// the same key are published in the order they were added.
	Key string
	// Body is the message data. A []byte or string is sent as is; any other
	// value is encoded as JSON.
	Body any
	// Attributes are extra message attributes.
	Attributes map[string]string
}

// record is an Event as stored in the outbox collection.
type record struct {
	ID          bson.ObjectID     `bson:"_id"`
	Topic       string            `bson:"topic"`
	Type        string            `bson:"type,omitempty"`
	Key         string            `bson:"key,omitempty"`
	Data        []byte            `bson:"data"`
	Attributes  map[string]string `bson:"attributes,omitempty"`
	OccurredAt  time.Time         `bson:"occurred_at"`
	PublishedAt *time.Time        `bson:"published_at"`
	Attempts    int               `bson:"attempts"`
	LastError   string            `bson:"last_error,omitempty"`
	// NextAttemptAt is set after a failure (backoff).
	NextAttemptAt *time.Time `bson:"next_attempt_at,omitempty"`
}

// ErrNoTransaction is returned by Add outside a transaction: the events
// would be stored even if the write that produced them failed.
var ErrNoTransaction = errors.New("outbox: Add must run inside the write transaction (pre-write hook on a replica set)")

// Outbox stores events in a collection.
type Outbox struct {
	coll *mongo.Collection
	// AllowNoTransaction lets Add run without a transaction, for a
	// standalone Mongo in development. Never in production.
	AllowNoTransaction bool
	// InTransaction reports whether ctx carries the write transaction Add
	// must run inside. New sets the Mongo-specific default
	// (mongo.SessionFromContext); a storage backend other than Mongo would
	// set its own, so this check is not hardcoded in Add itself.
	InTransaction func(ctx context.Context) bool
}

// New returns the outbox of db, in the "outbox" collection.
func New(db *mongo.Database) *Outbox {
	return &Outbox{coll: db.Collection(Collection), InTransaction: mongoInTransaction}
}

// Collection returns the outbox collection.
func (o *Outbox) Collection() *mongo.Collection { return o.coll }

var now = func() time.Time { return time.Now().UTC().Truncate(time.Millisecond) }

// Add stores events in the transaction of ctx.
func (o *Outbox) Add(ctx context.Context, events ...Event) error {
	if len(events) == 0 {
		return nil
	}
	if !o.AllowNoTransaction && !o.InTransaction(ctx) {
		return ErrNoTransaction
	}
	docs := make([]any, len(events))
	ts := now()
	for i, e := range events {
		if e.Topic == "" {
			return errors.New("outbox: event without topic")
		}
		data, err := encode(e.Body)
		if err != nil {
			return fmt.Errorf("outbox: encode %s event: %w", e.Topic, err)
		}
		docs[i] = record{
			ID:         bson.NewObjectID(),
			Topic:      e.Topic,
			Type:       e.Type,
			Key:        e.Key,
			Data:       data,
			Attributes: e.Attributes,
			OccurredAt: ts,
		}
	}
	_, err := o.coll.InsertMany(ctx, docs)
	return err
}

func mongoInTransaction(ctx context.Context) bool {
	sess := mongo.SessionFromContext(ctx)
	return sess != nil && sess.TransactionRunning()
}

func encode(body any) ([]byte, error) {
	switch b := body.(type) {
	case []byte:
		return b, nil
	case string:
		return []byte(b), nil
	case nil:
		return []byte{}, nil
	default:
		return json.Marshal(b)
	}
}
