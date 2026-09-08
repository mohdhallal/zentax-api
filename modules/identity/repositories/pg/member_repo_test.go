package pg

import (
	"context"
	"database/sql"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mohamadhallal/zentax-api/modules/identity/domain"
)

// recordingDB captures every row-returning statement without a database (the
// platform/seed/fake_db_test.go pattern): the tests assert the emitted SQL and
// bind order of the directory page + count, which is the whole contract of
// MemberRepo.List. An empty page short-circuits attachGrants, so exactly two
// statements are recorded per List call.
type recordingDB struct {
	queries []recorded
}

type recorded struct {
	Query string
	Args  []any
}

func (f *recordingDB) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return nil, nil //nolint:nilnil // never reached by List
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
	return nil, nil //nolint:nilnil // never reached by List
}

func (f *recordingDB) Rebind(query string) string { return query }

func listSQL(t *testing.T, args domain.ListMembersArgs) (page, count recorded) {
	t.Helper()
	db := &recordingDB{}
	_, _, err := NewMemberRepo(db).List(context.Background(), args)
	require.NoError(t, err)
	require.Len(t, db.queries, 2, "page then count")
	return db.queries[0], db.queries[1]
}

func TestMemberList_TenantOnly_NoOptionalPredicates(t *testing.T) {
	t.Parallel()

	page, count := listSQL(t, domain.ListMembersArgs{TenantID: "t-1", Limit: 100, Offset: 0})
	assert.Equal(t,
		`SELECT `+memberColumns+` FROM users WHERE tenant_id = $1 ORDER BY name, email, id LIMIT $2 OFFSET $3`,
		page.Query)
	assert.Equal(t, []any{"t-1", 100, 0}, page.Args)
	assert.Equal(t, `SELECT COUNT(*)::int FROM users WHERE tenant_id = $1`, count.Query)
	assert.Equal(t, []any{"t-1"}, count.Args)
}

func TestMemberList_SetFiltersAreRealPredicates(t *testing.T) {
	t.Parallel()

	page, count := listSQL(t, domain.ListMembersArgs{
		TenantID: "t-1", Kind: "human", Status: "active", Limit: 50, Offset: 100,
	})
	assert.Equal(t,
		`SELECT `+memberColumns+` FROM users WHERE tenant_id = $1 AND kind = $2 AND status = $3`+
			` ORDER BY name, email, id LIMIT $4 OFFSET $5`,
		page.Query)
	assert.Equal(t, []any{"t-1", "human", "active", 50, 100}, page.Args)
	assert.Equal(t, `SELECT COUNT(*)::int FROM users WHERE tenant_id = $1 AND kind = $2 AND status = $3`, count.Query)
	assert.Equal(t, []any{"t-1", "human", "active"}, count.Args)
	// Never the NULL-tolerant form: an unset filter is absent, not `$n IS NULL OR`.
	assert.NotContains(t, page.Query, "IS NULL")
}

func TestMemberList_Search_OneBindOverNameAndEmail_InPageAndCount(t *testing.T) {
	t.Parallel()

	page, count := listSQL(t, domain.ListMembersArgs{
		TenantID: "t-1", Status: "invited", Search: "  Under_score 100% \\ ", Limit: 20,
	})
	assert.Equal(t,
		`SELECT `+memberColumns+` FROM users WHERE tenant_id = $1 AND status = $2`+
			` AND (name ILIKE $3 ESCAPE '\' OR email ILIKE $3 ESCAPE '\')`+
			` ORDER BY name, email, id LIMIT $4 OFFSET $5`,
		page.Query)
	assert.Equal(t, []any{"t-1", "invited", `%Under\_score 100\% \\%`, 20, 0}, page.Args)
	assert.Equal(t,
		`SELECT COUNT(*)::int FROM users WHERE tenant_id = $1 AND status = $2`+
			` AND (name ILIKE $3 ESCAPE '\' OR email ILIKE $3 ESCAPE '\')`,
		count.Query)
	assert.Equal(t, []any{"t-1", "invited", `%Under\_score 100\% \\%`}, count.Args)
}

func TestMemberList_BlankSearch_IsNoPredicate(t *testing.T) {
	t.Parallel()

	page, count := listSQL(t, domain.ListMembersArgs{TenantID: "t-1", Search: "   ", Limit: 10})
	assert.NotContains(t, page.Query, "ILIKE")
	assert.NotContains(t, count.Query, "ILIKE")
	assert.Equal(t, []any{"t-1", 10, 0}, page.Args)
	assert.Equal(t, []any{"t-1"}, count.Args)
}
