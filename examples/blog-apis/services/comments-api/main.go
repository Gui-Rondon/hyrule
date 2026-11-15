// Command comments-api is Fase 7's end-to-end proof for the Postgres
// path: a real resource (blog comments) served over real HTTP by a
// real binary, backed by platform/postgres+restpostgres instead of
// Mongo. B3 of the "postgres em produção" plan moved it onto the
// shared app.Main bootstrap (app.BackendPostgres), the same one every
// Mongo-backed service in this directory already uses -- see
// tags-api's main.go for the pattern this converges to.
//
// Run comments-migrate explicitly first (see ../../internal/commentsschema).
//
//	POSTGRES_CONNECTION_STRING='postgres://postgres:postgres@localhost:5433/postgres?sslmode=disable' \
//	  PUBSUB_EMULATOR_HOST=localhost:8085 CONFIG_FILE=config.json PORT=8080 go run .
package main

import "github.com/Gui-Rondon/hyrule/app"

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	app.Main(spec())
}

func spec() app.Spec {
	return app.Spec{
		Name:            "comments-api",
		Backend:         app.BackendPostgres,
		DefaultDB:       "comments",
		Version:         version,
		Setup:           setup,
		PostgresIndexes: commentsIndexes(commentsTable),
	}
}
