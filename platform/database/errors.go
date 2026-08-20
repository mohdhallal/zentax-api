package database

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

const (
	PgUniqueViolation     = "23505"
	PgForeignKeyViolation = "23503"
	PgNotNullViolation    = "23502"
	PgCheckViolation      = "23514"
)

func IsPgError(err error) *pgconn.PgError {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr
	}
	return nil
}

func IsUniqueViolation(err error) bool {
	pgErr := IsPgError(err)
	return pgErr != nil && pgErr.Code == PgUniqueViolation
}

func IsForeignKeyViolation(err error) bool {
	pgErr := IsPgError(err)
	return pgErr != nil && pgErr.Code == PgForeignKeyViolation
}

func GetConstraintName(err error) string {
	pgErr := IsPgError(err)
	if pgErr != nil {
		return pgErr.ConstraintName
	}
	return ""
}
