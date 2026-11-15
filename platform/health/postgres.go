package health

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Postgres is Mongo's database_check for the Postgres backend -- same
// name, since a service picks exactly one backend (app.Spec.Backend),
// never both at once.
func Postgres(pool *pgxpool.Pool) Checker {
	return Checker{
		Name: "database_check",
		Check: func(ctx context.Context) (bool, string) {
			if err := pool.Ping(ctx); err != nil {
				return false, "Database NOK"
			}
			return true, "Database OK"
		},
	}
}
