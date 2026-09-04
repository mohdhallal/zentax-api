package dto

import (
	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/workflows/domain"
	"github.com/mohamadhallal/zentax-api/shared/dateonly"
)

// StartWorkflowBody is the OPTIONAL body of POST /workflows/{id}/start. An
// absent body, {} and {"taskOverrides":{}} all mean "no overrides". Keys are
// "<templateId>_<periodCode>" — the same pairs GET /workflows/{id}/preview
// lists — so a caller adjusts exactly the rows it was shown.
type StartWorkflowBody struct {
	TaskOverrides map[string]TaskOverrideBody `json:"taskOverrides" validate:"omitempty,dive"`
}

// TaskOverrideBody carries legal date-only values (ADR-0002).
type TaskOverrideBody struct {
	DueDate         *string `json:"dueDate"         validate:"omitempty,len=10" example:"2025-02-10"`
	PeriodEndDate   *string `json:"periodEndDate"   validate:"omitempty,len=10" example:"2025-01-31"`
	PaymentDeadline *string `json:"paymentDeadline" validate:"omitempty,len=10" example:"2025-03-10"`
}

// ToDomain parses the override dates; an unparsable date is a validation error
// naming the offending key. An empty string means "no override" (a cleared
// date input), exactly like an absent field.
func (b StartWorkflowBody) ToDomain() (domain.TaskOverrides, error) {
	if len(b.TaskOverrides) == 0 {
		return nil, nil
	}
	out := make(domain.TaskOverrides, len(b.TaskOverrides))
	for key, ov := range b.TaskOverrides {
		var d domain.TaskOverride
		var err error
		if d.DueDate, err = parseOverride(ov.DueDate); err != nil {
			return nil, apperrors.NewValidation("task override " + key + ": invalid dueDate (want YYYY-MM-DD)")
		}
		if d.PeriodEndDate, err = parseOverride(ov.PeriodEndDate); err != nil {
			return nil, apperrors.NewValidation("task override " + key + ": invalid periodEndDate (want YYYY-MM-DD)")
		}
		if d.PaymentDeadline, err = parseOverride(ov.PaymentDeadline); err != nil {
			return nil, apperrors.NewValidation("task override " + key + ": invalid paymentDeadline (want YYYY-MM-DD)")
		}
		out[key] = d
	}
	return out, nil
}

// parseOverride: nil or "" → no override; otherwise a strict YYYY-MM-DD.
func parseOverride(s *string) (*dateonly.Date, error) {
	if s == nil || *s == "" {
		return nil, nil //nolint:nilnil // no override
	}
	parsed, err := dateonly.Parse(*s)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}
