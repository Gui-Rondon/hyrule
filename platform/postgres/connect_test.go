package postgres

import (
	"context"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gui-Rondon/hyrule/platform/config"
)

func TestConnectRejectsOverflowingPoolSizesBeforeDialing(t *testing.T) {
	t.Setenv("POSTGRES_CONNECTION_STRING", "postgres://127.0.0.1:1/test?connect_timeout=1")
	for _, key := range []string{"max_conns", "min_conns"} {
		t.Run(key, func(t *testing.T) {
			for _, n := range []int64{math.MaxInt32 + 1, math.MaxInt64} {
				cfg := config.Config{"postgres": map[string]any{key: strconv.FormatInt(n, 10)}}
				pool, err := Connect(context.Background(), cfg, "test")
				if pool != nil {
					pool.Close()
					t.Fatal("overflowing pool size created a pool")
				}
				if err == nil || !strings.Contains(err.Error(), "postgres."+key) {
					t.Fatalf("%s=%d: %v", key, n, err)
				}
			}
		})
	}
}

func testURI(t *testing.T) string {
	uri := os.Getenv("POSTGRES_TEST_URI")
	if uri == "" {
		t.Skip("POSTGRES_TEST_URI not set")
	}
	return uri
}

// URI's precedence is pure (no database needed): env var, then
// postgres.uri, then host/port/username/password/dbname.

func TestURIFromEnv(t *testing.T) {
	t.Setenv("POSTGRES_CONNECTION_STRING", "postgres://h/mydb")
	uri, dbName, err := URI(config.Config{}, "default")
	if err != nil || uri != "postgres://h/mydb" || dbName != "mydb" {
		t.Fatalf("uri, dbName, err = %q, %q, %v", uri, dbName, err)
	}
}

func TestURIFromConfig(t *testing.T) {
	t.Setenv("POSTGRES_CONNECTION_STRING", "")
	cfg := config.Config{"postgres": map[string]any{"uri": "postgres://h/mydb"}}
	uri, dbName, err := URI(cfg, "default")
	if err != nil || uri != "postgres://h/mydb" || dbName != "mydb" {
		t.Fatalf("uri, dbName, err = %q, %q, %v", uri, dbName, err)
	}
}

func TestURIFromConfigNoPathUsesDefaultDB(t *testing.T) {
	t.Setenv("POSTGRES_CONNECTION_STRING", "")
	cfg := config.Config{"postgres": map[string]any{"uri": "postgres://h"}}
	_, dbName, err := URI(cfg, "default")
	if err != nil || dbName != "default" {
		t.Fatalf("dbName, err = %q, %v", dbName, err)
	}
}

func TestURIFromHostPortFallback(t *testing.T) {
	t.Setenv("POSTGRES_CONNECTION_STRING", "")
	cfg := config.Config{"postgres": map[string]any{"host": "h", "port": "5555"}}
	uri, dbName, err := URI(cfg, "default")
	if err != nil || uri != "postgres://h:5555/default" || dbName != "default" {
		t.Fatalf("uri, dbName, err = %q, %q, %v", uri, dbName, err)
	}
}

func TestURIFromHostPortWithUserPassword(t *testing.T) {
	t.Setenv("POSTGRES_CONNECTION_STRING", "")
	cfg := config.Config{"postgres": map[string]any{
		"host": "h", "port": "5555", "username": "u",
		"password": "cGFzcw==", // base64("pass")
	}}
	uri, _, err := URI(cfg, "default")
	if err != nil || uri != "postgres://u:pass@h:5555/default" {
		t.Fatalf("uri, err = %q, %v", uri, err)
	}
}

func TestURIFromHostPortBadBase64Password(t *testing.T) {
	t.Setenv("POSTGRES_CONNECTION_STRING", "")
	cfg := config.Config{"postgres": map[string]any{"username": "u", "password": "%%%"}}
	if _, _, err := URI(cfg, "default"); err == nil {
		t.Error("invalid base64 password must fail")
	}
}

func TestConnectAndPing(t *testing.T) {
	t.Setenv("POSTGRES_CONNECTION_STRING", testURI(t))
	ctx := context.Background()
	pool, err := Connect(ctx, config.Config{}, "x")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestConnectAppliesPoolSettings(t *testing.T) {
	t.Setenv("POSTGRES_CONNECTION_STRING", testURI(t))
	cfg := config.Config{"postgres": map[string]any{
		"max_conns":         "7",
		"min_conns":         "2",
		"max_conn_lifetime": "1h", "max_conn_idle_time": "2m", "health_check_period": "15s", "connect_timeout": "2s",
	}}
	pool, err := Connect(context.Background(), cfg, "x")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	got := pool.Config()
	if got.MaxConns != 7 || got.MinConns != 2 {
		t.Fatalf("MaxConns, MinConns = %d, %d", got.MaxConns, got.MinConns)
	}
	if got.MaxConnLifetime != time.Hour || got.MaxConnIdleTime != 2*time.Minute || got.HealthCheckPeriod != 15*time.Second || got.ConnConfig.ConnectTimeout != 2*time.Second {
		t.Fatalf("duration settings not applied: lifetime=%v idle=%v health=%v connect=%v", got.MaxConnLifetime, got.MaxConnIdleTime, got.HealthCheckPeriod, got.ConnConfig.ConnectTimeout)
	}
}

// TestConnectAppliesStatementTimeout proves, against the real server
// (not a mock of the config), that postgres.statement_timeout/
// postgres.lock_timeout actually reach the session -- the gap
// postgres-rfc.md point 8 named ("nothing sets statement_timeout
// today").
func TestConnectAppliesStatementTimeout(t *testing.T) {
	t.Setenv("POSTGRES_CONNECTION_STRING", testURI(t))
	cfg := config.Config{"postgres": map[string]any{
		"statement_timeout": "1500ms",
		"lock_timeout":      "750ms",
	}}
	ctx := context.Background()
	pool, err := Connect(ctx, cfg, "x")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	var statementTimeout, lockTimeout string
	if err := pool.QueryRow(ctx, "SHOW statement_timeout").Scan(&statementTimeout); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SHOW lock_timeout").Scan(&lockTimeout); err != nil {
		t.Fatal(err)
	}
	if statementTimeout != "1500ms" {
		t.Errorf("statement_timeout = %q, want 1500ms", statementTimeout)
	}
	if lockTimeout != "750ms" {
		t.Errorf("lock_timeout = %q, want 750ms", lockTimeout)
	}
}

func TestConnectFailsOnUnreachableServer(t *testing.T) {
	t.Setenv("POSTGRES_CONNECTION_STRING", "postgres://127.0.0.1:1/x?connect_timeout=1")
	if _, err := Connect(context.Background(), config.Config{}, "x"); err == nil || !strings.Contains(err.Error(), "ping") {
		t.Errorf("err = %v", err)
	}
}

func TestConnectFailsOnBadPassword(t *testing.T) {
	t.Setenv("POSTGRES_CONNECTION_STRING", "")
	cfg := config.Config{"postgres": map[string]any{"username": "u", "password": "%%%"}}
	if _, err := Connect(context.Background(), cfg, "x"); err == nil {
		t.Error("invalid base64 password must fail")
	}
}

func TestConnectFailsOnBadSetting(t *testing.T) {
	t.Setenv("POSTGRES_CONNECTION_STRING", testURI(t))
	cfg := config.Config{"postgres": map[string]any{"max_conns": "not-a-number"}}
	if _, err := Connect(context.Background(), cfg, "x"); err == nil {
		t.Error("invalid postgres.max_conns must fail")
	}
}

func TestConnectFailsOnBadDurationSetting(t *testing.T) {
	t.Setenv("POSTGRES_CONNECTION_STRING", testURI(t))
	cfg := config.Config{"postgres": map[string]any{"statement_timeout": "not-a-duration"}}
	if _, err := Connect(context.Background(), cfg, "x"); err == nil {
		t.Error("invalid postgres.statement_timeout must fail")
	}
}

// Invalid pool settings fail before dialing any server, including settings
// other than statement_timeout that previously had no rejection coverage.
func TestConnectRejectsInvalidPoolSettings(t *testing.T) {
	t.Setenv("POSTGRES_CONNECTION_STRING", "postgres://localhost/test?sslmode=disable")
	for _, key := range []string{"max_conns", "min_conns", "max_conn_lifetime", "max_conn_idle_time", "health_check_period", "connect_timeout", "statement_timeout", "lock_timeout"} {
		t.Run(key, func(t *testing.T) {
			pool, err := Connect(context.Background(), config.Config{"postgres": map[string]any{key: "invalid"}}, "fallback")
			if pool != nil {
				pool.Close()
				t.Fatal("invalid setting created pool")
			}
			if err == nil || !strings.Contains(err.Error(), "postgres."+key) {
				t.Fatalf("error for %s = %v", key, err)
			}
		})
	}
}
