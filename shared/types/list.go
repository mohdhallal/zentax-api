package types

// Filter is one equality predicate on an allowed column. A []string Value is a
// multi-value filter (any of the values match); a repository may additionally
// treat the "none" sentinel as IS NULL on columns it declares nullable.
type Filter struct {
	Column string
	Value  any
}

type SortField struct {
	Column string
	Desc   bool
}

type ListArgs struct {
	Limit   int
	Offset  int
	Sort    []SortField
	Filters []Filter
}

type ListResult[T any] struct {
	Items []T
	Total int
}
