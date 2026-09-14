package usecases

import (
	"context"
	"fmt"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/imports/domain"
)

// Get returns one batch. The kind is part of its identity: a batch of another
// kind reads as absent rather than as a wrong-kind error, so /imports/entities
// never confirms that an obligations batch exists.
func (uc *UseCases) Get(ctx context.Context, kind domain.Kind, batchID string) (*domain.Batch, error) {
	batch, err := uc.repo.GetBatch(ctx, batchID)
	if err != nil {
		return nil, err
	}
	if batch == nil || batch.Kind != kind {
		return nil, apperrors.NewNotFound(fmt.Sprintf("import %s not found", batchID))
	}
	return batch, nil
}

// Rows is the dry run's row-by-row report, paged.
//
// Paged rather than whole because the report is the one thing a customer reads
// carefully, and a thousand rows of it is a response nobody's browser wants in
// one piece. The order is the file's own, so a row number in the report is a
// row number in their spreadsheet.
func (uc *UseCases) Rows(ctx context.Context, kind domain.Kind, batchID string, limit, offset int) ([]domain.RowPlan, int, error) {
	if _, err := uc.Get(ctx, kind, batchID); err != nil {
		return nil, 0, err
	}
	return uc.repo.ListRows(ctx, batchID, limit, offset)
}

// List is the tenant's import history for a kind, newest first — which file was
// uploaded, what it promised, and whether it was applied.
func (uc *UseCases) List(ctx context.Context, kind domain.Kind, limit, offset int) ([]domain.Batch, int, error) {
	return uc.repo.ListBatches(ctx, kind, limit, offset)
}
