package entityobligations_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/mohamadhallal/zentax-api/acceptance"
)

type EntityObligationsSuite struct {
	acceptance.Suite
}

func TestEntityObligationsSuite(t *testing.T) {
	suite.Run(t, new(EntityObligationsSuite))
}

func (s *EntityObligationsSuite) createEntity(tenant, name, country string) string {
	var out struct {
		ID string `json:"id"`
	}
	resp := s.As(tenant).
		POST(s.T(), "/entities", map[string]any{"name": name, "country": country})
	resp.AssertStatus(s.T(), http.StatusCreated)
	resp.DecodeData(s.T(), &out)
	return out.ID
}

func (s *EntityObligationsSuite) createObligationType(tenant, code string) string {
	var out struct {
		ID string `json:"id"`
	}
	resp := s.As(tenant).
		POST(s.T(), "/obligation-types", map[string]any{"name": "VAT", "code": code, "template": "VAT"})
	resp.AssertStatus(s.T(), http.StatusCreated)
	resp.DecodeData(s.T(), &out)
	return out.ID
}

// TestTenantIsolationAndCrossTenantFK proves entity obligations are RLS-isolated
// and that the entity_id / obligation_type_id foreign keys are scoped to the
// tenant: tenant B cannot link to tenant A's entity/obligation type because
// those rows are invisible to B, so the FK check fails with 400 (ADR-0004).
func (s *EntityObligationsSuite) TestTenantIsolationAndCrossTenantFK() {
	tenantA := s.InsertTenant("eo-a", "EO Tenant A").String()
	tenantB := s.InsertTenant("eo-b", "EO Tenant B").String()

	entityA := s.createEntity(tenantA, "Acme A", "Germany")
	obTypeA := s.createObligationType(tenantA, "VAT-RET")

	body := map[string]any{
		"entityId":         entityA,
		"obligationTypeId": obTypeA,
		"periodicity":      "monthly",
		"deadlineRule": map[string]any{
			"type": "period_offset", "reference": "period_end",
			"offsetUnit": "days", "offsetValue": 20, "offsetDirection": "after",
		},
	}

	// Tenant A links its own entity + obligation type.
	s.As(tenantA).
		POST(s.T(), "/entity-obligations", body).
		AssertStatus(s.T(), http.StatusCreated)

	// Tenant A sees the link; tenant B does not (RLS).
	var listA, listB []map[string]any
	s.As(tenantA).GET(s.T(), "/entity-obligations").DecodeData(s.T(), &listA)
	s.As(tenantB).GET(s.T(), "/entity-obligations").DecodeData(s.T(), &listB)
	s.Require().Len(listA, 1)
	s.Require().Empty(listB)

	// Cross-tenant FK: tenant B references tenant A's ids — invisible under RLS,
	// so the FK check fails and the API returns 400, never leaking A's rows.
	s.As(tenantB).
		POST(s.T(), "/entity-obligations", body).
		AssertStatus(s.T(), http.StatusBadRequest)

	// No tenant → 401 (fail-closed).
	s.Client.External().GET(s.T(), "/entity-obligations").
		AssertStatus(s.T(), http.StatusUnauthorized)
}

// TestFullBuilderRoundTrip proves the legacy deadline builder + registration
// details survive the API → JSONB → API trip byte-for-byte: nested rule
// objects (periodStart, filingOffset, paymentOffset, additionalDeadlines), the
// three new columns (taxReferenceNumber, jurisdictionState, currency), and the
// `weekly` periodicity (recordable; the engine fails closed on it at start).
func (s *EntityObligationsSuite) TestFullBuilderRoundTrip() {
	tenant := s.InsertTenant("eo-rt", "EO Round Trip").String()
	entity := s.createEntity(tenant, "Acme GmbH", "Germany")
	obType := s.createObligationType(tenant, "PAYROLL")

	rule := map[string]any{
		"type":              "period_offset",
		"periodStart":       map[string]any{"day": float64(6), "month": float64(4)},
		"filingOffset":      map[string]any{"months": float64(1), "days": float64(7)},
		"paymentOffset":     map[string]any{"months": float64(0), "days": float64(22)},
		"weekendAdjustment": "next-business-day",
		"additionalDeadlines": []any{
			map[string]any{"type": "advance_payment", "months": float64(0), "days": float64(10)},
		},
	}
	body := map[string]any{
		"entityId":           entity,
		"obligationTypeId":   obType,
		"taxReferenceNumber": "DE-PAYE-0042",
		"jurisdiction":       "Germany",
		"jurisdictionState":  "Bavaria",
		"currency":           "EUR",
		"periodicity":        "weekly",
		"deadlineRule":       rule,
	}

	var created map[string]any
	resp := s.As(tenant).POST(s.T(), "/entity-obligations", body)
	resp.AssertStatus(s.T(), http.StatusCreated)
	resp.DecodeData(s.T(), &created)

	s.Equal("DE-PAYE-0042", created["taxReferenceNumber"])
	s.Equal("Germany", created["jurisdiction"])
	s.Equal("Bavaria", created["jurisdictionState"])
	s.Equal("EUR", created["currency"])
	s.Equal("weekly", created["periodicity"])
	s.Equal(rule, created["deadlineRule"])

	// Reading it back (a fresh scan of the JSONB row) yields the identical rule.
	var fetched map[string]any
	s.As(tenant).GET(s.T(), "/entity-obligations/"+created["id"].(string)).DecodeData(s.T(), &fetched)
	s.Equal(rule, fetched["deadlineRule"])
	s.Equal("EUR", fetched["currency"])
	s.Equal("Bavaria", fetched["jurisdictionState"])

	// A "payment same as filing" rule omits paymentOffset entirely (nil, not {}).
	sameAsFiling := map[string]any{
		"entityId": entity, "obligationTypeId": obType, "periodicity": "monthly",
		"deadlineRule": map[string]any{
			"type": "period_offset", "filingOffset": map[string]any{"months": float64(1), "days": float64(0)},
		},
	}
	var created2 map[string]any
	resp2 := s.As(tenant).POST(s.T(), "/entity-obligations", sameAsFiling)
	resp2.AssertStatus(s.T(), http.StatusCreated)
	resp2.DecodeData(s.T(), &created2)
	rule2 := created2["deadlineRule"].(map[string]any)
	_, hasPayment := rule2["paymentOffset"]
	s.False(hasPayment, "nil paymentOffset must not serialize")
	s.Nil(created2["currency"])
	s.Nil(created2["taxReferenceNumber"])
}

// TestPutReplacesRule proves PUT is a full replacement: switching from a
// period-offset rule to fixed filing + payment dates drops the offsets, and the
// registration columns are overwritten (or cleared) rather than merged.
func (s *EntityObligationsSuite) TestPutReplacesRule() {
	tenant := s.InsertTenant("eo-put", "EO Put").String()
	entity := s.createEntity(tenant, "Acme SA", "France")
	obType := s.createObligationType(tenant, "CIT-RET")

	var created map[string]any
	resp := s.As(tenant).POST(s.T(), "/entity-obligations", map[string]any{
		"entityId": entity, "obligationTypeId": obType,
		"taxReferenceNumber": "FR-CIT-1", "jurisdiction": "France", "currency": "EUR",
		"periodicity": "quarterly",
		"deadlineRule": map[string]any{
			"type":          "period_offset",
			"periodStart":   map[string]any{"day": 1, "month": 1},
			"filingOffset":  map[string]any{"months": 1, "days": 0},
			"paymentOffset": map[string]any{"months": 1, "days": 15},
		},
	})
	resp.AssertStatus(s.T(), http.StatusCreated)
	resp.DecodeData(s.T(), &created)
	id := created["id"].(string)

	fixed := map[string]any{
		"type":              "fixed",
		"fixedDates":        []any{"03-15", "09-15"},
		"paymentFixedDates": []any{"03-31", "09-30"},
	}
	var updated map[string]any
	put := s.As(tenant).PUT(s.T(), "/entity-obligations/"+id, map[string]any{
		"jurisdiction": "France",
		"currency":     "USD",
		"periodicity":  "bi-annual",
		"deadlineRule": fixed,
		"status":       "active",
	})
	put.AssertStatus(s.T(), http.StatusOK)
	put.DecodeData(s.T(), &updated)

	s.Equal("bi-annual", updated["periodicity"])
	s.Equal("USD", updated["currency"])
	s.Nil(updated["taxReferenceNumber"], "PUT without the field clears it")
	s.Equal(fixed, updated["deadlineRule"], "offsets from the old rule must not linger")

	var fetched map[string]any
	s.As(tenant).GET(s.T(), "/entity-obligations/"+id).DecodeData(s.T(), &fetched)
	s.Equal(fixed, fetched["deadlineRule"])
}

// TestInvalidCurrencyRejected: currency is ISO 4217 alpha-3 — a 4-letter or
// lower-case code is a 400, on create and on update alike.
func (s *EntityObligationsSuite) TestInvalidCurrencyRejected() {
	tenant := s.InsertTenant("eo-cur", "EO Currency").String()
	entity := s.createEntity(tenant, "Acme Ltd", "United Kingdom")
	obType := s.createObligationType(tenant, "VAT-UK")

	base := func(currency string) map[string]any {
		return map[string]any{
			"entityId": entity, "obligationTypeId": obType, "periodicity": "monthly",
			"currency": currency,
		}
	}
	s.As(tenant).POST(s.T(), "/entity-obligations", base("EURO")).
		AssertStatus(s.T(), http.StatusBadRequest)
	s.As(tenant).POST(s.T(), "/entity-obligations", base("eur")).
		AssertStatus(s.T(), http.StatusBadRequest)

	var created map[string]any
	ok := s.As(tenant).POST(s.T(), "/entity-obligations", base("GBP"))
	ok.AssertStatus(s.T(), http.StatusCreated)
	ok.DecodeData(s.T(), &created)

	s.As(tenant).PUT(s.T(), "/entity-obligations/"+created["id"].(string), map[string]any{
		"periodicity": "monthly", "currency": "GB",
	}).AssertStatus(s.T(), http.StatusBadRequest)

	// Rule-level validation also fails closed: a 13th month is a 400.
	bad := base("GBP")
	bad["deadlineRule"] = map[string]any{"type": "period_offset", "periodStart": map[string]any{"day": 1, "month": 13}}
	s.As(tenant).POST(s.T(), "/entity-obligations", bad).
		AssertStatus(s.T(), http.StatusBadRequest)
}
