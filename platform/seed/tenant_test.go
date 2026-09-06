package seed

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validParams() Params {
	return Params{
		Slug:     "acme",
		Name:     "Acme Group",
		Email:    "admin@acme.test",
		Password: "demo-password-1",
		UserName: "Group Head of Tax",
		Timezone: "Europe/Berlin",
	}
}

func TestCreateTenant_RejectsMissingFieldsBeforeTouchingTheDatabase(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		mutate  func(*Params)
		wantErr string
	}{
		{"no slug", func(p *Params) { p.Slug = "" }, "tenant slug is required"},
		{"blank slug", func(p *Params) { p.Slug = "   " }, "tenant slug is required"},
		{"no name", func(p *Params) { p.Name = "" }, "tenant name is required"},
		{"no email", func(p *Params) { p.Email = "" }, "admin email is required"},
		{"no password", func(p *Params) { p.Password = "" }, "admin password is required"},
		{"unloadable timezone", func(p *Params) { p.Timezone = "Mars/Olympus" }, "validate timezone"},
		{"Local is not a zone", func(p *Params) { p.Timezone = "Local" }, "validate timezone"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			params := validParams()
			tc.mutate(&params)
			db := &fakeDB{}

			_, err := CreateTenant(t.Context(), db, params)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
			assert.Empty(t, db.queries, "nothing may be written when the params are invalid")
			assert.Empty(t, db.execs)
		})
	}
}

func TestCreateTenant_NilDatabase(t *testing.T) {
	t.Parallel()
	_, err := CreateTenant(t.Context(), nil, validParams())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil database")
}

func TestCreateTenant_InsertsTheTenantRowFirst(t *testing.T) {
	t.Parallel()
	db := &fakeDB{}

	// The fake cannot return the RETURNING id, so this stops at the first
	// statement — which is the one under test.
	_, err := CreateTenant(t.Context(), db, validParams())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create tenant:", "the stage is named in the error")

	require.Len(t, db.queries, 1)
	assert.Contains(t, db.queries[0].Query, "INSERT INTO tenants (slug, name, timezone)")
	assert.Equal(t, []any{"acme", "Acme Group", "Europe/Berlin"}, db.queries[0].Args)
}

func TestCreateTenant_AppliesDefaults(t *testing.T) {
	t.Parallel()
	params := validParams()
	params.Timezone = ""
	params.UserName = ""
	db := &fakeDB{}

	_, err := CreateTenant(t.Context(), db, params)
	require.Error(t, err)

	require.Len(t, db.queries, 1)
	assert.Equal(t, "UTC", db.queries[0].Args[2], "an empty timezone defaults to UTC (ADR-0003)")
}

func TestCreateTenant_NormalisesTheEmailAndSlug(t *testing.T) {
	t.Parallel()
	params := validParams()
	params.Email = "  ADMIN@Acme.TEST  "
	params.Slug = " acme "
	normalised, err := params.normalized()
	require.NoError(t, err)
	assert.Equal(t, "admin@acme.test", normalised.Email, "users.email is stored lowercased")
	assert.Equal(t, "acme", normalised.Slug)
	assert.Equal(t, "Group Head of Tax", normalised.UserName)
}

func TestParamsNormalized_DefaultUserName(t *testing.T) {
	t.Parallel()
	params := validParams()
	params.UserName = "  "
	normalised, err := params.normalized()
	require.NoError(t, err)
	assert.Equal(t, "Admin", normalised.UserName)
}

func TestCreateTenant_ErrorsNeverEchoThePassword(t *testing.T) {
	t.Parallel()
	const secret = "correct-horse-battery-staple"
	params := validParams()
	params.Password = secret
	params.Timezone = "Mars/Olympus"

	_, err := CreateTenant(t.Context(), &fakeDB{}, params)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), secret)

	// and on the database path too
	params.Timezone = "UTC"
	_, err = CreateTenant(t.Context(), &fakeDB{}, params)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), secret)
}
