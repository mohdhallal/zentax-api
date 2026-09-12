package usecases

import (
	"github.com/mohamadhallal/zentax-api/modules/documents/domain"
	"github.com/mohamadhallal/zentax-api/platform/audit"
)

// auditValues is the ADR-0008 whitelist for a document — the evidence behind a
// filing. It feeds audit.Changes, which records the before and after of
// whatever actually moved.
//
// It replaces a payload that recorded the post-state of documentType and
// category unconditionally, whether or not the request touched them, and
// nothing at all about label and notes, which are the other two fields
// UpdateDocumentInput can move. That payload was not merely thin: an edit that
// only renamed a file recorded "category: → compliance", which an auditor reads
// as a reclassification that never happened, and a real reclassification
// rendered identically. Nothing else holds the before state — UpdateMetadata is
// an in-place UPDATE and document_versions is blob-level (storage key, file
// name, size, digest) with no metadata history — so this envelope is the only
// record that the edit happened.
//
// Recorded verbatim (fixed vocabularies and uuids — no free text):
//
//   - category: compliance | project. Which retention and which report a piece
//     of evidence falls under.
//   - documentType: the ten-value vocabulary in domain.DocumentTypes, enforced
//     by a CHECK constraint. draft_return → final_return is a real assertion
//     about what the file IS.
//   - workflowId: what the evidence belongs to, and the link an auditor
//     follows. Fixed at creation, so it never MOVES — which is exactly why it
//     is recorded on the one-sided envelopes at both ends of the document's
//     life AND on every version added in between: a workflow delete
//     hard-cascades documents and document_versions away without a per-row
//     entry, so once it runs these envelopes are the only thing left saying
//     which workflow the evidence was filed against.
//   - taskInstanceId: which step it was filed against, or null for a
//     workflow-level document. This is the ADR-0018 freeze boundary.
//
// Redacted — the change is dated, the value withheld: label and notes. Both are
// free text a user typed on the object closest to the filing, so both are
// exactly where a taxpayer's name or an adviser's phone number gets typed.
//
// Left out by design: the file name and MIME type (client-supplied strings, and
// a file name carries a client's name as often as a label does), the size,
// version number and SHA-256 (version facts, immutable once written, recorded
// beside this change set by document.created / document.version_added — see
// auditUploadDetails in upload.go), the actor / timestamp columns (the envelope
// carries those) and the id (that is resource_id).
func auditValues(d *domain.Document) audit.Values {
	if d == nil {
		return nil
	}
	return audit.Values{
		"label":          audit.Redact(d.Label),
		"notes":          audit.Redact(d.Notes),
		"category":       d.Category,
		"documentType":   d.DocumentType,
		"workflowId":     d.WorkflowID,
		"taskInstanceId": d.TaskInstanceID,
	}
}

// auditViewValues is the same whitelist read off the API projection, which is
// what an update holds afterwards. It narrows the view to the row rather than
// repeating the whitelist, so the field set cannot drift between the before and
// after sides of one envelope.
func auditViewValues(v *domain.DocumentView) audit.Values {
	if v == nil {
		return nil
	}
	return auditValues(&domain.Document{
		WorkflowID:     v.WorkflowID,
		TaskInstanceID: v.TaskInstanceID,
		Category:       v.Category,
		DocumentType:   v.DocumentType,
		Label:          v.Label,
		Notes:          v.Notes,
	})
}
