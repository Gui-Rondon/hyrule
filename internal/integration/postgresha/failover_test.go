package postgresha_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Gui-Rondon/hyrule/app"
	"github.com/Gui-Rondon/hyrule/platform/config"
	"github.com/Gui-Rondon/hyrule/platform/postgres"
	"github.com/Gui-Rondon/hyrule/rest"
	"github.com/Gui-Rondon/hyrule/restpostgres"
	"github.com/Gui-Rondon/hyrule/testkit"
)

type leaderState struct {
	node              *member
	systemID, address string
	timeline          uint64
}

// healthyCluster checks live SQL and Patroni state. During the outage it checks
// only the survivors; the stopped node must remain stopped until writes recover.
func healthyCluster(ctx context.Context, nodes []member, excluded string) (leaderState, error) {
	var leader leaderState
	count := 0
	clusterID := ""
	client := &http.Client{Timeout: 2 * time.Second}
	for i := range nodes {
		n := &nodes[i]
		if n.name == excluded {
			continue
		}
		var recovery bool
		var sid, address string
		if err := n.pool.QueryRow(ctx, "SELECT pg_is_in_recovery(), system_identifier::text, inet_server_addr()::text FROM pg_control_system()").Scan(&recovery, &sid, &address); err != nil {
			return leader, err
		}
		if clusterID != "" && clusterID != sid {
			return leader, fmt.Errorf("%s has another system identifier", n.name)
		}
		clusterID = sid
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/primary", n.apiPort), nil)
		resp, err := client.Do(req)
		if err != nil {
			return leader, err
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		expected := http.StatusServiceUnavailable
		if !recovery {
			expected = http.StatusOK
			leader = leaderState{node: n, systemID: sid, address: address}
			count++
		}
		if resp.StatusCode != expected {
			return leader, fmt.Errorf("%s recovery=%v /primary=%d", n.name, recovery, resp.StatusCode)
		}
	}
	if count != 1 {
		return leader, fmt.Errorf("leaders=%d, want one", count)
	}
	var streaming, sync int
	if err := leader.node.pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE state='streaming'), count(*) FILTER (WHERE state='streaming' AND sync_state='sync') FROM pg_stat_replication`).Scan(&streaming, &sync); err != nil {
		return leader, err
	}
	want := len(nodes) - 1
	if excluded != "" {
		want--
	}
	if streaming != want || sync != 1 {
		return leader, fmt.Errorf("streaming=%d sync=%d, want %d and one", streaming, sync, want)
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/config", leader.node.apiPort), nil)
	resp, err := client.Do(req)
	if err != nil {
		return leader, err
	}
	defer resp.Body.Close()
	var policy struct {
		Mode   bool `json:"synchronous_mode"`
		Strict bool `json:"synchronous_mode_strict"`
		Count  int  `json:"synchronous_node_count"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&policy); err != nil {
		return leader, err
	}
	if resp.StatusCode != http.StatusOK || !policy.Mode || !policy.Strict || policy.Count != 1 {
		return leader, fmt.Errorf("unexpected synchronous policy: %+v", policy)
	}
	var commit, segment string
	if err := leader.node.pool.QueryRow(ctx, "SELECT current_setting('synchronous_commit'), pg_walfile_name(pg_current_wal_insert_lsn())").Scan(&commit, &segment); err != nil {
		return leader, err
	}
	if commit != "on" || len(segment) != 24 {
		return leader, fmt.Errorf("synchronous_commit=%s WAL=%s", commit, segment)
	}
	leader.timeline, err = strconv.ParseUint(segment[:8], 16, 32)
	return leader, err
}

// The writer must still use the proxy and the cluster's synchronous commit
// policy, including on replacement pool connections after promotion.
func checkWriter(ctx context.Context, pool *pgxpool.Pool, leader leaderState) error {
	var sid, address, commit string
	var recovery bool
	if err := pool.QueryRow(ctx, "SELECT system_identifier::text, inet_server_addr()::text, pg_is_in_recovery(), current_setting('synchronous_commit') FROM pg_control_system()").Scan(&sid, &address, &recovery, &commit); err != nil {
		return err
	}
	if recovery || sid != leader.systemID || address != leader.address || commit != "on" {
		return fmt.Errorf("proxy writer mismatch: cluster=%s address=%s recovery=%v synchronous_commit=%s", sid, address, recovery, commit)
	}
	return nil
}

func etcdQuorum(ctx context.Context) error {
	out, err := compose(ctx, "exec", "-T", "etcd1", "etcdctl", "--endpoints=http://etcd1:2379,http://etcd2:2379,http://etcd3:2379", "endpoint", "health")
	if err != nil {
		return fmt.Errorf("etcd health: %w: %s", err, out)
	}
	return nil
}

// Inspect the exact Compose-selected container before allowing SIGKILL. No host
// port or inherited COMPOSE_PROJECT_NAME is ever used to select the victim.
func labContainer(ctx context.Context, node string) (string, bool, error) {
	switch node {
	case "pg1", "pg2", "pg3":
	default:
		return "", false, fmt.Errorf("invalid node %q", node)
	}
	out, err := compose(ctx, "ps", "--all", "--quiet", node)
	if err != nil {
		return "", false, fmt.Errorf("compose ps: %w: %s", err, out)
	}
	ids := strings.Fields(string(out))
	if len(ids) != 1 {
		return "", false, fmt.Errorf("%s container count=%d", node, len(ids))
	}
	out, err = exec.CommandContext(ctx, "docker", "inspect", ids[0]).CombinedOutput()
	if err != nil {
		return "", false, fmt.Errorf("inspect: %w: %s", err, out)
	}
	var containers []struct {
		Config     struct{ Labels map[string]string }
		State      struct{ Running bool }
		HostConfig struct{ RestartPolicy struct{ Name string } }
	}
	if err := json.Unmarshal(out, &containers); err != nil {
		return "", false, err
	}
	if len(containers) != 1 {
		return "", false, fmt.Errorf("inspect count=%d", len(containers))
	}
	c := containers[0]
	if c.Config.Labels["com.docker.compose.project"] != "hyrule-postgres-ha" || c.Config.Labels["com.docker.compose.service"] != node || c.HostConfig.RestartPolicy.Name != "no" {
		return "", false, fmt.Errorf("container identity or restart policy mismatch for %s", node)
	}
	return ids[0], c.State.Running, nil
}

type confirmedWrite struct{ id, marker string }

func createMarker(ctx context.Context, client *http.Client, base string) (confirmedWrite, error) {
	marker := uuid.NewString() // new logical operation on every attempt, never replay an ambiguous POST
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/markers", strings.NewReader(`{"marker":"`+marker+`"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return confirmedWrite{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return confirmedWrite{}, err
	}
	if resp.StatusCode != http.StatusCreated {
		return confirmedWrite{}, fmt.Errorf("POST=%d: %s", resp.StatusCode, body)
	}
	var result struct {
		ID string `json:"_id"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return confirmedWrite{}, err
	}
	if _, err := uuid.Parse(result.ID); err != nil {
		return confirmedWrite{}, fmt.Errorf("POST id: %w", err)
	}
	return confirmedWrite{id: result.ID, marker: marker}, nil
}

func verifyMarkers(ctx context.Context, client *http.Client, base string, pool *pgxpool.Pool, table string, writes []confirmedWrite) error {
	for _, w := range writes {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/markers/"+w.id, nil)
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		var doc struct {
			ID     string `json:"_id"`
			Marker string `json:"marker"`
		}
		err = json.NewDecoder(resp.Body).Decode(&doc)
		resp.Body.Close()
		if err != nil {
			return err
		}
		if resp.StatusCode != http.StatusOK || doc.ID != w.id || doc.Marker != w.marker {
			return fmt.Errorf("GET confirmed %s: status=%d doc=%+v", w.id, resp.StatusCode, doc)
		}
		var got string
		if err := pool.QueryRow(ctx, "SELECT doc->>'marker' FROM "+table+" WHERE id=$1::uuid", w.id).Scan(&got); err != nil {
			return err
		}
		if got != w.marker {
			return fmt.Errorf("SQL confirmed %s: marker=%s want=%s", w.id, got, w.marker)
		}
	}
	return nil
}

func TestPrimaryCrashApplicationRecovery(t *testing.T) {
	if os.Getenv("POSTGRES_HA_FAILOVER_TEST") != "1" {
		t.Skip("destructive local HA lab: make postgres-ha-failover-test")
	}
	// Never t.Parallel: this test owns the lab's failure/recovery sequence.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	t.Setenv("POSTGRES_CONNECTION_STRING", "")
	port := 5434
	if raw := os.Getenv("POSTGRES_HA_PORT"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 65535 {
			t.Fatalf("invalid POSTGRES_HA_PORT %q", raw)
		}
		port = n
	}
	cfg := func(port int) config.Config {
		return config.Config{"postgres": map[string]any{
			"uri":       fmt.Sprintf("postgres://postgres:postgres-ha-local@127.0.0.1:%d/postgres?sslmode=disable", port),
			"max_conns": 4, "connect_timeout": "2s", "statement_timeout": "3s", "lock_timeout": "1s",
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
	var before leaderState
	if err := await(ctx, func(ctx context.Context) error {
		var err error
		before, err = healthyCluster(ctx, nodes, "")
		return err
	}); err != nil {
		t.Fatalf("precondition topology: %v", err)
	}
	if err := etcdQuorum(ctx); err != nil {
		t.Fatal(err)
	}
	writer, err := postgres.Connect(ctx, cfg(port), "postgres")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(writer.Close)
	if err := checkWriter(ctx, writer, before); err != nil {
		t.Fatal(err)
	}
	name := "ha_failover_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	table := pgx.Identifier{name}.Sanitize()
	if _, err := writer.Exec(ctx, "CREATE TABLE "+table+` (
        id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
        doc jsonb NOT NULL DEFAULT '{}',
        created_at timestamptz NOT NULL DEFAULT now(),
        updated_at timestamptz NOT NULL DEFAULT now(),
        version bigint NOT NULL DEFAULT 1,
        deleted boolean NOT NULL DEFAULT false
    )`); err != nil {
		t.Fatal(err)
	}
	t.Logf("test table=%s", table)
	restored := true
	t.Cleanup(func() {
		if !restored {
			t.Logf("lab recovery failed; preserving table %s for diagnosis", table)
			return
		}
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := writer.Exec(cleanup, "DROP TABLE IF EXISTS "+table); err != nil {
			t.Errorf("cleanup table %s: %v", table, err)
		}
	})
	schema := rest.Schema{"marker": {Type: rest.TypeString, Required: true}}
	handler, err := app.Build(ctx, app.Spec{
		Name: "ha-failover-api", Backend: app.BackendPostgres, DefaultDB: "postgres", Version: "a2",
		Setup: func(_ context.Context, env *app.Env) error {
			adapter, err := restpostgres.NewAdapter(postgres.NewStore(env.Postgres, name), env.PostgresTx, restpostgres.Options{Schema: schema, Retry: rest.DefaultRetryOptions()})
			if err != nil {
				return err
			}
			env.API.Register(&rest.Resource{Name: "markers", Schema: schema, Adapter: adapter, ResourceMethods: []string{http.MethodGet, http.MethodPost}, ItemMethods: []string{http.MethodGet}})
			return nil
		},
	}, app.Deps{Config: cfg(port), Postgres: writer, Auth: testkit.DisabledAuth(t)})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := &http.Client{Timeout: 4 * time.Second}
	var confirmed []confirmedWrite
	for range 10 {
		w, err := createMarker(ctx, client, server.URL)
		if err != nil {
			t.Fatalf("before crash: %v", err)
		}
		confirmed = append(confirmed, w)
	}
	// Pin an existing proxy session to prove that a real pre-crash connection
	// breaks. Release it before polling: recovery must use new pool connections.
	stale, err := writer.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer stale.Release()
	id, running, err := labContainer(ctx, before.node.name)
	if err != nil || !running {
		t.Fatalf("victim identity: running=%v err=%v", running, err)
	}
	// Registered before mutation, independent of the test deadline, and runs
	// before HTTP/pool/table cleanup. A failed assertion still restarts the node.
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if t.Failed() {
			diag, stop := context.WithTimeout(cleanup, 10*time.Second)
			for _, args := range [][]string{{"ps", "-a"}, {"logs", "--no-color", "--tail=30"}} {
				out, err := compose(diag, args...)
				t.Logf("compose %v: %v\n%s", args, err, out)
			}
			stop()
		}
		if out, err := compose(cleanup, "start", before.node.name); err != nil {
			t.Errorf("restart %s: %v %s", before.node.name, err, out)
			return
		}
		var recovered leaderState
		if err := await(cleanup, func(ctx context.Context) error {
			var err error
			recovered, err = healthyCluster(ctx, nodes, "")
			if err != nil {
				return err
			}
			if recovered.systemID != before.systemID || (!t.Failed() && recovered.node.name == before.node.name) {
				return fmt.Errorf("unexpected recovery leader/cluster: %+v", recovered)
			}
			return etcdQuorum(ctx)
		}); err != nil {
			t.Errorf("lab reintegration: %v; preserve volumes, run make postgres-ha-up POSTGRES_HA_PORT=%d", err, port)
			return
		}
		if !t.Failed() {
			if err := await(cleanup, func(ctx context.Context) error {
				return verifyMarkers(ctx, client, server.URL, before.node.pool, table, confirmed)
			}); err != nil {
				t.Errorf("rejoined replica data: %v", err)
				return
			}
		}
		restored = true
		t.Logf("lab recovered: leader=%s, two streaming standbys; interrupted node=%s", recovered.node.name, before.node.name)
	})
	restored = false
	killedAt := time.Now()
	if out, err := compose(ctx, "kill", "-s", "SIGKILL", before.node.name); err != nil {
		t.Fatalf("kill: %v %s", err, out)
	}
	stoppedID, running, err := labContainer(ctx, before.node.name)
	if err != nil || running || stoppedID != id {
		t.Fatalf("victim did not stay stopped: running=%v err=%v", running, err)
	}
	broken, stop := context.WithTimeout(ctx, 4*time.Second)
	_, err = stale.Exec(broken, "SELECT 1")
	stop()
	stale.Release()
	if err == nil {
		t.Fatal("pre-crash proxy session unexpectedly survived SIGKILL")
	}
	t.Logf("killed primary=%s timeline=%d; pre-crash session failed: %v", before.node.name, before.timeline, err)
	failover, stopFailover := context.WithDeadline(ctx, killedAt.Add(120*time.Second))
	defer stopFailover()
	var after leaderState
	var electedAt, firstWriteAt time.Duration
	unknown := 0
	if err := await(failover, func(ctx context.Context) error {
		w, writeErr := createMarker(ctx, client, server.URL)
		if writeErr == nil {
			confirmed = append(confirmed, w)
			if firstWriteAt == 0 {
				firstWriteAt = time.Since(killedAt)
			}
		} else {
			unknown++
			if unknown == 1 || unknown%10 == 0 {
				t.Logf("unconfirmed POST attempt %d: %v", unknown, writeErr)
			}
		}
		var err error
		after, err = healthyCluster(ctx, nodes, before.node.name)
		if err != nil {
			return err
		}
		if after.node.name == before.node.name || after.systemID != before.systemID || after.timeline <= before.timeline {
			return fmt.Errorf("unexpected promoted leader: %+v", after)
		}
		if electedAt == 0 {
			electedAt = time.Since(killedAt)
		}
		if writeErr != nil {
			return writeErr
		}
		return nil
	}); err != nil {
		t.Fatalf("failover within 120s: %v", err)
	}
	// Require multiple new confirmed writes while the old primary is still down.
	for range 3 {
		attempt, stop := context.WithTimeout(failover, 4*time.Second)
		w, err := createMarker(attempt, client, server.URL)
		stop()
		if err != nil {
			t.Fatalf("post-failover write: %v", err)
		}
		confirmed = append(confirmed, w)
	}
	if _, running, err := labContainer(failover, before.node.name); err != nil || running {
		t.Fatalf("old primary restarted before acceptance: running=%v err=%v", running, err)
	}
	if err := checkWriter(failover, writer, after); err != nil {
		t.Fatal(err)
	}
	if err := verifyMarkers(failover, client, server.URL, after.node.pool, table, confirmed); err != nil {
		t.Fatalf("confirmed writes lost/changed: %v", err)
	}
	req, _ := http.NewRequestWithContext(failover, http.MethodGet, server.URL+"/markers/healthcheck", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthcheck=%d", resp.StatusCode)
	}
	t.Logf("same application/pool: %s -> %s, timeline %d -> %d, healthy leader observed=%s, first confirmed HTTP write=%s, confirmed=%d (10 pre-crash), unknown=%d; all confirmed contents verified via HTTP and new-leader SQL", before.node.name, after.node.name, before.timeline, after.timeline, electedAt, firstWriteAt, len(confirmed), unknown)
}
