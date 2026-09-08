package repositories

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"

	"github.com/mohamadhallal/zentax-api/platform/database"
	sharedtypes "github.com/mohamadhallal/zentax-api/shared/types"
)

// NullFilterValue is the sentinel a nullable filter column accepts to select
// rows whose column IS NULL (`financialYear=none` keeps project workflows,
// which carry no financial year, inside a year scope).
const NullFilterValue = "none"

// defaultTieBreaker is the unique column List orders by last when a module
// declares none: every generic table has a uuid primary key named id.
const defaultTieBreaker = "id"

type SQLConfig struct {
	GetById        string
	Create         string
	Update         string
	Delete         string
	Count          string
	ListBase       string
	AllowedColumns map[string]bool
	DefaultOrderBy string
	// DefaultOrderDesc sorts the default column descending (e.g. created_at →
	// newest first). Leave false for columns whose natural order is ascending:
	// due_date (earliest deadline first) and order_index (step order).
	DefaultOrderDesc bool
	// TieBreaker is the unique column List ALWAYS appends as the last ORDER BY
	// clause, in the direction of the primary (first) sort clause — `due_date
	// ASC, id ASC`, `created_at DESC, id DESC` — so offset pages are
	// deterministic where ties are densest (one instance per template per
	// period shares a due date; every row of one start shares created_at) and
	// one btree can serve the whole order. Empty means "id". A caller's sort
	// that already names the column is left alone.
	TieBreaker string
	// NullableFilters names the filter columns for which NullFilterValue
	// ("none") selects rows whose column IS NULL: `col IS NULL` when it is the
	// only value, `(col = ANY($n) OR col IS NULL)` when it accompanies others.
	// Undeclared columns treat "none" as an ordinary value.
	NullableFilters map[string]bool
	// SearchColumns are the columns the free-text search filter
	// (sharedtypes.SearchFilter) matches as a literal, case-insensitive
	// substring: `(c1 ILIKE $n ESCAPE '\' OR c2 ILIKE $n ESCAPE '\')`, ONE bind
	// (`%` + EscapeLike(term) + `%`) shared by every column. Names may be
	// alias-qualified (w.name) when ListBase joins. List and GetTotal both
	// append the predicate, so a searched page and its total agree. Empty means
	// the repository has no search: the filter is ignored.
	SearchColumns []string
}

type BaseRepo[T any, ID comparable] struct {
	DB  database.ExecerPg
	SQL SQLConfig
}

func NewBaseRepo[T any, ID comparable](db database.ExecerPg, cfg SQLConfig) BaseRepo[T, ID] {
	return BaseRepo[T, ID]{DB: db, SQL: cfg}
}

func (r *BaseRepo[T, ID]) GetById(ctx context.Context, id ID) (*T, error) {
	return r.QueryRow(ctx, r.SQL.GetById, id)
}

func (r *BaseRepo[T, ID]) Delete(ctx context.Context, id ID) (bool, error) {
	return r.ExecAffected(ctx, r.SQL.Delete, id)
}

func (r *BaseRepo[T, ID]) List(ctx context.Context, args sharedtypes.ListArgs) ([]T, error) {
	var sb strings.Builder
	sb.WriteString(r.SQL.ListBase)
	params := r.writeWhere(&sb, args.Filters)

	sb.WriteString(" ORDER BY ")
	sb.WriteString(strings.Join(r.orderClauses(args.Sort), ", "))

	sb.WriteString(" LIMIT $")
	sb.WriteString(strconv.Itoa(len(params) + 1))
	sb.WriteString(" OFFSET $")
	sb.WriteString(strconv.Itoa(len(params) + 2))
	params = append(params, args.Limit, args.Offset)

	var results []T
	err := r.DB.SelectContext(ctx, &results, sb.String(), params...)
	if err != nil {
		return nil, err
	}
	return results, nil
}

func (r *BaseRepo[T, ID]) GetTotal(ctx context.Context, filters []sharedtypes.Filter) (int, error) {
	var sb strings.Builder
	sb.WriteString(r.SQL.Count)
	params := r.writeWhere(&sb, filters)

	var total int
	err := r.DB.GetContext(ctx, &total, sb.String(), params...)
	if err != nil {
		return 0, err
	}
	return total, nil
}

// writeWhere appends the WHERE clause the filters describe (nothing when no
// allowed filter is set) and returns the positional parameters it consumed, in
// order. List and GetTotal share it so a page and its total always agree.
func (r *BaseRepo[T, ID]) writeWhere(sb *strings.Builder, filters []sharedtypes.Filter) []any {
	var params []any
	clauses := 0
	for _, f := range filters {
		var (
			pred   string
			values []any
		)
		switch {
		case f.Column == sharedtypes.SearchFilter:
			pred, values = r.searchPredicate(f, len(params)+1)
			if pred == "" {
				continue
			}
		case !r.SQL.AllowedColumns[f.Column]:
			continue
		default:
			pred, values = r.predicate(f, len(params)+1)
		}
		if clauses == 0 {
			sb.WriteString(" WHERE ")
		} else {
			sb.WriteString(" AND ")
		}
		sb.WriteString(pred)
		params = append(params, values...)
		clauses++
	}
	return params
}

// predicate renders one filter as SQL starting at placeholder $paramIdx and
// returns the parameters it binds (none for a pure IS NULL). A []string value
// is a multi-value filter (`col = ANY($n)`); on a nullable column the "none"
// sentinel — alone or among the values, single or multi — selects NULL rows.
func (r *BaseRepo[T, ID]) predicate(f sharedtypes.Filter, paramIdx int) (string, []any) {
	placeholder := "$" + strconv.Itoa(paramIdx)
	nullable := r.SQL.NullableFilters[f.Column]

	switch v := f.Value.(type) {
	case []string:
		values, wantNull := splitNullSentinel(v, nullable)
		switch {
		case wantNull && len(values) == 0:
			return f.Column + " IS NULL", nil
		case wantNull:
			return "(" + f.Column + " = ANY(" + placeholder + ") OR " + f.Column + " IS NULL)", []any{values}
		default:
			return f.Column + " = ANY(" + placeholder + ")", []any{values}
		}
	case string:
		if nullable && v == NullFilterValue {
			return f.Column + " IS NULL", nil
		}
	}
	return f.Column + " = " + placeholder, []any{f.Value}
}

// searchPredicate renders the free-text search filter over SearchColumns as
// one OR-group bound to a single placeholder — empty (no predicate, no
// parameter) when the term is blank or the repository declares no search
// columns. The metacharacters are escaped so the term is matched literally.
func (r *BaseRepo[T, ID]) searchPredicate(f sharedtypes.Filter, paramIdx int) (string, []any) {
	term, _ := f.Value.(string)
	term = strings.TrimSpace(term)
	if term == "" || len(r.SQL.SearchColumns) == 0 {
		return "", nil
	}
	placeholder := "$" + strconv.Itoa(paramIdx)
	parts := make([]string, 0, len(r.SQL.SearchColumns))
	for _, col := range r.SQL.SearchColumns {
		parts = append(parts, col+" ILIKE "+placeholder+` ESCAPE '\'`)
	}
	return "(" + strings.Join(parts, " OR ") + ")", []any{"%" + EscapeLike(term) + "%"}
}

// splitNullSentinel removes the "none" sentinel from a nullable column's values
// and reports whether it was present. On a non-nullable column the slice is
// returned unchanged (a copy either way, so the caller's args are never
// mutated).
func splitNullSentinel(values []string, nullable bool) ([]string, bool) {
	out := make([]string, 0, len(values))
	wantNull := false
	for _, v := range values {
		if nullable && v == NullFilterValue {
			wantNull = true
			continue
		}
		out = append(out, v)
	}
	return out, wantNull
}

// orderClauses renders the ORDER BY list: the caller's allowed sort fields (or
// the module default), then the tie-breaker in the primary clause's direction
// unless a clause already names it.
func (r *BaseRepo[T, ID]) orderClauses(sort []sharedtypes.SortField) []string {
	tie := r.SQL.TieBreaker
	if tie == "" {
		tie = defaultTieBreaker
	}

	var clauses []string
	primaryDir := ""
	tieNamed := false
	for _, sf := range sort {
		if !r.SQL.AllowedColumns[sf.Column] {
			continue
		}
		dir := direction(sf.Desc)
		if primaryDir == "" {
			primaryDir = dir
		}
		if sf.Column == tie {
			tieNamed = true
		}
		clauses = append(clauses, sf.Column+" "+dir)
	}
	if len(clauses) == 0 {
		primaryDir = direction(r.SQL.DefaultOrderDesc)
		tieNamed = r.SQL.DefaultOrderBy == tie
		clauses = []string{r.SQL.DefaultOrderBy + " " + primaryDir}
	}
	if !tieNamed {
		clauses = append(clauses, tie+" "+primaryDir)
	}
	return clauses
}

func direction(desc bool) string {
	if desc {
		return "DESC"
	}
	return "ASC"
}

func (r *BaseRepo[T, ID]) QueryRow(ctx context.Context, query string, args ...any) (*T, error) {
	var result T
	err := r.DB.GetContext(ctx, &result, query, args...)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil //nolint:nilnil // nil,nil means not found
		}
		return nil, err
	}
	return &result, nil
}

func (r *BaseRepo[T, ID]) ExecAffected(ctx context.Context, query string, args ...any) (bool, error) {
	result, err := r.DB.ExecContext(ctx, query, args...)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows > 0, nil
}
