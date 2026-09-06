package database

import (
	"context"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
)

// A statement runs on the ambient request transaction when there is one, and on
// the pool when there is not — which is how a route that declares no
// transaction (/auth/login) gets a connection per statement, held only for that
// statement.
func TestFromContext_TransactionOrPool(t *testing.T) {
	t.Parallel()

	db := &sqlx.DB{}
	e := NewExec(db)

	assert.Same(t, db, e.fromContext(context.Background()),
		"no ambient transaction: statements go to the pool")

	tx := &sqlx.Tx{}
	txCtx := context.WithValue(context.Background(), ctxTxKey, tx)
	assert.Same(t, tx, e.fromContext(txCtx))

	// An explicit nil is "none", not a transaction to run on.
	nilCtx := context.WithValue(context.Background(), ctxTxKey, (*sqlx.Tx)(nil))
	assert.Same(t, db, e.fromContext(nilCtx))
}
