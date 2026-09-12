package audit_test

import (
	"net/http"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/suite"

	"github.com/mohamadhallal/zentax-api/acceptance"
	"github.com/mohamadhallal/zentax-api/platform/audit"
	"github.com/mohamadhallal/zentax-api/platform/database"
)

// sqlstateInsufficientPrivilege is Postgres' insufficient_privilege. A
// prod-shaped database refuses a write to audit_log with this before RLS is
// ever consulted, because deployment/docker/migrate.sh revokes UPDATE and
// DELETE on audit_log from the app role.
const sqlstateInsufficientPrivilege = "42501"

// AuditSuite proves the ADR-0008 application audit trail end to end: every
// domain mutation appends a PII-free, actor-attributed entry; the per-tenant
// hash chain verifies; the table is append-only even for the app role; and the
// trail is RLS-isolated per tenant.
type AuditSuite struct {
	acceptance.Suite
}

func TestAuditSuite(t *testing.T) {
	suite.Run(t, new(AuditSuite))
}

// inTenant runs fn on a tx with the tenant GUC bound (audit_log is RLS'd, so
// reads outside a tenant context see nothing).
func (s *AuditSuite) inTenant(tenantID string, fn func(tx *sqlx.Tx)) {
	tx, err := s.DB.Beginx()
	s.Require().NoError(err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.Exec(`SELECT set_config('app.tenant_id', $1, true)`, tenantID)
	s.Require().NoError(err)
	fn(tx)
}

func (s *AuditSuite) TestTrailChainAttributionAndAppendOnly() {
	tenant := s.InsertTenant("aud-a", "Audit A").String()
	other := s.InsertTenant("aud-b", "Audit B").String()

	// Drive a full flow over HTTP: admin sets up, a preparer submits, the
	// admin approves (SoD: different actors).
	var entity, obType, wf struct {
		ID string `json:"id"`
	}
	r := s.As(tenant).POST(s.T(), "/entities", map[string]any{
		"name": "Acme", "country": "Germany", "financialYearEnd": "12-31", "fiscalCalendarPattern": "standard",
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &entity)

	r = s.As(tenant).POST(s.T(), "/obligation-types", map[string]any{"name": "VAT", "code": "VAT-RET", "template": "VAT"})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &obType)

	r = s.As(tenant).POST(s.T(), "/workflows", map[string]any{
		"name": "Monthly VAT", "workflowCategory": "recurring",
		"entityId": entity.ID, "obligationTypeId": obType.ID,
		"periodicity": "monthly", "financialYear": "2025", "selectedPeriods": []string{"M1"},
		"dueDateRule": map[string]any{"reference": "period_end", "offsetUnit": "days", "offsetValue": 15, "offsetDirection": "after"},
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &wf)

	s.As(tenant).POST(s.T(), "/workflow-tasks", map[string]any{
		"workflowId": wf.ID, "name": "Prepare", "taskType": "preparation", "approvalRequired": true,
		"dueDateReference": "filing_deadline", "dueDateOffsetValue": 5, "dueDateOffsetUnit": "days", "dueDateOffsetDirection": "before",
	}).AssertStatus(s.T(), http.StatusCreated)

	s.As(tenant).POST(s.T(), "/workflows/"+wf.ID+"/start", nil).AssertStatus(s.T(), http.StatusCreated)

	var list []struct {
		ID string `json:"id"`
	}
	s.As(tenant).GET(s.T(), "/task-instances").DecodeData(s.T(), &list)
	s.Require().Len(list, 1)
	tiID := list[0].ID

	s.AsRole(tenant, "preparer").POST(s.T(), "/task-instances/"+tiID+"/submit-for-approval", nil).
		AssertStatus(s.T(), http.StatusOK)
	s.As(tenant).POST(s.T(), "/task-instances/"+tiID+"/approve", nil).
		AssertStatus(s.T(), http.StatusOK)

	// ---- Verify the trail. ----
	const cols = `event_id, tenant_id, seq, actor_id, action, resource_type, resource_id,
	              occurred_at, request_id, details, prev_hash, hash`
	var entries []audit.Entry
	s.inTenant(tenant, func(tx *sqlx.Tx) {
		s.Require().NoError(tx.Select(&entries,
			`SELECT `+cols+` FROM audit_log ORDER BY seq ASC`))
	})

	wantActions := []string{
		"entity.created", "obligation_type.created", "workflow.created",
		"workflow_task.created", "workflow.started",
		"task_instance.submitted", "task_instance.approved",
	}
	s.Require().Len(entries, len(wantActions))
	for i, want := range wantActions {
		s.Require().Equal(want, entries[i].Action, "seq %d", i+1)
		s.Require().NotEmpty(entries[i].ActorID)
		s.Require().NotEmpty(entries[i].RequestID)
	}

	// The hash chain recomputes end-to-end.
	s.Require().NoError(audit.VerifyChain(entries))

	// Attribution: SoD is visible in the trail — the submitter and approver
	// are different actors; setup actions share the admin actor.
	s.Require().Equal(entries[0].ActorID, entries[6].ActorID, "setup + approve = admin")
	s.Require().NotEqual(entries[5].ActorID, entries[6].ActorID, "submitter must differ from approver")

	// created_by on the domain row matches the trail's actor (GUC attribution).
	s.inTenant(tenant, func(tx *sqlx.Tx) {
		var createdBy string
		s.Require().NoError(tx.Get(&createdBy, `SELECT created_by FROM entities WHERE id = $1`, entity.ID))
		s.Require().Equal(entries[0].ActorID, createdBy)
	})

	// ---- Append-only: the app cannot rewrite history. ----
	// Two independent defences cover this and a prod-shaped database has both:
	// the app role holds no UPDATE/DELETE privilege on audit_log (migrate.sh
	// revokes them, so the statement is refused with 42501 before RLS is
	// consulted), and audit_log carries no policy FOR UPDATE or FOR DELETE (so
	// a role that did hold the privilege would still match zero rows). Either
	// refusal satisfies the guarantee; a statement that succeeds does not.
	// Each statement gets its own transaction, since a privilege error aborts
	// the transaction it was issued on.
	refuse := func(stmt string) {
		s.inTenant(tenant, func(tx *sqlx.Tx) {
			res, err := tx.Exec(stmt)
			if err != nil {
				pgErr := database.IsPgError(err)
				s.Require().NotNil(pgErr, "unexpected error for %q: %v", stmt, err)
				s.Require().Equal(sqlstateInsufficientPrivilege, pgErr.Code,
					"audit entries must not be mutable, refused for the wrong reason: %v", err)
				return
			}
			n, _ := res.RowsAffected()
			s.Require().Zero(n, "audit entries must not be mutable: %s", stmt)
		})
	}
	refuse(`UPDATE audit_log SET action = 'tampered' WHERE seq = 1`)
	refuse(`DELETE FROM audit_log WHERE seq = 1`)

	// ...and whichever defence refused it, the entry is provably untouched.
	s.inTenant(tenant, func(tx *sqlx.Tx) {
		var action string
		s.Require().NoError(tx.Get(&action, `SELECT action FROM audit_log WHERE seq = 1`))
		s.Require().Equal(wantActions[0], action)
	})

	// ---- Tenant isolation: the other tenant's view of the trail is empty. ----
	s.inTenant(other, func(tx *sqlx.Tx) {
		var n int
		s.Require().NoError(tx.Get(&n, `SELECT COUNT(*)::int FROM audit_log`))
		s.Require().Zero(n)
	})
}
