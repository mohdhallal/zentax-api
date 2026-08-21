package serviceaccounts_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/mohamadhallal/zentax-api/acceptance"
)

// ServiceAccountsSuite proves machine identity (agentic-AI readiness B1) end to
// end: a tenant admin provisions a service account + API token; the token
// authenticates through the same grants → capability → scope → RLS chain as a
// human; approval stays human-only; revocation and expiry kill access.
type ServiceAccountsSuite struct {
	acceptance.Suite
}

func TestServiceAccountsSuite(t *testing.T) {
	suite.Run(t, new(ServiceAccountsSuite))
}

type issued struct {
	ID    string `json:"id"`
	Token string `json:"token"`
}

func (s *ServiceAccountsSuite) createSA(tenant, name, role string) string {
	var sa struct {
		ID string `json:"id"`
	}
	r := s.As(tenant).POST(s.T(), "/service-accounts", map[string]any{"name": name, "role": role})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &sa)
	return sa.ID
}

func (s *ServiceAccountsSuite) issueToken(tenant, saID string) issued {
	var tok issued
	r := s.As(tenant).POST(s.T(), "/service-accounts/"+saID+"/tokens",
		map[string]any{"label": "test-token", "expiresInDays": 30})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &tok)
	s.Require().NotEmpty(tok.Token)
	s.Require().Contains(tok.Token, "ztx_")
	return tok
}

func (s *ServiceAccountsSuite) TestMachineIdentityLifecycle() {
	tenant := s.InsertTenant("sa-a", "SA Tenant A").String()
	other := s.InsertTenant("sa-b", "SA Tenant B").String()

	// Only member:manage can provision machine identities.
	s.AsRole(tenant, "preparer").POST(s.T(), "/service-accounts",
		map[string]any{"name": "rogue", "role": "manager"}).
		AssertStatus(s.T(), http.StatusForbidden)

	saID := s.createSA(tenant, "filing-agent", "manager")
	tok := s.issueToken(tenant, saID)

	// The bearer token authenticates and the machine can do manager-level work.
	var entity struct {
		ID        string  `json:"id"`
		CreatedBy *string `json:"createdBy"`
	}
	r := s.Client.External().WithBearer(tok.Token).
		POST(s.T(), "/entities", map[string]any{"name": "Machine GmbH", "country": "Germany"})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &entity)

	// Attribution: the row is stamped with the service account's user id.
	s.Require().NotNil(entity.CreatedBy)
	s.Require().Equal(saID, *entity.CreatedBy)

	// Tenant isolation holds for machines: the other tenant sees nothing.
	var listOther []map[string]any
	s.As(other).GET(s.T(), "/task-instances").DecodeData(s.T(), &listOther)
	var listOtherEntities []map[string]any
	s.As(other).GET(s.T(), "/entities").DecodeData(s.T(), &listOtherEntities)
	s.Require().Empty(listOtherEntities)

	// Garbage / non-ztx bearer values are rejected (no session fallback with
	// a malformed cookie either).
	s.Client.External().WithBearer("ztx_not-a-real-token").GET(s.T(), "/entities").
		AssertStatus(s.T(), http.StatusUnauthorized)

	// Revocation kills the token immediately.
	s.As(tenant).POST(s.T(), "/tokens/"+tok.ID+"/revoke", nil).
		AssertStatus(s.T(), http.StatusNoContent)
	s.Client.External().WithBearer(tok.Token).GET(s.T(), "/entities").
		AssertStatus(s.T(), http.StatusUnauthorized)

	// A revoked token cannot be revoked twice (tombstone, not delete).
	s.As(tenant).POST(s.T(), "/tokens/"+tok.ID+"/revoke", nil).
		AssertStatus(s.T(), http.StatusNotFound)

	// Cross-tenant revocation is a 404 (no existence leak, no effect).
	tok2 := s.issueToken(tenant, saID)
	s.As(other).POST(s.T(), "/tokens/"+tok2.ID+"/revoke", nil).
		AssertStatus(s.T(), http.StatusNotFound)
	s.Client.External().WithBearer(tok2.Token).GET(s.T(), "/entities").
		AssertStatus(s.T(), http.StatusOK)

	// Expiry: force the token past its expiry directly in the DB → 401.
	_, err := s.DB.Exec(`UPDATE api_tokens SET expires_at = NOW() - INTERVAL '1 minute' WHERE id = $1`, tok2.ID)
	s.Require().NoError(err)
	s.Client.External().WithBearer(tok2.Token).GET(s.T(), "/entities").
		AssertStatus(s.T(), http.StatusUnauthorized)

	// The admin can list the tenant's machine principals.
	var accounts []map[string]any
	s.As(tenant).GET(s.T(), "/service-accounts").DecodeData(s.T(), &accounts)
	s.Require().Len(accounts, 1)
	s.Require().Equal("filing-agent", accounts[0]["name"])
	s.Require().Equal("service", accounts[0]["kind"])
}

// TestApprovalIsHumanOnly: even a manager-role machine — whose ROLE carries
// task:approve — is denied the approval verb (ADR-0012: approval is an
// attestation; machines prepare and submit, humans approve).
func (s *ServiceAccountsSuite) TestApprovalIsHumanOnly() {
	tenant := s.InsertTenant("sa-c", "SA Tenant C").String()

	saID := s.createSA(tenant, "workflow-agent", "manager")
	tok := s.issueToken(tenant, saID)
	machine := func() *acceptance.RequestBuilder { return s.Client.External().WithBearer(tok.Token) }

	// The machine builds the whole compliance program itself…
	var entity, obType, wf struct {
		ID string `json:"id"`
	}
	r := machine().POST(s.T(), "/entities", map[string]any{
		"name": "Agent Corp", "country": "Germany", "financialYearEnd": "12-31", "fiscalCalendarPattern": "standard",
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &entity)

	r = machine().POST(s.T(), "/obligation-types", map[string]any{"name": "VAT", "code": "VAT-M", "template": "VAT"})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &obType)

	r = machine().POST(s.T(), "/workflows", map[string]any{
		"name": "Monthly VAT", "workflowCategory": "recurring",
		"entityId": entity.ID, "obligationTypeId": obType.ID,
		"periodicity": "monthly", "financialYear": "2025", "selectedPeriods": []string{"M1"},
		"dueDateRule": map[string]any{"reference": "period_end", "offsetUnit": "days", "offsetValue": 15, "offsetDirection": "after"},
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &wf)

	machine().POST(s.T(), "/workflow-tasks", map[string]any{
		"workflowId": wf.ID, "name": "Prepare", "taskType": "preparation", "approvalRequired": true,
		"dueDateReference": "filing_deadline", "dueDateOffsetValue": 5, "dueDateOffsetUnit": "days", "dueDateOffsetDirection": "before",
	}).AssertStatus(s.T(), http.StatusCreated)

	machine().POST(s.T(), "/workflows/"+wf.ID+"/start", nil).AssertStatus(s.T(), http.StatusCreated)

	var list []struct {
		ID string `json:"id"`
	}
	machine().GET(s.T(), "/task-instances").DecodeData(s.T(), &list)
	s.Require().Len(list, 1)
	tiID := list[0].ID

	// …and submits for approval (machines may prepare + submit)…
	machine().POST(s.T(), "/task-instances/"+tiID+"/submit-for-approval", nil).
		AssertStatus(s.T(), http.StatusOK)

	// …but can NEVER approve or reject, despite the manager role.
	machine().POST(s.T(), "/task-instances/"+tiID+"/approve", nil).
		AssertStatus(s.T(), http.StatusForbidden)
	machine().POST(s.T(), "/task-instances/"+tiID+"/reject", map[string]any{"reason": "self-review"}).
		AssertStatus(s.T(), http.StatusForbidden)

	// A human reviewer completes the flow — and SoD still holds end to end.
	s.AsRole(tenant, "reviewer").POST(s.T(), "/task-instances/"+tiID+"/approve", nil).
		AssertStatus(s.T(), http.StatusOK)

	// Machines also cannot manage identities (member:manage on a manager role
	// is absent) — no self-replication.
	machine().POST(s.T(), "/service-accounts", map[string]any{"name": "clone", "role": "manager"}).
		AssertStatus(s.T(), http.StatusForbidden)
}
