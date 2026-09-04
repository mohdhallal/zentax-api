package usecases

import (
	"github.com/mohamadhallal/zentax-api/modules/workflowtasks/domain"
	"github.com/mohamadhallal/zentax-api/platform/audit"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

type UseCases struct {
	repo       domain.WorkflowTaskRepository
	authorizer *authz.Authorizer
	audit      *audit.Recorder
}

// NewUseCases builds the workflow-task use cases; the authorizer is optional
// (variadic) so unit tests skip scope checks while the app always wires one.
func NewUseCases(repo domain.WorkflowTaskRepository, authorizer ...*authz.Authorizer) *UseCases {
	uc := &UseCases{repo: repo}
	if len(authorizer) > 0 {
		uc.authorizer = authorizer[0]
	}
	return uc
}

// applyDueDateDefaults fills the offset-rule fields the DB defaults would set,
// since the INSERT/UPDATE always passes explicit values. A payment-type task
// with no explicit reference is due off the payment deadline (ADR-0023 §5);
// every other task off the filing deadline.
func applyDueDateDefaults(taskType string, reference, unit, direction *string) {
	if *reference == "" {
		if taskType == "payment" {
			*reference = "payment_deadline"
		} else {
			*reference = "filing_deadline"
		}
	}
	if *unit == "" {
		*unit = "days"
	}
	if *direction == "" {
		*direction = "before"
	}
}

// WithAudit injects the audit recorder (ADR-0008). Optional and nil-safe —
// unit tests construct without it; the container always chains it on.
func (uc *UseCases) WithAudit(r *audit.Recorder) *UseCases {
	uc.audit = r
	return uc
}
