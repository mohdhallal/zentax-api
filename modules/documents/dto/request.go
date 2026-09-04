package dto

// documentTypeOneOf mirrors domain.DocumentTypes for the validator / OpenAPI enum.
const documentTypeOneOf = "draft_return final_return payment_confirmation advisor_memo workings supporting_docs working_papers correspondence deliverable other"

type DocumentIdParams struct {
	ID string `json:"id" validate:"required,uuid" example:"6ba7b810-9dad-11d1-80b4-00c04fd430c8"`
}

type WorkflowIdParams struct {
	ID string `json:"id" validate:"required,uuid" example:"6ba7b810-9dad-11d1-80b4-00c04fd430c8"`
}

type TaskInstanceIdParams struct {
	ID string `json:"id" validate:"required,uuid" example:"6ba7b810-9dad-11d1-80b4-00c04fd430c8"`
}

type DocumentVersionParams struct {
	ID        string `json:"id"        validate:"required,uuid" example:"6ba7b810-9dad-11d1-80b4-00c04fd430c8"`
	VersionID string `json:"versionId" validate:"required,uuid" example:"6ba7b810-9dad-11d1-80b4-00c04fd430c8"`
}

// UpdateDocumentBody is a partial update: an omitted (or null) field is left
// unchanged; an empty label / notes clears it.
type UpdateDocumentBody struct {
	Label        *string `json:"label"        validate:"omitempty,max=500"`
	Notes        *string `json:"notes"        validate:"omitempty,max=5000"`
	DocumentType *string `json:"documentType" validate:"omitempty,oneof=draft_return final_return payment_confirmation advisor_memo workings supporting_docs working_papers correspondence deliverable other"`
	Category     *string `json:"category"     validate:"omitempty,oneof=compliance project"`
}

// ListDocumentsQuery: "all" or empty = no filter for every filter (plain
// strings, so `?workflowId=` — an empty value — is "no filter", not a 400).
type ListDocumentsQuery struct {
	Limit          int    `json:"limit"          default:"100" validate:"min=1,max=500" example:"100"`
	Offset         int    `json:"offset"         default:"0"   validate:"min=0" example:"0"`
	EntityID       string `json:"entityId"       validate:"omitempty,uuid|eq=all"`
	WorkflowID     string `json:"workflowId"     validate:"omitempty,uuid|eq=all"`
	TaskInstanceID string `json:"taskInstanceId" validate:"omitempty,uuid|eq=all"`
	DocumentType   string `json:"documentType"   validate:"omitempty,max=40"`
	Year           string `json:"year"           validate:"omitempty,max=9"`
	Search         string `json:"search"         validate:"omitempty,max=200"`
}

// Filter returns nil for "" / "all", else a pointer to the value.
func Filter(v string) *string {
	if v == "" || v == "all" {
		return nil
	}
	return &v
}
