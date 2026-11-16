package rest

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestQueryContextClassificationPreservesHTTPAndCause(t *testing.T) {
	svc := NewService(&Resource{Adapter: &errorAdapter{alwaysTimeout: true}}, NoTransaction{})
	cause := context.DeadlineExceeded
	classified := svc.queryError(cause)
	var httpErr *Error
	if !errors.Is(classified, cause) || !errors.As(classified, &httpErr) || httpErr.Status != 503 || classified.Error() != http.StatusText(503) {
		t.Fatalf("classification lost information: %#v", classified)
	}
	rec := httptest.NewRecorder()
	WriteError(rec, httptest.NewRequest("GET", "/items", nil), classified)
	if rec.Code != 503 {
		t.Fatalf("HTTP status=%d", rec.Code)
	}
	live := context.Background()
	expired, cancel := context.WithCancel(live)
	cancel()
	got := classifyContextErr(live, expired, classified)
	var budget *Error
	if !errors.As(got, &budget) || budget.RetryExhaustedReason != "max_duration" {
		t.Fatalf("budget cause lost: %v", got)
	}
	if got := classifyContextErr(expired, expired, classified); !errors.Is(got, context.Canceled) {
		t.Fatalf("caller cancellation lost: %v", got)
	}
	if got := classifyContextErr(live, live, classified); got != classified {
		t.Fatal("independent query timeout attributed to retry")
	}
	// Backend timeouts are still classified as 503 without inventing a context cause.
	backend := svc.queryError(boomErr)
	if errors.Is(backend, cause) {
		t.Fatal("backend timeout gained a deadline cause")
	}
	if got := classifyContextErr(live, expired, backend); got != backend {
		t.Fatal("backend timeout attributed to retry")
	}
}
