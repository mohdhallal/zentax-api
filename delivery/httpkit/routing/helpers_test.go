package routing

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	gochi "github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	sharedtypes "github.com/mohamadhallal/zentax-api/shared/types"
)

// --- shouldMount ---

func TestShouldMount(t *testing.T) {
	t.Parallel()

	cases := []struct {
		exposure types.Exposure
		mode     types.ServerMode
		want     bool
	}{
		{types.Exposures.External, types.ModeExternal, true},
		{types.Exposures.External, types.ModeInternal, false},
		{types.Exposures.Internal, types.ModeExternal, false},
		{types.Exposures.Internal, types.ModeInternal, true},
		{types.Exposures.Both, types.ModeExternal, true},
		{types.Exposures.Both, types.ModeInternal, true},
	}

	for _, tc := range cases {
		t.Run(string(tc.exposure)+"/"+string(tc.mode), func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, shouldMount(tc.exposure, tc.mode))
		})
	}
}

// --- sanitize ---

func TestSanitize_String_Trimmed(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "hello", sanitize("  hello  "))
}

func TestSanitize_Nil_ReturnsNil(t *testing.T) {
	t.Parallel()
	assert.Nil(t, sanitize(nil))
}

func TestSanitize_Number_Unchanged(t *testing.T) {
	t.Parallel()
	assert.Equal(t, float64(42), sanitize(float64(42)))
}

func TestSanitize_Bool_Unchanged(t *testing.T) {
	t.Parallel()
	assert.Equal(t, true, sanitize(true))
}

func TestSanitize_Map_RecursiveTrim(t *testing.T) {
	t.Parallel()

	input := map[string]any{
		"name":  "  Alice  ",
		"email": " alice@example.com ",
	}
	result, ok := sanitize(input).(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "Alice", result["name"])
	assert.Equal(t, "alice@example.com", result["email"])
}

func TestSanitize_Slice_RecursiveTrim(t *testing.T) {
	t.Parallel()

	input := []any{"  a  ", "  b  "}
	result, ok := sanitize(input).([]any)
	require.True(t, ok)
	assert.Equal(t, "a", result[0])
	assert.Equal(t, "b", result[1])
}

func TestSanitize_NestedMap_DeepTrim(t *testing.T) {
	t.Parallel()

	input := map[string]any{
		"user": map[string]any{
			"name": "  Bob  ",
		},
	}
	result, ok := sanitize(input).(map[string]any)
	require.True(t, ok)
	user, ok := result["user"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "Bob", user["name"])
}

// --- parseAndSanitizeBody ---

func TestParseAndSanitizeBody_NilBody_ReturnsNil(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", http.NoBody)
	r.Body = nil
	assert.Nil(t, parseAndSanitizeBody(r))
}

func TestParseAndSanitizeBody_ZeroContentLength_ReturnsNil(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", http.NoBody)
	r.ContentLength = 0
	assert.Nil(t, parseAndSanitizeBody(r))
}

func TestParseAndSanitizeBody_ValidJSON_ParsedAndTrimmed(t *testing.T) {
	t.Parallel()

	body := `{"name": "  Alice  ", "email": " a@b.com "}`
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", strings.NewReader(body))
	r.ContentLength = int64(len(body))

	result := parseAndSanitizeBody(r)
	require.NotNil(t, result)
	m, ok := result.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "Alice", m["name"])
	assert.Equal(t, "a@b.com", m["email"])
}

// --- writeResponse ---

func TestWriteResponse_WithData_WritesEnvelope(t *testing.T) {
	t.Parallel()

	w := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)

	writeResponse(w, r, &types.HttpResponse{
		Status: http.StatusOK,
		Data:   map[string]string{"id": "1"},
	})

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, true, body["status"])
	data, ok := body["data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "1", data["id"])
}

func TestWriteResponse_NilData_NoBody(t *testing.T) {
	t.Parallel()

	w := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodDelete, "/", http.NoBody)

	writeResponse(w, r, &types.HttpResponse{Status: http.StatusNoContent})

	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Empty(t, w.Body.String())
}

func TestWriteResponse_WithPagination_IncludesPaginationField(t *testing.T) {
	t.Parallel()

	w := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)

	writeResponse(w, r, &types.HttpResponse{
		Status: http.StatusOK,
		Data:   []string{"a", "b"},
		Pagination: &types.Pagination{
			Total: 100, Limit: 10, Offset: 0, HasMore: true,
		},
	})

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Contains(t, body, "pagination")
	pg, ok := body["pagination"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, map[string]any{
		"total": float64(100), "limit": float64(10), "offset": float64(0), "hasMore": true,
	}, pg, "the envelope carries exactly total/limit/offset/hasMore")
}

func TestWriteResponse_EnvelopeViaWithPagination_DerivesHasMore(t *testing.T) {
	t.Parallel()

	w := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)

	// The handlers' idiom: httpkit.Ok(rows).WithPagination(...) — the last page.
	resp := (&types.HttpResponse{Status: http.StatusOK, Data: []string{"y", "z"}}).
		WithPagination(types.Pagination{Total: 12, Limit: 5, Offset: 10})
	writeResponse(w, r, resp)

	var body struct {
		Status     bool     `json:"status"`
		Data       []string `json:"data"`
		Pagination struct {
			Total   int  `json:"total"`
			Limit   int  `json:"limit"`
			Offset  int  `json:"offset"`
			HasMore bool `json:"hasMore"`
		} `json:"pagination"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.True(t, body.Status)
	assert.Equal(t, []string{"y", "z"}, body.Data)
	assert.Equal(t, 12, body.Pagination.Total)
	assert.Equal(t, 5, body.Pagination.Limit)
	assert.Equal(t, 10, body.Pagination.Offset)
	assert.False(t, body.Pagination.HasMore)
}

func TestWriteResponse_CustomHeaders_Set(t *testing.T) {
	t.Parallel()

	w := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)

	writeResponse(w, r, &types.HttpResponse{
		Status:  http.StatusOK,
		Data:    "ok",
		Headers: map[string]string{"X-Custom": "value123"},
	})

	assert.Equal(t, "value123", w.Header().Get("X-Custom"))
}

func TestWriteResponse_RequestIdInContext_SetInHeader(t *testing.T) {
	t.Parallel()

	w := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	r = r.WithContext(app.WithRequestId(r.Context(), "req-xyz"))

	writeResponse(w, r, &types.HttpResponse{Status: http.StatusOK, Data: "ok"})

	assert.Equal(t, "req-xyz", w.Header().Get("X-Request-Id"))
}

func TestWriteResponse_NoRequestId_NoHeader(t *testing.T) {
	t.Parallel()

	w := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)

	writeResponse(w, r, &types.HttpResponse{Status: http.StatusOK, Data: "ok"})

	assert.Empty(t, w.Header().Get("X-Request-Id"))
}

// --- extractPagination ---

type paginationQuery struct {
	Limit  int      `json:"limit"`
	Offset int      `json:"offset"`
	Status *string  `json:"status" filter:"status"`
	Name   string   `json:"name"   filter:"name"`
	Sort   []string `json:"sort"`
}

// multiValueQuery mirrors the workflows list query: repeated query params bind
// into []string fields that carry a filter tag.
type multiValueQuery struct {
	Limit         int      `json:"limit"`
	Status        []string `json:"status"        filter:"status"`
	FinancialYear []string `json:"financialYear" filter:"financial_year"`
	EntityID      *string  `json:"entityId"      filter:"entity_id"`
	Sort          []string `json:"sort"`
}

func TestExtractPagination_SliceFilter_PassedThroughAsMultiValue(t *testing.T) {
	t.Parallel()

	entity := "e-1"
	q := &multiValueQuery{
		Limit:         25,
		Status:        []string{"active", "draft"},
		FinancialYear: []string{"2026", "none"},
		EntityID:      &entity,
		Sort:          []string{"createdAt:desc"},
	}
	result := extractPagination(q, map[string]string{"createdAt": "created_at"})

	require.Len(t, result.Filters, 3)
	assert.Equal(t, sharedtypes.Filter{Column: "status", Value: []string{"active", "draft"}}, result.Filters[0])
	assert.Equal(t, sharedtypes.Filter{Column: "financial_year", Value: []string{"2026", "none"}}, result.Filters[1])
	assert.Equal(t, sharedtypes.Filter{Column: "entity_id", Value: "e-1"}, result.Filters[2])
	// The sort slice carries no filter tag and is never mistaken for one.
	require.Len(t, result.Sort, 1)
	assert.Equal(t, "created_at", result.Sort[0].Column)
}

func TestExtractPagination_SliceFilter_SingleValueStillASlice(t *testing.T) {
	t.Parallel()

	q := &multiValueQuery{Status: []string{"active"}}
	result := extractPagination(q, nil)
	require.Len(t, result.Filters, 1)
	assert.Equal(t, []string{"active"}, result.Filters[0].Value)
}

func TestExtractPagination_SliceFilter_NilOrEmpty_Skipped(t *testing.T) {
	t.Parallel()

	assert.Empty(t, extractPagination(&multiValueQuery{Limit: 10}, nil).Filters)
	assert.Empty(t, extractPagination(&multiValueQuery{Limit: 10, Status: []string{}}, nil).Filters)
}

func TestValidateQuery_RepeatedParams_BindIntoSliceFilter(t *testing.T) {
	t.Parallel()

	// End to end through the query binder: ?status=active&status=draft lands in
	// the []string field, and a single value is a one-element slice.
	type query struct {
		Status []string `json:"status" filter:"status" validate:"omitempty,dive,oneof=draft active"`
	}
	validated, err := validateQuery(map[string][]string{"status": {"active", " draft "}}, query{})
	require.NoError(t, err)
	args := extractPagination(validated, nil)
	require.Len(t, args.Filters, 1)
	assert.Equal(t, []string{"active", "draft"}, args.Filters[0].Value)

	validated, err = validateQuery(map[string][]string{"status": {"active"}}, query{})
	require.NoError(t, err)
	assert.Equal(t, []string{"active"}, extractPagination(validated, nil).Filters[0].Value)

	// dive validates each element: one bad value rejects the request.
	_, err = validateQuery(map[string][]string{"status": {"active", "bogus"}}, query{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be one of")
}

func TestExtractPagination_LimitOffset(t *testing.T) {
	t.Parallel()

	q := &paginationQuery{Limit: 25, Offset: 50}
	result := extractPagination(q, nil)
	assert.Equal(t, 25, result.Limit)
	assert.Equal(t, 50, result.Offset)
}

func TestExtractPagination_FilterTag_NilPointer_Skipped(t *testing.T) {
	t.Parallel()

	q := &paginationQuery{Limit: 10}
	result := extractPagination(q, nil)
	assert.Empty(t, result.Filters)
}

func TestExtractPagination_FilterTag_SetPointer_Included(t *testing.T) {
	t.Parallel()

	s := "active"
	q := &paginationQuery{Limit: 10, Status: &s}
	result := extractPagination(q, nil)
	require.Len(t, result.Filters, 1)
	assert.Equal(t, "status", result.Filters[0].Column)
	assert.Equal(t, "active", result.Filters[0].Value)
}

func TestExtractPagination_FilterTag_ZeroString_Skipped(t *testing.T) {
	t.Parallel()

	q := &paginationQuery{Limit: 10, Name: ""}
	result := extractPagination(q, nil)
	assert.Empty(t, result.Filters)
}

func TestExtractPagination_FilterTag_NonZeroString_Included(t *testing.T) {
	t.Parallel()

	q := &paginationQuery{Limit: 10, Name: "alice"}
	result := extractPagination(q, nil)
	require.Len(t, result.Filters, 1)
	assert.Equal(t, "name", result.Filters[0].Column)
	assert.Equal(t, "alice", result.Filters[0].Value)
}

func TestExtractPagination_Sort_MappedToColumn(t *testing.T) {
	t.Parallel()

	q := &paginationQuery{Sort: []string{"createdAt:desc", "name:asc"}}
	colMap := map[string]string{"createdAt": "created_at", "name": "name"}
	result := extractPagination(q, colMap)

	require.Len(t, result.Sort, 2)
	assert.Equal(t, "created_at", result.Sort[0].Column)
	assert.True(t, result.Sort[0].Desc)
	assert.Equal(t, "name", result.Sort[1].Column)
	assert.False(t, result.Sort[1].Desc)
}

func TestExtractPagination_NonStruct_ReturnsEmpty(t *testing.T) {
	t.Parallel()

	result := extractPagination("not-a-struct", nil)
	assert.Equal(t, &sharedtypes.ListArgs{}, result)
}

// --- parseSortFields ---

func TestParseSortFields_Desc_Default(t *testing.T) {
	t.Parallel()

	// no direction qualifier → desc=true
	sliceVal := makeSortSlice(t, []string{"name:desc"})
	colMap := map[string]string{"name": "name"}
	result := parseSortFields(sliceVal, colMap)

	require.Len(t, result, 1)
	assert.True(t, result[0].Desc)
}

func TestParseSortFields_Asc(t *testing.T) {
	t.Parallel()

	sliceVal := makeSortSlice(t, []string{"name:asc"})
	colMap := map[string]string{"name": "name"}
	result := parseSortFields(sliceVal, colMap)

	require.Len(t, result, 1)
	assert.False(t, result[0].Desc)
}

func TestParseSortFields_UnknownField_Skipped(t *testing.T) {
	t.Parallel()

	sliceVal := makeSortSlice(t, []string{"unknown:asc"})
	colMap := map[string]string{"name": "name"}
	result := parseSortFields(sliceVal, colMap)

	assert.Empty(t, result)
}

func TestParseSortFields_Mixed_OnlyKnownMapped(t *testing.T) {
	t.Parallel()

	sliceVal := makeSortSlice(t, []string{"name:asc", "unknown:desc", "createdAt:desc"})
	colMap := map[string]string{"name": "name", "createdAt": "created_at"}
	result := parseSortFields(sliceVal, colMap)

	require.Len(t, result, 2)
	assert.Equal(t, "name", result[0].Column)
	assert.Equal(t, "created_at", result[1].Column)
}

// --- extractParams ---

func TestExtractParams_NoChiContext_ReturnsEmpty(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	result := extractParams(r)
	assert.Empty(t, result)
}

func TestExtractParams_WithChiParams_Extracted(t *testing.T) {
	t.Parallel()

	rctx := gochi.NewRouteContext()
	rctx.URLParams.Add("id", "uuid-123")
	rctx.URLParams.Add("name", "alice")

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	r = r.WithContext(context.WithValue(r.Context(), gochi.RouteCtxKey, rctx))

	result := extractParams(r)
	assert.Equal(t, "uuid-123", result["id"])
	assert.Equal(t, "alice", result["name"])
}

// makeSortSlice returns a reflect.Value (kind Slice) for use in parseSortFields tests.
func makeSortSlice(_ *testing.T, values []string) reflect.Value {
	return reflect.ValueOf(values)
}
