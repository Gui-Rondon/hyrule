package postgres

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Gui-Rondon/hyrule/platform/config"
)

// URI resolves the connection string with the same precedence as
// platform/mongodb.URI: POSTGRES_CONNECTION_STRING, then postgres.uri,
// then host/port/username/password (base64 encoded)/dbname.
func URI(cfg config.Config, defaultDB string) (uri, dbName string, err error) {
	pgCfg := cfg.Sub("postgres")
	switch {
	case os.Getenv("POSTGRES_CONNECTION_STRING") != "":
		uri = os.Getenv("POSTGRES_CONNECTION_STRING")
	case pgCfg.String("uri", "") != "":
		uri = pgCfg.String("uri", "")
	default:
		host := pgCfg.String("host", "postgres")
		port := pgCfg.String("port", "5432")
		dbName = pgCfg.String("dbname", defaultDB)
		userinfo := ""
		if user := pgCfg.String("username", ""); user != "" {
			pass := pgCfg.String("password", "")
			if pass != "" {
				decoded, err := base64.StdEncoding.DecodeString(pass)
				if err != nil {
					return "", "", fmt.Errorf("postgres: decode password: %w", err)
				}
				pass = strings.TrimSpace(string(decoded))
			}
			userinfo = url.UserPassword(user, pass).String() + "@"
		}
		return fmt.Sprintf("postgres://%s%s:%s/%s", userinfo, host, port, dbName), dbName, nil
	}

	parsed, err := url.Parse(uri)
	if err == nil {
		dbName = strings.TrimPrefix(parsed.Path, "/")
	}
	if dbName == "" {
		dbName = defaultDB
	}
	return uri, dbName, nil
}

// Connect opens a pool tuned from postgres.* configuration -- platform/
// mongodb.Connect's counterpart (postgres-rfc.md point 9's "Connection
// pool tuning" gap): pgxpool.New today takes the connection string's own
// defaults, with nothing exposing MaxConns/MinConns/health-check
// interval the way mongodb.Connect's mongo.* keys do. A setting left
// unconfigured keeps pgxpool's own default instead of being forced to a
// value picked here.
//
// postgres.statement_timeout/postgres.lock_timeout (point 8's "query
// timeout" gap: nothing sets statement_timeout today, so a pathological
// query can hold a connection indefinitely) are sent as startup
// RuntimeParams, which Postgres applies to a connection exactly as SET
// would -- every connection the pool opens carries them, not just the
// first.
func Connect(ctx context.Context, cfg config.Config, defaultDB string) (*pgxpool.Pool, error) {
	uri, _, err := URI(cfg, defaultDB)
	if err != nil {
		return nil, err
	}
	poolCfg, err := pgxpool.ParseConfig(uri)
	if err != nil {
		return nil, fmt.Errorf("postgres: parse config: %w", err)
	}

	pgCfg := cfg.Sub("postgres")
	if n, err := poolIntSetting(pgCfg, "max_conns"); err != nil {
		return nil, err
	} else if n > 0 {
		poolCfg.MaxConns = int32(n)
	}
	if n, err := poolIntSetting(pgCfg, "min_conns"); err != nil {
		return nil, err
	} else if n > 0 {
		poolCfg.MinConns = int32(n)
	}
	if d, err := poolDurationSetting(pgCfg, "max_conn_lifetime"); err != nil {
		return nil, err
	} else if d > 0 {
		poolCfg.MaxConnLifetime = d
	}
	if d, err := poolDurationSetting(pgCfg, "max_conn_idle_time"); err != nil {
		return nil, err
	} else if d > 0 {
		poolCfg.MaxConnIdleTime = d
	}
	if d, err := poolDurationSetting(pgCfg, "health_check_period"); err != nil {
		return nil, err
	} else if d > 0 {
		poolCfg.HealthCheckPeriod = d
	}
	if d, err := poolDurationSetting(pgCfg, "connect_timeout"); err != nil {
		return nil, err
	} else if d > 0 {
		poolCfg.ConnConfig.ConnectTimeout = d
	}
	if d, err := poolDurationSetting(pgCfg, "statement_timeout"); err != nil {
		return nil, err
	} else if d > 0 {
		setRuntimeParamMillis(poolCfg, "statement_timeout", d)
	}
	if d, err := poolDurationSetting(pgCfg, "lock_timeout"); err != nil {
		return nil, err
	} else if d > 0 {
		setRuntimeParamMillis(poolCfg, "lock_timeout", d)
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("postgres: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: ping: %w", err)
	}
	return pool, nil
}

func setRuntimeParamMillis(poolCfg *pgxpool.Config, name string, d time.Duration) {
	if poolCfg.ConnConfig.RuntimeParams == nil {
		poolCfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	poolCfg.ConnConfig.RuntimeParams[name] = strconv.FormatInt(d.Milliseconds(), 10)
}

func poolIntSetting(cfg config.Config, key string) (int, error) {
	v := cfg.String(key, "")
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("postgres.%s: %w", key, err)
	}
	return n, nil
}

func poolDurationSetting(cfg config.Config, key string) (time.Duration, error) {
	v := cfg.String(key, "")
	if v == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("postgres.%s: %w", key, err)
	}
	return d, nil
}
