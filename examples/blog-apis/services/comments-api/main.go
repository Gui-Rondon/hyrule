// Command comments-api is Fase 7's end-to-end proof for the Postgres
// path: a real resource (blog comments) served over real HTTP by a
// real binary, backed by platform/postgres+restpostgres instead of
// Mongo. app.Main gained a Postgres side (app.BackendPostgres) after
// this was written, but this command still wires platform/server,
// platform/postgres, restpostgres and rest directly instead of
// app.Main -- moving it onto the shared bootstrap is separate,
// not-yet-attempted work (the "postgres em produção" plan's Stream B,
// fase B3). This remains a valid, independent way to serve a Postgres
// resource either way, the same four packages a restpostgres
// integration test wires, just as a real main() instead of a
// *testing.T helper.
//
//	POSTGRES_CONNECTION_STRING='postgres://postgres:postgres@localhost:5433/postgres?sslmode=disable' \
//	  PORT=8080 go run .
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Gui-Rondon/hyrule/platform/server"
)

func main() {
	server.SetupLogging()
	ctx := context.Background()

	uri := os.Getenv("POSTGRES_CONNECTION_STRING")
	if uri == "" {
		slog.Error("POSTGRES_CONNECTION_STRING not set")
		os.Exit(1)
	}
	pool, err := pgxpool.New(ctx, uri)
	if err != nil {
		slog.Error("pgxpool.New", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	api, err := setup(ctx, pool, commentsTable)
	if err != nil {
		slog.Error("setup", "err", err)
		os.Exit(1)
	}
	if err := server.Run(api); err != nil {
		slog.Error("server.Run", "err", err)
		os.Exit(1)
	}
}
