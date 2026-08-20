package database

import (
	"context"
	"database/sql"

	"github.com/jmoiron/sqlx"
)

type ExecerPg interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	GetContext(ctx context.Context, dest any, query string, args ...any) error
	SelectContext(ctx context.Context, dest any, query string, args ...any) error
	QueryRowxContext(ctx context.Context, query string, args ...any) *sqlx.Row
	QueryxContext(ctx context.Context, query string, args ...any) (*sqlx.Rows, error)
	Rebind(query string) string
}

type ExecerPgTx interface {
	WithinTransaction(ctx context.Context, fn func(ctx context.Context) error) error
}
