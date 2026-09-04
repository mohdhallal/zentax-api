package usecases

import (
	"github.com/mohamadhallal/zentax-api/modules/taskinstances/domain"
	"github.com/mohamadhallal/zentax-api/platform/audit"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

type UseCases struct {
	repo       domain.TaskInstanceRepository
	authorizer *authz.Authorizer
	audit      *audit.Recorder
	assignees  domain.AssigneeChecker
	templates  domain.TemplateResolver
}

// WithTemplateResolver injects the data-template lookup that tax data is
// validated against (ADR-0001 server-side authority). Optional and nil-safe —
// without it, tax data is stored as sent (unit tests); the container always
// wires one.
func (uc *UseCases) WithTemplateResolver(r domain.TemplateResolver) *UseCases {
	uc.templates = r
	return uc
}

// WithAssigneeChecker injects the tenant-directory check for assigneeId
// (identity module). Optional and nil-safe — without it, assignment is not
// validated (unit tests); the container always wires one.
func (uc *UseCases) WithAssigneeChecker(c domain.AssigneeChecker) *UseCases {
	uc.assignees = c
	return uc
}

// NewUseCases builds the task-instance use cases; the authorizer is optional
// (variadic) so unit tests skip scope checks while the app always wires one.
func NewUseCases(repo domain.TaskInstanceRepository, authorizer ...*authz.Authorizer) *UseCases {
	uc := &UseCases{repo: repo}
	if len(authorizer) > 0 {
		uc.authorizer = authorizer[0]
	}
	return uc
}

// WithAudit injects the audit recorder (ADR-0008). Optional and nil-safe —
// unit tests construct without it; the container always chains it on.
func (uc *UseCases) WithAudit(r *audit.Recorder) *UseCases {
	uc.audit = r
	return uc
}
