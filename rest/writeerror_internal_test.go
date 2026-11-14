package rest

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"testing"
)

// boomErr is errorAdapter's one sentinel: every CRUD method that
// writes returns it, so isTimeout/isDuplicate alone decide how
// writeError/timeoutError (rest/service.go) classify it.
var boomErr = errors.New("errorAdapter: boom")

// errorAdapter is a minimal Adapter double, in the same style as
// fakeRetryAdapter, built only to pin writeError/timeoutError's HTTP
// mapping (section 12 of the Postgres portability plan): a backend
// error an Adapter recognizes as IsTimeout (Postgres's statement
// timeout, deadlock or serialization failure, all folded into
// postgresAdapter.IsTimeout -- restpostgres/errors.go) must become
// 503 on every write path, not just reads (queryError already covered
// those).
type errorAdapter struct {
	isTimeout, isDuplicate bool
	// alwaysTimeout, when set, makes IsTimeout ignore err entirely --
	// only TestTimeoutErrorExcludesBareContextErrors uses this, to
	// confirm timeoutError's own context.DeadlineExceeded/Canceled
	// exclusion applies even when the Adapter's IsTimeout would
	// otherwise say true for literally anything.
	alwaysTimeout bool
}

func (a *errorAdapter) IsNotFound(error) bool { return false }
func (a *errorAdapter) IsDuplicateKey(err error) bool {
	return a.isDuplicate && errors.Is(err, boomErr)
}
func (a *errorAdapter) IsClientError(error) bool { return false }
func (a *errorAdapter) IsTimeout(err error) bool {
	return a.alwaysTimeout || (a.isTimeout && errors.Is(err, boomErr))
}
func (a *errorAdapter) ValidatesMergedDocument() bool { return false }

// ClassifiesWriteTimeouts is always true: this fake exists to pin
// writeError/timeoutError's own classification logic, not the gate
// itself -- mongoAdapter's/postgresAdapter's false/true cases are
// covered directly on those production types (rest/mongoadapter.go,
// restpostgres/errors.go).
func (a *errorAdapter) ClassifiesWriteTimeouts() bool { return true }

func (a *errorAdapter) NewID() any                          { return "fake-id" }
func (a *errorAdapter) IDString(id any) string              { return fmt.Sprint(id) }
func (a *errorAdapter) LookupByID(value string) (any, bool) { return value, true }
func (a *errorAdapter) LookupID(id any) any                 { return id }
func (a *errorAdapter) IDFilter(id any) (any, error)        { return id, nil }
func (a *errorAdapter) RetryPolicy() RetryOptions           { return RetryOptions{} }

func (a *errorAdapter) ParseWhere(string, *Resource) (any, error) { return nil, nil }
func (a *errorAdapter) ParseSort(string) (any, error)             { return nil, nil }
func (a *errorAdapter) ParseProjection(string) (any, error)       { return nil, nil }
func (a *errorAdapter) CombineFilter(*Resource, any, bool) (any, error) {
	return nil, nil
}
func (a *errorAdapter) CombineSort(*Resource, any) any { return nil }
func (a *errorAdapter) CombineProjection(*Resource, any) (any, error) {
	return nil, nil
}
func (a *errorAdapter) WriteLookup(original Doc) (any, error) { return original["_etag"], nil }

func (a *errorAdapter) Find(context.Context, any, any, any, int64, int64) ([]Doc, error) {
	return nil, errors.New("errorAdapter: Find not implemented")
}
func (a *errorAdapter) CountDocuments(context.Context, any) (int64, error) {
	return 0, errors.New("errorAdapter: CountDocuments not implemented")
}
func (a *errorAdapter) FindOne(ctx context.Context, lookup, projection any) (Doc, error) {
	return Doc{"_id": "fake-id", "_etag": "1", "name": "a"}, nil
}
func (a *errorAdapter) InsertMany(context.Context, []Doc) error { return boomErr }
func (a *errorAdapter) UpdateOne(context.Context, any, Doc, Doc) (int64, error) {
	return 0, boomErr
}
func (a *errorAdapter) ReplaceOne(context.Context, any, Doc) (int64, error) {
	return 0, boomErr
}
func (a *errorAdapter) DeleteOne(context.Context, any) (int64, error) {
	return 0, boomErr
}

func (a *errorAdapter) MergePatch(original, changes Doc) (Doc, error) {
	merged := maps.Clone(original)
	maps.Copy(merged, changes)
	return merged, nil
}
func (a *errorAdapter) EqualsFilter(string, any, any) (any, error) { return nil, nil }

var _ Adapter = (*errorAdapter)(nil)

func newErrorAdapterService(isTimeout, isDuplicate bool, softDelete bool) *Service {
	adapter := &errorAdapter{isTimeout: isTimeout, isDuplicate: isDuplicate}
	res := &Resource{Adapter: adapter, AllowUnknown: true, SoftDelete: softDelete}
	res.setDefaults()
	return NewService(res, NoTransaction{})
}

func want503(t *testing.T, err error) {
	t.Helper()
	e, ok := errors.AsType[*Error](err)
	if !ok || e.Status != http.StatusServiceUnavailable {
		t.Fatalf("err = %#v, want a 503 Error", err)
	}
}

func TestInsertTimeoutBecomes503(t *testing.T) {
	svc := newErrorAdapterService(true, false, false)
	_, err := svc.Insert(context.Background(), []Doc{{"name": "a"}}, WriteOptions{})
	want503(t, err)
}

func TestInsertTimeoutFalseLeavesErrorUnclassified(t *testing.T) {
	svc := newErrorAdapterService(false, false, false)
	_, err := svc.Insert(context.Background(), []Doc{{"name": "a"}}, WriteOptions{})
	if !errors.Is(err, boomErr) {
		t.Fatalf("err = %v, want boomErr unwrapped (WriteError's default 500 path), not a classified *Error", err)
	}
}

func TestPatchTimeoutBecomes503(t *testing.T) {
	svc := newErrorAdapterService(true, false, false)
	_, err := svc.Patch(context.Background(), "fake-id", Doc{"name": "b"}, WriteOptions{SkipConcurrency: true})
	want503(t, err)
}

func TestReplaceTimeoutBecomes503(t *testing.T) {
	svc := newErrorAdapterService(true, false, false)
	_, err := svc.Replace(context.Background(), "fake-id", Doc{"name": "b"}, WriteOptions{SkipConcurrency: true})
	want503(t, err)
}

func TestDeleteTimeoutBecomes503(t *testing.T) {
	svc := newErrorAdapterService(true, false, false)
	err := svc.Delete(context.Background(), "fake-id", WriteOptions{SkipConcurrency: true})
	want503(t, err)
}

// TestDeleteTimeoutBecomes503SoftDelete covers Delete's other write
// call site (UpdateOne, for SoftDelete) -- before this PR, both of
// Delete's write call sites returned the backend error completely
// unclassified, so a timeout there fell through to WriteError's
// generic 500, never the 503 section 12 of the Postgres portability
// plan calls for.
func TestDeleteTimeoutBecomes503SoftDelete(t *testing.T) {
	svc := newErrorAdapterService(true, false, true)
	err := svc.Delete(context.Background(), "fake-id", WriteOptions{SkipConcurrency: true})
	want503(t, err)
}

// TestInsertDuplicateKeyTakesPriorityOverTimeout: when an Adapter
// happens to classify the same error as both (never true for any real
// adapter's own codes today, but writeError's order must still be
// deterministic), the more specific, client-actionable duplicate-key
// status wins over the generic 503.
func TestInsertDuplicateKeyTakesPriorityOverTimeout(t *testing.T) {
	svc := newErrorAdapterService(true, true, false)
	_, err := svc.Insert(context.Background(), []Doc{{"name": "a"}}, WriteOptions{})
	e, ok := errors.AsType[*Error](err)
	if !ok || e.Status != http.StatusConflict {
		t.Fatalf("err = %#v, want a 409 Error (duplicate key), not 503", err)
	}
}

// TestTimeoutErrorExcludesBareContextErrors pins the exclusion
// timeoutError applies before ever trusting the Adapter's own
// IsTimeout on a write: a bare context.DeadlineExceeded/Canceled must
// stay unclassified even when IsTimeout would say true for literally
// anything (alwaysTimeout). This is the regression this PR's first
// version produced before the exclusion existed: it reused IsTimeout
// for writes unconditionally, and both mongoAdapter's and
// postgresAdapter's own IsTimeout fall back to exactly
// errors.Is(err, context.DeadlineExceeded) for an error neither
// recognizes by its backend-specific signal -- turning
// rest/dryrun_test.go's TestDryRunTimeout (a write cut by
// DryRunTimeout, pinned to a plain 500, "as any write error") into a
// 503.
func TestTimeoutErrorExcludesBareContextErrors(t *testing.T) {
	adapter := &errorAdapter{alwaysTimeout: true}
	res := &Resource{Adapter: adapter, AllowUnknown: true}
	res.setDefaults()
	svc := NewService(res, NoTransaction{})

	if got := svc.timeoutError(context.DeadlineExceeded); !errors.Is(got, context.DeadlineExceeded) {
		t.Errorf("timeoutError(context.DeadlineExceeded) = %#v, want it returned unclassified", got)
	}
	if got := svc.timeoutError(context.Canceled); !errors.Is(got, context.Canceled) {
		t.Errorf("timeoutError(context.Canceled) = %#v, want it returned unclassified", got)
	}
	// A genuine backend signal (boomErr, standing in for a real
	// pgconn.PgError/mongo timeout) is unaffected by the exclusion.
	want503(t, svc.timeoutError(boomErr))
}
