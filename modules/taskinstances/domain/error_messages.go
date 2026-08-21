package domain

func ErrTaskInstanceNotFound(id TaskInstanceID) string {
	return "task instance not found: " + id
}

// Approval-flow messages (ADR-0018 attestation + ADR-0012 segregation of duties).
const (
	MsgNotPendingApproval  = "task instance is not pending approval"
	MsgAlreadyPending      = "task instance is already pending approval"
	MsgApprovedImmutable   = "task instance is approved and cannot be modified — create an amendment (ADR-0018)"
	MsgApprovalNotRequired = "task instance does not require approval"
	MsgCannotApproveOwn    = "you cannot approve a task instance you submitted (separation of duties)"
)
