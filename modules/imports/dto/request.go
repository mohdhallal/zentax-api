package dto

// The upload routes declare no Body: the file arrives as multipart/form-data
// and the handler reads the stream itself, bounded, before the router would
// otherwise have buffered it (see handlers/multipart.go). Everything else the
// module takes is a path parameter or a page.

// BatchIdParams addresses one import batch.
type BatchIdParams struct {
	ID string `json:"id" validate:"required,uuid" example:"6ba7b810-9dad-11d1-80b4-00c04fd430c8"`
}

// ListImportsQuery pages the tenant's import history for a kind.
type ListImportsQuery struct {
	Limit  int `json:"limit"  default:"20" validate:"min=1,max=100" example:"20"`
	Offset int `json:"offset" default:"0"  validate:"min=0" example:"0"`
}

// ListRowsQuery pages the row-by-row report.
//
// The page is larger than a list's — the report is read as a whole document,
// not browsed — and still bounded, because a thousand rows of resolved records
// is a response no browser wants in one piece.
type ListRowsQuery struct {
	Limit  int `json:"limit"  default:"100" validate:"min=1,max=500" example:"100"`
	Offset int `json:"offset" default:"0"   validate:"min=0" example:"0"`
}
