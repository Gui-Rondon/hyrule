package rest

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strconv"
	"testing"
	"time"
)

// fakeRetryAdapter is a minimal, in-memory Adapter double used only to
// drive patchOnce's retry loop (Patch) deterministically -- real
// concurrency (two real clients racing a real backend) can't reliably
// produce "exactly N conflicts, then success" on demand, which is what
// the retry loop's own test matrix (section 9.7 of the Postgres
// portability plan) needs. forceConflicts UpdateOne calls report
// matched=0 (a lost optimistic-concurrency race); the next one
// succeeds. updateCalls counts every UpdateOne call, so a test can
// assert exactly how many patchOnce attempts ran.
type fakeRetryAdapter struct {
	doc            Doc
	forceConflicts int
	updateCalls    int
	retry          RetryOptions
	// validatesMerged is ValidatesMergedDocument's return value --
	// false by default (every existing retry test), matching Mongo's
	// contract; set true only by the patch-merge-validation tests
	// (rest/patchmerge_internal_test.go), which need MergePatch's real
	// RFC 7396 null-deletes-the-key semantics below to produce
	// anything for a post-merge check to catch.
	validatesMerged bool
}

func (a *fakeRetryAdapter) IsNotFound(error) bool         { return false }
func (a *fakeRetryAdapter) IsDuplicateKey(error) bool     { return false }
func (a *fakeRetryAdapter) IsClientError(error) bool      { return false }
func (a *fakeRetryAdapter) IsTimeout(error) bool          { return false }
func (a *fakeRetryAdapter) ClassifiesWriteTimeouts() bool { return false }

func (a *fakeRetryAdapter) ValidatesMergedDocument() bool { return a.validatesMerged }

func (a *fakeRetryAdapter) NewID() any                          { return "fake-id" }
func (a *fakeRetryAdapter) IDString(id any) string              { return fmt.Sprint(id) }
func (a *fakeRetryAdapter) LookupByID(value string) (any, bool) { return value, true }
func (a *fakeRetryAdapter) LookupID(id any) any                 { return id }
func (a *fakeRetryAdapter) IDFilter(id any) (any, error)        { return id, nil }
func (a *fakeRetryAdapter) RetryPolicy() RetryOptions           { return a.retry }

func (a *fakeRetryAdapter) ParseWhere(string, *Resource) (any, error) { return nil, nil }
func (a *fakeRetryAdapter) ParseSort(string) (any, error)             { return nil, nil }
func (a *fakeRetryAdapter) ParseProjection(string) (any, error)       { return nil, nil }
func (a *fakeRetryAdapter) CombineFilter(*Resource, any, bool) (any, error) {
	return nil, nil
}
func (a *fakeRetryAdapter) CombineSort(*Resource, any) any { return nil }
func (a *fakeRetryAdapter) CombineProjection(*Resource, any) (any, error) {
	return nil, nil
}
func (a *fakeRetryAdapter) WriteLookup(original Doc) (any, error) {
	return original["_etag"], nil
}

func (a *fakeRetryAdapter) Find(context.Context, any, any, any, int64, int64) ([]Doc, error) {
	return nil, errors.New("fakeRetryAdapter: Find not implemented")
}
func (a *fakeRetryAdapter) CountDocuments(context.Context, any) (int64, error) {
	return 0, errors.New("fakeRetryAdapter: CountDocuments not implemented")
}
func (a *fakeRetryAdapter) FindOne(ctx context.Context, lookup, projection any) (Doc, error) {
	return maps.Clone(a.doc), nil
}
func (a *fakeRetryAdapter) InsertMany(context.Context, []Doc) error {
	return errors.New("fakeRetryAdapter: InsertMany not implemented")
}

// UpdateOne simulates a lost optimistic-concurrency race for the first
// forceConflicts calls (matched=0, no error, same contract real
// adapters use -- Service's checkMatched decides what that means), then
// applies updated for real from the next call on.
func (a *fakeRetryAdapter) UpdateOne(ctx context.Context, lookup any, updated, changes Doc) (int64, error) {
	a.updateCalls++
	if a.updateCalls <= a.forceConflicts {
		return 0, nil
	}
	version, _ := strconv.Atoi(a.doc["_etag"].(string))
	updated["_etag"] = strconv.Itoa(version + 1)
	updated["_updated"] = time.Now().UTC()
	a.doc = maps.Clone(updated)
	return 1, nil
}
func (a *fakeRetryAdapter) ReplaceOne(context.Context, any, Doc) (int64, error) {
	return 0, errors.New("fakeRetryAdapter: ReplaceOne not implemented")
}
func (a *fakeRetryAdapter) DeleteOne(context.Context, any) (int64, error) {
	return 0, errors.New("fakeRetryAdapter: DeleteOne not implemented")
}

// MergePatch applies changes as an RFC 7396 merge, same as
// postgresAdapter's real one (restpostgres/patch.go): a nil value
// deletes the key, anything else overwrites it -- never a mere
// maps.Copy, which would just store a literal nil and never delete
// anything, the Mongo-only behavior this fake must not default to
// once a test turns validatesMerged on.
func (a *fakeRetryAdapter) MergePatch(original, changes Doc) (Doc, error) {
	merged := maps.Clone(original)
	for k, v := range changes {
		if v == nil {
			delete(merged, k)
			continue
		}
		merged[k] = v
	}
	return merged, nil
}
func (a *fakeRetryAdapter) EqualsFilter(string, any, any) (any, error) { return nil, nil }

var _ Adapter = (*fakeRetryAdapter)(nil)

// newRetryTestService builds a Service over a fakeRetryAdapter seeded
// with one document at _etag "1", with retry configured as retry
// (RetryOptions{} disables the loop entirely, same as mongoAdapter).
func newRetryTestService(retry RetryOptions, forceConflicts int) (*Service, *fakeRetryAdapter) {
	adapter := &fakeRetryAdapter{
		doc:            Doc{"_id": "fake-id", "_etag": "1", "name": "a"},
		forceConflicts: forceConflicts,
		retry:          retry,
	}
	res := &Resource{Adapter: adapter, AllowUnknown: true}
	res.setDefaults()
	return NewService(res, NoTransaction{}), adapter
}

// TestPatchWildcardWithoutRetryIsAPlain412 covers MaxAttempts=0: a
// single attempt, a conflict there is the usual 412, never a 503 --
// "exhausting retries" makes no sense when zero were configured
// (section 9.7).
func TestPatchWildcardWithoutRetryIsAPlain412(t *testing.T) {
	retry := RetryOptions{MaxAttempts: 0, BaseBackoff: time.Millisecond, MaxBackoff: time.Millisecond, MaxDuration: time.Second}
	svc, adapter := newRetryTestService(retry, 1)
	match, _ := ParseIfMatch([]string{"*"})

	_, err := svc.Patch(context.Background(), "fake-id", Doc{"name": "b"}, WriteOptions{IfMatch: match})
	if _, ok := errors.AsType[*PreconditionFailedError](err); !ok {
		t.Fatalf("err = %v, want *PreconditionFailedError", err)
	}
	if adapter.updateCalls != 1 {
		t.Errorf("UpdateOne called %d times, want exactly 1 (no retry loop)", adapter.updateCalls)
	}
}

// TestPatchWildcardRetriesUntilSuccess covers the loop's main job:
// enough attempts configured, enough to outlast forceConflicts.
func TestPatchWildcardRetriesUntilSuccess(t *testing.T) {
	retry := RetryOptions{MaxAttempts: 5, BaseBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond, MaxDuration: time.Second}
	svc, adapter := newRetryTestService(retry, 3)
	match, _ := ParseIfMatch([]string{"*"})

	updated, err := svc.Patch(context.Background(), "fake-id", Doc{"name": "b"}, WriteOptions{IfMatch: match})
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	if updated["name"] != "b" {
		t.Errorf("updated[name] = %v, want b", updated["name"])
	}
	// 3 forced conflicts + the attempt that actually succeeds
	if adapter.updateCalls != 4 {
		t.Errorf("UpdateOne called %d times, want exactly 4", adapter.updateCalls)
	}
}

// TestPatchWildcardMaxAttemptsExhausted covers MaxAttempts=1: a
// conflict on both the initial attempt and its one retry -> 503,
// category "max_attempts" -- never 412, never "max_duration".
func TestPatchWildcardMaxAttemptsExhausted(t *testing.T) {
	retry := RetryOptions{MaxAttempts: 1, BaseBackoff: time.Millisecond, MaxBackoff: time.Millisecond, MaxDuration: time.Second}
	svc, adapter := newRetryTestService(retry, 99) // always conflicts
	match, _ := ParseIfMatch([]string{"*"})

	_, err := svc.Patch(context.Background(), "fake-id", Doc{"name": "b"}, WriteOptions{IfMatch: match})
	e, ok := errors.AsType[*Error](err)
	if !ok || e.Status != 503 || e.RetryExhaustedReason != "max_attempts" {
		t.Fatalf("err = %#v, want a 503 Error with RetryExhaustedReason=max_attempts", err)
	}
	if adapter.updateCalls != 2 {
		t.Errorf("UpdateOne called %d times, want exactly 2 (1 initial + 1 retry)", adapter.updateCalls)
	}
}

// TestPatchWildcardDefaultRetryExhausted covers the default
// RetryOptions (MaxAttempts 5): a conflict on every one of the 6
// attempts (1 initial + 5 retries) -> 503 max_attempts, exactly 6
// patchOnce calls, not 5 and not 7.
func TestPatchWildcardDefaultRetryExhausted(t *testing.T) {
	retry := DefaultRetryOptions()
	retry.BaseBackoff, retry.MaxBackoff = time.Millisecond, 2*time.Millisecond
	svc, adapter := newRetryTestService(retry, 99)
	match, _ := ParseIfMatch([]string{"*"})

	_, err := svc.Patch(context.Background(), "fake-id", Doc{"name": "b"}, WriteOptions{IfMatch: match})
	e, ok := errors.AsType[*Error](err)
	if !ok || e.Status != 503 || e.RetryExhaustedReason != "max_attempts" {
		t.Fatalf("err = %#v, want a 503 Error with RetryExhaustedReason=max_attempts", err)
	}
	if adapter.updateCalls != 6 {
		t.Errorf("UpdateOne called %d times, want exactly 6 (1 initial + 5 retries)", adapter.updateCalls)
	}
}

// TestPatchWildcardMaxDurationExhausted covers the other exhaustion
// category: the retry budget's own context deadline, not a count of
// attempts -- RetryExhaustedReason must say "max_duration", and the
// loop must give up well before MaxAttempts would have run out on its
// own (a conflict every time, but each backoff alone outlasts
// MaxDuration).
func TestPatchWildcardMaxDurationExhausted(t *testing.T) {
	retry := RetryOptions{MaxAttempts: 1000, BaseBackoff: 50 * time.Millisecond, MaxBackoff: 50 * time.Millisecond, MaxDuration: 30 * time.Millisecond}
	svc, _ := newRetryTestService(retry, 99999)
	match, _ := ParseIfMatch([]string{"*"})

	_, err := svc.Patch(context.Background(), "fake-id", Doc{"name": "b"}, WriteOptions{IfMatch: match})
	e, ok := errors.AsType[*Error](err)
	if !ok || e.Status != 503 || e.RetryExhaustedReason != "max_duration" {
		t.Fatalf("err = %#v, want a 503 Error with RetryExhaustedReason=max_duration", err)
	}
}

// TestPatchPlainIfMatchNeverRetries covers the common case: a plain
// If-Match (one concrete entity-tag), not the wildcard -- the loop
// must never engage even when RetryPolicy allows it, since the client
// did name the exact version it expects.
func TestPatchPlainIfMatchNeverRetries(t *testing.T) {
	retry := DefaultRetryOptions()
	svc, adapter := newRetryTestService(retry, 1)
	match, _ := ParseIfMatch([]string{`"1"`})

	_, err := svc.Patch(context.Background(), "fake-id", Doc{"name": "b"}, WriteOptions{IfMatch: match})
	if _, ok := errors.AsType[*PreconditionFailedError](err); !ok {
		t.Fatalf("err = %v, want *PreconditionFailedError", err)
	}
	if adapter.updateCalls != 1 {
		t.Errorf("UpdateOne called %d times, want exactly 1 (plain If-Match never retries)", adapter.updateCalls)
	}
}

// TestPatchWildcardOriginalContextCanceledPropagates covers
// classifyContextErr's first branch: the caller's own context, not the
// retry budget, is why this failed -- must propagate as ctx.Err(),
// never a 503.
func TestPatchWildcardOriginalContextCanceledPropagates(t *testing.T) {
	retry := RetryOptions{MaxAttempts: 5, BaseBackoff: 20 * time.Millisecond, MaxBackoff: 20 * time.Millisecond, MaxDuration: time.Second}
	svc, _ := newRetryTestService(retry, 99999)
	match, _ := ParseIfMatch([]string{"*"})

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(5 * time.Millisecond); cancel() }()

	_, err := svc.Patch(ctx, "fake-id", Doc{"name": "b"}, WriteOptions{IfMatch: match})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if _, ok := errors.AsType[*Error](err); ok {
		t.Fatalf("err = %#v, must not be classified as a retry-exhaustion Error", err)
	}
}
