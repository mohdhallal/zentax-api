package routing

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Body validation ---

type createBody struct {
	Email string `json:"email" validate:"required,email"`
	Name  string `json:"name"  validate:"required,max=100"`
}

type bodyWithDefault struct {
	Limit int `json:"limit" default:"20"`
}

func TestValidateBody_Valid(t *testing.T) {
	t.Parallel()

	raw := map[string]any{"email": "test@example.com", "name": "Alice"}
	result, err := validateBody(raw, &createBody{})

	require.NoError(t, err)
	body, ok := result.(*createBody)
	require.True(t, ok)
	assert.Equal(t, "test@example.com", body.Email)
	assert.Equal(t, "Alice", body.Name)
}

func TestValidateBody_MissingRequired(t *testing.T) {
	t.Parallel()

	raw := map[string]any{"name": "Alice"}
	_, err := validateBody(raw, &createBody{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "email")
	assert.Contains(t, err.Error(), "is required")
}

func TestValidateBody_InvalidEmail(t *testing.T) {
	t.Parallel()

	raw := map[string]any{"email": "not-an-email", "name": "Alice"}
	_, err := validateBody(raw, &createBody{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "email")
	assert.Contains(t, err.Error(), "invalid email format")
}

func TestValidateBody_UnknownField(t *testing.T) {
	t.Parallel()

	raw := map[string]any{"email": "a@b.com", "name": "Alice", "unknown": "val"}
	_, err := validateBody(raw, &createBody{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown")
}

func TestValidateBody_NilRaw_AppliesDefaults(t *testing.T) {
	t.Parallel()

	result, err := validateBody(nil, &bodyWithDefault{})

	require.NoError(t, err)
	body, ok := result.(*bodyWithDefault)
	require.True(t, ok)
	assert.Equal(t, 20, body.Limit)
}

// --- Query validation ---

type listQuery struct {
	Limit  int    `json:"limit"  default:"20"`
	Offset int    `json:"offset" default:"0"`
	Name   string `json:"name"`
}

func TestValidateQuery_Valid(t *testing.T) {
	t.Parallel()

	raw := map[string][]string{
		"limit":  {"10"},
		"offset": {"5"},
		"name":   {"alice"},
	}
	result, err := validateQuery(raw, &listQuery{})

	require.NoError(t, err)
	q, ok := result.(*listQuery)
	require.True(t, ok)
	assert.Equal(t, 10, q.Limit)
	assert.Equal(t, 5, q.Offset)
	assert.Equal(t, "alice", q.Name)
}

func TestValidateQuery_Empty_AppliesDefaults(t *testing.T) {
	t.Parallel()

	result, err := validateQuery(map[string][]string{}, &listQuery{})

	require.NoError(t, err)
	q, ok := result.(*listQuery)
	require.True(t, ok)
	assert.Equal(t, 20, q.Limit)
	assert.Equal(t, 0, q.Offset)
}

func TestValidateQuery_InvalidInt(t *testing.T) {
	t.Parallel()

	raw := map[string][]string{"limit": {"notanint"}}
	_, err := validateQuery(raw, &listQuery{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "limit")
	assert.Contains(t, err.Error(), "integer")
}

// --- Params validation ---

type idParams struct {
	ID string `json:"id" validate:"required,uuid"`
}

func TestValidateParams_Valid(t *testing.T) {
	t.Parallel()

	raw := map[string]string{"id": "550e8400-e29b-41d4-a716-446655440000"}
	result, err := validateParams(raw, &idParams{})

	require.NoError(t, err)
	p, ok := result.(*idParams)
	require.True(t, ok)
	assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", p.ID)
}

func TestValidateParams_InvalidUUID(t *testing.T) {
	t.Parallel()

	raw := map[string]string{"id": "not-a-uuid"}
	_, err := validateParams(raw, &idParams{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "id")
	assert.Contains(t, err.Error(), "valid UUID")
}

func TestValidateParams_Missing_Required(t *testing.T) {
	t.Parallel()

	raw := map[string]string{}
	_, err := validateParams(raw, &idParams{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "id")
	assert.Contains(t, err.Error(), "is required")
}

// --- setFieldFromString ---

func TestSetFieldFromString_Bool(t *testing.T) {
	t.Parallel()

	type boolQuery struct {
		Active bool `json:"active"`
	}

	raw := map[string][]string{"active": {"true"}}
	result, err := validateQuery(raw, &boolQuery{})

	require.NoError(t, err)
	q, ok := result.(*boolQuery)
	require.True(t, ok)
	assert.True(t, q.Active)
}

func TestSetFieldFromString_Uint(t *testing.T) {
	t.Parallel()

	type paginationQuery struct {
		Page uint `json:"page"`
	}

	raw := map[string][]string{"page": {"3"}}
	result, err := validateQuery(raw, &paginationQuery{})

	require.NoError(t, err)
	q, ok := result.(*paginationQuery)
	require.True(t, ok)
	assert.Equal(t, uint(3), q.Page)
}

func TestSetFieldFromString_Uint_Negative_Fails(t *testing.T) {
	t.Parallel()

	type paginationQuery struct {
		Page uint `json:"page"`
	}

	raw := map[string][]string{"page": {"-1"}}
	_, err := validateQuery(raw, &paginationQuery{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "page")
}

func TestSetFieldFromString_Pointer(t *testing.T) {
	t.Parallel()

	type filterQuery struct {
		Status *string `json:"status"`
	}

	raw := map[string][]string{"status": {"active"}}
	result, err := validateQuery(raw, &filterQuery{})

	require.NoError(t, err)
	q, ok := result.(*filterQuery)
	require.True(t, ok)
	require.NotNil(t, q.Status)
	assert.Equal(t, "active", *q.Status)
}

// --- applyDefaults ---

func TestApplyDefaults_DoesNotOverrideSetValue(t *testing.T) {
	t.Parallel()

	type query struct {
		Limit int `json:"limit" default:"20"`
	}

	raw := map[string][]string{"limit": {"5"}}
	result, err := validateQuery(raw, &query{})

	require.NoError(t, err)
	q, ok := result.(*query)
	require.True(t, ok)
	assert.Equal(t, 5, q.Limit)
}

// --- validationMessage ---

type strictBody struct {
	Email string `json:"email" validate:"required,email,max=10"`
	Role  string `json:"role"  validate:"required,oneof=admin user"`
	MinID int    `json:"minId" validate:"min=1"`
}

func TestValidationMessage_MaxString(t *testing.T) {
	t.Parallel()

	raw := map[string]any{"email": "toolongemail@example.com", "role": "admin"}
	_, err := validateBody(raw, &strictBody{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "at most 10 characters")
}

func TestValidationMessage_Oneof(t *testing.T) {
	t.Parallel()

	raw := map[string]any{"email": "a@b.com", "role": "superadmin"}
	_, err := validateBody(raw, &strictBody{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be one of")
	assert.Contains(t, err.Error(), "admin")
	assert.Contains(t, err.Error(), "user")
}

func TestValidationMessage_MinInt(t *testing.T) {
	t.Parallel()

	type body struct {
		Count int `json:"count" validate:"min=1"`
	}

	raw := map[string]any{"count": 0}
	_, err := validateBody(raw, &body{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least 1")
}

// --- StringSlice in query ---

func TestValidateQuery_StringSlice(t *testing.T) {
	t.Parallel()

	type filterQuery struct {
		Tags []string `json:"tags"`
	}

	raw := map[string][]string{"tags": {"go", "api", "test"}}
	result, err := validateQuery(raw, &filterQuery{})

	require.NoError(t, err)
	q, ok := result.(*filterQuery)
	require.True(t, ok)
	assert.Equal(t, []string{"go", "api", "test"}, q.Tags)
}
