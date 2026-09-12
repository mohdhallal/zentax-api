package dto

import (
	"encoding/json"

	"github.com/mohamadhallal/zentax-api/modules/auditlog/domain"
)

const instantLayout = "2006-01-02T15:04:05.000Z"

// EntryToJSON renders one audit event. occurredAt is a UTC instant (ADR-0003);
// details is passed through as the stored JSON object (PII-free by
// construction, ADR-0008).
//
// `seq` is the entry's position in the tenant's hash chain and is present only
// for a reader who may read the whole tenant. The counter is tenant-wide and
// dense, so for a reader narrowed to part of the tenant the gaps between its
// own rows would count — exactly — the entries withheld from it; the repository
// zeroes it for such a reader (pg.SeqWithheld, which carries the decision and
// why the alternative, renumbering per reader, was rejected). Omitting the key
// rather than sending 0 keeps the absence legible: a client shows "withheld",
// never a chain position that is not one.
func EntryToJSON(e *domain.Entry) map[string]any {
	details := e.Details
	if len(details) == 0 {
		details = json.RawMessage("{}")
	}
	out := map[string]any{
		"id":           e.ID,
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
	if e.Seq > 0 {
		out["seq"] = e.Seq
	}
	return out
}
