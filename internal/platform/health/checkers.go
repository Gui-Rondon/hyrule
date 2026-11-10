package health

import (
	"context"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// Mongo is eve-healthcheck's database_check.
func Mongo(db *mongo.Database) Checker {
	return Checker{
		Name: "database_check",
		Check: func(ctx context.Context) (bool, string) {
			if err := db.RunCommand(ctx, bson.D{{Key: "ping", Value: 1}}).Err(); err != nil {
				return false, "Database NOK"
			}
			return true, "Database OK"
		},
	}
}
