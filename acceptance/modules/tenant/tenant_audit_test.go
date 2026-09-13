package tenant_test

import (
	"encoding/json"
	"net/http"

	"github.com/mohamadhallal/zentax-api/platform/audit"
)

// The account timezone is the reference every stored instant is presented in
// and every reminder is scheduled against (ADR-0003), and no tenant history
// table exists — so the audit entry is the ONLY record of what the zone was
// before. Recorded with the new value alone, a single entry says "the zone was
// set" and an auditor reading it cannot tell whether the account moved by an
// hour or by twelve; every deadline day in every report is reinterpreted
// either way.
//
// This walks three writes against the stored envelope: a zone move with the
// name left alone, a rename with the zone left alone, and a rewrite that moves
// nothing — then re-verifies the chain over the richer payloads.

// trail reads one tenant's whole chain from the rows, oldest first. audit_log
// is RLS'd, so the read binds the tenant GUC on its own transaction.
func (s *TenantSuite) trail(tenantID string) []audit.Entry {
	// audit.ChainColumns, not a copy of it: a verifier that reads a column list
	// of its own drifts behind the envelope and reports sound rows as tampering.
	const columns = audit.ChainColumns
	var entries []audit.Entry
	tx, err := s.DB.Beginx()
	s.Require().NoError(err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.Exec(`SELECT set_config('app.tenant_id', $1, true)`, tenantID)
	s.Require().NoError(err)
	s.Require().NoError(tx.Select(&entries, `SELECT `+columns+` FROM audit_log ORDER BY seq ASC`))
	return entries
}

// fieldsOf decodes an entry's change set: field name -> {"from":…, "to":…}.
func (s *TenantSuite) fieldsOf(e audit.Entry) map[string]map[string]any {
	var payload struct {
		Fields map[string]map[string]any `json:"fields"`
	}
	s.Require().NoError(json.Unmarshal(e.Details, &payload), "details must decode: %s", string(e.Details))
	return payload.Fields
}

func (s *TenantSuite) TestTimezoneChangeRecordsBothSides() {
	tenant := s.InsertTenant("tz-d", "Zone Tenant D").String()
	admin := s.As(tenant)

	// 1. The zone moves twelve hours; the name is written back unchanged.
	admin.PUT(s.T(), "/tenant", map[string]any{"name": "Zone Tenant D", "timezone": "Pacific/Kiritimati"}).
		AssertStatus(s.T(), http.StatusOK)
	// 2. …and back across the date line. This is the entry the control needs:
	//    read alone, it must still say where the account came FROM.
	admin.PUT(s.T(), "/tenant", map[string]any{"name": "Zone Tenant D", "timezone": "Pacific/Pago_Pago"}).
		AssertStatus(s.T(), http.StatusOK)
	// 3. A rename with the zone left alone.
	admin.PUT(s.T(), "/tenant", map[string]any{"name": "Zone Tenant D Ltd", "timezone": "Pacific/Pago_Pago"}).
		AssertStatus(s.T(), http.StatusOK)
	// 4. A rewrite with the same values — nothing moved.
	admin.PUT(s.T(), "/tenant", map[string]any{"name": "Zone Tenant D Ltd", "timezone": "Pacific/Pago_Pago"}).
		AssertStatus(s.T(), http.StatusOK)

	entries := s.trail(tenant)
	var updates []audit.Entry
	for _, e := range entries {
		if e.Action == "tenant.updated" {
			s.Require().Equal("tenant", e.ResourceType)
			s.Require().Equal(tenant, e.ResourceID)
			s.Require().NotEmpty(e.ActorID)
			updates = append(updates, e)
		}
	}
	s.Require().Len(updates, 4)

	// 1 + 2: both sides of the zone, and the unchanged name left out — the
	// key set is the change set, not a row dump.
	first := s.fieldsOf(updates[0])
	s.Require().Equal(map[string]any{"from": "UTC", "to": "Pacific/Kiritimati"}, first["timezone"])
	s.Require().NotContains(first, "name")

	second := s.fieldsOf(updates[1])
	s.Require().Equal(map[string]any{"from": "Pacific/Kiritimati", "to": "Pacific/Pago_Pago"}, second["timezone"])

	// 3: the rename is dated, the names withheld (free text, ADR-0007/0008).
	third := s.fieldsOf(updates[2])
	s.Require().Equal(map[string]any{"from": "set", "to": "set"}, third["name"])
	s.Require().NotContains(third, "timezone")

	// 4: nothing moved, so nothing is claimed.
	s.Require().Equal("{}", string(updates[3].Details))

	// No name ever reaches the envelope, on any entry.
	for _, e := range entries {
		for _, secret := range []string{"Zone Tenant D", "Zone Tenant D Ltd"} {
			s.Require().NotContains(string(e.Details), secret, "seq %d leaked the tenant name", e.Seq)
		}
	}

	// The chain still recomputes from the stored rows across the new payloads.
	s.Require().NoError(audit.VerifyChain(entries))

	// The zone on the row is the one the last entry claims.
	var zone string
	s.Require().NoError(s.DB.Get(&zone, `SELECT timezone FROM tenants WHERE id = $1`, tenant))
	s.Require().Equal("Pacific/Pago_Pago", zone)
}

// A refused update writes no entry at all: the zone is validated before the
// before-state is read, and an invalid one must not leave a trace of a change
// that never happened.
func (s *TenantSuite) TestRefusedUpdateRecordsNothing() {
	tenant := s.InsertTenant("tz-e", "Zone Tenant E").String()
	s.As(tenant).PUT(s.T(), "/tenant", map[string]any{"name": "Zone Tenant E", "timezone": "Mars/Olympus"}).
		AssertStatus(s.T(), http.StatusBadRequest)

	var n int
	tx, err := s.DB.Beginx()
	s.Require().NoError(err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.Exec(`SELECT set_config('app.tenant_id', $1, true)`, tenant)
	s.Require().NoError(err)
	s.Require().NoError(tx.Get(&n, `SELECT COUNT(*)::int FROM audit_log`))
	s.Require().Zero(n)
}
