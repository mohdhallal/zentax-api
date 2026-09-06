package seed

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"sync"

	"github.com/jmoiron/sqlx"

	"github.com/mohamadhallal/zentax-api/app"
)

// fakeDB records every statement a seed function issues, without a database.
// Row-returning calls answer "no rows" (the *sqlx.Row comes from a stub
// database/sql driver, since sqlx.Row has no usable zero value), which is
// enough to test the statements, their order, their arguments and the
// transaction's tenant binding. The paths that need real ids back are proved
// against Postgres in phase 3.
type fakeDB struct {
	mu sync.Mutex

	// execs are the ExecContext calls, in order.
	execs []recordedStatement
	// queries are the row-returning calls, in order.
	queries []recordedStatement
	// txTenants are the app.tenant_id values bound by WithinTransaction, in order.
	txTenants []string

	// execErr, when set, is returned for any ExecContext whose statement
	// contains execErrMatch.
	execErrMatch string
	execErr      error
}

type recordedStatement struct {
	Query string
	Args  []any
}

func (f *fakeDB) record(dst *[]recordedStatement, query string, args []any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	*dst = append(*dst, recordedStatement{Query: query, Args: args})
}

func (f *fakeDB) execQueries() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.execs))
	for _, e := range f.execs {
		out = append(out, e.Query)
	}
	return out
}

func (f *fakeDB) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	f.record(&f.execs, query, args)
	if f.execErr != nil && f.execErrMatch != "" && strings.Contains(query, f.execErrMatch) {
		return nil, f.execErr
	}
	return fakeResult{}, nil
}

func (f *fakeDB) QueryRowxContext(ctx context.Context, query string, args ...any) *sqlx.Row {
	f.record(&f.queries, query, args)
	return emptyRowSource().QueryRowxContext(ctx, query, args...)
}

func (f *fakeDB) GetContext(_ context.Context, _ any, query string, args ...any) error {
	f.record(&f.queries, query, args)
	return sql.ErrNoRows
}

func (f *fakeDB) SelectContext(_ context.Context, _ any, query string, args ...any) error {
	f.record(&f.queries, query, args)
	return nil
}

func (f *fakeDB) QueryxContext(ctx context.Context, query string, args ...any) (*sqlx.Rows, error) {
	f.record(&f.queries, query, args)
	return emptyRowSource().QueryxContext(ctx, query, args...)
}

func (f *fakeDB) Rebind(query string) string { return query }

func (f *fakeDB) WithinTransaction(ctx context.Context, fn func(ctx context.Context) error) error {
	f.mu.Lock()
	f.txTenants = append(f.txTenants, app.GetTenantID(ctx))
	f.mu.Unlock()
	return fn(ctx)
}

type fakeResult struct{}

func (fakeResult) LastInsertId() (int64, error) { return 0, nil }
func (fakeResult) RowsAffected() (int64, error) { return 0, nil }

// --- an sql driver that answers every query with zero rows -----------------

var (
	rowSourceOnce sync.Once
	rowSource     *sqlx.DB
)

func emptyRowSource() *sqlx.DB {
	rowSourceOnce.Do(func() {
		sql.Register("seedstub", stubDriver{})
		conn, err := sql.Open("seedstub", "")
		if err != nil {
			panic(err)
		}
		rowSource = sqlx.NewDb(conn, "seedstub")
	})
	return rowSource
}

type stubDriver struct{}

func (stubDriver) Open(string) (driver.Conn, error) { return stubConn{}, nil }

type stubConn struct{}

func (stubConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("stub: Prepare unsupported")
}
func (stubConn) Close() error              { return nil }
func (stubConn) Begin() (driver.Tx, error) { return nil, errors.New("stub: Begin unsupported") }

func (stubConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return emptyRows{}, nil
}

type emptyRows struct{}

func (emptyRows) Columns() []string         { return []string{"id"} }
func (emptyRows) Close() error              { return nil }
func (emptyRows) Next([]driver.Value) error { return io.EOF }
