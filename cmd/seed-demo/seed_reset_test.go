package main

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mohamadhallal/zentax-api/seed/demo/spec"
)

// The reset guard is the only thing between "run the demo tooling" and "delete
// tenants". It has to fail closed: any DSN it cannot positively identify as
// local is refused, and no flag turns that off.

func TestResetGuardAllowsLocalDatabases(t *testing.T) {
	noEnv := func(string) string { return "" }
	for _, dsn := range []string{
		"postgres://zentax_app:pw@localhost:5433/zentax?sslmode=disable",
		"postgres://zentax_app:pw@127.0.0.1:5433/zentax",
		"postgres://zentax_app:pw@[::1]:5433/zentax",
		"postgres:///zentax", // the local Unix socket
		"host=localhost port=5433 dbname=zentax user=zentax_app",
		"host=127.0.0.1 dbname=zentax",
		"HOST='localhost' dbname=zentax",
	} {
		assert.NoError(t, resetGuard(dsn, noEnv), dsn)
	}
}

func TestResetGuardRefusesRemoteDatabases(t *testing.T) {
	noEnv := func(string) string { return "" }
	for _, dsn := range []string{
		"postgres://app:pw@zentax-prod.abc123.eu-west-1.rds.amazonaws.com:5432/zentax",
		"postgres://app:pw@10.0.4.17:5432/zentax",
		"postgres://app:pw@db.internal:5432/zentax",
		"host=zentax-staging.internal dbname=zentax",
	} {
		err := resetGuard(dsn, noEnv)
		require.Error(t, err, dsn)
		assert.Contains(t, err.Error(), "not local")
	}
}

func TestResetGuardRefusesADSNItCannotRead(t *testing.T) {
	noEnv := func(string) string { return "" }
	for _, dsn := range []string{"", "   ", "not a dsn at all"} {
		err := resetGuard(dsn, noEnv)
		require.Error(t, err, "%q", dsn)
		assert.Contains(t, err.Error(), "no recognisable host")
	}
}

// The compose stack reaches Postgres by its service name, so a development
// environment is trusted on its own declaration — that is what APP_ENV means
// everywhere else in this repository.
func TestResetGuardAllowsDevelopmentEnvironments(t *testing.T) {
	development := func(name string) string {
		if name == "APP_ENV" {
			return developmentEnv
		}
		return ""
	}
	assert.NoError(t, resetGuard("postgres://zentax_app:pw@postgres:5432/zentax", development))

	production := func(name string) string {
		if name == "APP_ENV" {
			return "production"
		}
		return ""
	}
	assert.Error(t, resetGuard("postgres://zentax_app:pw@postgres:5432/zentax", production))
}

func TestDatabaseHost(t *testing.T) {
	assert.Equal(t, "localhost", databaseHost("postgres://u:p@localhost:5433/db"))
	assert.Equal(t, "::1", databaseHost("postgres://u:p@[::1]:5433/db"))
	assert.Equal(t, "", databaseHost("postgres:///db"))
	assert.Equal(t, "example.com", databaseHost("host=example.com port=5432 dbname=db"))
	assert.Equal(t, unknownHost, databaseHost("port=5432 dbname=db"))
	assert.Equal(t, unknownHost, databaseHost(""))
}

// runReset stops before any deletion when --yes is missing, and says what it
// would have deleted.
func TestRunResetRequiresYes(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	d := &deps{
		Tenants:     []spec.Tenant{{Key: "acme", Slug: "acme-demo"}},
		DatabaseURL: "postgres://zentax_app:pw@localhost:5433/zentax",
		Out:         out,
		Err:         errOut,
	}
	err := runReset(t.Context(), d)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--yes")
	assert.Contains(t, errOut.String(), "acme-demo")
	// Nothing was attempted: deps.DB is nil, so a deletion would have panicked.
	assert.Empty(t, out.String())
}

// The guard is checked before --yes, so a remote database is refused even when
// the operator confirmed.
func TestRunResetRefusesARemoteDatabaseEvenWithYes(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	d := &deps{
		Tenants:     []spec.Tenant{{Key: "acme", Slug: "acme-demo"}},
		DatabaseURL: "postgres://app:pw@zentax-prod.eu-west-1.rds.amazonaws.com:5432/zentax",
		Yes:         true,
		Out:         out,
		Err:         errOut,
	}
	err := runReset(t.Context(), d)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not local")
	assert.Empty(t, out.String())
}
