package mongodb

import (
	"context"
	"errors"
	"testing"
)

func TestStandaloneTransactorPreservesContextAndErrors(t *testing.T) {
	ctx := context.WithValue(context.Background(), struct{}{}, "marker")
	sentinel := errors.New("write failed")
	for _, want := range []error{nil, sentinel} {
		called := false
		got := (direct{}).WithTransaction(ctx, func(actual context.Context) error {
			called = true
			if actual != ctx {
				t.Fatal("context replaced")
			}
			return want
		})
		if !called || !errors.Is(got, want) {
			t.Fatalf("callback/error contract: %v", got)
		}
	}
}
