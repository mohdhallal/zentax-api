package authz_test

import "net/http"

func (s *AuthzSuite) mkEntity(tenant, name, parent string) string {
	body := map[string]any{"name": name, "country": "Germany"}
	if parent != "" {
		body["parentEntityId"] = parent
	}
	var out struct {
		ID string `json:"id"`
	}
	r := s.As(tenant).POST(s.T(), "/entities", body)
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &out)
	return out.ID
}

func (s *AuthzSuite) mkObligationType(tenant, code string) string {
	var out struct {
		ID string `json:"id"`
	}
	r := s.As(tenant).POST(s.T(), "/obligation-types",
		map[string]any{"name": "VAT", "code": code, "template": "VAT"})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &out)
	return out.ID
}

// TestEntitySubtreeScope proves a scoped grant only authorizes writes within its
// entity subtree (ADR-0012, Increment B-2): a manager scoped to entity A can act
// on A and its descendants but not on a sibling B, cannot touch tenant-level
// content, and a tenant-wide manager can act anywhere.
func (s *AuthzSuite) TestEntitySubtreeScope() {
	tenant := s.InsertTenant("scope", "Scope Co").String()

	// Admin (tenant-wide) builds the tree: A (root) → A1 (child); B (separate
	// root); plus a tenant-level obligation type.
	entA := s.mkEntity(tenant, "Alpha", "")
	entA1 := s.mkEntity(tenant, "Alpha-Sub", entA)
	entB := s.mkEntity(tenant, "Beta", "")
	ot := s.mkObligationType(tenant, "VAT-RET")

	eoBody := func(entityID string) map[string]any {
		return map[string]any{
			"entityId": entityID, "obligationTypeId": ot, "periodicity": "monthly",
			"deadlineRule": map[string]any{
				"type": "period_offset", "reference": "period_end",
				"offsetUnit": "days", "offsetValue": 20, "offsetDirection": "after",
			},
		}
	}

	// A manager scoped to A.
	scopedA := s.AsScopedRole(tenant, "manager", entA)

	// In scope: A itself and its descendant A1.
	scopedA.POST(s.T(), "/entity-obligations", eoBody(entA)).AssertStatus(s.T(), http.StatusCreated)
	scopedA.POST(s.T(), "/entity-obligations", eoBody(entA1)).AssertStatus(s.T(), http.StatusCreated)

	// Out of scope: sibling subtree B.
	scopedA.POST(s.T(), "/entity-obligations", eoBody(entB)).AssertStatus(s.T(), http.StatusForbidden)

	// Tenant-level content (obligation types) needs a tenant-wide grant.
	scopedA.POST(s.T(), "/obligation-types",
		map[string]any{"name": "CIT", "code": "CIT-1", "template": "CIT"}).
		AssertStatus(s.T(), http.StatusForbidden)

	// Creating an entity: allowed under A, denied under B.
	scopedA.POST(s.T(), "/entities",
		map[string]any{"name": "Alpha-Sub2", "country": "Germany", "parentEntityId": entA}).
		AssertStatus(s.T(), http.StatusCreated)
	scopedA.POST(s.T(), "/entities",
		map[string]any{"name": "Beta-Sub", "country": "Germany", "parentEntityId": entB}).
		AssertStatus(s.T(), http.StatusForbidden)

	// A tenant-wide manager can act on B.
	s.AsRole(tenant, "manager").POST(s.T(), "/entity-obligations", eoBody(entB)).
		AssertStatus(s.T(), http.StatusCreated)
}
