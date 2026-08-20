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

type SQLConfig struct {
	GetById        string
	Create         string
	Update         string
	Delete         string
	Count          string
	ListBase       string
	AllowedColumns map[string]bool
	DefaultOrderBy string
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
	paramIdx := 1
	var params []any

	for _, f := range args.Filters {
		if !r.SQL.AllowedColumns[f.Column] {
			continue
		}
		if paramIdx == 1 {
			sb.WriteString(" WHERE ")
		} else {
			sb.WriteString(" AND ")
		}
		sb.WriteString(f.Column)
		sb.WriteString(" = $")
		sb.WriteString(strconv.Itoa(paramIdx))
		params = append(params, f.Value)
		paramIdx++
	}

	var orderClauses []string
	for _, sf := range args.Sort {
		if !r.SQL.AllowedColumns[sf.Column] {
			continue
		}
		dir := "ASC"
		if sf.Desc {
			dir = "DESC"
		}
		orderClauses = append(orderClauses, sf.Column+" "+dir)
	}
	if len(orderClauses) == 0 {
		orderClauses = []string{r.SQL.DefaultOrderBy + " DESC"}
	}
	sb.WriteString(" ORDER BY ")
	sb.WriteString(strings.Join(orderClauses, ", "))

	sb.WriteString(" LIMIT $")
	sb.WriteString(strconv.Itoa(paramIdx))
	sb.WriteString(" OFFSET $")
	sb.WriteString(strconv.Itoa(paramIdx + 1))
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
	paramIdx := 1
	var params []any

	for _, f := range filters {
		if !r.SQL.AllowedColumns[f.Column] {
			continue
		}
		if paramIdx == 1 {
			sb.WriteString(" WHERE ")
		} else {
			sb.WriteString(" AND ")
		}
		sb.WriteString(f.Column)
		sb.WriteString(" = $")
		sb.WriteString(strconv.Itoa(paramIdx))
		params = append(params, f.Value)
		paramIdx++
	}

	var total int
	err := r.DB.GetContext(ctx, &total, sb.String(), params...)
	if err != nil {
		return 0, err
	}
	return total, nil
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
