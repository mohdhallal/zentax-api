package usecases

import (
	"context"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/datatemplates/domain"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

// Create adds a CUSTOM template. The category is forced here — the API body
// does not accept one, and the only way to obtain predefined rows is
// SeedPredefined.
func (uc *UseCases) Create(ctx context.Context, input domain.CreateDataTemplateInput) (*domain.DataTemplate, error) {
	if err := uc.authorizer.EnsureEntity(ctx, "", authz.DataTemplateWrite); err != nil {
		return nil, err
	}
	if err := domain.ValidateFields(input.Fields); err != nil {
		return nil, apperrors.NewValidation(err.Error())
	}
	input.Category = domain.CategoryCustom

	t, err := uc.repo.Create(ctx, input)
	if err != nil {
		return nil, err
	}
	if err := uc.audit.Record(ctx, "data_template.created", "data_template", t.ID,
		map[string]any{"templateType": t.TemplateType, "fields": len(t.Fields)}); err != nil {
		return nil, err
	}
	return t, nil
}

func (uc *UseCases) GetById(ctx context.Context, id domain.DataTemplateID) (*domain.DataTemplate, error) {
	t, err := uc.repo.GetById(ctx, id)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, apperrors.NewNotFound(domain.ErrDataTemplateNotFound(id))
	}
	return t, nil
}

// Update replaces name / templateType / description / fields of a CUSTOM
// template; predefined templates are immutable (403). Once a template is in
// use (referenced by a workflow task or a task instance) its existing fields
// are frozen in id and type — they may be renamed, described or made
// mandatory, and new fields added, but removing or retyping one would orphan
// or invalidate the data already recorded against it (409).
func (uc *UseCases) Update(ctx context.Context, id domain.DataTemplateID, input domain.UpdateDataTemplateInput) (*domain.DataTemplate, error) {
	if err := uc.authorizer.EnsureEntity(ctx, "", authz.DataTemplateWrite); err != nil {
		return nil, err
	}
	if err := domain.ValidateFields(input.Fields); err != nil {
		return nil, apperrors.NewValidation(err.Error())
	}

	current, err := uc.repo.GetById(ctx, id)
	if err != nil {
		return nil, err
	}
	if current == nil {
		return nil, apperrors.NewNotFound(domain.ErrDataTemplateNotFound(id))
	}
	if current.IsPredefined() {
		return nil, apperrors.NewForbidden(domain.MsgPredefinedImmutable)
	}
	if !domain.FieldsCompatible(current.Fields, input.Fields) {
		refs, err := uc.repo.ReferenceCount(ctx, id)
		if err != nil {
			return nil, err
		}
		if refs > 0 {
			return nil, apperrors.NewConflict(domain.MsgTemplateInUseFieldsFrozen)
		}
	}

	t, err := uc.repo.Update(ctx, id, input)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, apperrors.NewNotFound(domain.ErrDataTemplateNotFound(id))
	}
	if err := uc.audit.Record(ctx, "data_template.updated", "data_template", id,
		map[string]any{"templateType": t.TemplateType, "fields": len(t.Fields)}); err != nil {
		return nil, err
	}
	return t, nil
}

// Delete removes a CUSTOM template that nothing references: predefined → 403,
// referenced by a workflow task or task instance of the tenant → 409.
func (uc *UseCases) Delete(ctx context.Context, id domain.DataTemplateID) error {
	if err := uc.authorizer.EnsureEntity(ctx, "", authz.DataTemplateWrite); err != nil {
		return err
	}
	current, err := uc.repo.GetById(ctx, id)
	if err != nil {
		return err
	}
	if current == nil {
		return apperrors.NewNotFound(domain.ErrDataTemplateNotFound(id))
	}
	if current.IsPredefined() {
		return apperrors.NewForbidden(domain.MsgPredefinedImmutable)
	}
	refs, err := uc.repo.ReferenceCount(ctx, id)
	if err != nil {
		return err
	}
	if refs > 0 {
		return apperrors.NewConflict(domain.MsgTemplateInUse)
	}

	deleted, err := uc.repo.Delete(ctx, id)
	if err != nil {
		return err
	}
	if !deleted {
		return apperrors.NewNotFound(domain.ErrDataTemplateNotFound(id))
	}
	return uc.audit.Record(ctx, "data_template.deleted", "data_template", id, nil)
}

func (uc *UseCases) List(ctx context.Context, args domain.ListDataTemplatesArgs) ([]domain.DataTemplate, error) {
	items, err := uc.repo.List(ctx, args)
	if err != nil {
		return nil, err
	}
	if items == nil {
		items = []domain.DataTemplate{}
	}
	return items, nil
}
