package repositories

import (
	"context"
	"database/sql"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sharedtypes "github.com/mohamadhallal/zentax-api/shared/types"
)

// recordingDB captures the statement and arguments of every row-returning
// call, without a database (the pattern of platform/seed/fake_db_test.go):
// the tests below assert the emitted SQL text and parameter order, which is
// the whole contract of List / GetTotal.
type recordingDB struct {
	queries []recorded
}

type recorded struct {
	Query string
	Args  []any
}

func (f *recordingDB) last() recorded { return f.queries[len(f.queries)-1] }

func (f *recordingDB) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return nil, nil //nolint:nilnil // never reached by List/GetTotal
}

func (f *recordingDB) GetContext(_ context.Context, _ any, query string, args ...any) error {
	f.queries = append(f.queries, recorded{Query: query, Args: args})
	return nil
}

func (f *recordingDB) SelectContext(_ context.Context, _ any, query string, args ...any) error {
	f.queries = append(f.queries, recorded{Query: query, Args: args})
	return nil
}

func (f *recordingDB) QueryRowxContext(context.Context, string, ...any) *sqlx.Row { return nil }

func (f *recordingDB) QueryxContext(context.Context, string, ...any) (*sqlx.Rows, error) {
	return nil, nil //nolint:nilnil // never reached by List/GetTotal
}

func (f *recordingDB) Rebind(query string) string { return query }

type row struct {
	ID string `db:"id"`
}

func newRepo(cfg SQLConfig) (*BaseRepo[row, string], *recordingDB) {
	db := &recordingDB{}
	repo := NewBaseRepo[row, string](db, cfg)
	return &repo, db
}

func taskConfig() SQLConfig {
	return SQLConfig{
		ListBase: "SELECT id FROM task_instances",
		Count:    "SELECT COUNT(*)::int AS total FROM task_instances",
		AllowedColumns: map[string]bool{
			"due_date": true, "created_at": true, "status": true, "workflow_id": true, "id": true,
		},
		DefaultOrderBy: "due_date",
	}
}

func workflowConfig() SQLConfig {
	return SQLConfig{
		ListBase:         "SELECT id FROM workflows",
		Count:            "SELECT COUNT(*)::int AS total FROM workflows",
		AllowedColumns:   map[string]bool{"created_at": true, "name": true, "status": true, "financial_year": true},
		DefaultOrderBy:   "created_at",
		DefaultOrderDesc: true,
		NullableFilters:  map[string]bool{"financial_year": true},
	}
}

func list(t *testing.T, repo *BaseRepo[row, string], db *recordingDB, args sharedtypes.ListArgs) recorded {
	t.Helper()
	_, err := repo.List(context.Background(), args)
	require.NoError(t, err)
	return db.last()
}

func total(t *testing.T, repo *BaseRepo[row, string], db *recordingDB, filters []sharedtypes.Filter) recorded {
	t.Helper()
	_, err := repo.GetTotal(context.Background(), filters)
	require.NoError(t, err)
	return db.last()
}

// --- deterministic ordering ---

func TestList_DefaultOrder_AppendsTieBreakerInDefaultDirection(t *testing.T) {
	t.Parallel()

	repo, db := newRepo(taskConfig())
	got := list(t, repo, db, sharedtypes.ListArgs{Limit: 20, Offset: 40})
	assert.Equal(t, "SELECT id FROM task_instances ORDER BY due_date ASC, id ASC LIMIT $1 OFFSET $2", got.Query)
	assert.Equal(t, []any{20, 40}, got.Args)

	repo, db = newRepo(workflowConfig())
	got = list(t, repo, db, sharedtypes.ListArgs{Limit: 5, Offset: 0})
	assert.Equal(t, "SELECT id FROM workflows ORDER BY created_at DESC, id DESC LIMIT $1 OFFSET $2", got.Query)
}

func TestList_CallerSort_TieBreakerFollowsPrimaryDirection(t *testing.T) {
	t.Parallel()

	repo, db := newRepo(taskConfig())

	got := list(t, repo, db, sharedtypes.ListArgs{
		Limit: 5, Sort: []sharedtypes.SortField{{Column: "created_at", Desc: false}},
	})
	assert.Equal(t, "SELECT id FROM task_instances ORDER BY created_at ASC, id ASC LIMIT $1 OFFSET $2", got.Query)

	got = list(t, repo, db, sharedtypes.ListArgs{
		Limit: 5, Sort: []sharedtypes.SortField{{Column: "due_date", Desc: true}},
	})
	assert.Equal(t, "SELECT id FROM task_instances ORDER BY due_date DESC, id DESC LIMIT $1 OFFSET $2", got.Query)

	// Several clauses: the tie-breaker follows the FIRST one, and comes last.
	got = list(t, repo, db, sharedtypes.ListArgs{
		Limit: 5, Sort: []sharedtypes.SortField{{Column: "due_date", Desc: true}, {Column: "created_at", Desc: false}},
	})
	assert.Equal(t, "SELECT id FROM task_instances ORDER BY due_date DESC, created_at ASC, id DESC LIMIT $1 OFFSET $2", got.Query)
}

func TestList_CallerSortNamingTieBreaker_NotAppendedTwice(t *testing.T) {
	t.Parallel()

	repo, db := newRepo(taskConfig())

	got := list(t, repo, db, sharedtypes.ListArgs{
		Limit: 5, Sort: []sharedtypes.SortField{{Column: "id", Desc: false}},
	})
	assert.Equal(t, "SELECT id FROM task_instances ORDER BY id ASC LIMIT $1 OFFSET $2", got.Query)

	got = list(t, repo, db, sharedtypes.ListArgs{
		Limit: 5, Sort: []sharedtypes.SortField{{Column: "due_date", Desc: false}, {Column: "id", Desc: true}},
	})
	assert.Equal(t, "SELECT id FROM task_instances ORDER BY due_date ASC, id DESC LIMIT $1 OFFSET $2", got.Query)
}

func TestList_DisallowedSort_FallsBackToDefaultWithTieBreaker(t *testing.T) {
	t.Parallel()

	repo, db := newRepo(taskConfig())
	got := list(t, repo, db, sharedtypes.ListArgs{
		Limit: 5, Sort: []sharedtypes.SortField{{Column: "tenant_id; DROP TABLE x", Desc: true}},
	})
	assert.Equal(t, "SELECT id FROM task_instances ORDER BY due_date ASC, id ASC LIMIT $1 OFFSET $2", got.Query)
}

func TestList_CustomTieBreaker(t *testing.T) {
	t.Parallel()

	cfg := workflowConfig()
	cfg.ListBase = "SELECT w.id FROM workflows w"
	cfg.AllowedColumns = map[string]bool{"w.created_at": true, "w.name": true}
	cfg.DefaultOrderBy = "w.created_at"
	cfg.TieBreaker = "w.id"
	repo, db := newRepo(cfg)

	got := list(t, repo, db, sharedtypes.ListArgs{Limit: 5})
	assert.Equal(t, "SELECT w.id FROM workflows w ORDER BY w.created_at DESC, w.id DESC LIMIT $1 OFFSET $2", got.Query)

	got = list(t, repo, db, sharedtypes.ListArgs{Limit: 5, Sort: []sharedtypes.SortField{{Column: "w.name"}}})
	assert.Equal(t, "SELECT w.id FROM workflows w ORDER BY w.name ASC, w.id ASC LIMIT $1 OFFSET $2", got.Query)
}

func TestList_DefaultOrderIsTieBreaker_NotAppendedTwice(t *testing.T) {
	t.Parallel()

	cfg := taskConfig()
	cfg.DefaultOrderBy = "id"
	cfg.DefaultOrderDesc = true
	repo, db := newRepo(cfg)

	got := list(t, repo, db, sharedtypes.ListArgs{Limit: 5})
	assert.Equal(t, "SELECT id FROM task_instances ORDER BY id DESC LIMIT $1 OFFSET $2", got.Query)
}

// --- filters + placeholders ---

func TestList_Filters_PlaceholdersInOrderThenLimitOffset(t *testing.T) {
	t.Parallel()

	repo, db := newRepo(taskConfig())
	got := list(t, repo, db, sharedtypes.ListArgs{
		Limit: 10, Offset: 30,
		Filters: []sharedtypes.Filter{
			{Column: "workflow_id", Value: "wf-1"},
			{Column: "tenant_id", Value: "smuggled"}, // not allowed: dropped, consumes no placeholder
			{Column: "status", Value: "in_progress"},
		},
		Sort: []sharedtypes.SortField{{Column: "created_at", Desc: true}},
	})
	assert.Equal(t,
		"SELECT id FROM task_instances WHERE workflow_id = $1 AND status = $2 ORDER BY created_at DESC, id DESC LIMIT $3 OFFSET $4",
		got.Query)
	assert.Equal(t, []any{"wf-1", "in_progress", 10, 30}, got.Args)
}

func TestGetTotal_SameWhereAsList(t *testing.T) {
	t.Parallel()

	repo, db := newRepo(taskConfig())
	filters := []sharedtypes.Filter{{Column: "workflow_id", Value: "wf-1"}, {Column: "status", Value: "blocked"}}
	got := total(t, repo, db, filters)
	assert.Equal(t, "SELECT COUNT(*)::int AS total FROM task_instances WHERE workflow_id = $1 AND status = $2", got.Query)
	assert.Equal(t, []any{"wf-1", "blocked"}, got.Args)

	got = total(t, repo, db, nil)
	assert.Equal(t, "SELECT COUNT(*)::int AS total FROM task_instances", got.Query)
	assert.Empty(t, got.Args)
}

// --- multi-value filters ---

func TestList_MultiValueFilter_EmitsAny(t *testing.T) {
	t.Parallel()

	repo, db := newRepo(workflowConfig())
	got := list(t, repo, db, sharedtypes.ListArgs{
		Limit: 25, Filters: []sharedtypes.Filter{{Column: "status", Value: []string{"active", "draft"}}},
	})
	assert.Equal(t,
		"SELECT id FROM workflows WHERE status = ANY($1) ORDER BY created_at DESC, id DESC LIMIT $2 OFFSET $3",
		got.Query)
	assert.Equal(t, []any{[]string{"active", "draft"}, 25, 0}, got.Args)

	got = total(t, repo, db, []sharedtypes.Filter{{Column: "status", Value: []string{"active", "draft"}}})
	assert.Equal(t, "SELECT COUNT(*)::int AS total FROM workflows WHERE status = ANY($1)", got.Query)
	assert.Equal(t, []any{[]string{"active", "draft"}}, got.Args)
}

func TestList_MultiValueFilter_SingleElementStillAny(t *testing.T) {
	t.Parallel()

	repo, db := newRepo(workflowConfig())
	got := list(t, repo, db, sharedtypes.ListArgs{
		Limit: 25, Filters: []sharedtypes.Filter{{Column: "status", Value: []string{"active"}}},
	})
	assert.Equal(t,
		"SELECT id FROM workflows WHERE status = ANY($1) ORDER BY created_at DESC, id DESC LIMIT $2 OFFSET $3",
		got.Query)
	assert.Equal(t, []any{[]string{"active"}, 25, 0}, got.Args)
}

func TestList_NullableFilter_NoneAmongValues(t *testing.T) {
	t.Parallel()

	repo, db := newRepo(workflowConfig())
	in := []string{"2026", "none", "2025"}
	got := list(t, repo, db, sharedtypes.ListArgs{
		Limit: 25, Offset: 50,
		Filters: []sharedtypes.Filter{
			{Column: "financial_year", Value: in},
			{Column: "status", Value: []string{"active", "draft"}},
		},
	})
	assert.Equal(t,
		"SELECT id FROM workflows WHERE (financial_year = ANY($1) OR financial_year IS NULL) AND status = ANY($2)"+
			" ORDER BY created_at DESC, id DESC LIMIT $3 OFFSET $4",
		got.Query)
	assert.Equal(t, []any{[]string{"2026", "2025"}, []string{"active", "draft"}, 25, 50}, got.Args)
	assert.Equal(t, []string{"2026", "none", "2025"}, in, "the caller's slice is not mutated")

	got = total(t, repo, db, []sharedtypes.Filter{
		{Column: "financial_year", Value: []string{"2026", "none"}},
		{Column: "status", Value: []string{"active", "draft"}},
	})
	assert.Equal(t,
		"SELECT COUNT(*)::int AS total FROM workflows WHERE (financial_year = ANY($1) OR financial_year IS NULL) AND status = ANY($2)",
		got.Query)
	assert.Equal(t, []any{[]string{"2026"}, []string{"active", "draft"}}, got.Args)
}

func TestList_NullableFilter_NoneAlone_IsNullConsumesNoPlaceholder(t *testing.T) {
	t.Parallel()

	repo, db := newRepo(workflowConfig())
	got := list(t, repo, db, sharedtypes.ListArgs{
		Limit: 25,
		Filters: []sharedtypes.Filter{
			{Column: "financial_year", Value: []string{"none"}},
			{Column: "status", Value: "draft"},
		},
	})
	assert.Equal(t,
		"SELECT id FROM workflows WHERE financial_year IS NULL AND status = $1 ORDER BY created_at DESC, id DESC LIMIT $2 OFFSET $3",
		got.Query)
	assert.Equal(t, []any{"draft", 25, 0}, got.Args)

	// The single-value form honours the sentinel too.
	got = total(t, repo, db, []sharedtypes.Filter{{Column: "financial_year", Value: "none"}})
	assert.Equal(t, "SELECT COUNT(*)::int AS total FROM workflows WHERE financial_year IS NULL", got.Query)
	assert.Empty(t, got.Args)
}

func TestList_NoneOnNonNullableColumn_IsAnOrdinaryValue(t *testing.T) {
	t.Parallel()

	repo, db := newRepo(workflowConfig())
	got := list(t, repo, db, sharedtypes.ListArgs{
		Limit: 25, Filters: []sharedtypes.Filter{{Column: "status", Value: []string{"none"}}},
	})
	assert.Equal(t,
		"SELECT id FROM workflows WHERE status = ANY($1) ORDER BY created_at DESC, id DESC LIMIT $2 OFFSET $3",
		got.Query)
	assert.Equal(t, []any{[]string{"none"}, 25, 0}, got.Args)

	got = total(t, repo, db, []sharedtypes.Filter{{Column: "status", Value: "none"}})
	assert.Equal(t, "SELECT COUNT(*)::int AS total FROM workflows WHERE status = $1", got.Query)
	assert.Equal(t, []any{"none"}, got.Args)
}
