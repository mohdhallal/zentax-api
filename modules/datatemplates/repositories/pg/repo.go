package pg

import (
	"context"
	"database/sql"
	"errors"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/datatemplates/domain"
	"github.com/mohamadhallal/zentax-api/platform/database"
)

var _ domain.DataTemplateRepository = (*DataTemplateRepo)(nil)

// DataTemplateRepo persists data templates. Tenant isolation is enforced by
// RLS (ADR-0004); a duplicate (tenant_id, name) surfaces as a domain conflict.
type DataTemplateRepo struct {
	db database.ExecerPg
}

func NewDataTemplateRepo(db database.ExecerPg) *DataTemplateRepo {
	return &DataTemplateRepo{db: db}
}

func (r *DataTemplateRepo) Create(ctx context.Context, input domain.CreateDataTemplateInput) (*domain.DataTemplate, error) {
	t, err := r.queryRow(ctx, createSQL,
		input.Name, input.TemplateType, input.Category, input.Description, input.Fields)
	if err != nil {
		if database.IsUniqueViolation(err) {
			return nil, apperrors.NewConflict(domain.ErrDataTemplateNameExists(input.Name))
		}
		return nil, err
	}
	return t, nil
}

func (r *DataTemplateRepo) GetById(ctx context.Context, id domain.DataTemplateID) (*domain.DataTemplate, error) {
	return r.queryRow(ctx, getByIdSQL, id)
}

func (r *DataTemplateRepo) Update(ctx context.Context, id domain.DataTemplateID, input domain.UpdateDataTemplateInput) (*domain.DataTemplate, error) {
	t, err := r.queryRow(ctx, updateSQL,
		id, input.Name, input.TemplateType, input.Description, input.Fields)
	if err != nil {
		if database.IsUniqueViolation(err) {
			return nil, apperrors.NewConflict(domain.ErrDataTemplateNameExists(input.Name))
		}
		return nil, err
	}
	return t, nil
}

func (r *DataTemplateRepo) Delete(ctx context.Context, id domain.DataTemplateID) (bool, error) {
	res, err := r.db.ExecContext(ctx, deleteSQL, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (r *DataTemplateRepo) List(ctx context.Context, args domain.ListDataTemplatesArgs) ([]domain.DataTemplate, error) {
	var items []domain.DataTemplate
	if err := r.db.SelectContext(ctx, &items, listSQL, args.TemplateType, args.Category); err != nil {
		return nil, err
	}
	return items, nil
}

func (r *DataTemplateRepo) ReferenceCount(ctx context.Context, id domain.DataTemplateID) (int, error) {
	var n int
	if err := r.db.GetContext(ctx, &n, referenceCountSQL, id); err != nil {
		return 0, err
	}
	return n, nil
}

func (r *DataTemplateRepo) queryRow(ctx context.Context, query string, args ...any) (*domain.DataTemplate, error) {
	var t domain.DataTemplate
	if err := r.db.GetContext(ctx, &t, query, args...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil //nolint:nilnil // nil,nil means not found
		}
		return nil, err
	}
	return &t, nil
}
