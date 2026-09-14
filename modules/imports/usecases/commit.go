package usecases

import (
	"context"
	"fmt"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/imports/domain"
	"github.com/mohamadhallal/zentax-api/platform/audit"
)

// Commit applies a validated plan — all of it, or none of it.
//
// It runs inside the request's single transaction (the route declares Tenant,
// which implies Tx), so every return of an error below rolls back the whole
// thing: the records, the audit entries that describe them, and the batch's own
// change of state. There is no partial import, and there is no import whose
// evidence is missing.
//
// The order of business is: lock the batch, re-derive the plan from the tenant
// as it is NOW, refuse unless it says exactly what the stored plan says, and
// only then write. The check is the feature — without it "the dry run said
// twelve creates" would be a statement about a moment that has passed.
func (uc *UseCases) Commit(ctx context.Context, kind domain.Kind, batchID string) (*domain.Batch, error) {
	// FOR UPDATE, so two commits of one batch cannot both see it committable.
	// The second waits here, then finds it 'committed' and is refused below.
	batch, err := uc.repo.GetBatchForUpdate(ctx, batchID)
	if err != nil {
		return nil, err
	}
	// A batch of another kind is not this route's to commit, and saying "not
	// found" rather than "wrong kind" keeps /imports/entities from confirming
	// the existence of an obligations batch.
	if batch == nil || batch.Kind != kind {
		return nil, apperrors.NewNotFound(fmt.Sprintf("import %s not found", batchID))
	}
	switch batch.Status {
	case domain.BatchRejected:
		return nil, apperrors.NewConflict(
			fmt.Sprintf("this file has %d row(s) that cannot be imported, so none of it can be; correct the file and upload it again", batch.InvalidCount))
	case domain.BatchCommitted:
		return nil, apperrors.NewConflict("this file has already been imported")
	}

	stored, total, err := uc.repo.ListRows(ctx, batchID, batch.RowCount, 0)
	if err != nil {
		return nil, err
	}
	if total != batch.RowCount || len(stored) != batch.RowCount {
		return nil, apperrors.NewConflict("this import's plan is incomplete and cannot be applied")
	}

	drafts, err := draftRows(kind, stored)
	if err != nil {
		return nil, err
	}

	rederived, state, err := uc.planFile(ctx, kind, drafts)
	if err != nil {
		return nil, err
	}
	if err := planStillHolds(stored, rederived.Rows); err != nil {
		return nil, err
	}

	if kind == domain.KindEntityObligations {
		err = uc.applyObligations(ctx, stored, drafts, state)
	} else {
		err = uc.applyEntities(ctx, stored, drafts, state)
	}
	if err != nil {
		return nil, err
	}

	committed, err := uc.repo.MarkCommitted(ctx, batchID)
	if err != nil {
		return nil, err
	}
	if committed == nil {
		return nil, apperrors.NewConflict("this file has already been imported")
	}

	// One envelope for the OPERATION, on top of the one per record written
	// above. It is what lets an auditor group a hundred entity.updated entries
	// under the file that caused them, and it names the file by checksum — a
	// server-computed hex digest — rather than by the name the customer gave it,
	// which is free text (ADR-0008's PII rule).
	//
	// It is recorded ONE-SIDED (before == nil), like a create, rather than as
	// the difference between the batch before and after. A difference would
	// carry one field — status, validated → committed — which is the one
	// transition the lifecycle permits and therefore the one thing an auditor
	// could already infer; what they cannot infer, and what a commit envelope
	// has to say, is WHICH FILE and HOW MANY records. That is the PB-D1 lesson
	// applied to this module: record what happened, not that something did.
	if err := uc.audit.Record(ctx, "import.committed", "import_batch", committed.ID,
		audit.Changes(nil, auditBatchValues(committed))); err != nil {
		return nil, err
	}
	return committed, nil
}

// draftRows turns the stored plan back into the rows the planner reads, so that
// the commit re-derives the plan through the SAME function that produced it.
// The issues are deliberately not carried: a validated batch has none, and
// re-deriving from the payload alone is what makes this a genuine second
// opinion rather than a replay of the first.
func draftRows(kind domain.Kind, stored []domain.RowPlan) ([]domain.DraftRow, error) {
	out := make([]domain.DraftRow, 0, len(stored))
	for i := range stored {
		row := &stored[i]
		draft := domain.DraftRow{Number: row.RowNumber}
		if kind == domain.KindEntityObligations {
			obligation, err := row.ObligationDraft()
			if err != nil {
				return nil, apperrors.NewConflict(fmt.Sprintf("row %d of this import's plan could not be read back", row.RowNumber))
			}
			draft.Obligation = obligation
		} else {
			entity, err := row.EntityDraft()
			if err != nil {
				return nil, apperrors.NewConflict(fmt.Sprintf("row %d of this import's plan could not be read back", row.RowNumber))
			}
			draft.Entity = entity
		}
		out = append(out, draft)
	}
	return out, nil
}

// planStillHolds is the honesty check.
//
// It compares the stored plan against the plan the same code derives from the
// tenant right now, row by row, on the three facts a write depends on: WHAT
// will happen, to WHICH record, and from WHICH version of it. Any difference
// refuses the whole batch with a message naming the row, because every
// difference means the same thing — the world moved under a promise somebody
// was shown, and applying the promise anyway would write something nobody
// agreed to.
func planStillHolds(stored, rederived []domain.RowPlan) error {
	if len(stored) != len(rederived) {
		return apperrors.NewConflict("this import's plan no longer matches the data it was checked against; upload the file again")
	}
	for i := range stored {
		was, now := &stored[i], &rederived[i]
		switch {
		case was.RowNumber != now.RowNumber:
			return staleConflict(was.RowNumber, "the plan's rows no longer line up")
		case was.Action != now.Action:
			return staleConflict(was.RowNumber,
				fmt.Sprintf("it would now %s rather than %s", actionPhrase(now.Action), actionPhrase(was.Action)))
		case !samePtr(was.TargetID, now.TargetID):
			return staleConflict(was.RowNumber, "it now matches a different record")
		case !sameVersion(was.TargetUpdatedAt, now.TargetUpdatedAt):
			return staleConflict(was.RowNumber, "the record it would change has been edited since this file was checked")
		}
	}
	return nil
}

func staleConflict(row int, why string) error {
	return apperrors.NewConflict(fmt.Sprintf(
		"row %d can no longer be imported as planned: %s. Nothing was imported; upload the file again to see what it would do now", row, why))
}

// actionPhrase is a row action written as something a row DOES, for the
// sentence above. The action names are a state vocabulary — create, update,
// unchanged, invalid — and dropping them straight into a verb slot produced
// "it would now unchanged rather than create", which is what a customer read at
// the one moment they most needed the product to sound like it knew what had
// happened.
func actionPhrase(action domain.RowAction) string {
	switch action {
	case domain.RowCreate:
		return "add a new record"
	case domain.RowUpdate:
		return "change the record it matches"
	case domain.RowUnchanged:
		return "leave the record it matches unchanged"
	case domain.RowInvalid:
		return "be refused"
	default:
		return string(action)
	}
}
