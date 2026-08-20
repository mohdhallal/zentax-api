package routing

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Embedded anonymous struct support ---

type paginationBase struct {
	Limit  int `json:"limit"  default:"20"`
	Offset int `json:"offset" default:"0"`
}

type embeddedQuery struct {
	paginationBase
	Name string `json:"name"`
}

func TestValidateQuery_EmbeddedStruct_Decoded(t *testing.T) {
	t.Parallel()

	raw := map[string][]string{
		"limit":  {"50"},
		"offset": {"10"},
		"name":   {"alice"},
	}
	result, err := validateQuery(raw, &embeddedQuery{})

	require.NoError(t, err)
	q, ok := result.(*embeddedQuery)
	require.True(t, ok)
	assert.Equal(t, 50, q.Limit)
	assert.Equal(t, 10, q.Offset)
	assert.Equal(t, "alice", q.Name)
}

func TestValidateQuery_EmbeddedStruct_DefaultsApplied(t *testing.T) {
	t.Parallel()

	result, err := validateQuery(map[string][]string{}, &embeddedQuery{})

	require.NoError(t, err)
	q, ok := result.(*embeddedQuery)
	require.True(t, ok)
	assert.Equal(t, 20, q.Limit)
	assert.Equal(t, 0, q.Offset)
}

func TestValidateQuery_EmbeddedStruct_InvalidField_ReturnsFieldError(t *testing.T) {
	t.Parallel()

	raw := map[string][]string{"limit": {"notanint"}}
	_, err := validateQuery(raw, &embeddedQuery{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "limit")
}

// Embedded in params map.

type baseParams struct {
	Version string `json:"version" validate:"required"`
}

type embeddedParams struct {
	baseParams
	ID string `json:"id" validate:"required,uuid"`
}

func TestValidateParams_EmbeddedStruct_Decoded(t *testing.T) {
	t.Parallel()

	raw := map[string]string{
		"version": "v2",
		"id":      "550e8400-e29b-41d4-a716-446655440000",
	}
	result, err := validateParams(raw, &embeddedParams{})

	require.NoError(t, err)
	p, ok := result.(*embeddedParams)
	require.True(t, ok)
	assert.Equal(t, "v2", p.Version)
	assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", p.ID)
}

func TestValidateParams_EmbeddedStruct_MissingField_ReturnsError(t *testing.T) {
	t.Parallel()

	raw := map[string]string{"id": "550e8400-e29b-41d4-a716-446655440000"}
	_, err := validateParams(raw, &embeddedParams{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "version")
}

// --- Pointer default ---

type queryWithPtrDefault struct {
	PageSize *int `json:"pageSize" default:"10"`
}

func TestApplyDefaults_PointerField_SetToDefault(t *testing.T) {
	t.Parallel()

	result, err := validateQuery(map[string][]string{}, &queryWithPtrDefault{})

	require.NoError(t, err)
	q, ok := result.(*queryWithPtrDefault)
	require.True(t, ok)
	require.NotNil(t, q.PageSize)
	assert.Equal(t, 10, *q.PageSize)
}

func TestApplyDefaults_PointerField_ExplicitValue_NotOverridden(t *testing.T) {
	t.Parallel()

	raw := map[string][]string{"pageSize": {"99"}}
	result, err := validateQuery(raw, &queryWithPtrDefault{})

	require.NoError(t, err)
	q, ok := result.(*queryWithPtrDefault)
	require.True(t, ok)
	require.NotNil(t, q.PageSize)
	assert.Equal(t, 99, *q.PageSize)
}

// --- Embedded struct default propagation ---

func TestApplyDefaults_EmbeddedStruct_DefaultsPropagated(t *testing.T) {
	t.Parallel()

	type inner struct {
		Size int `json:"size" default:"5"`
	}
	type outer struct {
		inner
		Name string `json:"name"`
	}

	result, err := validateQuery(map[string][]string{}, &outer{})

	require.NoError(t, err)
	q, ok := result.(*outer)
	require.True(t, ok)
	assert.Equal(t, 5, q.Size)
}
