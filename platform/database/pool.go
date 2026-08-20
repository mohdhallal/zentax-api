package database

import (
	"context"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"

	"github.com/mohamadhallal/zentax-api/config"
	"github.com/mohamadhallal/zentax-api/logger"
)

func ConnectDB(dbCfg *config.DatabaseConfig) (*sqlx.DB, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(dbCfg.ConnectionTimeoutMs)*time.Millisecond)
	defer cancel()

	conn, err := sqlx.ConnectContext(ctx, "pgx", dbCfg.URL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	conn.SetMaxOpenConns(dbCfg.PoolMax)
	conn.SetMaxIdleConns(dbCfg.PoolMax / 2)
	conn.SetConnMaxIdleTime(time.Duration(dbCfg.IdleTimeoutMs) * time.Millisecond)
	conn.SetConnMaxLifetime(time.Duration(dbCfg.ConnMaxLifetimeMs) * time.Millisecond)

	logger.Log.Debug("Database connection established")
	return conn, nil
}
