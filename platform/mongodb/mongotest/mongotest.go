// Package mongotest prepares the Mongo of the tests: a server started with
// --replSet but not initiated yet (a CI service container) is initiated as a
// single node replica set, and every caller waits until it has a primary.
// It imports only the driver, so the tests of platform/mongodb can use it
// (testkit cannot be imported there).
package mongotest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// Server codes of replSetGetStatus and replSetInitiate.
const (
	codeNoReplication     = 76
	codeAlreadyInitiated  = 23
	codeNotYetInitialized = 94
)

// Prepare initiates the replica set when needed and waits for a writable
// primary. A standalone server (no --replSet) is left alone. Test packages
// run as parallel processes: another one may initiate first.
func Prepare(ctx context.Context, c *mongo.Client) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	admin := c.Database("admin")
	err := admin.RunCommand(ctx, bson.D{{Key: "replSetGetStatus", Value: 1}}).Err()
	var cmdErr mongo.CommandError
	switch {
	case errors.As(err, &cmdErr) && cmdErr.Code == codeNoReplication:
		return nil
	case errors.As(err, &cmdErr) && cmdErr.Code == codeNotYetInitialized:
		name := os.Getenv("MONGO_TEST_REPLSET")
		if name == "" {
			name = "rs0"
		}
		cfg := bson.D{{Key: "_id", Value: name}, {Key: "members", Value: bson.A{
			bson.D{{Key: "_id", Value: 0}, {Key: "host", Value: "localhost:27017"}},
		}}}
		err := admin.RunCommand(ctx, bson.D{{Key: "replSetInitiate", Value: cfg}}).Err()
		if err != nil && !(errors.As(err, &cmdErr) && cmdErr.Code == codeAlreadyInitiated) {
			return fmt.Errorf("replSetInitiate: %w", err)
		}
	}
	for ctx.Err() == nil {
		var hello struct {
			Primary bool `bson:"isWritablePrimary"`
		}
		if admin.RunCommand(ctx, bson.D{{Key: "hello", Value: 1}}).Decode(&hello) == nil && hello.Primary {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("mongo test server has no primary: %w", ctx.Err())
}
