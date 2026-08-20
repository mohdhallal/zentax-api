package types

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
