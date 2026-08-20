package database

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
)

func TestIsUniqueViolation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "unique violation",
			err:      &pgconn.PgError{Code: PgUniqueViolation},
			expected: true,
		},
		{
			name:     "foreign key violation",
			err:      &pgconn.PgError{Code: PgForeignKeyViolation},
			expected: false,
		},
		{
			name:     "generic error",
			err:      errors.New("something broke"),
			expected: false,
		},
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.err == nil {
				assert.False(t, IsUniqueViolation(errors.New("not pg")))
				return
			}
			assert.Equal(t, tc.expected, IsUniqueViolation(tc.err))
		})
	}
}

func TestIsForeignKeyViolation(t *testing.T) {
	t.Parallel()

	assert.True(t, IsForeignKeyViolation(&pgconn.PgError{Code: PgForeignKeyViolation}))
	assert.False(t, IsForeignKeyViolation(&pgconn.PgError{Code: PgUniqueViolation}))
	assert.False(t, IsForeignKeyViolation(errors.New("generic")))
}

func TestIsPgError(t *testing.T) {
	t.Parallel()

	pgErr := &pgconn.PgError{Code: "42000"}
	assert.Equal(t, pgErr, IsPgError(pgErr))
	assert.Nil(t, IsPgError(errors.New("not pg")))
}

func TestGetConstraintName(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "users_email_key", GetConstraintName(&pgconn.PgError{ConstraintName: "users_email_key"}))
	assert.Equal(t, "", GetConstraintName(errors.New("not pg")))
}
