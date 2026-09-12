package workflows_test

import (
	"encoding/json"
	"net/http"

	"github.com/jmoiron/sqlx"

	"github.com/mohamadhallal/zentax-api/acceptance"
)

// chain is one entity → obligation type → recurring workflow → approval-required
// task template → started, with the generated instance ids.
type chain struct {
	EntityID         string
	ObligationTypeID string
	WorkflowID       string
	WorkflowTaskID   string
	Instances        []string
}

// seedChain builds the whole workflow chain over HTTP, as the API's own users
// would, and starts it so real task instances exist.
func (s *WorkflowsSuite) seedChain(tenant, name string, periods []string) chain {
	post := func(path string, body any) *acceptance.TestResponse {
		return s.As(tenant).POST(s.T(), path, body)
	}
	var entity, obType, wf, wt struct {
		ID string `json:"id"`
	}
	r := post("/entities", map[string]any{
		"name": name, "country": "Germany", "financialYearEnd": "12-31", "fiscalCalendarPattern": "standard",
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &entity)

	r = post("/obligation-types", map[string]any{"name": "VAT " + name, "code": "VAT-" + name, "template": "VAT"})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &obType)

	r = post("/workflows", map[string]any{
		"name": name + " VAT", "workflowCategory": "recurring",
		"entityId": entity.ID, "obligationTypeId": obType.ID,
		"periodicity": "monthly", "financialYear": "2025", "selectedPeriods": periods,
		"dueDateRule": map[string]any{
			"reference": "period_end", "offsetUnit": "days", "offsetValue": 15, "offsetDirection": "after",
		},
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &wf)

	r = post("/workflow-tasks", map[string]any{
		"workflowId": wf.ID, "name": "Prepare", "taskType": "preparation", "approvalRequired": true,
		"dueDateReference": "filing_deadline", "dueDateOffsetValue": 5,
		"dueDateOffsetUnit": "days", "dueDateOffsetDirection": "before",
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &wt)

	s.As(tenant).POST(s.T(), "/workflows/"+wf.ID+"/start", nil).AssertStatus(s.T(), http.StatusCreated)

	var list []struct {
		ID string `json:"id"`
	}
	s.As(tenant).GET(s.T(), "/task-instances?workflowId="+wf.ID).DecodeData(s.T(), &list)
	out := chain{
		EntityID: entity.ID, ObligationTypeID: obType.ID,
		WorkflowID: wf.ID, WorkflowTaskID: wt.ID,
	}
	for _, ti := range list {
		out.Instances = append(out.Instances, ti.ID)
	}
	s.Require().NotEmpty(out.Instances)
	return out
}

// approve drives a real submit → approve through two different actors, so the
// instance carries approved_at exactly as production would (ADR-0012 SoD).
func (s *WorkflowsSuite) approve(tenant, instanceID string) {
	s.AsRole(tenant, "preparer").POST(s.T(), "/task-instances/"+instanceID+"/submit-for-approval", nil).
		AssertStatus(s.T(), http.StatusOK)
	s.AsRole(tenant, "reviewer").POST(s.T(), "/task-instances/"+instanceID+"/approve", nil).
		AssertStatus(s.T(), http.StatusOK)
}

// inTenant runs fn on a tx with the tenant GUC bound — every domain table is
// RLS'd, so a read outside a tenant context sees nothing.
func (s *WorkflowsSuite) inTenant(tenantID string, fn func(tx *sqlx.Tx)) {
	tx, err := s.DB.Beginx()
	s.Require().NoError(err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.Exec(`SELECT set_config('app.tenant_id', $1, true)`, tenantID)
	s.Require().NoError(err)
	fn(tx)
}

// auditDetails returns the details payload of the newest audit entry for an
// action.
func (s *WorkflowsSuite) auditDetails(tenantID, action string) map[string]any {
	var raw []byte
	s.inTenant(tenantID, func(tx *sqlx.Tx) {
		s.Require().NoError(tx.Get(&raw,
			`SELECT details FROM audit_log WHERE action = $1 ORDER BY seq DESC LIMIT 1`, action))
	})
	out := map[string]any{}
	s.Require().NoError(json.Unmarshal(raw, &out))
	return out
}

// TestDeleteRefusedWhenApprovedWorkExists is the headline regression: a
// workflow delete used to destroy approved filings through the FK cascade
// (108 instances, 24 approved, in the audit that found this). ADR-0018 makes an
// approved instance attested evidence, so the delete is now refused, the error
// names what is in the way and how many, and offers the archive that already
// exists in the domain — and every row is still there afterwards.
func (s *WorkflowsSuite) TestDeleteRefusedWhenApprovedWorkExists() {
	tenant := s.InsertTenant("wf-del-a", "WF Delete A").String()
	c := s.seedChain(tenant, "Acme", []string{"M1", "M2", "M3"})
	s.Require().Len(c.Instances, 3)

	s.approve(tenant, c.Instances[0])

	resp := s.As(tenant).DELETE(s.T(), "/workflows/"+c.WorkflowID)
	resp.AssertStatus(s.T(), http.StatusConflict)
	resp.AssertErrorCode(s.T(), "CONFLICT")
	body := resp.BodyString()
	s.Require().Contains(body, "1 approved task instance(s)")
	s.Require().Contains(body, "archive")

	// Nothing was destroyed: the workflow, its template and all three instances
	// are still readable through the API.
	s.As(tenant).GET(s.T(), "/workflows/"+c.WorkflowID).AssertStatus(s.T(), http.StatusOK)
	for _, id := range c.Instances {
		s.As(tenant).GET(s.T(), "/task-instances/"+id).AssertStatus(s.T(), http.StatusOK)
	}

	// And the refusal produced no audit entry claiming a delete happened.
	s.inTenant(tenant, func(tx *sqlx.Tx) {
		var n int
		s.Require().NoError(tx.Get(&n,
			`SELECT COUNT(*)::int FROM audit_log WHERE action = 'workflow.deleted'`))
		s.Require().Zero(n, "a refused delete must not be recorded as one")
	})

	// The archive the error points at works, and keeps the record.
	s.As(tenant).PUT(s.T(), "/workflows/"+c.WorkflowID, map[string]any{
		"name": "Acme VAT", "workflowCategory": "recurring", "status": "archived",
		"entityId": c.EntityID, "obligationTypeId": c.ObligationTypeID,
		"periodicity": "monthly", "financialYear": "2025",
	}).AssertStatus(s.T(), http.StatusOK)
	s.As(tenant).GET(s.T(), "/task-instances/"+c.Instances[0]).AssertStatus(s.T(), http.StatusOK)
}

// TestDeleteSucceedsWithoutApprovedWorkAndAuditsTheCascade: with nothing
// attested below it the delete goes through, and the audit envelope now says
// what it took — counted by kind — instead of recording an empty {}.
func (s *WorkflowsSuite) TestDeleteSucceedsWithoutApprovedWorkAndAuditsTheCascade() {
	tenant := s.InsertTenant("wf-del-b", "WF Delete B").String()
	c := s.seedChain(tenant, "Beta", []string{"M1", "M2"})
	s.Require().Len(c.Instances, 2)

	s.As(tenant).DELETE(s.T(), "/workflows/"+c.WorkflowID).AssertStatus(s.T(), http.StatusNoContent)

	s.As(tenant).GET(s.T(), "/workflows/"+c.WorkflowID).AssertStatus(s.T(), http.StatusNotFound)
	for _, id := range c.Instances {
		s.As(tenant).GET(s.T(), "/task-instances/"+id).AssertStatus(s.T(), http.StatusNotFound)
	}

	details := s.auditDetails(tenant, "workflow.deleted")
	s.Require().Equal(float64(2), details["taskInstances"])
	s.Require().Equal(float64(1), details["workflowTasks"])
	s.Require().Equal(float64(0), details["documents"])
	s.Require().Equal(float64(0), details["documentVersions"])
	s.Require().Equal(float64(0), details["blobsQueuedForReclaim"])
}

// TestDeleteQueuesDocumentBlobsForThePurgeJob: the metadata rows cascade away,
// so the delete first hands the purge job the storage keys — otherwise the
// objects sit in the bucket forever with nothing referencing them (exactly what
// the audit found: 4 documents and 5 versions orphaned by one entity delete).
func (s *WorkflowsSuite) TestDeleteQueuesDocumentBlobsForThePurgeJob() {
	tenant := s.InsertTenant("wf-del-c", "WF Delete C").String()
	c := s.seedChain(tenant, "Gamma", []string{"M1"})

	pdf := []byte("%PDF-1.4\n1 0 obj << /Type /Catalog >> endobj\n%%EOF\n")
	var doc struct {
		ID string `json:"id"`
	}
	r := s.As(tenant).POSTMultipart(s.T(), "/workflows/"+c.WorkflowID+"/documents",
		map[string]string{"documentType": "workings", "taskInstanceId": c.Instances[0]},
		acceptance.MultipartFile{FileName: "workings.pdf", ContentType: "application/pdf", Content: pdf})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &doc)

	// A second version, so there are two distinct blobs behind one document.
	s.As(tenant).POSTMultipart(s.T(), "/documents/"+doc.ID+"/versions", nil,
		acceptance.MultipartFile{
			FileName: "workings-v2.pdf", ContentType: "application/pdf",
			Content: append(pdf, []byte("% v2\n")...),
		}).AssertStatus(s.T(), http.StatusCreated)

	var keysBefore []string
	s.inTenant(tenant, func(tx *sqlx.Tx) {
		s.Require().NoError(tx.Select(&keysBefore,
			`SELECT storage_key FROM document_versions ORDER BY storage_key`))
	})
	s.Require().Len(keysBefore, 2)

	s.As(tenant).DELETE(s.T(), "/workflows/"+c.WorkflowID).AssertStatus(s.T(), http.StatusNoContent)

	// The metadata is gone...
	s.As(tenant).GET(s.T(), "/documents/"+doc.ID).AssertStatus(s.T(), http.StatusNotFound)
	s.inTenant(tenant, func(tx *sqlx.Tx) {
		var n int
		s.Require().NoError(tx.Get(&n, `SELECT COUNT(*)::int FROM document_versions`))
		s.Require().Zero(n)
	})

	// ...but every key it named is now the purge job's to reclaim, tagged with
	// the delete that queued it.
	var queued []struct {
		StorageKey   string  `db:"storage_key"`
		Reason       string  `db:"reason"`
		ResourceType string  `db:"resource_type"`
		ResourceID   string  `db:"resource_id"`
		ReclaimedAt  *string `db:"reclaimed_at"`
	}
	s.inTenant(tenant, func(tx *sqlx.Tx) {
		s.Require().NoError(tx.Select(&queued,
			`SELECT storage_key, reason, resource_type, resource_id, reclaimed_at
			 FROM storage_reclaim ORDER BY storage_key`))
	})
	s.Require().Len(queued, 2)
	for i, q := range queued {
		s.Require().Equal(keysBefore[i], q.StorageKey)
		s.Require().Equal("workflow.deleted", q.Reason)
		s.Require().Equal("workflow", q.ResourceType)
		s.Require().Equal(c.WorkflowID, q.ResourceID)
		s.Require().Nil(q.ReclaimedAt, "queued, not yet reclaimed — the purge job does that")
	}

	details := s.auditDetails(tenant, "workflow.deleted")
	s.Require().Equal(float64(1), details["documents"])
	s.Require().Equal(float64(2), details["documentVersions"])
	s.Require().Equal(float64(2), details["blobsQueuedForReclaim"])
}

// TestDatabaseRefusesTheDeleteEvenWithoutTheUseCase: the use-case check gives
// the good message, the trigger makes it true. A raw DELETE — a psql session, a
// future endpoint that forgets — is refused by the database itself (ADR-0018).
func (s *WorkflowsSuite) TestDatabaseRefusesTheDeleteEvenWithoutTheUseCase() {
	tenant := s.InsertTenant("wf-del-d", "WF Delete D").String()
	c := s.seedChain(tenant, "Delta", []string{"M1"})
	s.approve(tenant, c.Instances[0])

	// One transaction each: a refused statement aborts its transaction, so the
	// two probes cannot share one.
	s.inTenant(tenant, func(tx *sqlx.Tx) {
		_, err := tx.Exec(`DELETE FROM workflows WHERE id = $1`, c.WorkflowID)
		s.Require().Error(err, "the database must refuse this on its own")
		s.Require().Contains(err.Error(), "approved task instance")
	})

	// Deleting the template row directly — "remove a step" — is refused too.
	s.inTenant(tenant, func(tx *sqlx.Tx) {
		_, err := tx.Exec(`DELETE FROM workflow_tasks WHERE id = $1`, c.WorkflowTaskID)
		s.Require().Error(err)
		s.Require().Contains(err.Error(), "approved task instance")
	})
}
