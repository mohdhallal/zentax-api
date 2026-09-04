package dto

// ListAuditLogQuery filters + pages the audit trail. from/to are date-only
// (YYYY-MM-DD, inclusive) windows on occurred_at; the datetime tag validates
// the layout before the handler parses them.
type ListAuditLogQuery struct {
	Limit        int     `json:"limit"        default:"100" validate:"min=1,max=500" example:"100"`
	Offset       int     `json:"offset"       default:"0"   validate:"min=0" example:"0"`
	WorkflowID   *string `json:"workflowId"   validate:"omitempty,uuid" example:"6ba7b810-9dad-11d1-80b4-00c04fd430c8"`
	ResourceType *string `json:"resourceType" validate:"omitempty,oneof=entity obligation_type entity_obligation workflow workflow_task task_instance" example:"workflow"`
	ResourceID   *string `json:"resourceId"   validate:"omitempty,uuid" example:"6ba7b810-9dad-11d1-80b4-00c04fd430c8"`
	Action       *string `json:"action"       validate:"omitempty,max=60" example:"workflow.started"`
	From         *string `json:"from"         validate:"omitempty,datetime=2006-01-02" example:"2026-01-01"`
	To           *string `json:"to"           validate:"omitempty,datetime=2006-01-02" example:"2026-12-31"`
}
