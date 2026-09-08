package dto

type CreateObligationTypeBody struct {
	Name        string  `json:"name"        validate:"required,min=1,max=200" example:"VAT Return"`
	Code        string  `json:"code"        validate:"required,min=1,max=50" example:"VAT-RET"`
	Template    string  `json:"template"    validate:"required,oneof=VAT CIT TP WHT Custom" example:"VAT"`
	Category    string  `json:"category"    validate:"omitempty,oneof=predefined custom" example:"custom"`
	Description *string `json:"description" validate:"omitempty,max=2000"`
}

type UpdateObligationTypeBody struct {
	Name        string  `json:"name"        validate:"required,min=1,max=200"`
	Code        string  `json:"code"        validate:"required,min=1,max=50"`
	Template    string  `json:"template"    validate:"required,oneof=VAT CIT TP WHT Custom"`
	Category    string  `json:"category"    validate:"omitempty,oneof=predefined custom"`
	Status      string  `json:"status"      validate:"omitempty,oneof=active inactive"`
	Description *string `json:"description" validate:"omitempty,max=2000"`
}

type ObligationTypeIdParams struct {
	ID string `json:"id" validate:"required,uuid" example:"6ba7b810-9dad-11d1-80b4-00c04fd430c8"`
}

// ListObligationTypesQuery pages and filters the obligation-type list. search
// is a literal, case-insensitive substring of name or code (% _ \ match
// themselves); blank means no search, and the total counts the same predicate
// as the page.
type ListObligationTypesQuery struct {
	Limit    int      `json:"limit"    default:"20" validate:"min=1,max=100" example:"20"`
	Offset   int      `json:"offset"   default:"0"  validate:"min=0" example:"0"`
	Sort     []string `json:"sort"     validate:"omitempty,dive,oneof=createdAt:asc createdAt:desc name:asc name:desc code:asc code:desc" example:"createdAt:desc"`
	Template *string  `json:"template" filter:"template" validate:"omitempty,oneof=VAT CIT TP WHT Custom" example:"VAT"`
	Category *string  `json:"category" filter:"category" validate:"omitempty,oneof=predefined custom" example:"custom"`
	Status   *string  `json:"status"   filter:"status"   validate:"omitempty,oneof=active inactive" example:"active"`
	Search   string   `json:"search"   validate:"omitempty,max=200" example:"VAT"`
}
