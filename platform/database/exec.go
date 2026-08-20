package database

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jmoiron/sqlx"
)

type ctxKey string

const ctxTxKey ctxKey = "db_tx"

type Exec struct {
	db *sqlx.DB
}

func NewExec(db *sqlx.DB) *Exec {
	return &Exec{db: db}
}

func (e *Exec) fromContext(ctx context.Context) ExecerPg {
	if tx, ok := ctx.Value(ctxTxKey).(*sqlx.Tx); ok {
		return tx
	}
	return e.db
}

func (e *Exec) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return e.fromContext(ctx).ExecContext(ctx, query, args...)
}

func (e *Exec) GetContext(ctx context.Context, dest any, query string, args ...any) error {
	return e.fromContext(ctx).GetContext(ctx, dest, query, args...)
}

func (e *Exec) SelectContext(ctx context.Context, dest any, query string, args ...any) error {
	return e.fromContext(ctx).SelectContext(ctx, dest, query, args...)
}

func (e *Exec) QueryRowxContext(ctx context.Context, query string, args ...any) *sqlx.Row {
	return e.fromContext(ctx).QueryRowxContext(ctx, query, args...)
}

func (e *Exec) QueryxContext(ctx context.Context, query string, args ...any) (*sqlx.Rows, error) {
	return e.fromContext(ctx).QueryxContext(ctx, query, args...)
}

func (e *Exec) Rebind(query string) string {
	return e.db.Rebind(query)
}

func (e *Exec) WithinTransaction(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := ctx.Value(ctxTxKey).(*sqlx.Tx); ok {
		return fn(ctx)
	}

	tx, err := e.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	txCtx := context.WithValue(ctx, ctxTxKey, tx)

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()

	if err := fn(txCtx); err != nil {
		_ = tx.Rollback()
		return err
	}

	return tx.Commit()
}
