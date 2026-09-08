package main

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mohamadhallal/zentax-api/seed/demo/scale"
	"github.com/mohamadhallal/zentax-api/shared/dateonly"
)

// The scale fixture's generator mirrors the oracle's planner rather than
// importing it (the oracle is package main). This is the tripwire: every
// generated instance's four dates must equal what the oracle recomputes for
// the same spec — the writer persists the oracle's plan, so a divergence
// would put the generator's status bands on the wrong days.
func TestScalePlanMatchesTheOracle(t *testing.T) {
	asOf := dateonly.New(2026, 9, 8)
	res, err := scale.Generate(scale.Config{AsOf: asOf})
	require.NoError(t, err)

	world, err := buildOracleTenant(res.Spec, *res.Tenant, time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC), asOf)
	require.NoError(t, err)
	require.Equal(t, len(res.Plan), len(world.instances), "the oracle materializes exactly the generated instances")

	byRef := make(map[string]*oracleInstance, len(world.instances))
	for _, inst := range world.instances {
		byRef[inst.ref()] = inst
	}
	mismatches := 0
	for _, p := range res.Plan {
		ref := p.WorkflowKey + "/" + p.PeriodCode + "/" + p.TemplateKey
		inst, ok := byRef[ref]
		if !ok {
			t.Fatalf("the oracle has no instance %s", ref)
		}
		if inst.PeriodEnd != p.PeriodEnd || inst.FilingDeadline != p.FilingDeadline ||
			inst.DueDate != p.DueDate || inst.PaymentDeadline == nil || *inst.PaymentDeadline != p.PaymentDeadline {
			mismatches++
			if mismatches <= 5 {
				t.Errorf("%s: generator (end %s filing %s payment %s due %s) vs oracle (end %s filing %s payment %v due %s)",
					ref, p.PeriodEnd, p.FilingDeadline, p.PaymentDeadline, p.DueDate,
					inst.PeriodEnd, inst.FilingDeadline, inst.PaymentDeadline, inst.DueDate)
			}
		}
		assert.Equal(t, p.Status, inst.Status, "%s: status", ref)
	}
	assert.Zero(t, mismatches, "instances whose dates differ between the generator and the oracle")
}

// --- the bulk writer ------------------------------------------------------

func TestScaleBatches(t *testing.T) {
	assert.Equal(t, [][2]int{{0, 5}, {5, 10}, {10, 12}}, scaleBatches(12, 5))
	assert.Equal(t, [][2]int{{0, 3}}, scaleBatches(3, 5))
	assert.Nil(t, scaleBatches(0, 5))
	assert.Equal(t, [][2]int{{0, 1}, {1, 2}}, scaleBatches(2, 0), "a non-positive size degrades to one row per batch")
}

// One INSERT … SELECT FROM unnest(...) per batch: as many array parameters
// as columns, every parameter sliced to the batch, casts applied in SQL.
func TestScaleInsertRendersParallelArrays(t *testing.T) {
	rows := newScaleRows()
	for i := 0; i < 7; i++ {
		rows.text["id"] = append(rows.text["id"], "id"+string(rune('a'+i)))
		rows.text["notes"] = append(rows.text["notes"], "")
		rows.bools["approval_required"] = append(rows.bools["approval_required"], i%2 == 0)
		rows.ints["order_index"] = append(rows.ints["order_index"], int32(i))
		rows.n++
	}
	columns := []scaleColumn{
		{"id", "text", castUUID}, {"notes", "text", castTextNull},
		{"approval_required", "bool", ""}, {"order_index", "int", ""},
	}
	stmt, args := scaleInsert("task_instances", columns, rows, 5, 7)
	assert.Contains(t, stmt, "INSERT INTO task_instances (id, notes, approval_required, order_index)")
	assert.Contains(t, stmt, "SELECT t.c0::uuid, NULLIF(t.c1, ''), t.c2, t.c3")
	assert.Contains(t, stmt, "FROM unnest($1::text[], $2::text[], $3::bool[], $4::int4[]) AS t(c0, c1, c2, c3)")
	require.Len(t, args, 4)
	assert.Equal(t, []string{"idf", "idg"}, args[0])
	assert.Equal(t, []string{"", ""}, args[1])
	assert.Equal(t, []bool{false, true}, args[2])
	assert.Equal(t, []int32{5, 6}, args[3])
	assert.NotContains(t, stmt, "task_instances_p", "the parent table, never a partition")
}

// recordingDB captures every statement the writer issues; it answers nothing.
type recordingDB struct {
	execs []recordedExec
}

type recordedExec struct {
	query string
	args  []any
}

func (r *recordingDB) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	r.execs = append(r.execs, recordedExec{query: query, args: args})
	return recordedResult{}, nil
}
func (r *recordingDB) GetContext(context.Context, any, string, ...any) error      { return sql.ErrNoRows }
func (r *recordingDB) SelectContext(context.Context, any, string, ...any) error   { return nil }
func (r *recordingDB) QueryRowxContext(context.Context, string, ...any) *sqlx.Row { return nil }
func (r *recordingDB) QueryxContext(context.Context, string, ...any) (*sqlx.Rows, error) {
	return nil, nil
}
func (r *recordingDB) Rebind(q string) string { return q }
func (r *recordingDB) WithinTransaction(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

type recordedResult struct{}

func (recordedResult) LastInsertId() (int64, error) { return 0, nil }
func (recordedResult) RowsAffected() (int64, error) { return 1, nil }

// The instance writer emits ceil(n / batch) statements over the parent
// table, every row with the oracle's dates and instants: a completed row has
// completed_at, an approved one approved_by / approved_at, a pending one
// submitted_by / submitted_at, and an unassigned not_started one nothing.
func TestScaleWriteInstancesEmitsBatchedStatementsWithTheOraclesInstants(t *testing.T) {
	asOf := dateonly.New(2026, 9, 8)
	res, err := scale.Generate(scale.Config{Entities: 7, Years: 1, AsOf: asOf, Seed: 5})
	require.NoError(t, err)
	world, err := buildOracleTenant(res.Spec, *res.Tenant, time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC), asOf)
	require.NoError(t, err)

	m := &scaleIDs{
		tenantID: "tenant", adminID: "admin", users: map[string]string{},
		entities: map[string]string{}, types: map[string]string{}, eos: map[string]string{},
		wfs: map[string]string{}, tmpls: map[string]string{}, dts: map[string]string{"VAT": "dt-vat", "CIT": "dt-cit", "WHT": "dt-wht"},
	}
	for _, u := range res.Tenant.AllUsers() {
		m.users[u.Key] = "user-" + u.Key
	}
	for _, w := range res.Tenant.Workflows {
		m.wfs[w.Key] = "wf-" + w.Key
		for _, tmpl := range w.Templates {
			m.tmpls[w.Key+"|"+tmpl.Key] = "tmpl-" + w.Key + "-" + tmpl.Key
		}
	}

	db := &recordingDB{}
	n, err := scaleWriteInstances(context.Background(), db, res.Spec, world, m)
	require.NoError(t, err)
	assert.Equal(t, scale.InstancesFor(7, 1), n)
	assert.Len(t, db.execs, len(scaleBatches(n, scaleBatchRows)))

	total := 0
	completedWithInstant, approvedWithBy, pendingWithSubmission, bareNotStarted := 0, 0, 0, 0
	for _, e := range db.execs {
		assert.True(t, strings.HasPrefix(e.query, "INSERT INTO task_instances ("), e.query[:40])
		require.Len(t, e.args, len(scaleInstanceColumns))
		col := func(name string) []string {
			for i, c := range scaleInstanceColumns {
				if c.name == name {
					return e.args[i].([]string)
				}
			}
			t.Fatalf("no column %s", name)
			return nil
		}
		status, completedAt, approvedBy, approvedAt := col("status"), col("completed_at"), col("approved_by"), col("approved_at")
		submittedBy, submittedAt, assignee := col("submitted_by"), col("submitted_at"), col("assignee_id")
		due, taxData, taxDataStatus := col("due_date"), col("tax_data"), col("tax_data_status")
		for i := range status {
			total++
			assert.Len(t, due[i], 10, "a YYYY-MM-DD date")
			switch status[i] {
			case "completed":
				assert.NotEmpty(t, completedAt[i])
				assert.NotEmpty(t, assignee[i])
				completedWithInstant++
				if approvedBy[i] != "" {
					assert.Equal(t, completedAt[i], approvedAt[i], "approved_at = completed_at")
					assert.NotEmpty(t, submittedAt[i])
					assert.NotEmpty(t, submittedBy[i])
					approvedWithBy++
				}
			case "pending_approval":
				assert.NotEmpty(t, submittedBy[i])
				assert.NotEmpty(t, submittedAt[i])
				assert.Empty(t, completedAt[i])
				pendingWithSubmission++
			case "not_started":
				assert.Empty(t, completedAt[i])
				if assignee[i] == "" {
					bareNotStarted++
				}
			}
			if taxData[i] != "" {
				assert.Equal(t, "final", taxDataStatus[i])
				assert.True(t, strings.HasPrefix(taxData[i], "{"), "JSON object")
			} else {
				assert.Equal(t, "draft", taxDataStatus[i])
			}
		}
	}
	assert.Equal(t, n, total)
	assert.Positive(t, completedWithInstant)
	assert.Positive(t, approvedWithBy)
	assert.Positive(t, pendingWithSubmission)
	assert.Positive(t, bareNotStarted)
}

// --- the subcommand -------------------------------------------------------

func TestRunScaleDryRunWritesNothing(t *testing.T) {
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	err := runScale(context.Background(), ScaleDeps{
		Stdout: out, Stderr: errOut,
		Args: []string{"--dry-run", "--as-of", "2026-09-08", "--entities", "7", "--years", "1", "--seed", "9"},
	})
	require.NoError(t, err)
	assert.Contains(t, out.String(), "dry run: nothing was written")
	assert.Contains(t, out.String(), "7 entities, 1 years, asOf 2026-09-08")
	assert.Contains(t, out.String(), "instances by status:")
}

func TestRunScaleRequiresYes(t *testing.T) {
	out := &bytes.Buffer{}
	err := runScale(context.Background(), ScaleDeps{
		Stdout: out, Stderr: out,
		Args: []string{"--as-of", "2026-09-08", "--entities", "7", "--years", "1"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--yes")
}

// The generation record round-trips the Config the fixture is a function of,
// so a later verify --scale can recompute exactly the same world.
func TestScaleOutputRoundTripsTheConfig(t *testing.T) {
	cfg := scale.Config{Seed: 7, Entities: 12, Years: 2, AsOf: dateonly.New(2026, 9, 8),
		Slug: "scale", Name: "Scale Group AG", Timezone: "Europe/Berlin"}
	path := t.TempDir() + "/seed-demo-scale.json"
	out := newScaleOutput(time.Date(2026, 9, 8, 19, 20, 0, 0, time.UTC), cfg,
		scaleCounts{TenantID: "t", AdminID: "a", Instances: 6072, Elapsed: 1500 * time.Millisecond})
	require.NoError(t, writeScaleOutput(path, out))

	read, err := readScaleOutput(path)
	require.NoError(t, err)
	assert.Equal(t, out, read)
	restored, err := scaleConfigFromOutput(read)
	require.NoError(t, err)
	assert.Equal(t, cfg, restored)
	assert.Equal(t, 1500, read.Counts.ElapsedMs)
	assert.Contains(t, read.Warning, "different asOf")
}

func TestRunScaleRejectsABadAsOf(t *testing.T) {
	err := runScale(context.Background(), ScaleDeps{Args: []string{"--as-of", "yesterday"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--as-of")
}

// `reset --scale` addresses the fixture by slug and refuses to combine with
// --only; and --scale is reset's alone.
func TestResetScaleFlag(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	err := run(context.Background(), []string{"seed", "--scale", "--spec", "../../seed/demo/dataset.json"}, out, errOut)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--scale applies to `seed-demo reset`")

	err = run(context.Background(), []string{"reset", "--scale", "--only", "acme", "--spec", "../../seed/demo/dataset.json",
		"--database-url", "postgres://zentax_app:pw@localhost:5433/zentax"}, out, errOut)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--scale and --only do not combine")
}
