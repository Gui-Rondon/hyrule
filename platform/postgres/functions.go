package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The three functions below convert a jsonb-sourced text value to a
// typed SQL value without ever raising inside a query -- every
// malformed or out-of-range input returns SQL NULL, not an error. Each
// guards against a real Postgres limit: numeric's 131072-digit cap for
// hyrule_safe_integer/hyrule_safe_numeric, and timestamptz parsing
// raising on garbage input for hyrule_safe_timestamptz.
const (
	hyruleSafeIntegerSQL = `
CREATE OR REPLACE FUNCTION hyrule_safe_integer(value text) RETURNS numeric
    LANGUAGE plpgsql IMMUTABLE AS $$
BEGIN
    IF value IS NULL THEN RETURN NULL; END IF;
    IF value !~ '^[+-]?[0-9]+$' THEN RETURN NULL; END IF;
    RETURN value::numeric;
EXCEPTION WHEN OTHERS THEN RETURN NULL;
END;
$$;`

	hyruleSafeNumericSQL = `
CREATE OR REPLACE FUNCTION hyrule_safe_numeric(value text) RETURNS numeric
    LANGUAGE plpgsql IMMUTABLE AS $$
BEGIN
    IF value IS NULL THEN RETURN NULL; END IF;
    IF value !~ '^[+-]?[0-9]+(\.[0-9]+)?$' THEN RETURN NULL; END IF;
    RETURN value::numeric;
EXCEPTION WHEN OTHERS THEN RETURN NULL;
END;
$$;`

	// IMMUTABLE is safe here because the regex requires an explicit
	// offset (Z or ±HH:MM), which fixes the absolute instant regardless
	// of the session's TimeZone setting.
	hyruleSafeTimestamptzSQL = `
CREATE OR REPLACE FUNCTION hyrule_safe_timestamptz(value text) RETURNS timestamptz
    LANGUAGE plpgsql IMMUTABLE AS $$
BEGIN
    IF value IS NULL THEN RETURN NULL; END IF;
    IF value = '' THEN RETURN NULL; END IF;
    IF value !~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]+)?(Z|[+-][0-9]{2}:[0-9]{2})$' THEN
        RETURN NULL;
    END IF;
    RETURN value::timestamptz;
EXCEPTION WHEN OTHERS THEN
    RETURN NULL;
END;
$$;`
)

// EnsureFunctions creates (or replaces) the three hyrule_safe_* SQL
// helper functions this package's generated SQL may rely on. It's
// idempotent -- safe to call on every process start.
func EnsureFunctions(ctx context.Context, pool *pgxpool.Pool) error {
	for _, stmt := range []string{hyruleSafeIntegerSQL, hyruleSafeNumericSQL, hyruleSafeTimestamptzSQL} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("postgres: EnsureFunctions: %w", err)
		}
	}
	return nil
}
