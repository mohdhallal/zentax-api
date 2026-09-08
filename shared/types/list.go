package types

// Filter is one equality predicate on an allowed column. A []string Value is a
// multi-value filter (any of the values match); a repository may additionally
// treat the "none" sentinel as IS NULL on columns it declares nullable. The
// free-text search travels as a Filter too — see SearchFilter.
type Filter struct {
	Column string
	Value  any
}

// SearchFilter is the Column of the free-text search filter. Its Value is the
// trimmed term (a string) the repository matches case-insensitively, as a
// LITERAL substring (ILIKE with % _ \ escaped), against the columns it
// declares in its SQL config; a repository that declares none ignores it. The
// term rides the ONE filter list the use cases hand to both List and GetTotal,
// so a page and its total always agree — a separate ListArgs field could never
// reach GetTotal(ctx, filters). "search" is not a SQL identifier: repositories
// dispatch on the column name before the allowed-column check.
const SearchFilter = "search"

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
