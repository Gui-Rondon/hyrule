package postgresha_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Gui-Rondon/hyrule/platform/config"
	"github.com/Gui-Rondon/hyrule/platform/postgres"
)

func backupNames(ctx context.Context, node string) (map[string]bool, error) {
	// Output keeps WAL-G's stderr diagnostics out of the JSON stream.
	out, err := composeCommand(ctx, "exec", "-T", "--user", "postgres", node, "wal-g", "backup-list", "--json").Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("backup-list: %w: %s", err, exit.Stderr)
		}
		return nil, fmt.Errorf("backup-list: %w", err)
	}
	var backups []struct {
		Name string `json:"backup_name"`
	}
	if err := json.Unmarshal(out, &backups); err != nil {
		return nil, fmt.Errorf("backup-list JSON: %w: %s", err, out)
	}
	names := make(map[string]bool, len(backups))
	for _, b := range backups {
		if !strings.HasPrefix(b.Name, "base_") {
			return nil, fmt.Errorf("unexpected backup metadata: %s", out)
		}
		names[b.Name] = true
	}
	return names, nil
}

func dockerOutput(ctx context.Context, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, "docker", args...).CombinedOutput()
}

func markerState(ctx context.Context, pool *pgxpool.Pool, table string, want map[string]string) error {
	rows, err := pool.Query(ctx, "SELECT marker, value FROM "+table+" ORDER BY marker")
	if err != nil {
		return err
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var marker, value string
		if err := rows.Scan(&marker, &value); err != nil {
			return err
		}
		got[marker] = value
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(got) != len(want) {
		return fmt.Errorf("rows=%v want=%v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			return fmt.Errorf("rows=%v want=%v", got, want)
		}
	}
	return nil
}

func TestBaseBackupPointInTimeRecovery(t *testing.T) {
	if os.Getenv("POSTGRES_HA_PITR_TEST") != "1" {
		t.Skip("local WAL-G/PITR: make postgres-ha-pitr-test")
	}
	// Own the lab for this sequence; no parallel smoke/failover/reset/writers.
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Minute)
	defer cancel()
	t.Setenv("POSTGRES_CONNECTION_STRING", "")
	cfg := func(port int) config.Config {
		return config.Config{"postgres": map[string]any{
			"uri":       fmt.Sprintf("postgres://postgres:postgres-ha-local@127.0.0.1:%d/postgres?sslmode=disable", port),
			"max_conns": 2, "connect_timeout": "2s", "statement_timeout": "5s", "lock_timeout": "2s",
		}}
	}
	nodes := []member{{name: "pg1", sqlPort: 5435, apiPort: 18008}, {name: "pg2", sqlPort: 5436, apiPort: 18009}, {name: "pg3", sqlPort: 5437, apiPort: 18010}}
	for i := range nodes {
		pool, err := postgres.Connect(ctx, cfg(nodes[i].sqlPort), "postgres")
		if err != nil {
			t.Fatalf("precondition %s: %v", nodes[i].name, err)
		}
		nodes[i].pool = pool
		t.Cleanup(pool.Close)
	}
	var source leaderState
	if err := await(ctx, func(ctx context.Context) error {
		var err error
		source, err = healthyCluster(ctx, nodes, "")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := etcdQuorum(ctx); err != nil {
		t.Fatal(err)
	}
	writer := source.node.pool
	table := pgx.Identifier{"ha_pitr_" + strings.ReplaceAll(uuid.NewString(), "-", "")}.Sanitize()
	t.Logf("source test table=%s", table)
	if _, err := writer.Exec(ctx, "CREATE TABLE "+table+" (marker text PRIMARY KEY, value text NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := writer.Exec(cleanup, "DROP TABLE IF EXISTS "+table); err != nil {
			t.Errorf("source table cleanup %s: %v", table, err)
		}
	})
	if _, err := writer.Exec(ctx, "INSERT INTO "+table+" VALUES ('baseline','before-backup')"); err != nil {
		t.Fatal(err)
	}
	before, err := backupNames(ctx, source.node.name)
	if err != nil {
		t.Fatal(err)
	}
	backupStart := time.Now()
	// Explicit local socket/auth: never consume host application credentials.
	// Also bound the process inside Docker: canceling the CLI alone does not
	// guarantee that a remote exec (and its backup session) has stopped.
	out, err := compose(ctx, "exec", "-T", "--user", "postgres", source.node.name, "timeout", "--kill-after=5s", "180s", "env", "PGHOST=/var/run/postgresql", "PGUSER=postgres", "PGDATABASE=postgres", "wal-g", "backup-push", "/var/lib/postgresql/data/pgdata", "--full", "--verify")
	if err != nil {
		t.Fatalf("backup-push: %v\n%s", err, out)
	}
	t.Logf("full backup with page checksum verification completed in %s\n%s", time.Since(backupStart), out)
	after, err := backupNames(ctx, source.node.name)
	if err != nil {
		t.Fatal(err)
	}
	backup := ""
	for name := range after {
		if !before[name] {
			if backup != "" {
				t.Fatal("multiple new backups; concurrent lab operation")
			}
			backup = name
		}
	}
	if backup == "" {
		t.Fatal("no new completed backup")
	}
	if _, err := writer.Exec(ctx, "INSERT INTO "+table+" VALUES ('after-backup','must-replay-from-WAL')"); err != nil {
		t.Fatal(err)
	}
	// Choose a wall-clock target strictly after the acknowledged insert commit.
	// pg_current_wal_lsn captures the flushed end of that commit for replay checks.
	var target time.Time
	var includedLSN string
	if err := writer.QueryRow(ctx, "SELECT clock_timestamp(), pg_current_wal_lsn()::text").Scan(&target, &includedLSN); err != nil {
		t.Fatal(err)
	}
	if err := await(ctx, func(ctx context.Context) error {
		var later bool
		if err := writer.QueryRow(ctx, "SELECT clock_timestamp() > $1::timestamptz", target).Scan(&later); err != nil {
			return err
		}
		if !later {
			return fmt.Errorf("waiting for source clock to advance beyond target")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Deliberately destructive only to this run's rows, in one confirmed commit.
	if _, err := writer.Exec(ctx, "WITH changed AS (UPDATE "+table+" SET value='destroyed-after-target' WHERE marker='baseline') INSERT INTO "+table+" VALUES ('after-target','must-not-replay')"); err != nil {
		t.Fatal(err)
	}
	sourceWant := map[string]string{"baseline": "destroyed-after-target", "after-backup": "must-replay-from-WAL", "after-target": "must-not-replay"}
	if err := markerState(ctx, writer, table, sourceWant); err != nil {
		t.Fatal(err)
	}
	var segment string
	if err := writer.QueryRow(ctx, "SELECT pg_walfile_name(pg_current_wal_insert_lsn())").Scan(&segment); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Exec(ctx, "SELECT pg_switch_wal()"); err != nil {
		t.Fatal(err)
	}
	if err := await(ctx, func(ctx context.Context) error {
		var archived bool
		if err := writer.QueryRow(ctx, "SELECT last_archived_wal >= $1 FROM pg_stat_archiver", segment).Scan(&archived); err != nil {
			return err
		}
		if !archived {
			return fmt.Errorf("waiting for archived WAL %s", segment)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Fail if another operation changed leadership/timeline while taking backup.
	current, err := healthyCluster(ctx, nodes, "")
	if err != nil || current.node.name != source.node.name || current.timeline != source.timeline || current.systemID != source.systemID {
		t.Fatalf("source changed during backup: %+v err=%v", current, err)
	}
	sandbox := "hyrule-postgres-ha-pitr-" + uuid.NewString()
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if t.Failed() {
			out, err := dockerOutput(cleanup, "logs", "--tail=100", sandbox)
			t.Logf("restore diagnostics %s: %v\n%s", sandbox, err, out)
		}
		// Only the generated sandbox and its anonymous data volume, never lab volumes.
		if out, err := dockerOutput(cleanup, "rm", "--force", "--volumes", sandbox); err != nil {
			t.Errorf("remove sandbox %s: %v %s", sandbox, err, out)
		}
	})
	targetText := target.UTC().Format("2006-01-02 15:04:05.000000+00:00")
	t.Logf("source=%s cluster=%s timeline=%d backup=%s target=%s includedLSN=%s archivedWAL=%s sandbox=%s", source.node.name, source.systemID, source.timeline, backup, targetText, includedLSN, segment, sandbox)
	out, err = compose(ctx, "run", "--detach", "--no-deps", "--pull", "never", "--service-ports", "--name", sandbox,
		"-e", "PITR_BACKUP="+backup, "-e", "PITR_TARGET_TIME="+targetText, "-e", fmt.Sprintf("PITR_TIMELINE=%d", source.timeline), "pitr")
	if err != nil {
		t.Fatalf("start sandbox: %v %s", err, out)
	}
	out, err = dockerOutput(ctx, "inspect", "--format", `{{(index (index .NetworkSettings.Ports "5432/tcp") 0).HostPort}}`, sandbox)
	if err != nil {
		t.Fatalf("sandbox port: %v %s", err, out)
	}
	port, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || port < 1 || port > 65535 {
		t.Fatalf("invalid sandbox port %q", out)
	}
	// The sandbox binds only to loopback, on an automatically allocated port.
	var restored *pgxpool.Pool
	if err := await(ctx, func(ctx context.Context) error {
		state, err := dockerOutput(ctx, "inspect", "--format", "{{.State.Status}}", sandbox)
		if err != nil {
			t.Fatalf("inspect sandbox startup: %v %s", err, state)
		}
		if status := strings.TrimSpace(string(state)); status == "exited" || status == "dead" {
			t.Fatalf("sandbox exited during restore startup (%s)", status)
		}
		restored, err = postgres.Connect(ctx, cfg(port), "postgres")
		return err
	}); err != nil {
		t.Fatalf("restore startup: %v", err)
	}
	t.Cleanup(restored.Close)
	if err := await(ctx, func(ctx context.Context) error {
		var recovery, paused, replayed bool
		var sid, receiverState string
		if err := restored.QueryRow(ctx, `SELECT pg_is_in_recovery(), pg_get_wal_replay_pause_state(), pg_last_wal_replay_lsn() >= $1::pg_lsn, system_identifier::text FROM pg_control_system()`, includedLSN).Scan(&recovery, &receiverState, &replayed, &sid); err != nil {
			return err
		}
		paused = receiverState == "paused"
		if !recovery || !paused || !replayed || sid != source.systemID {
			return fmt.Errorf("restore recovery=%v paused=%v replayed=%v cluster=%s", recovery, paused, replayed, sid)
		}
		var receivers int
		var archive, conninfo string
		if err := restored.QueryRow(ctx, "SELECT (SELECT count(*) FROM pg_stat_wal_receiver), current_setting('archive_mode'), current_setting('primary_conninfo')").Scan(&receivers, &archive, &conninfo); err != nil {
			return err
		}
		if receivers != 0 || archive != "off" || conninfo != "" {
			return fmt.Errorf("sandbox not isolated: receivers=%d archive=%s conninfo=%q", receivers, archive, conninfo)
		}
		return nil
	}); err != nil {
		t.Fatalf("PITR target not reached: %v", err)
	}
	restoredWant := map[string]string{"baseline": "before-backup", "after-backup": "must-replay-from-WAL"}
	if err := markerState(ctx, restored, table, restoredWant); err != nil {
		t.Fatalf("PITR contents: %v", err)
	}
	// A read-only target snapshot is not enough: explicitly finish recovery and
	// prove the restored copy can commit independently without synchronous peers.
	var promoted bool
	if err := restored.QueryRow(ctx, "SELECT pg_promote(true, 30)").Scan(&promoted); err != nil || !promoted {
		t.Fatalf("promote restored copy: %v promoted=%v", err, promoted)
	}
	if _, err := restored.Exec(ctx, "INSERT INTO "+table+" VALUES ('restored-only','new-independent-write')"); err != nil {
		t.Fatalf("restored write: %v", err)
	}
	restoredWant["restored-only"] = "new-independent-write"
	if err := markerState(ctx, restored, table, restoredWant); err != nil {
		t.Fatal(err)
	}
	if err := markerState(ctx, writer, table, sourceWant); err != nil {
		t.Fatalf("source changed by restore: %v", err)
	}
	current, err = healthyCluster(ctx, nodes, "")
	if err != nil || current.node.name != source.node.name || current.timeline != source.timeline || current.systemID != source.systemID {
		t.Fatalf("source unhealthy after restore: %+v err=%v", current, err)
	}
	if err := etcdQuorum(ctx); err != nil {
		t.Fatal(err)
	}
	t.Logf("PITR reached %s: baseline preserved, post-backup WAL replayed, later destructive commit excluded; restored copy promoted and committed independently; source cluster unchanged", targetText)
}
