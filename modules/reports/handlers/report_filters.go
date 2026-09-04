package handlers

import (
	"strings"

	"github.com/google/uuid"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/reports/domain"
	"github.com/mohamadhallal/zentax-api/modules/reports/dto"
	"github.com/mohamadhallal/zentax-api/shared/dateonly"
)

// The legacy report pages send "all" for an unset filter (and nothing, or "",
// for others). Every filter param therefore normalizes nil / "" / "all" to
// "no filter" before validation of its actual value.

func optional(s *string) *string {
	if s == nil || *s == "" || *s == "all" {
		return nil
	}
	return s
}

func optionalID(name string, s *string) (*string, error) {
	s = optional(s)
	if s == nil {
		return nil, nil //nolint:nilnil // absent filter
	}
	if _, err := uuid.Parse(*s); err != nil {
		return nil, apperrors.NewValidation(name + " must be a valid UUID")
	}
	return s, nil
}

// optionalEnum normalizes like optional, then admits only the listed values —
// so "" / "all" / absent mean "no filter" while a typo is still a 400.
func optionalEnum(name string, s *string, allowed ...string) (*string, error) {
	s = optional(s)
	if s == nil {
		return nil, nil //nolint:nilnil // absent filter
	}
	for _, a := range allowed {
		if *s == a {
			return s, nil
		}
	}
	return nil, apperrors.NewValidation(name + " must be one of: " + strings.Join(allowed, ", "))
}

func optionalDate(name string, s *string) (*dateonly.Date, error) {
	s = optional(s)
	if s == nil {
		return nil, nil //nolint:nilnil // absent filter
	}
	d, err := dateonly.Parse(*s)
	if err != nil {
		return nil, apperrors.NewValidation(name + " must be a YYYY-MM-DD date")
	}
	return &d, nil
}

func reportFilters(q dto.ReportFilterQuery) (domain.ReportFilters, error) {
	entityID, err := optionalID("entityId", q.EntityID)
	if err != nil {
		return domain.ReportFilters{}, err
	}
	obligationTypeID, err := optionalID("obligationTypeId", q.ObligationTypeID)
	if err != nil {
		return domain.ReportFilters{}, err
	}
	return domain.ReportFilters{
		FinancialYear:    optional(q.Year),
		EntityID:         entityID,
		ObligationTypeID: obligationTypeID,
	}, nil
}
