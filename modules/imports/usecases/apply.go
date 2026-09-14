package usecases

import (
	"context"
	"fmt"
	"time"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/imports/domain"
	"github.com/mohamadhallal/zentax-api/platform/audit"
)

// applyEntities writes the entity rows of a plan.
//
// PARENTS BEFORE CHILDREN. A file may create a group and its subsidiaries in
// one go, and a subsidiary's parent id does not exist until the parent row has
// been written, so the rows are applied in dependency order rather than file
// order. The order exists because the planner refused any file whose parent
// chain loops.
func (uc *UseCases) applyEntities(
	ctx context.Context, plan []domain.RowPlan, drafts []domain.DraftRow, state *resolved,
) error {
	byRow := make(map[int]*domain.EntityDraft, len(drafts))
	for i := range drafts {
		byRow[drafts[i].Number] = drafts[i].Entity
	}

	// created maps a natural key to the id the commit has just given it, so a
	// child written later in this same transaction can find its parent.
	created := make(map[string]string, len(plan))

	for _, index := range entityApplyOrder(plan, byRow) {
		row := &plan[index]
		if row.Action == domain.RowUnchanged {
			continue // nothing to write, and therefore nothing to record
		}
		draft := byRow[row.RowNumber]
		if draft == nil {
			return apperrors.NewConflict(fmt.Sprintf("row %d of this import's plan could not be read back", row.RowNumber))
		}

		parentID, err := uc.resolveParent(draft, state, created, row.RowNumber)
		if err != nil {
			return err
		}

		switch row.Action {
		case domain.RowCreate:
			entity, err := uc.repo.CreateEntity(ctx, draft, parentID)
			if err != nil {
				return err
			}
			created[draft.NaturalKey()] = entity.ID
			// The SAME action name and the SAME whitelist as
			// modules/entities/usecases.Create. An auditor asking "what has ever
			// happened to this entity" must not have to know that a second
			// vocabulary exists for records that arrived by file.
			if err := uc.audit.Record(ctx, "entity.created", "entity", entity.ID,
				audit.Changes(nil, auditEntityValues(entity))); err != nil {
				return err
			}
		case domain.RowUpdate:
			before, ok := state.entities[draft.NaturalKey()]
			if !ok || row.TargetUpdatedAt == nil {
				return staleConflict(row.RowNumber, "the record it would change is no longer there")
			}
			// An update MERGES: the file's own values for the columns it has,
			// the record's current values for the ones it does not (changes.go).
			// The planner compared exactly these fields, so what is written here
			// is what the dry run listed and nothing besides.
			entity, err := uc.repo.UpdateEntity(ctx, before.ID, *row.TargetUpdatedAt,
				mergeEntity(draft, before), mergeParent(draft, parentID, before))
			if err != nil {
				return err
			}
			// The version-pinned UPDATE matched nothing, which can only mean the
			// record moved between the check above and this statement. Refusing
			// rolls back everything already written by this commit.
			if entity == nil {
				return staleConflict(row.RowNumber, "the record it would change has been edited since this file was checked")
			}
			if err := uc.audit.Record(ctx, "entity.updated", "entity", entity.ID,
				audit.Changes(auditEntityValues(&before), auditEntityValues(entity))); err != nil {
				return err
			}
		}
	}
	return nil
}

// resolveParent finds the parent id for a row: an entity that already existed,
// or one this same commit has just created.
func (uc *UseCases) resolveParent(
	draft *domain.EntityDraft, state *resolved, created map[string]string, rowNumber int,
) (*string, error) {
	key := draft.ParentKey()
	if key == "" {
		return nil, nil
	}
	if parent, ok := state.entities[key]; ok && !parent.Ambiguous {
		id := parent.ID
		return &id, nil
	}
	if id, ok := created[key]; ok {
		return &id, nil
	}
	// The planner proved this parent resolvable and the ordering proved it is
	// written by now, so reaching here means the two disagree — a bug, not a
	// data condition. It refuses rather than writing a top-level entity that
	// the customer asked to be a subsidiary.
	return nil, apperrors.NewConflict(fmt.Sprintf(
		"row %d names a parent that could not be resolved while importing; nothing was imported", rowNumber))
}

// entityApplyOrder returns plan indexes with every in-file parent before its
// children. It is a depth-first post-order over the file's own parent graph;
// rows whose parent already exists (or that have none) are roots.
func entityApplyOrder(plan []domain.RowPlan, byRow map[int]*domain.EntityDraft) []int {
	indexOfKey := make(map[string]int, len(plan))
	for i := range plan {
		if draft := byRow[plan[i].RowNumber]; draft != nil {
			if _, dup := indexOfKey[draft.NaturalKey()]; !dup {
				indexOfKey[draft.NaturalKey()] = i
			}
		}
	}

	order := make([]int, 0, len(plan))
	const (
		pending = 0
		active  = 1
		done    = 2
	)
	state := make([]int, len(plan))

	var visit func(i int)
	visit = func(i int) {
		if state[i] != pending {
			return // already emitted, or already on the stack (a loop the planner refused)
		}
		state[i] = active
		if draft := byRow[plan[i].RowNumber]; draft != nil {
			if parent, ok := indexOfKey[draft.ParentKey()]; ok && parent != i {
				visit(parent)
			}
		}
		state[i] = done
		order = append(order, i)
	}
	for i := range plan {
		visit(i)
	}
	return order
}

// applyObligations writes the entity-obligation rows of a plan. No ordering is
// needed: an obligation references an entity and an obligation type, both of
// which must already exist — this import never creates either.
func (uc *UseCases) applyObligations(
	ctx context.Context, plan []domain.RowPlan, drafts []domain.DraftRow, state *resolved,
) error {
	byRow := make(map[int]*domain.ObligationDraft, len(drafts))
	for i := range drafts {
		byRow[drafts[i].Number] = drafts[i].Obligation
	}

	for i := range plan {
		row := &plan[i]
		if row.Action == domain.RowUnchanged {
			continue
		}
		draft := byRow[row.RowNumber]
		if draft == nil {
			return apperrors.NewConflict(fmt.Sprintf("row %d of this import's plan could not be read back", row.RowNumber))
		}

		switch row.Action {
		case domain.RowCreate:
			entity, okEntity := state.entities[draft.EntityKey()]
			typeID, okType := state.typeIDs[draft.ObligationTypeKey()]
			if !okEntity || entity.Ambiguous || !okType {
				return staleConflict(row.RowNumber, "the entity or obligation type it names is no longer resolvable")
			}
			obligation, err := uc.repo.CreateObligation(ctx, draft, entity.ID, typeID)
			if err != nil {
				return err
			}
			if err := uc.audit.Record(ctx, "entity_obligation.created", "entity_obligation", obligation.ID,
				audit.Changes(nil, auditObligationValues(obligation))); err != nil {
				return err
			}
		case domain.RowUpdate:
			before, ok := state.obligations[draft.NaturalKey()]
			if !ok || row.TargetUpdatedAt == nil {
				return staleConflict(row.RowNumber, "the record it would change is no longer there")
			}
			// The same merge as an entity's, and for this record it is the
			// difference between correcting a VAT number and deleting the
			// quarterly filing calendar that VAT number files under.
			merged, err := mergeObligation(draft, before)
			if err != nil {
				return err
			}
			obligation, err := uc.repo.UpdateObligation(ctx, before.ID, *row.TargetUpdatedAt, merged)
			if err != nil {
				return err
			}
			if obligation == nil {
				return staleConflict(row.RowNumber, "the record it would change has been edited since this file was checked")
			}
			if err := uc.audit.Record(ctx, "entity_obligation.updated", "entity_obligation", obligation.ID,
				audit.Changes(auditObligationValues(&before), auditObligationValues(obligation))); err != nil {
				return err
			}
		}
	}
	return nil
}

// sameVersion compares two record versions. Both nil is "neither row points at
// a record", which is how two creates agree.
func sameVersion(a, b *time.Time) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	default:
		return a.Equal(*b)
	}
}
