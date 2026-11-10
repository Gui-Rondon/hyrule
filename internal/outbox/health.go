package outbox

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/Gui-Rondon/hyrule/internal/platform/config"
	"github.com/Gui-Rondon/hyrule/internal/platform/health"
)

// Stats describe the events not published yet.
type Stats struct {
	Pending  int64
	Retrying int64 // pending events that failed at least once
	Oldest   time.Duration
}

// Stats counts the pending events (index pending) and the age of the oldest.
func (r *Relay) Stats(ctx context.Context) (Stats, error) {
	var s Stats
	var err error
	if s.Pending, err = r.Outbox.coll.CountDocuments(ctx, bson.M{"published_at": nil}); err != nil {
		return s, err
	}
	if s.Pending == 0 {
		return s, nil
	}
	if s.Retrying, err = r.Outbox.coll.CountDocuments(ctx, bson.M{"published_at": nil, "attempts": bson.M{"$gt": 0}}); err != nil {
		return s, err
	}
	s.Oldest, err = r.OldestPending(ctx)
	return s, err
}

// Check is the outbox_check of /healthcheck: it fails when the oldest
// pending event is older than maxDelay, i.e. the relay is stuck or the
// destination keeps failing.
func Check(r *Relay, maxDelay time.Duration) health.Checker {
	return health.Checker{
		Name: "outbox_check",
		Check: func(ctx context.Context) (bool, string) {
			s, err := r.Stats(ctx)
			if err != nil {
				return false, "Outbox Nok, " + err.Error()
			}
			if s.Pending == 0 {
				return true, "Outbox OK, 0 pending"
			}
			ok := s.Oldest <= maxDelay
			status := "OK"
			if !ok {
				status = "Nok"
			}
			return ok, fmt.Sprintf("Outbox %s, %d pending (%d retrying), oldest %s", status, s.Pending, s.Retrying, s.Oldest.Round(time.Second))
		},
	}
}

// defaultMaxDelay is outbox.max_delay's default.
const defaultMaxDelay = 5 * time.Minute

// CheckFromConfig is Check with outbox.max_delay (a Go duration, default 5m).
func CheckFromConfig(cfg config.Config, r *Relay) (health.Checker, error) {
	d, err := time.ParseDuration(cfg.String("outbox.max_delay", defaultMaxDelay.String()))
	if err != nil {
		return health.Checker{}, fmt.Errorf("outbox.max_delay: %w", err)
	}
	return Check(r, d), nil
}
