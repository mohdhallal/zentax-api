package entities_test

import (
	"encoding/json"
	"net/http"

	"github.com/jmoiron/sqlx"

	"github.com/mohamadhallal/zentax-api/acceptance"
)

// deleteChain is one entity carrying a started recurring workflow, so an entity
// delete has a real cascade underneath it.
type deleteChain struct {
	EntityID   string
	WorkflowID string
	Instances  []string
}

func (s *EntitiesSuite) seedDeleteChain(tenant, name string, periods []string) deleteChain {
	post := func(path string, body any) *acceptance.TestResponse {
		return s.As(tenant).POST(s.T(), path, body)
	}
	var entity, obType, wf struct {
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

	post("/workflow-tasks", map[string]any{
		"workflowId": wf.ID, "name": "Prepare", "taskType": "preparation", "approvalRequired": true,
		"dueDateReference": "filing_deadline", "dueDateOffsetValue": 5,
		"dueDateOffsetUnit": "days", "dueDateOffsetDirection": "before",
	}).AssertStatus(s.T(), http.StatusCreated)

	s.As(tenant).POST(s.T(), "/workflows/"+wf.ID+"/start", nil).AssertStatus(s.T(), http.StatusCreated)

	var list []struct {
		ID string `json:"id"`
	}
	s.As(tenant).GET(s.T(), "/task-instances?workflowId="+wf.ID).DecodeData(s.T(), &list)
	out := deleteChain{EntityID: entity.ID, WorkflowID: wf.ID}
	for _, ti := range list {
		out.Instances = append(out.Instances, ti.ID)
	}
	s.Require().NotEmpty(out.Instances)
	return out
}

func (s *EntitiesSuite) approve(tenant, instanceID string) {
	s.AsRole(tenant, "preparer").POST(s.T(), "/task-instances/"+instanceID+"/submit-for-approval", nil).
		AssertStatus(s.T(), http.StatusOK)
	s.AsRole(tenant, "reviewer").POST(s.T(), "/task-instances/"+instanceID+"/approve", nil).
		AssertStatus(s.T(), http.StatusOK)
}

func (s *EntitiesSuite) inTenant(tenantID string, fn func(tx *sqlx.Tx)) {
	tx, err := s.DB.Beginx()
	s.Require().NoError(err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.Exec(`SELECT set_config('app.tenant_id', $1, true)`, tenantID)
	s.Require().NoError(err)
	fn(tx)
}

func (s *EntitiesSuite) auditDetails(tenantID, action string) map[string]any {
	var raw []byte
	s.inTenant(tenantID, func(tx *sqlx.Tx) {
		s.Require().NoError(tx.Get(&raw,
			`SELECT details FROM audit_log WHERE action = $1 ORDER BY seq DESC LIMIT 1`, action))
	})
	out := map[string]any{}
	s.Require().NoError(json.Unmarshal(raw, &out))
	return out
}

// TestDeleteRefusedWhenApprovedWorkExists: the audit reproduced one entity
// delete removing 4 workflows, 14 approved instances, 4 documents and 5
// document versions. An entity is the top of the cascade, so the refusal has to
// reach all the way down — and it does, through the workflows below it.
func (s *EntitiesSuite) TestDeleteRefusedWhenApprovedWorkExists() {
	tenant := s.InsertTenant("ent-del-a", "Entity Delete A").String()
	c := s.seedDeleteChain(tenant, "Acme", []string{"M1", "M2"})
	s.approve(tenant, c.Instances[0])

	resp := s.As(tenant).DELETE(s.T(), "/entities/"+c.EntityID)
	resp.AssertStatus(s.T(), http.StatusConflict)
	resp.AssertErrorCode(s.T(), "CONFLICT")
	body := resp.BodyString()
	s.Require().Contains(body, "1 approved task instance(s) across 1 workflow(s)")
	s.Require().Contains(body, "archive")

	// The whole subtree survived.
	s.As(tenant).GET(s.T(), "/entities/"+c.EntityID).AssertStatus(s.T(), http.StatusOK)
	s.As(tenant).GET(s.T(), "/workflows/"+c.WorkflowID).AssertStatus(s.T(), http.StatusOK)
	for _, id := range c.Instances {
		s.As(tenant).GET(s.T(), "/task-instances/"+id).AssertStatus(s.T(), http.StatusOK)
	}

	s.inTenant(tenant, func(tx *sqlx.Tx) {
		var n int
		s.Require().NoError(tx.Get(&n,
			`SELECT COUNT(*)::int FROM audit_log WHERE action = 'entity.deleted'`))
		s.Require().Zero(n, "a refused delete must not be recorded as one")
	})

	// The archive the error points at works, and keeps the record.
	s.As(tenant).PUT(s.T(), "/entities/"+c.EntityID, map[string]any{
		"name": "Acme", "country": "Germany", "status": "archived",
	}).AssertStatus(s.T(), http.StatusOK)
	s.As(tenant).GET(s.T(), "/task-instances/"+c.Instances[0]).AssertStatus(s.T(), http.StatusOK)
}

// TestDeleteSucceedsWithoutApprovedWorkAndAuditsTheCascade: nothing attested,
// so the delete goes through — and the envelope now names the whole cascade,
// including the child entity it merely detached and the scoped RBAC grant it
// quietly revoked.
func (s *EntitiesSuite) TestDeleteSucceedsWithoutApprovedWorkAndAuditsTheCascade() {
	tenant := s.InsertTenant("ent-del-b", "Entity Delete B").String()
	c := s.seedDeleteChain(tenant, "Beta", []string{"M1", "M2"})

	// A child entity hanging off the parent: it is unparented, not deleted.
	var child struct {
		ID string `json:"id"`
	}
	r := s.As(tenant).POST(s.T(), "/entities", map[string]any{
		"name": "Beta Sub", "country": "Germany", "parentEntityId": c.EntityID,
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &child)

	s.As(tenant).DELETE(s.T(), "/entities/"+c.EntityID).AssertStatus(s.T(), http.StatusNoContent)

	s.As(tenant).GET(s.T(), "/entities/"+c.EntityID).AssertStatus(s.T(), http.StatusNotFound)
	s.As(tenant).GET(s.T(), "/workflows/"+c.WorkflowID).AssertStatus(s.T(), http.StatusNotFound)
	// The child survived the delete of its parent.
	s.As(tenant).GET(s.T(), "/entities/"+child.ID).AssertStatus(s.T(), http.StatusOK)

	details := s.auditDetails(tenant, "entity.deleted")
	s.Require().Equal(float64(1), details["workflows"])
	s.Require().Equal(float64(1), details["workflowTasks"])
	s.Require().Equal(float64(2), details["taskInstances"])
	s.Require().Equal(float64(0), details["entityObligations"])
	s.Require().Equal(float64(0), details["documents"])
	s.Require().Equal(float64(0), details["documentVersions"])
	s.Require().Equal(float64(1), details["childEntitiesDetached"])
	s.Require().Equal(float64(0), details["blobsQueuedForReclaim"])
}

// TestDeleteQueuesDocumentBlobsForThePurgeJob: an entity delete reaches
// documents two levels down, so its blob keys have to be queued just as a
// workflow delete's are — tagged entity.deleted so the trail leads back.
func (s *EntitiesSuite) TestDeleteQueuesDocumentBlobsForThePurgeJob() {
	tenant := s.InsertTenant("ent-del-c", "Entity Delete C").String()
	c := s.seedDeleteChain(tenant, "Gamma", []string{"M1"})

	pdf := []byte("%PDF-1.4\n1 0 obj << /Type /Catalog >> endobj\n%%EOF\n")
	s.As(tenant).POSTMultipart(s.T(), "/workflows/"+c.WorkflowID+"/documents",
		map[string]string{"documentType": "final_return"},
		acceptance.MultipartFile{FileName: "return.pdf", ContentType: "application/pdf", Content: pdf}).
		AssertStatus(s.T(), http.StatusCreated)

	var keysBefore []string
	s.inTenant(tenant, func(tx *sqlx.Tx) {
		s.Require().NoError(tx.Select(&keysBefore, `SELECT storage_key FROM document_versions`))
	})
	s.Require().Len(keysBefore, 1)

	s.As(tenant).DELETE(s.T(), "/entities/"+c.EntityID).AssertStatus(s.T(), http.StatusNoContent)

	var queued []struct {
		StorageKey   string `db:"storage_key"`
		Reason       string `db:"reason"`
		ResourceType string `db:"resource_type"`
		ResourceID   string `db:"resource_id"`
	}
	s.inTenant(tenant, func(tx *sqlx.Tx) {
		s.Require().NoError(tx.Select(&queued,
			`SELECT storage_key, reason, resource_type, resource_id FROM storage_reclaim`))
	})
	s.Require().Len(queued, 1)
	s.Require().Equal(keysBefore[0], queued[0].StorageKey)
	s.Require().Equal("entity.deleted", queued[0].Reason)
	s.Require().Equal("entity", queued[0].ResourceType)
	s.Require().Equal(c.EntityID, queued[0].ResourceID)

	details := s.auditDetails(tenant, "entity.deleted")
	s.Require().Equal(float64(1), details["documents"])
	s.Require().Equal(float64(1), details["documentVersions"])
	s.Require().Equal(float64(1), details["blobsQueuedForReclaim"])
}

// TestDatabaseRefusesTheDeleteEvenWithoutTheUseCase: an entity has no trigger of
// its own — the guard on workflows fires for each workflow the cascade reaches,
// which is what makes a raw DELETE on the parent safe too (ADR-0018).
func (s *EntitiesSuite) TestDatabaseRefusesTheDeleteEvenWithoutTheUseCase() {
	tenant := s.InsertTenant("ent-del-d", "Entity Delete D").String()
	c := s.seedDeleteChain(tenant, "Delta", []string{"M1"})
	s.approve(tenant, c.Instances[0])

	s.inTenant(tenant, func(tx *sqlx.Tx) {
		_, err := tx.Exec(`DELETE FROM entities WHERE id = $1`, c.EntityID)
		s.Require().Error(err, "the cascade into workflows must be refused by the database")
		s.Require().Contains(err.Error(), "approved task instance")
	})
}

// TestTenantWholesaleRemovalStillWorks: the guard must not get in the way of
// off-boarding. platform/seed.DeleteTenant removes a tenant by deleting the
// tenant-scoped tables CHILDREN FIRST; by the time the parents go, no approved
// instance is left to guard — which is exactly why the triggers sit on the
// parents and not on task_instances.
func (s *EntitiesSuite) TestTenantWholesaleRemovalStillWorks() {
	tenant := s.InsertTenant("ent-del-e", "Entity Delete E").String()
	c := s.seedDeleteChain(tenant, "Epsilon", []string{"M1"})
	s.approve(tenant, c.Instances[0])

	// The off-boarding order, run as the app role under the tenant GUC.
	s.inTenant(tenant, func(tx *sqlx.Tx) {
		for _, table := range []string{
			"document_versions", "documents",
			"task_instances", "workflow_tasks", "workflows",
			"entity_obligations", "obligation_types",
			"entity_closure", "user_grants",
			"entities",
		} {
			_, err := tx.Exec(`DELETE FROM `+table+` WHERE tenant_id = $1`, tenant)
			s.Require().NoError(err, "clearing %s must not trip the attested-delete guard", table)
		}
	})
}
