package database

import (
	"context"

	"github.com/jmoiron/sqlx"
)

// Pinger runs `SELECT 1` through the pool — the readiness probe's view of the
// database. A round trip (not sql.DB.Ping, which may reuse a cached
// connection without talking to the server) proves the server answers.
type Pinger struct {
	db *sqlx.DB
}

func NewPinger(db *sqlx.DB) *Pinger {
	return &Pinger{db: db}
}

func (p *Pinger) Ping(ctx context.Context) error {
	var one int
	return p.db.QueryRowContext(ctx, "SELECT 1").Scan(&one)
}
