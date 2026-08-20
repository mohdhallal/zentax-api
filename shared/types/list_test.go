package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestListResult_Generic_String(t *testing.T) {
	t.Parallel()

	result := ListResult[string]{
		Items: []string{"a", "b", "c"},
		Total: 3,
	}

	assert.Equal(t, 3, result.Total)
	assert.Equal(t, []string{"a", "b", "c"}, result.Items)
}

func TestListResult_Generic_Struct(t *testing.T) {
	t.Parallel()

	type Item struct{ ID int }

	result := ListResult[Item]{
		Items: []Item{{1}, {2}},
		Total: 100,
	}

	assert.Equal(t, 100, result.Total)
	assert.Len(t, result.Items, 2)
	assert.Equal(t, 1, result.Items[0].ID)
}

func TestListResult_EmptyItems(t *testing.T) {
	t.Parallel()

	result := ListResult[int]{Items: []int{}, Total: 0}

	assert.Empty(t, result.Items)
	assert.Equal(t, 0, result.Total)
}

func TestListArgs_Filters(t *testing.T) {
	t.Parallel()

	args := ListArgs{
		Limit:  20,
		Offset: 40,
		Filters: []Filter{
			{Column: "status", Value: "active"},
			{Column: "account_id", Value: 42},
		},
		Sort: []SortField{
			{Column: "created_at", Desc: true},
		},
	}

	assert.Equal(t, 20, args.Limit)
	assert.Equal(t, 40, args.Offset)
	assert.Len(t, args.Filters, 2)
	assert.Equal(t, "status", args.Filters[0].Column)
	assert.Equal(t, "active", args.Filters[0].Value)
	assert.Equal(t, 42, args.Filters[1].Value)
	assert.True(t, args.Sort[0].Desc)
	assert.Equal(t, "created_at", args.Sort[0].Column)
}

func TestSortField_Asc(t *testing.T) {
	t.Parallel()

	sf := SortField{Column: "name", Desc: false}
	assert.False(t, sf.Desc)
	assert.Equal(t, "name", sf.Column)
}
