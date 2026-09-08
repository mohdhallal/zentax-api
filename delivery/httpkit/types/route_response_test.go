package types

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWithPagination_HasMore_DerivedFromOffsetPageAndTotal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		data   any
		total  int
		offset int
		want   bool
	}{
		{"first page of many", []string{"a", "b", "c"}, 10, 0, true},
		{"middle page", []string{"a", "b", "c"}, 10, 3, true},
		{"last full page", []string{"a", "b", "c"}, 9, 6, false},
		{"last short page", []string{"a"}, 7, 6, false},
		{"exactly one page", []string{"a", "b"}, 2, 0, false},
		{"empty result", []string{}, 0, 0, false},
		{"offset past the end", []string{}, 5, 10, false},
		{"nil data still counts rows before it", nil, 5, 2, true},
		{"array payload", [2]int{1, 2}, 3, 0, true},
		{"non-slice payload counts as no rows", map[string]any{"x": 1}, 1, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := &HttpResponse{Status: 200, Data: tc.data}
			r.WithPagination(Pagination{Total: tc.total, Limit: 3, Offset: tc.offset})
			require.NotNil(t, r.Pagination)
			assert.Equal(t, tc.want, r.Pagination.HasMore)
			assert.Equal(t, tc.total, r.Pagination.Total)
			assert.Equal(t, tc.offset, r.Pagination.Offset)
			assert.Equal(t, 3, r.Pagination.Limit)
		})
	}
}

func TestWithPagination_CallerHasMoreIsOverwritten(t *testing.T) {
	t.Parallel()

	r := &HttpResponse{Status: 200, Data: []int{1, 2}}
	r.WithPagination(Pagination{Total: 2, Limit: 2, Offset: 0, HasMore: true})
	assert.False(t, r.Pagination.HasMore)
}

func TestPagination_JSONShape(t *testing.T) {
	t.Parallel()

	out, err := json.Marshal(Pagination{Total: 243, Limit: 25, Offset: 25, HasMore: true})
	require.NoError(t, err)
	assert.JSONEq(t, `{"total":243,"limit":25,"offset":25,"hasMore":true}`, string(out))
}
