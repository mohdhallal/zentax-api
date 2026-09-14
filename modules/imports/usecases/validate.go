package usecases

import (
	"context"
	"strings"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/imports/domain"
	"github.com/mohamadhallal/zentax-api/platform/audit"
)

// Validate is the upload AND the dry run, in one request.
//
// One request rather than two, deliberately. If the file were parsed on upload
// and again on a separate "preview", there would be two chances to produce two
// answers about one file — which is precisely the thing a dry run exists to
// rule out. The file is read once, planned once, and the plan is stored; every
// later view of it, including the commit's own check, reads that stored plan.
//
// Nothing of the customer's tax book is written here. What IS written is the
// plan, so that the promise made to the customer exists as a record before they
// act on it, and so the commit has something to be measured against.
func (uc *UseCases) Validate(ctx context.Context, upload domain.Upload) (*domain.Batch, *domain.BatchPlan, error) {
	kind := domain.KindOf(upload.Target)

	outcome, err := uc.reader.Read(ctx, upload)
	if err != nil {
		return nil, nil, err
	}

	// A file whose header could not be bound, or whose sheet could not be
	// chosen, has no row-by-row report to give: there is nothing to plan and
	// nothing to store. It is refused as a validation error naming every reason
	// at once, which is also the only shape the batch table would accept — a
	// batch with no rows cannot be 'rejected', because 'rejected' means "some
	// row is invalid".
	if outcome.HasFileError() {
		return nil, nil, apperrors.NewValidation(fileIssueMessage(outcome.FileIssues))
	}

	plan, _, err := uc.planFile(ctx, kind, outcome.Rows)
	if err != nil {
		return nil, nil, err
	}
	plan.FileIssues = outcome.FileIssues
	plan.HeaderRow = outcome.HeaderRow
	plan.Columns = outcome.Columns
	plan.Ignored = outcome.Ignored

	batch := &domain.Batch{
		Kind:           kind,
		FileName:       upload.FileName,
		ByteSize:       int64(len(upload.Content)),
		Checksum:       upload.Checksum,
		RowCount:       len(plan.Rows),
		CreateCount:    plan.CreateCount,
		UpdateCount:    plan.UpdateCount,
		UnchangedCount: plan.UnchangedCount,
		InvalidCount:   plan.InvalidCount,
		Status:         plan.Status(),
	}
	stored, err := uc.repo.CreateBatch(ctx, batch, plan.Rows)
	if err != nil {
		return nil, nil, err
	}

	// The dry run is recorded too, and not as a formality: it is the moment the
	// product told a customer what would happen. An auditor reading a commit
	// later needs the promise it was measured against, dated and attributed.
	if err := uc.audit.Record(ctx, "import.validated", "import_batch", stored.ID,
		audit.Changes(nil, auditBatchValues(stored))); err != nil {
		return nil, nil, err
	}
	return stored, plan, nil
}

// fileIssueMessage renders the file-level refusals as one sentence. Every
// reason is included rather than the first: a customer fixing a file should not
// have to upload it once per problem.
func fileIssueMessage(issues []domain.Issue) string {
	messages := make([]string, 0, len(issues))
	for _, issue := range issues {
		if issue.IsError() {
			messages = append(messages, issue.Message)
		}
	}
	if len(messages) == 0 {
		return "this file could not be read"
	}
	return strings.Join(messages, "; ")
}
