package database

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jmoiron/sqlx"

	"github.com/mohamadhallal/zentax-api/app"
)

type ctxKey string

const ctxTxKey ctxKey = "db_tx"

// txFromContext returns the ambient request transaction, if any. A context
// carrying an explicit nil counts as "none".
func txFromContext(ctx context.Context) (*sqlx.Tx, bool) {
	tx, ok := ctx.Value(ctxTxKey).(*sqlx.Tx)
	return tx, ok && tx != nil
}

type Exec struct {
	db *sqlx.DB
}

func NewExec(db *sqlx.DB) *Exec {
	return &Exec{db: db}
}

func (e *Exec) fromContext(ctx context.Context) ExecerPg {
	if tx, ok := txFromContext(ctx); ok {
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
	if _, ok := txFromContext(ctx); ok {
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

	// ADR-0004: bind the caller's tenant to this transaction so Postgres RLS
	// isolates every query to that tenant. set_config(..., is_local=true) is
	// exactly SET LOCAL — transaction-scoped, hence safe under connection
	// pooling. A tenant-less transaction (e.g. health, or the tenants
	// registry) simply leaves the GUC unset; RLS on tenant-scoped tables then
	// fails closed (current_setting returns NULL → no rows match).
	//
	// ADR-0008: app.user_id is bound alongside it so created_by/updated_by
	// default from the acting user (actor-by-ID attribution). Empty when the
	// requester is not a user (NULLIF in the DDL turns it into NULL).
	if tenantID := app.GetTenantID(ctx); tenantID != "" {
		userID := ""
		if req := app.GetRequester(ctx); req != nil && req.IsUser() {
			userID = req.ID
		}
		if _, err := tx.ExecContext(ctx,
			`SELECT set_config('app.tenant_id', $1, true), set_config('app.user_id', $2, true)`,
			tenantID, userID); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("bind tenant to transaction: %w", err)
		}
	}

	if err := fn(txCtx); err != nil {
		_ = tx.Rollback()
		return err
	}

	return tx.Commit()
}
