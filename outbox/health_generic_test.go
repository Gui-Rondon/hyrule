package outbox_test

import (
	"context"
	"testing"
	"time"

	"github.com/Gui-Rondon/hyrule/outbox"
	"github.com/Gui-Rondon/hyrule/platform/config"
)

// fakeStatsProvider proves Check/CheckFromConfig work with any
// outbox.StatsProvider, not just a concrete *outbox.Relay -- the
// generalization outboxpostgres.Relay (C2) relies on.
type fakeStatsProvider struct {
	stats outbox.Stats
	err   error
}

func (f fakeStatsProvider) Stats(context.Context) (outbox.Stats, error) { return f.stats, f.err }

func TestCheckWithNonRelayStatsProvider(t *testing.T) {
	ok, msg := outbox.Check(fakeStatsProvider{stats: outbox.Stats{Pending: 2, Retrying: 1, Oldest: time.Second}}, time.Minute).Check(context.Background())
	if !ok {
		t.Errorf("ok = false, msg = %q", msg)
	}

	ok, _ = outbox.Check(fakeStatsProvider{stats: outbox.Stats{Pending: 1, Oldest: time.Hour}}, time.Minute).Check(context.Background())
	if ok {
		t.Error("ok = true, want false (oldest pending way past maxDelay)")
	}

	checker, err := outbox.CheckFromConfig(config.Config{}, fakeStatsProvider{})
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := checker.Check(context.Background()); !ok {
		t.Error("CheckFromConfig with no pending events = false")
	}
}
