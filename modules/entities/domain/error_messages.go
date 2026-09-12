package domain

import "strconv"

func ErrEntityNotFound(id EntityID) string {
	return "entity not found: " + id
}

// ErrEntityHasApprovedWork is the ADR-0018 refusal: the entity cannot be
// deleted because approved task instances in its workflows are attested
// evidence. It names what is in the way, how many, and the way out — an entity
// can be retired by setting its status to `archived`, which keeps the record.
// A count of zero (the database trigger refused a delete the use case did not
// see, e.g. a concurrent approval) drops the number rather than printing "0".
func ErrEntityHasApprovedWork(approved, workflows int) string {
	what := "approved task instances in its workflows are"
	if approved > 0 {
		what = strconv.Itoa(approved) + " approved task instance(s) across " +
			strconv.Itoa(workflows) + " workflow(s) are"
	}
	return "entity cannot be deleted: " + what +
		" attested evidence (ADR-0018) — archive the entity instead (status: archived)"
}
