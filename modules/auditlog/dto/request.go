package dto

import "strings"

// ResourceTypes is the audit trail's resource vocabulary: every value
// `platform/audit`'s Recorder is called with anywhere in this tree, and so
// every value `audit_log.resource_type` can hold. It is the ONE definition the
// filter is built from — `resourceTypeRule` renders the `oneof=` list of the
// struct tag below from it, and `resource_types_test.go` re-derives the same
// set from the Record CALL SITES, so a resource type that starts being
// recorded without being listed here fails the build instead of quietly
// becoming unfilterable. (Half of them were: until 2026-09-13 this list held
// the six workflow-chain types only, while the trail wrote twelve, so an
// auditor could not ask for a token, a service account, the tenant record, a
// member, a document or a data template at all.)
//
// Order is the order the product reads in — the workflow chain, then what
// hangs off it, then the tenant's own administration — and it is the order the
// OpenAPI enum and the 400 message list.
//
// A type STAYS here once listed even if the code stops writing it: the ledger
// is append-only, so rows recorded under a retired type must remain
// filterable. The test that pins this list to the call sites carries an
// (empty) escape hatch for exactly that case.
var ResourceTypes = []string{
	"entity",
	"obligation_type",
	"entity_obligation",
	"workflow",
	"workflow_task",
	"task_instance",
	"data_template",
	"document",
	// `user` is the member directory: member.invited / invite_reissued /
	// activated / updated / role_changed all record against the user row.
	"user",
	"service_account",
	"api_token",
	"tenant",
}

// resourceTypeRule is the validator rule the ResourceType tag must carry
// verbatim. Struct tags are compile-time literals, so the tag cannot be built
// from ResourceTypes at run time — TestResourceTypeTagIsTheOneDefinition
// compares the two instead, which makes the duplication a CHECKED one rather
// than a hand-maintained one. The `oneof=` form is deliberate: the swagger
// generator turns it into the published enum and the validator turns it into
// "must be one of: …" in the 400.
func resourceTypeRule() string { return "oneof=" + strings.Join(ResourceTypes, " ") }

// ListAuditLogQuery filters + pages the audit trail. from/to are date-only
// (YYYY-MM-DD, inclusive) windows on occurred_at; the datetime tag validates
// the layout before the handler parses them. action repeats
// (`?action=workflow.started&action=workflow.created`): the entries matching
// ANY of the given actions — so a UI verb ("started") can map onto several
// stored actions; a single value keeps working.
//
// resourceType accepts every value the trail records (ResourceTypes).
// Filtering is orthogonal to visibility: a reader narrowed to a subtree
// (ADR-0012) still sees only the rows its grant reaches, and the tenant-level
// families — tenant, user, service_account, api_token, data_template,
// obligation_type — resolve no owning entity, so a narrowed reader filtering
// on one of those correctly gets an empty page rather than a 400.
type ListAuditLogQuery struct {
	Limit        int      `json:"limit"        default:"100" validate:"min=1,max=500" example:"100"`
	Offset       int      `json:"offset"       default:"0"   validate:"min=0" example:"0"`
	WorkflowID   *string  `json:"workflowId"   validate:"omitempty,uuid" example:"6ba7b810-9dad-11d1-80b4-00c04fd430c8"`
	ResourceType *string  `json:"resourceType" validate:"omitempty,oneof=entity obligation_type entity_obligation workflow workflow_task task_instance data_template document user service_account api_token tenant" example:"workflow"`
	ResourceID   *string  `json:"resourceId"   validate:"omitempty,uuid" example:"6ba7b810-9dad-11d1-80b4-00c04fd430c8"`
	Action       []string `json:"action"       validate:"omitempty,dive,max=60" example:"workflow.started"`
	From         *string  `json:"from"         validate:"omitempty,datetime=2006-01-02" example:"2026-01-01"`
	To           *string  `json:"to"           validate:"omitempty,datetime=2006-01-02" example:"2026-12-31"`
}
