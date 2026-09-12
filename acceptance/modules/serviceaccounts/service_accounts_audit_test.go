package serviceaccounts_test

import (
	"encoding/json"
	"net/http"

	"github.com/mohamadhallal/zentax-api/platform/audit"
)

// A service account reaches exactly the data a member reaches, and its API
// tokens are the standing route into a tenant — but provisioning one, issuing
// its credential and revoking that credential used to write nothing at all,
// while granting the SAME scoped role to a PERSON next door wrote an entry.
// An auditor asked "who issued the token that read the tenant last month" had
// api_tokens.created_by, mutable operational state outside the hash chain and
// invisible in the Audit Trail — and for the account itself, nothing whatever:
// users carries no created_by and user_grants is hard-deleted on change.
//
// This walks the three verbs against the stored envelope, and holds the line
// on what must NOT be there: the account's name, the token's label, the
// cleartext credential and its hash.

// trail reads one tenant's whole chain from the rows, oldest first. audit_log
// is RLS'd, so the read binds the tenant GUC on its own transaction.
func (s *ServiceAccountsSuite) trail(tenantID string) []audit.Entry {
	const columns = `event_id, tenant_id, seq, actor_id, action, resource_type, resource_id,
	                 occurred_at, request_id, details, prev_hash, hash`
	var entries []audit.Entry
	tx, err := s.DB.Beginx()
	s.Require().NoError(err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.Exec(`SELECT set_config('app.tenant_id', $1, true)`, tenantID)
	s.Require().NoError(err)
	s.Require().NoError(tx.Select(&entries, `SELECT `+columns+` FROM audit_log ORDER BY seq ASC`))
	return entries
}

// entryFor returns the single entry for an action on a resource.
func (s *ServiceAccountsSuite) entryFor(entries []audit.Entry, action, resourceID string) audit.Entry {
	var found []audit.Entry
	for _, e := range entries {
		if e.Action == action && e.ResourceID == resourceID {
			found = append(found, e)
		}
	}
	s.Require().Len(found, 1, "expected exactly one %s for %s", action, resourceID)
	return found[0]
}

// payloadOf decodes an entry's envelope: the change set under "fields", plus
// whatever sits beside it.
func (s *ServiceAccountsSuite) payloadOf(e audit.Entry) (map[string]map[string]any, map[string]any) {
	var whole map[string]any
	s.Require().NoError(json.Unmarshal(e.Details, &whole), "details must decode: %s", string(e.Details))
	var payload struct {
		Fields map[string]map[string]any `json:"fields"`
	}
	s.Require().NoError(json.Unmarshal(e.Details, &payload))
	s.Require().NotEmpty(payload.Fields, "%s recorded no fields: %s", e.Action, string(e.Details))
	return payload.Fields, whole
}

func (s *ServiceAccountsSuite) TestCredentialLifecycleIsRecorded() {
	tenant := s.InsertTenant("sa-aud", "SA Audit Tenant").String()
	admin := s.As(tenant)

	// A scope to grant over: the machine gets a reviewer role bounded to one
	// entity — the same grant whose HUMAN twin records member.role_changed.
	var entity struct {
		ID string `json:"id"`
	}
	r := admin.POST(s.T(), "/entities", map[string]any{
		"name": "Audited GmbH", "country": "Germany",
		"financialYearEnd": "12-31", "fiscalCalendarPattern": "standard",
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &entity)

	var sa struct {
		ID string `json:"id"`
	}
	r = admin.POST(s.T(), "/service-accounts", map[string]any{
		"name": "Filing Agent Pty", "role": "reviewer", "scopeEntityId": entity.ID,
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &sa)

	tok := s.issueToken(tenant, sa.ID)

	// The credential really does reach the tenant's data — the reason the
	// entries above have to exist.
	s.Client.External().WithBearer(tok.Token).GET(s.T(), "/entities").
		AssertStatus(s.T(), http.StatusOK)

	admin.POST(s.T(), "/tokens/"+tok.ID+"/revoke", nil).AssertStatus(s.T(), http.StatusNoContent)

	entries := s.trail(tenant)

	// ---- the account ----
	created := s.entryFor(entries, "service_account.created", sa.ID)
	s.Require().Equal("service_account", created.ResourceType)
	s.Require().NotEmpty(created.ActorID)
	s.Require().NotEmpty(created.RequestID)
	fields, _ := s.payloadOf(created)
	s.Require().Equal(map[string]any{"to": "service"}, fields["kind"])
	s.Require().Equal(map[string]any{"to": "active"}, fields["status"])
	// The capability set, in the member envelope's shape: role + scope entity
	// id, never the scope entity's name.
	s.Require().Equal(map[string]any{"to": []any{
		map[string]any{"role": "reviewer", "scopeEntityId": entity.ID},
	}}, fields["grants"])
	s.Require().NotContains(fields, "name")

	// The actor is the human tenant admin who provisioned it, not the machine.
	s.Require().NotEqual(sa.ID, created.ActorID)

	// ---- the credential ----
	issued := s.entryFor(entries, "api_token.issued", tok.ID)
	s.Require().Equal("api_token", issued.ResourceType)
	s.Require().Equal(created.ActorID, issued.ActorID)
	fields, whole := s.payloadOf(issued)
	s.Require().Equal(map[string]any{"to": sa.ID}, fields["serviceAccountId"])
	s.Require().Equal(map[string]any{"to": float64(30)}, fields["ttlDays"])
	s.Require().Contains(fields, "expiresAt")
	// The scheme prefix rides beside the change set — the credential class,
	// never a fragment of the secret.
	s.Require().Equal("ztx_", whole["tokenPrefix"])
	s.Require().NotContains(fields, "label")

	revoked := s.entryFor(entries, "api_token.revoked", tok.ID)
	s.Require().Equal("api_token", revoked.ResourceType)
	fields, whole = s.payloadOf(revoked)
	s.Require().Equal(map[string]any{"from": false, "to": true}, fields["revoked"])
	s.Require().Equal("ztx_", whole["tokenPrefix"])

	// Order: the account, then its credential, then the revocation.
	s.Require().Less(created.Seq, issued.Seq)
	s.Require().Less(issued.Seq, revoked.Seq)

	// ---- what must never be in the ledger ----
	var tokenHash string
	s.Require().NoError(s.DB.Get(&tokenHash, `SELECT token_hash FROM api_tokens WHERE id = $1`, tok.ID))
	for _, e := range entries {
		for _, secret := range []string{"Filing Agent Pty", "test-token", tok.Token, tokenHash, "@service.zentax.internal"} {
			s.Require().NotContains(string(e.Details), secret,
				"seq %d (%s) leaked a withheld value", e.Seq, e.Action)
		}
	}

	// The chain still recomputes from the stored rows with the new entries in
	// it — including the ones the credential's own reads sit between.
	s.Require().NoError(audit.VerifyChain(entries))

	// And an auditor reads them the way an auditor actually reads them: off
	// GET /audit-log, not out of psql. (The `resourceType` FILTER predates
	// machine identity and still accepts only the six workflow-chain types, so
	// the read is by action — the rows themselves carry the new types.)
	var read []struct {
		Action       string         `json:"action"`
		ResourceType string         `json:"resourceType"`
		ResourceID   string         `json:"resourceId"`
		ActorID      string         `json:"actorId"`
		Details      map[string]any `json:"details"`
	}
	admin.GET(s.T(), "/audit-log?action=api_token.issued&action=api_token.revoked").DecodeData(s.T(), &read)
	s.Require().Len(read, 2)
	for _, e := range read {
		s.Require().Equal("api_token", e.ResourceType)
		s.Require().Equal(tok.ID, e.ResourceID)
		s.Require().Equal(created.ActorID, e.ActorID)
		s.Require().Equal("ztx_", e.Details["tokenPrefix"])
	}
	admin.GET(s.T(), "/audit-log?action=service_account.created").DecodeData(s.T(), &read)
	s.Require().Len(read, 1)
	s.Require().Equal("service_account", read[0].ResourceType)
	s.Require().Equal(sa.ID, read[0].ResourceID)
}

// A refused write leaves no trace of a credential that never existed: a second
// revoke of the same token, a revoke from another tenant, and a role the
// matrix does not grant a machine.
func (s *ServiceAccountsSuite) TestRefusedCredentialWritesRecordNothing() {
	tenant := s.InsertTenant("sa-aud-b", "SA Audit Tenant B").String()
	other := s.InsertTenant("sa-aud-c", "SA Audit Tenant C").String()
	saID := s.createSA(tenant, "reporting-agent", "preparer")
	tok := s.issueToken(tenant, saID)

	s.As(tenant).POST(s.T(), "/tokens/"+tok.ID+"/revoke", nil).AssertStatus(s.T(), http.StatusNoContent)
	s.As(tenant).POST(s.T(), "/tokens/"+tok.ID+"/revoke", nil).AssertStatus(s.T(), http.StatusNotFound)
	s.As(other).POST(s.T(), "/tokens/"+tok.ID+"/revoke", nil).AssertStatus(s.T(), http.StatusNotFound)
	s.As(tenant).POST(s.T(), "/service-accounts",
		map[string]any{"name": "would-be-admin", "role": "tenant_admin"}).
		AssertStatus(s.T(), http.StatusBadRequest)

	entries := s.trail(tenant)
	var actions []string
	for _, e := range entries {
		actions = append(actions, e.Action)
	}
	s.Require().Equal([]string{"service_account.created", "api_token.issued", "api_token.revoked"}, actions)

	// The other tenant's chain never saw the cross-tenant attempt.
	s.Require().Empty(s.trail(other))
	s.Require().NoError(audit.VerifyChain(entries))
}
