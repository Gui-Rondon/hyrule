// Package postgresha_test exercises the opt-in, local HA lab. Ordinary
// go test ./... only compiles it; make postgres-ha-test enables the real checks.
package postgresha_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Gui-Rondon/hyrule/platform/config"
	"github.com/Gui-Rondon/hyrule/platform/postgres"
)

type member struct {
	name             string
	sqlPort, apiPort int
	pool             *pgxpool.Pool
}

func composeCommand(ctx context.Context, args ...string) *exec.Cmd {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../../.."))
	return exec.CommandContext(ctx, "docker", append([]string{"compose", "--project-name", "hyrule-postgres-ha", "-f", filepath.Join(root, "infra/postgres-ha/compose.yml")}, args...)...)
}

func compose(ctx context.Context, args ...string) ([]byte, error) {
	return composeCommand(ctx, args...).CombinedOutput()
}

func await(ctx context.Context, check func(context.Context) error) error {
	var last error
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		attempt, cancel := context.WithTimeout(ctx, 4*time.Second)
		last = check(attempt)
		cancel()
		if last == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%w (last observation: %v)", ctx.Err(), last)
		case <-ticker.C:
		}
	}
}

func TestTopologyReplicationAndWALArchive(t *testing.T) {
	if os.Getenv("POSTGRES_HA_TEST") != "1" {
		t.Skip("local HA lab: make postgres-ha-up postgres-ha-test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		diag, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, args := range [][]string{{"ps", "-a"}, {"logs", "--no-color", "--tail=25"}} {
			out, err := compose(diag, args...)
			t.Logf("compose %v: %v\n%s", args, err, out)
		}
	})
	// Connect must exercise the explicit lab URI, regardless of the developer's
	// application environment (URI normally gives that environment precedence).
	t.Setenv("POSTGRES_CONNECTION_STRING", "")
	uri := func(port int) string {
		return fmt.Sprintf("postgres://postgres:postgres-ha-local@127.0.0.1:%d/postgres?sslmode=disable", port)
	}
	nodes := []member{{name: "pg1", sqlPort: 5435, apiPort: 18008}, {name: "pg2", sqlPort: 5436, apiPort: 18009}, {name: "pg3", sqlPort: 5437, apiPort: 18010}}
	cfg := func(port int) config.Config {
		return config.Config{"postgres": map[string]any{"uri": uri(port), "max_conns": 4, "connect_timeout": "2s", "statement_timeout": "5s"}}
	}
	for i := range nodes {
		n := &nodes[i]
		if err := await(ctx, func(ctx context.Context) error {
			var err error
			n.pool, err = postgres.Connect(ctx, cfg(n.sqlPort), "postgres")
			return err
		}); err != nil {
			t.Fatalf("connect %s: %v", n.name, err)
		}
		t.Cleanup(n.pool.Close)
	}
	proxyPort := 5434
	if v := os.Getenv("POSTGRES_HA_PORT"); v != "" {
		parsed, err := strconv.Atoi(v)
		if err != nil || parsed < 1 || parsed > 65535 {
			t.Fatalf("invalid POSTGRES_HA_PORT %q", v)
		}
		proxyPort = parsed
	}
	var writer *pgxpool.Pool
	if err := await(ctx, func(ctx context.Context) error {
		var err error
		writer, err = postgres.Connect(ctx, cfg(proxyPort), "postgres")
		return err
	}); err != nil {
		t.Fatalf("connect through proxy: %v", err)
	}
	t.Cleanup(writer.Close)
	var primary *member
	var clusterID string
	httpClient := &http.Client{Timeout: 3 * time.Second}
	if err := await(ctx, func(ctx context.Context) error {
		count := 0
		systemID := ""
		for i := range nodes {
			n := &nodes[i]
			var recovery bool
			var sid string
			if err := n.pool.QueryRow(ctx, "SELECT pg_is_in_recovery(), system_identifier::text FROM pg_control_system()").Scan(&recovery, &sid); err != nil {
				return err
			}
			if systemID != "" && systemID != sid {
				return fmt.Errorf("%s belongs to a different database cluster", n.name)
			}
			systemID = sid
			clusterID = sid
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/primary", n.apiPort), nil)
			resp, err := httpClient.Do(req)
			if err != nil {
				return err
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if !recovery {
				if resp.StatusCode != 200 {
					return fmt.Errorf("%s primary has no Patroni leader lock", n.name)
				}
				primary = n
				count++
			} else if resp.StatusCode != 503 {
				return fmt.Errorf("%s replica primary endpoint returned %d", n.name, resp.StatusCode)
			}
		}
		if count != 1 {
			return fmt.Errorf("primaries=%d, want one", count)
		}
		var streaming, synchronous int
		if err := primary.pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE state='streaming'), count(*) FILTER (WHERE state='streaming' AND sync_state='sync') FROM pg_stat_replication`).Scan(&streaming, &synchronous); err != nil {
			return err
		}
		if streaming != 2 || synchronous < 1 {
			return fmt.Errorf("streaming=%d synchronous=%d, want two and at least one", streaming, synchronous)
		}
		return nil
	}); err != nil {
		t.Fatalf("topology: %v", err)
	}
	// Verify Patroni's actual dynamic policy, not merely its bootstrap YAML.
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/config", primary.apiPort), nil)
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var policy struct {
		SynchronousMode bool `json:"synchronous_mode"`
		Strict          bool `json:"synchronous_mode_strict"`
		Nodes           int  `json:"synchronous_node_count"`
	}
	err = json.NewDecoder(resp.Body).Decode(&policy)
	resp.Body.Close()
	if err != nil || !policy.SynchronousMode || !policy.Strict || policy.Nodes != 1 {
		t.Fatalf("Patroni sync policy: %+v %v", policy, err)
	}
	var recovery bool
	var syncCommit, archiveMode, writerID, checksums string
	if err := writer.QueryRow(ctx, "SELECT pg_is_in_recovery(), current_setting('synchronous_commit'), current_setting('archive_mode'), system_identifier::text, current_setting('data_checksums') FROM pg_control_system()").Scan(&recovery, &syncCommit, &archiveMode, &writerID, &checksums); err != nil {
		t.Fatal(err)
	}
	if writerID != clusterID || checksums != "on" {
		t.Fatalf("proxy cluster=%s want=%s; checksums=%s", writerID, clusterID, checksums)
	}
	if recovery || syncCommit != "on" || archiveMode != "on" {
		t.Fatalf("writer recovery=%v synchronous_commit=%s archive_mode=%s", recovery, syncCommit, archiveMode)
	}
	table := pgx.Identifier{"ha_smoke_" + strings.ReplaceAll(uuid.NewString(), "-", "")}.Sanitize()
	if _, err := writer.Exec(ctx, "CREATE TABLE "+table+" (marker text PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := writer.Exec(cleanup, "DROP TABLE IF EXISTS "+table); err != nil {
			t.Errorf("cleanup table: %v", err)
		}
	})
	marker := uuid.NewString()
	if _, err := writer.Exec(ctx, "INSERT INTO "+table+" VALUES ($1)", marker); err != nil {
		t.Fatal(err)
	}
	for _, n := range nodes {
		if n.name == primary.name {
			continue
		}
		if err := await(ctx, func(ctx context.Context) error {
			var got string
			err := n.pool.QueryRow(ctx, "SELECT marker FROM "+table).Scan(&got)
			if err != nil {
				return err
			}
			if got != marker {
				return fmt.Errorf("%s marker=%q", n.name, got)
			}
			return nil
		}); err != nil {
			t.Fatalf("replication %s: %v", n.name, err)
		}
		_, err := n.pool.Exec(ctx, "INSERT INTO "+table+" VALUES ('must-not-write')")
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "25006" {
			t.Fatalf("%s accepted a write or failed unexpectedly: %v", n.name, err)
		}
	}
	var segment string
	if err := writer.QueryRow(ctx, "SELECT pg_walfile_name(pg_current_wal_insert_lsn())").Scan(&segment); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Exec(ctx, "SELECT pg_switch_wal()"); err != nil {
		t.Fatal(err)
	}
	// Snapshot the completed source segment before it can be recycled. The
	// downloaded copy has a unique destination and can only come from S3.
	source, err := compose(ctx, "exec", "-T", "--user", "postgres", primary.name, "sha256sum", "/var/lib/postgresql/data/pgdata/pg_wal/"+segment)
	if err != nil {
		t.Fatalf("source WAL hash: %v %s", err, source)
	}
	fields := strings.Fields(string(source))
	if len(fields) < 1 {
		t.Fatal("missing source WAL hash")
	}
	expected := fields[0]
	target := "/tmp/ha-smoke-" + uuid.NewString() + ".wal"
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if out, err := compose(cleanup, "exec", "-T", "--user", "postgres", primary.name, "rm", "-f", target); err != nil {
			t.Errorf("cleanup fetched WAL: %v %s", err, out)
		}
	})
	if err := await(ctx, func(ctx context.Context) error {
		var archived bool
		if err := primary.pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_stat_archiver WHERE last_archived_wal >= $1)", segment).Scan(&archived); err != nil {
			return err
		}
		if !archived {
			return fmt.Errorf("waiting for archiver to complete %s", segment)
		}
		out, err := compose(ctx, "exec", "-T", "--user", "postgres", primary.name, "wal-g", "wal-fetch", segment, target)
		if err != nil {
			return fmt.Errorf("wal-fetch: %w: %s", err, out)
		}
		return nil
	}); err != nil {
		t.Fatalf("S3 WAL archive: %v", err)
	}
	fetched, err := compose(ctx, "exec", "-T", "--user", "postgres", primary.name, "sha256sum", target)
	if err != nil {
		t.Fatalf("downloaded WAL hash: %v %s", err, fetched)
	}
	fields = strings.Fields(string(fetched))
	if len(fields) < 1 || fields[0] != expected {
		t.Fatalf("WAL content mismatch: source=%s downloaded=%s", expected, fetched)
	}
	t.Logf("primary=%s, two streaming replicas, synchronous standby confirmed; marker=%s; WAL=%s SHA256=%s round-tripped through S3", primary.name, marker, segment, expected)
}
