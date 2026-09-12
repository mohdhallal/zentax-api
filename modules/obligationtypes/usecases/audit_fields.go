package usecases

import (
	"github.com/mohamadhallal/zentax-api/modules/obligationtypes/domain"
	"github.com/mohamadhallal/zentax-api/platform/audit"
)

// auditValues is the ADR-0008 whitelist for an obligation type — the reference
// row that says what a tax obligation IS. It feeds audit.Changes, which records
// the before and after of whatever actually moved.
//
// Obligation types are the definitions every entity obligation, every workflow
// and every compliance report joins to, and nothing versions them: one PUT
// replaces the only copy in existence. The entity-obligation envelope records
// only obligationTypeId, so without this the question "what did this definition
// say when that return was filed?" had no answer anywhere in the system — the
// trail said `{}` for create, update and delete alike. ADR-0017's curated
// content service, which the schema comment defers predefined types to, does
// not exist yet, so this envelope is the whole history.
//
// A value is quoted verbatim ONLY when the request contract constrains its
// shape, checked against modules/obligationtypes/dto/request.go:
//
//   - template: `oneof VAT CIT TP WHT Custom` — which engine and data template
//     the obligation drives. Re-pointing it changes what the workflows built on
//     this definition actually compute.
//   - category: `oneof predefined custom` — whether this is curated content or
//     the tenant's own. Nothing else in the module guards the boundary, so a
//     curated definition being re-pointed shows up here or nowhere.
//   - status: `oneof active inactive` — whether the definition is selectable.
//
// Redacted — the change is dated, the value withheld:
//
//   - name / description: free text (`max=200` / `max=2000`).
//   - code: the tenant-unique short key ("VAT-RET"), and the field an auditor
//     would most like quoted. It is `max=50` free text with no shape rule, so
//     quoting it would put the same unbounded input in the log that country and
//     jurisdiction were just taken out of. The definition is identified by the
//     envelope's own resource_id, and the live code is on the row; what the
//     trail adds is the date a rename happened and who did it.
//
// Left out by design: createdBy / updatedBy / createdAt / updatedAt (the
// envelope's actor_id and occurred_at say it better) and the id (resource_id).
func auditValues(ot *domain.ObligationType) audit.Values {
	if ot == nil {
		return nil
	}
	return audit.Values{
		"name":        audit.Redact(ot.Name),
		"code":        audit.Redact(ot.Code),
		"description": audit.Redact(ot.Description),
		"category":    ot.Category,
		"template":    ot.Template,
		"status":      ot.Status,
	}
}
