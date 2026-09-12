package usecases

import (
	"context"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	datatemplatesdomain "github.com/mohamadhallal/zentax-api/modules/datatemplates/domain"
	"github.com/mohamadhallal/zentax-api/modules/taskinstances/domain"
	"github.com/mohamadhallal/zentax-api/platform/audit"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

func (uc *UseCases) Update(ctx context.Context, id domain.TaskInstanceID, input domain.UpdateTaskInstanceInput) (*domain.TaskInstance, error) {
	if err := uc.authorizer.EnsureTaskInstance(ctx, id, authz.TaskWrite); err != nil {
		return nil, err
	}

	// ADR-0018: a submitted (pending) or approved instance is locked against
	// in-place edits — a change must go through reject/amend, not overwrite.
	current, err := uc.repo.GetById(ctx, id)
	if err != nil {
		return nil, err
	}
	if current == nil {
		return nil, apperrors.NewNotFound(domain.ErrTaskInstanceNotFound(id))
	}
	if current.IsApproved() {
		return nil, apperrors.NewConflict(domain.MsgApprovedImmutable)
	}
	if current.IsPendingApproval() {
		return nil, apperrors.NewConflict(domain.MsgAlreadyPending)
	}

	// assigneeId must be an ACTIVE HUMAN member of the caller's tenant (the
	// column carries no FK — users is not RLS-scoped — so it is checked here).
	// Only a CHANGE of assignee is checked: clients send the current assignee
	// back on every save, and a task whose assignee was since disabled must
	// stay editable (and reassignable).
	if input.AssigneeID != nil && uc.assignees != nil &&
		(current.AssigneeID == nil || *current.AssigneeID != *input.AssigneeID) {
		ok, err := uc.assignees.IsAssignable(ctx, *input.AssigneeID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, apperrors.NewValidation(domain.MsgAssigneeNotMember)
		}
	}

	if input.TaxDataStatus == "" {
		input.TaxDataStatus = domain.TaxDataStatusDraft
	}

	// ADR-0001 tax-data authority: with a template attached (the incoming
	// dataTemplateId, else the one inherited from the workflow task), the tax
	// data must conform to it; a final record carries every mandatory field.
	tpl, err := uc.resolveTemplate(ctx, input.DataTemplateID, current.DataTemplateID)
	if err != nil {
		return nil, err
	}
	// Only a change is validated: clients replay the stored record on every
	// save, and a record that predates a template edit must not make the
	// instance un-editable (status, assignee, notes…). Attaching a template or
	// asking for a final record always validates.
	if tpl != nil {
		requireMandatory := input.TaxDataStatus == domain.TaxDataStatusFinal
		if requireMandatory || input.DataTemplateID != nil || !domain.TaxDataEqual(input.TaxData, current.TaxData) {
			cleaned, err := domain.ValidateTaxData(tpl, input.TaxData, current.TaxData, requireMandatory)
			if err != nil {
				return nil, apperrors.NewValidation(err.Error())
			}
			if input.TaxData != nil {
				input.TaxData = cleaned
			}
		}
	}

	ti, err := uc.repo.Update(ctx, id, input)
	if err != nil {
		return nil, err
	}
	if ti == nil {
		return nil, apperrors.NewNotFound(domain.ErrTaskInstanceNotFound(id))
	}
	// The evidence costs no extra read: `current` was loaded above for the
	// ADR-0018 freeze check, on this request's transaction, so the whole
	// before/after envelope comes from rows already in hand.
	details := withTaxDataChange(
		audit.Changes(auditValues(current), auditValues(ti)),
		current.TaxData, ti.TaxData)
	if err := uc.audit.Record(ctx, "task_instance.updated", "task_instance", id, details); err != nil {
		return nil, err
	}
	return ti, nil
}

// resolveTemplate loads the template that governs the instance after the
// update: the incoming id wins over the current one. An incoming id that does
// not resolve in this tenant is a validation error (the composite FK would
// refuse it anyway). Nil resolver (unit tests) or no template → nil.
func (uc *UseCases) resolveTemplate(ctx context.Context, incoming, current *string) (*datatemplatesdomain.DataTemplate, error) {
	if uc.templates == nil {
		return nil, nil //nolint:nilnil // no resolver wired = no validation
	}
	templateID := current
	if incoming != nil {
		templateID = incoming
	}
	if templateID == nil || *templateID == "" {
		return nil, nil //nolint:nilnil // no template attached
	}
	tpl, err := uc.templates.ResolveTemplate(ctx, *templateID)
	if err != nil {
		return nil, err
	}
	if tpl == nil {
		if incoming != nil {
			return nil, apperrors.NewValidation(domain.MsgDataTemplateNotFound)
		}
		// The inherited template vanished (deleted → SET NULL races): nothing to
		// validate against.
		return nil, nil //nolint:nilnil // template gone
	}
	return tpl, nil
}
