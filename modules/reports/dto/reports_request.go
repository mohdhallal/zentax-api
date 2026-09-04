package dto

// Query contracts for the four compliance / financial reports. Parameter
// NAMES are the legacy Express ones so the frontend proxy is a pass-through;
// for every filter the value "all" (or absent / empty) means "no filter" — the
// handlers normalize it, so the tags below only bound the raw text and the
// enum params accept "all" explicitly. limit/offset follow ADR-0021 rule 2
// (default 1000, max 5000).

// ReportFilterQuery is the workflow-level filter trio shared by the heatmap,
// compliance-status and tax-financial reports.
type ReportFilterQuery struct {
	Year             *string `json:"year"             validate:"omitempty,max=9"  example:"2025"`
	EntityID         *string `json:"entityId"         validate:"omitempty,max=64" example:"6ba7b810-9dad-11d1-80b4-00c04fd430c8"`
	ObligationTypeID *string `json:"obligationTypeId" validate:"omitempty,max=64" example:"6ba7b810-9dad-11d1-80b4-00c04fd430c8"`
}

type ComplianceHeatmapQuery struct {
	ReportFilterQuery
	ViewMode string `json:"viewMode" default:"period" validate:"oneof=period tax-type" example:"period"`
}

type ComplianceStatusQuery struct {
	ReportFilterQuery
	// Enum enforced in the handler after "" / "all" normalize to "no filter".
	Status *string `json:"status" validate:"omitempty,max=20" example:"late"`
	Limit  int     `json:"limit"  default:"1000" validate:"min=1,max=5000" example:"1000"`
	Offset int     `json:"offset" default:"0"    validate:"min=0" example:"0"`
}

type TaxFinancialQuery struct {
	ReportFilterQuery
	GroupBy string `json:"groupBy" default:"entity" validate:"oneof=entity country taxType period obligation" example:"entity"`
	Limit   int    `json:"limit"   default:"1000" validate:"min=1,max=5000" example:"1000"`
	Offset  int    `json:"offset"  default:"0"    validate:"min=0" example:"0"`
}

type ExportRawQuery struct {
	Dataset          string  `json:"dataset"          default:"tasks" validate:"oneof=workflows tasks tax-data" example:"tasks"`
	EntityID         *string `json:"entityId"         validate:"omitempty,max=64" example:"6ba7b810-9dad-11d1-80b4-00c04fd430c8"`
	ObligationTypeID *string `json:"obligationTypeId" validate:"omitempty,max=64" example:"6ba7b810-9dad-11d1-80b4-00c04fd430c8"`
	Category         *string `json:"category"         validate:"omitempty,max=20" example:"recurring"` // enum enforced in the handler
	DateFrom         *string `json:"dateFrom"         validate:"omitempty,max=10" example:"2025-01-01"`
	DateTo           *string `json:"dateTo"           validate:"omitempty,max=10" example:"2025-12-31"`
	Limit            int     `json:"limit"            default:"1000" validate:"min=1,max=5000" example:"1000"`
	Offset           int     `json:"offset"           default:"0"    validate:"min=0" example:"0"`
}
