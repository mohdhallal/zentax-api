package dto

import (
	"encoding/json"

	"github.com/mohamadhallal/zentax-api/modules/auditlog/domain"
)

const instantLayout = "2006-01-02T15:04:05.000Z"

// EntryToJSON renders one audit event. occurredAt is a UTC instant (ADR-0003);
// details is passed through as the stored JSON object (PII-free by
// construction, ADR-0008).
func EntryToJSON(e *domain.Entry) map[string]any {
	details := e.Details
	if len(details) == 0 {
		details = json.RawMessage("{}")
	}
	return map[string]any{
		"id":           e.ID,
		"seq":          e.Seq,
		"action":       e.Action,
		"resourceType": e.ResourceType,
		"resourceId":   e.ResourceID,
		"actorId":      e.ActorID,
		"actorName":    e.ActorName,
		"occurredAt":   e.OccurredAt.UTC().Format(instantLayout),
		"requestId":    e.RequestID,
		"details":      details,
		"workflowId":   e.WorkflowID,
		"workflowName": e.WorkflowName,
		"hash":         e.Hash,
	}
}
