package domain

func ErrDataTemplateNotFound(id DataTemplateID) string {
	return "data template not found: " + id
}

func ErrDataTemplateNameExists(name string) string {
	return "data template name already exists: " + name
}

const (
	// MsgPredefinedImmutable: category=predefined rows cannot be edited or deleted.
	MsgPredefinedImmutable = "predefined templates are immutable"
	// MsgTemplateInUse: a template referenced by a workflow task or task instance.
	MsgTemplateInUse = "template is in use"
	// MsgTemplateInUseFieldsFrozen: an in-use template keeps its existing field
	// ids and types; only additive edits are allowed.
	MsgTemplateInUseFieldsFrozen = "template is in use: existing fields cannot be removed or change type (rename them or add new fields instead)"
)
