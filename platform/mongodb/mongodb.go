// Package mongodb connects to Mongo with the settings read by the Eve APIs.
package mongodb

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"strings"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/Gui-Rondon/hyrule/platform/config"
)

// URI resolves the connection string with the precedence of settings.py:
// MONGO_CONNECTION_STRING, then mongo.uri, then host/port/username/password
// (base64 encoded)/dbname.
func URI(cfg config.Config, defaultDB string) (uri, dbName string, err error) {
	mongoCfg := cfg.Sub("mongo")
	switch {
	case os.Getenv("MONGO_CONNECTION_STRING") != "":
		uri = os.Getenv("MONGO_CONNECTION_STRING")
	case mongoCfg.String("uri", "") != "":
		uri = mongoCfg.String("uri", "")
	default:
		host := mongoCfg.String("host", "mongodb")
		port := mongoCfg.String("port", "27017")
		dbName = mongoCfg.String("dbname", defaultDB)
		userinfo := ""
		if user := mongoCfg.String("username", ""); user != "" {
			pass := mongoCfg.String("password", "")
			if pass != "" {
				decoded, err := base64.StdEncoding.DecodeString(pass)
				if err != nil {
					return "", "", fmt.Errorf("mongodb: decode password: %w", err)
				}
				pass = strings.TrimSpace(string(decoded))
			}
			userinfo = url.UserPassword(user, pass).String() + "@"
		}
		return fmt.Sprintf("mongodb://%s%s:%s/%s", userinfo, host, port, dbName), dbName, nil
	}

	// Flask-PyMongo uses the database of the URI
	parsed, err := url.Parse(uri)
	if err == nil {
		dbName = strings.TrimPrefix(parsed.Path, "/")
	}
	if dbName == "" {
		dbName = defaultDB
	}
	return uri, dbName, nil
}

// Connect opens the client and returns the database; extra options apply
// after the URI (e.g. the dry run WriteGuard monitor).
func Connect(ctx context.Context, cfg config.Config, defaultDB string, extra ...*options.ClientOptions) (*mongo.Client, *mongo.Database, error) {
	uri, dbName, err := URI(cfg, defaultDB)
	if err != nil {
		return nil, nil, err
	}
	client, err := mongo.Connect(append([]*options.ClientOptions{options.Client().ApplyURI(uri)}, extra...)...)
	if err != nil {
		return nil, nil, fmt.Errorf("mongodb: connect: %w", err)
	}
	if err := client.Ping(ctx, nil); err != nil {
		return nil, nil, fmt.Errorf("mongodb: ping: %w", err)
	}
	return client, client.Database(dbName), nil
}
