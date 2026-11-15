// Package pgexplain supports manual local integration gates. It only explains
// SELECTs captured while a real restpostgres adapter executes its Find method.
package pgexplain

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Gui-Rondon/hyrule/platform/postgres"
	"github.com/Gui-Rondon/hyrule/rest"
	"github.com/Gui-Rondon/hyrule/restpostgres"
)

type captureKey struct{}
type capture struct {
	sync.Mutex
	sql   string
	args  []any
	count int
}

// Tracer observes the query rather than replacing any database operation.
// Queries without a capture context (DDL, migrations, etc.) are ignored.
type Tracer struct{}

func (Tracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	if c, ok := ctx.Value(captureKey{}).(*capture); ok {
		c.Lock()
		defer c.Unlock()
		c.count++
		c.sql = d.SQL
		c.args = append([]any(nil), d.Args...)
	}
	return ctx
}
func (Tracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

type Case struct {
	WarmupFilter   any
	WantWarmupRows int
	Name           string
	Index          postgres.IndexSpec
	Schema         rest.Schema
	Filter         any
	Sort           []restpostgres.SortTerm
	Skip           int64
	Limit          int64
	WantRows       int
	Check          func([]rest.Doc) error
}

func indexName(spec postgres.IndexSpec) string {
	if spec.Name != "" {
		return spec.Name
	}
	fields := make([]string, len(spec.Fields))
	for i, f := range spec.Fields {
		fields[i] = f.Field
	}
	return spec.Table + "_" + strings.Join(fields, "_") + "_idx"
}

// Inventory rejects missing cases, duplicate names and cases whose index does
// not match a declaration, so a newly declared index cannot disappear silently.
func Inventory(declared []postgres.IndexSpec, cases []Case) error {
	seen := map[string]bool{}
	for _, c := range cases {
		if c.Name == "" || seen[c.Name] {
			return fmt.Errorf("empty/duplicate case name %q", c.Name)
		}
		seen[c.Name] = true
		found := false
		for _, spec := range declared {
			if reflect.DeepEqual(spec, c.Index) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("case %s has no matching index declaration", c.Name)
		}
	}
	for _, spec := range declared {
		found := false
		for _, c := range cases {
			if reflect.DeepEqual(spec, c.Index) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("index %s has no scenario", indexName(spec))
		}
	}
	if len(declared) == 0 {
		return fmt.Errorf("empty index inventory")
	}
	return nil
}

type Node struct {
	Type      string   `json:"Node Type"`
	Index     string   `json:"Index Name"`
	Estimated *float64 `json:"Plan Rows"`
	Actual    *float64 `json:"Actual Rows"`
	Loops     float64  `json:"Actual Loops"`
	Hits      *int64   `json:"Shared Hit Blocks"`
	Reads     *int64   `json:"Shared Read Blocks"`
	Plans     []Node   `json:"Plans"`
}
type planDocument struct {
	Plan        Node     `json:"Plan"`
	PlanningMS  *float64 `json:"Planning Time"`
	ExecutionMS *float64 `json:"Execution Time"`
}

type Result struct {
	WarmupRows      int             `json:"warmup_rows,omitempty"`
	WarmupArgs      []any           `json:"warmup_args,omitempty"`
	ExplainPlanKind string          `json:"explain_plan_kind"`
	Case            string          `json:"case"`
	Mode            string          `json:"mode"`
	ExpectedIndex   string          `json:"expected_index"`
	UsedIndexes     []string        `json:"used_indexes"`
	ScanTypes       []string        `json:"scan_types"`
	Status          string          `json:"status"`
	SQL             string          `json:"sql"`
	Args            []any           `json:"args"`
	Rows            int             `json:"rows"`
	ResultSHA256    string          `json:"result_sha256"`
	EstimatedRows   float64         `json:"estimated_rows"`
	ActualRows      float64         `json:"actual_rows"`
	PlanningMS      float64         `json:"planning_ms"`
	ExecutionMS     float64         `json:"execution_ms"`
	SharedHits      int64           `json:"shared_hits"`
	SharedReads     int64           `json:"shared_reads"`
	CustomPlans     int64           `json:"custom_plans"`
	GenericPlans    int64           `json:"generic_plans"`
	Plan            json.RawMessage `json:"plan"`
}

type Report struct {
	RegressionChecks []string             `json:"regression_checks,omitempty"`
	NegativeControls []string             `json:"negative_controls"`
	Settings         map[string]string    `json:"settings"`
	Complete         bool                 `json:"complete"`
	CreatedUTC       time.Time            `json:"created_utc"`
	ServerVersion    string               `json:"server_version"`
	Schema           string               `json:"schema"`
	SystemID         string               `json:"system_identifier"`
	RowsSeeded       int                  `json:"rows_seeded_per_table"`
	Declarations     []postgres.IndexSpec `json:"declarations"`
	Scenarios        []string             `json:"scenarios"`
	Results          []Result             `json:"results"`
	Errors           []string             `json:"errors,omitempty"`
}

func Collect(plan Node) (indexes, scans []string) {
	idx, scan := map[string]bool{}, map[string]bool{}
	var walk func(Node)
	walk = func(n Node) {
		if n.Index != "" {
			idx[n.Index] = true
		}
		if strings.Contains(n.Type, "Scan") {
			scan[n.Type] = true
		}
		for _, child := range n.Plans {
			walk(child)
		}
	}
	walk(plan)
	for name := range idx {
		indexes = append(indexes, name)
	}
	for name := range scan {
		scans = append(scans, name)
	}
	sort.Strings(indexes)
	sort.Strings(scans)
	return indexes, scans
}

func fingerprint(ids []string, ordered bool) string {
	copyIDs := append([]string(nil), ids...)
	if !ordered {
		sort.Strings(copyIDs)
	}
	sum := sha256.Sum256([]byte(strings.Join(copyIDs, "\n")))
	return fmt.Sprintf("%x", sum)
}

func readIDs(ctx context.Context, tx pgx.Tx, sql string, args []any) ([]string, error) {
	// SQL EXECUTE is a utility command. pgx's simple protocol safely renders its
	// arguments as SQL literals; do not implement a second literal renderer here.
	bind := append([]any{pgx.QueryExecModeSimpleProtocol}, args...)
	rows, err := tx.Query(ctx, sql, bind...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return nil, err
		}
		if len(values) == 0 {
			return nil, fmt.Errorf("empty adapter row")
		}
		id, ok := values[0].(string)
		if !ok {
			return nil, fmt.Errorf("adapter id is %T", values[0])
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

type warmup struct {
	args []any
	rows int
	hash string
}

func explainMode(ctx context.Context, pool *pgxpool.Pool, c Case, sql string, args []any, wantHash, mode string, warm warmup) (result Result, err error) {
	result = Result{Case: c.Name, Mode: mode, ExpectedIndex: indexName(c.Index), SQL: sql, Args: args}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return result, err
	}
	defer conn.Release()
	name := "a6_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return result, err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		tx.Rollback(cleanup)
		if err := conn.Conn().Deallocate(cleanup, name); err != nil {
			conn.Conn().Close(cleanup)
		}
	}()
	switch mode {
	case "auto", "force_custom_plan", "force_generic_plan":
	default:
		return result, fmt.Errorf("invalid planner mode")
	}
	if _, err := tx.Exec(ctx, "SET LOCAL plan_cache_mode = '"+mode+"'"); err != nil {
		return result, err
	}
	if _, err := conn.Conn().Prepare(ctx, name, sql); err != nil {
		return result, err
	}
	placeholders := make([]string, len(args))
	for i := range placeholders {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
	}
	execute := "EXECUTE " + pgx.Identifier{name}.Sanitize()
	if len(args) > 0 {
		execute += "(" + strings.Join(placeholders, ",") + ")"
	}
	// In auto mode, exercise the five custom executions preceding the planner's
	// generic/custom decision. Report counters rather than presuming its choice.
	if mode == "auto" {
		result.WarmupRows = warm.rows
		result.WarmupArgs = warm.args
		for range 5 {
			ids, err := readIDs(ctx, tx, execute, warm.args)
			if err != nil {
				return result, err
			}
			if len(ids) != warm.rows || fingerprint(ids, len(c.Sort) > 0) != warm.hash {
				return result, fmt.Errorf("auto warmup result differs from adapter")
			}
		}
	}
	var beforeCustom, beforeGeneric int64
	if err := tx.QueryRow(ctx, "SELECT custom_plans,generic_plans FROM pg_prepared_statements WHERE name=$1", name).Scan(&beforeCustom, &beforeGeneric); err != nil {
		return result, err
	}
	bind := append([]any{pgx.QueryExecModeSimpleProtocol}, args...)
	if err := tx.QueryRow(ctx, "EXPLAIN (ANALYZE, BUFFERS, SETTINGS, FORMAT JSON) "+execute, bind...).Scan(&result.Plan); err != nil {
		return result, err
	}
	if err := tx.QueryRow(ctx, "SELECT custom_plans,generic_plans FROM pg_prepared_statements WHERE name=$1", name).Scan(&result.CustomPlans, &result.GenericPlans); err != nil {
		return result, err
	}
	switch {
	case result.CustomPlans-beforeCustom == 1 && result.GenericPlans == beforeGeneric:
		result.ExplainPlanKind = "custom"
	case result.GenericPlans-beforeGeneric == 1 && result.CustomPlans == beforeCustom:
		result.ExplainPlanKind = "generic"
	default:
		return result, fmt.Errorf("could not identify the explained prepared plan")
	}
	var plan []planDocument
	if err := json.Unmarshal(result.Plan, &plan); err != nil {
		return result, err
	}
	if len(plan) != 1 || plan[0].Plan.Type == "" || plan[0].PlanningMS == nil || plan[0].ExecutionMS == nil || plan[0].Plan.Estimated == nil || plan[0].Plan.Actual == nil || plan[0].Plan.Hits == nil || plan[0].Plan.Reads == nil {
		return result, fmt.Errorf("incomplete EXPLAIN document")
	}
	root := plan[0]
	result.UsedIndexes, result.ScanTypes = Collect(root.Plan)
	result.Status = "expected_index_not_used"
	for _, name := range result.UsedIndexes {
		if name == result.ExpectedIndex {
			result.Status = "expected_index_used"
		}
	}
	result.EstimatedRows, result.ActualRows = *root.Plan.Estimated, *root.Plan.Actual
	result.PlanningMS, result.ExecutionMS = *root.PlanningMS, *root.ExecutionMS
	// Parent block counts already include their descendants; never sum the tree.
	result.SharedHits, result.SharedReads = *root.Plan.Hits, *root.Plan.Reads
	ids, err := readIDs(ctx, tx, execute, args)
	if err != nil {
		return result, err
	}
	result.Rows = len(ids)
	result.ResultSHA256 = fingerprint(ids, len(c.Sort) > 0)
	if result.Rows != c.WantRows || result.ResultSHA256 != wantHash || result.ActualRows != float64(c.WantRows) {
		return result, fmt.Errorf("result differs: rows=%d actual=%v expected=%d", result.Rows, result.ActualRows, c.WantRows)
	}
	if mode == "force_custom_plan" && len(args) > 0 && result.ExplainPlanKind != "custom" || mode == "force_generic_plan" && result.ExplainPlanKind != "generic" {
		return result, fmt.Errorf("prepared plan mode was not exercised")
	}
	return result, nil
}

// Run requires a pool configured with Tracer. Missing/invalid catalog indexes,
// SQL/result/report errors fail; an optimizer choosing another plan does not.
func (r *Report) Run(ctx context.Context, pool *pgxpool.Pool, c Case) (err error) {
	defer func() {
		if err != nil {
			r.Errors = append(r.Errors, c.Name+": "+err.Error())
		}
	}()
	var valid, ready bool
	if err := pool.QueryRow(ctx, `SELECT i.indisvalid,i.indisready FROM pg_index i
 JOIN pg_class idx ON idx.oid=i.indexrelid JOIN pg_class tab ON tab.oid=i.indrelid
 JOIN pg_namespace n ON n.oid=tab.relnamespace
 WHERE n.nspname=current_schema() AND tab.relname=$1 AND idx.relname=$2`, c.Index.Table, indexName(c.Index)).Scan(&valid, &ready); err != nil {
		return fmt.Errorf("index catalog: %w", err)
	}
	if !valid || !ready {
		return fmt.Errorf("index invalid/not ready")
	}
	adapter, err := restpostgres.NewAdapter(postgres.NewStore(pool, c.Index.Table), postgres.Transactor{Pool: pool}, restpostgres.Options{Schema: c.Schema, Retry: rest.DefaultRetryOptions()})
	if err != nil {
		return err
	}
	captured := &capture{}
	docs, err := adapter.Find(context.WithValue(ctx, captureKey{}, captured), c.Filter, c.Sort, nil, c.Skip, c.Limit)
	if err != nil {
		return err
	}
	if len(docs) != c.WantRows {
		return fmt.Errorf("adapter rows=%d expected=%d", len(docs), c.WantRows)
	}
	if c.Check != nil {
		if err := c.Check(docs); err != nil {
			return err
		}
	}
	captured.Lock()
	sql, args, count := captured.sql, captured.args, captured.count
	captured.Unlock()
	if count != 1 || !strings.HasPrefix(sql, "SELECT ") {
		return fmt.Errorf("expected one real adapter SELECT; captured=%d", count)
	}
	ids := make([]string, len(docs))
	for i, doc := range docs {
		ids[i] = fmt.Sprint(doc["_id"])
	}
	wantHash := fingerprint(ids, len(c.Sort) > 0)
	warm := warmup{args: args, rows: c.WantRows, hash: wantHash}
	if c.WarmupFilter != nil {
		warmCapture := &capture{}
		warmDocs, err := adapter.Find(context.WithValue(ctx, captureKey{}, warmCapture), c.WarmupFilter, c.Sort, nil, c.Skip, c.Limit)
		if err != nil {
			return err
		}
		if len(warmDocs) != c.WantWarmupRows {
			return fmt.Errorf("warmup rows=%d expected=%d", len(warmDocs), c.WantWarmupRows)
		}
		warmCapture.Lock()
		warmSQL, warmArgs, warmCount := warmCapture.sql, warmCapture.args, warmCapture.count
		warmCapture.Unlock()
		if warmCount != 1 || warmSQL != sql {
			return fmt.Errorf("warmup must execute the same prepared query shape")
		}
		warmIDs := make([]string, len(warmDocs))
		for i, doc := range warmDocs {
			warmIDs[i] = fmt.Sprint(doc["_id"])
		}
		warm = warmup{args: warmArgs, rows: c.WantWarmupRows, hash: fingerprint(warmIDs, len(c.Sort) > 0)}
	}
	for _, mode := range []string{"auto", "force_custom_plan", "force_generic_plan"} {
		result, err := explainMode(ctx, pool, c, sql, args, wantHash, mode, warm)
		r.Results = append(r.Results, result)
		if err != nil {
			return fmt.Errorf("%s: %w", mode, err)
		}
	}
	return nil
}

// Save preserves partial reports on failure. Plans and summaries contain only
// deterministic fixture SQL/data, never the connection URI or credentials.
func (r *Report) Save(dir string) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	// Publish an incomplete snapshot first. Only publish complete=true after
	// every plan and summary has been persisted, using atomic metadata rename.
	writeReport := func(complete bool) error {
		snapshot := *r
		snapshot.Complete = complete
		raw, err := json.MarshalIndent(snapshot, "", "  ")
		if err != nil {
			return err
		}
		target := filepath.Join(dir, "report.json")
		temp := target + ".tmp"
		defer os.Remove(temp)
		if err := os.WriteFile(temp, raw, 0644); err != nil {
			return err
		}
		return os.Rename(temp, target)
	}
	if err := writeReport(false); err != nil {
		return err
	}

	var summary strings.Builder
	fmt.Fprintf(&summary, "# PostgreSQL EXPLAIN report\n\nCompletion status: report.json\n\nServer: %s\nSchema: %s\n\n", r.ServerVersion, r.Schema)
	fmt.Fprintf(&summary, "Negative catalog controls: %s\n\n", strings.Join(r.NegativeControls, ", "))
	summary.WriteString("| Case | Mode / actual | Expected index | Used indexes | Scans | Status | Rows est/actual | Hits/reads | Plan/exec ms |\n|---|---|---|---|---|---|---|---|---|\n")
	for i, result := range r.Results {
		fmt.Fprintf(&summary, "| %s | %s / %s | %s | %s | %s | %s | %.0f/%.0f | %d/%d | %.3f/%.3f |\n", result.Case, result.Mode, result.ExplainPlanKind, result.ExpectedIndex, strings.Join(result.UsedIndexes, ", "), strings.Join(result.ScanTypes, ", "), result.Status, result.EstimatedRows, result.ActualRows, result.SharedHits, result.SharedReads, result.PlanningMS, result.ExecutionMS)
		if len(result.Plan) > 0 {
			if !json.Valid(result.Plan) {
				return fmt.Errorf("invalid plan JSON for %s", result.Case)
			}
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("plan-%02d-%s.json", i, result.Mode)), result.Plan, 0644); err != nil {
				return err
			}
		}
	}
	for _, err := range r.Errors {
		fmt.Fprintf(&summary, "\nError: %s\n", err)
	}
	summary.WriteString("\nPlanner non-use is observational. Times are local measurements, not SLAs.\n")
	if err := os.WriteFile(filepath.Join(dir, "summary.md"), []byte(summary.String()), 0644); err != nil {
		return err
	}
	return writeReport(r.Complete)
}
