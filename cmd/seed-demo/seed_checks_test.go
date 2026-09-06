package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mohamadhallal/zentax-api/seed/demo/spec"
	"github.com/mohamadhallal/zentax-api/shared/apiclient"
	"github.com/mohamadhallal/zentax-api/shared/dateonly"
)

// --- who performs the setup writes ---------------------------------------

func TestSetupActorKey(t *testing.T) {
	manager := spec.User{Key: "acme.manager", Role: "manager", Status: spec.StatusActive}
	scoped := "acme.fr"

	t.Run("the tenant's active, tenant-wide manager", func(t *testing.T) {
		tenant := spec.Tenant{
			Admin: spec.User{Key: "acme.admin"},
			Users: []spec.User{{Key: "acme.preparer", Role: "preparer", Status: spec.StatusActive}, manager},
		}
		assert.Equal(t, "acme.manager", setupActorKey(tenant))
	})

	t.Run("the admin when there is no manager", func(t *testing.T) {
		tenant := spec.Tenant{
			Admin: spec.User{Key: "initech.admin"},
			Users: []spec.User{{Key: "initech.preparer", Role: "preparer", Status: spec.StatusActive}},
		}
		assert.Equal(t, "initech.admin", setupActorKey(tenant))
	})

	t.Run("never a disabled or entity-scoped manager", func(t *testing.T) {
		tenant := spec.Tenant{
			Admin: spec.User{Key: "acme.admin"},
			Users: []spec.User{
				{Key: "acme.gone", Role: "manager", Status: spec.StatusDisabled},
				{Key: "acme.fr-manager", Role: "manager", Status: spec.StatusActive, ScopeEntity: &scoped},
			},
		}
		assert.Equal(t, "acme.admin", setupActorKey(tenant))
	})
}

func TestSortedTemplatesLeavesTheDatasetAlone(t *testing.T) {
	templates := []spec.TaskTemplate{
		{Key: "pay", OrderIndex: 4},
		{Key: "collect", OrderIndex: 0},
		{Key: "review", OrderIndex: 2},
	}
	sorted := sortedTemplates(templates)
	assert.Equal(t, []string{"collect", "review", "pay"}, []string{sorted[0].Key, sorted[1].Key, sorted[2].Key})
	assert.Equal(t, "pay", templates[0].Key, "the caller's slice must not be reordered")
}

// --- start is what preview promised --------------------------------------

func previewFixture() workflowPreview {
	payment := "2026-03-10"
	return workflowPreview{
		TotalPeriods: 1, TaskTemplates: 2, TotalTasks: 2,
		Tasks: []previewTask{
			{TemplateID: "wt-collect", PeriodCode: "M1", Name: "Collect data",
				DueDate: "2026-02-02", PeriodEndDate: "2026-01-31", FilingDeadline: "2026-02-16"},
			{TemplateID: "wt-pay", PeriodCode: "M1", Name: "Pay",
				DueDate: "2026-03-10", PeriodEndDate: "2026-01-31", FilingDeadline: "2026-02-16",
				PaymentDeadline: &payment},
		},
	}
}

func instancesFixture() []taskInstanceRow {
	payment := "2026-03-10"
	return []taskInstanceRow{
		{ID: "ti-1", WorkflowTaskID: "wt-collect", PeriodCode: "M1", Status: "not_started",
			DueDate: "2026-02-02", PeriodEndDate: "2026-01-31", FilingDeadline: "2026-02-16"},
		{ID: "ti-2", WorkflowTaskID: "wt-pay", PeriodCode: "M1", Status: "not_started",
			DueDate: "2026-03-10", PeriodEndDate: "2026-01-31", FilingDeadline: "2026-02-16",
			PaymentDeadline: &payment},
	}
}

func TestCheckAgainstPreview(t *testing.T) {
	templateIDs := map[string]string{"collect": "wt-collect", "pay": "wt-pay"}

	t.Run("matching plan", func(t *testing.T) {
		require.NoError(t, checkAgainstPreview(previewFixture(), instancesFixture(), templateIDs))
	})

	t.Run("a moved due date is reported with both dates", func(t *testing.T) {
		instances := instancesFixture()
		instances[0].DueDate = "2026-02-03"
		err := checkAgainstPreview(previewFixture(), instances, templateIDs)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "2026-02-03")
		assert.Contains(t, err.Error(), "2026-02-02")
	})

	t.Run("a payment deadline that vanished is a difference", func(t *testing.T) {
		instances := instancesFixture()
		instances[1].PaymentDeadline = nil
		err := checkAgainstPreview(previewFixture(), instances, templateIDs)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "payment=null")
	})

	t.Run("a missing instance is a count mismatch", func(t *testing.T) {
		err := checkAgainstPreview(previewFixture(), instancesFixture()[:1], templateIDs)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "has 1 task instances but the preview planned 2")
	})
}

// --- the world matches the dataset ---------------------------------------

func TestCheckInstanceStatuses(t *testing.T) {
	workflow := spec.Workflow{
		Key: "acme.w1",
		Instances: []spec.Instance{
			{Period: "M1", Task: "collect", Status: "completed", Via: spec.ViaPut},
		},
	}
	templateKeyByID := map[string]string{"wt-collect": "collect", "wt-pay": "pay"}

	t.Run("listed instances carry their status, unlisted ones stay not_started", func(t *testing.T) {
		instances := []taskInstanceRow{
			{ID: "ti-1", WorkflowTaskID: "wt-collect", PeriodCode: "M1", Status: "completed"},
			{ID: "ti-2", WorkflowTaskID: "wt-pay", PeriodCode: "M1", Status: "not_started"},
		}
		tally := map[string]int{}
		require.NoError(t, checkInstanceStatuses(workflow, instances, templateKeyByID, tally))
		assert.Equal(t, map[string]int{"completed": 1, "not_started": 1}, tally)
	})

	t.Run("a deviation names the workflow, period and task", func(t *testing.T) {
		instances := []taskInstanceRow{
			{ID: "ti-1", WorkflowTaskID: "wt-collect", PeriodCode: "M1", Status: "in_progress"},
		}
		err := checkInstanceStatuses(workflow, instances, templateKeyByID, map[string]int{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "acme.w1 M1|collect")
		assert.Contains(t, err.Error(), `"in_progress"`)
		assert.Contains(t, err.Error(), `"completed"`)
	})

	t.Run("an untouched instance that moved is caught too", func(t *testing.T) {
		instances := []taskInstanceRow{
			{ID: "ti-2", WorkflowTaskID: "wt-pay", PeriodCode: "M1", Status: "blocked"},
		}
		err := checkInstanceStatuses(workflow, instances, templateKeyByID, map[string]int{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not_started")
	})
}

// --- generated document bodies -------------------------------------------

func TestDocumentBodyKinds(t *testing.T) {
	t.Run("pdf is a real PDF and sniffs as one", func(t *testing.T) {
		body, err := documentBody("pdf", "vat.pdf", "VAT return Jan", 1)
		require.NoError(t, err)
		assert.True(t, bytes.HasPrefix(body, []byte("%PDF-")))
		assert.True(t, bytes.HasSuffix(body, []byte("%%EOF\n")))
		assert.Contains(t, string(body), "startxref")
		// The upload rejects a declared type the content contradicts.
		assert.Equal(t, "application/pdf", http.DetectContentType(body))
		assert.Equal(t, "application/pdf", apiclient.ContentTypeFor("vat.pdf", body))
	})

	t.Run("text and csv sniff as plain text, which the allowlist treats as generic", func(t *testing.T) {
		text, err := documentBody("text", "remittance.txt", "Remittance advice", 1)
		require.NoError(t, err)
		assert.Contains(t, string(text), "Remittance advice")
		assert.True(t, strings.HasPrefix(http.DetectContentType(text), "text/plain"))

		csv, err := documentBody("csv", "figures.csv", "Figures", 1)
		require.NoError(t, err)
		assert.True(t, strings.HasPrefix(string(csv), "field,value\n"))
		assert.True(t, strings.HasPrefix(http.DetectContentType(csv), "text/plain"))
	})

	t.Run("versions differ, so the second version is a real one", func(t *testing.T) {
		first, err := documentBody("pdf", "vat.pdf", "VAT return Jan", 1)
		require.NoError(t, err)
		second, err := documentBody("pdf", "vat.pdf", "VAT return Jan", 2)
		require.NoError(t, err)
		assert.NotEqual(t, first, second)
	})

	t.Run("the same inputs always produce the same bytes", func(t *testing.T) {
		a, err := documentBody("pdf", "vat.pdf", "VAT return Jan", 1)
		require.NoError(t, err)
		b, err := documentBody("pdf", "vat.pdf", "VAT return Jan", 1)
		require.NoError(t, err)
		assert.Equal(t, a, b)
	})

	t.Run("a label with PDF metacharacters is escaped", func(t *testing.T) {
		body, err := documentBody("pdf", "x.pdf", `Liasse (2025) \ final`, 1)
		require.NoError(t, err)
		assert.Contains(t, string(body), `Liasse \(2025\) \\ final`)
	})

	t.Run("an unknown kind is an error, not an empty file", func(t *testing.T) {
		_, err := documentBody("docx", "x.docx", "", 1)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "docx")
	})
}

// --- the key → id map -----------------------------------------------------

func TestWriteSeedOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "seed-demo-output.json")
	tenant := newTenantOutput("acme", "acme-demo", "Acme", "Europe/Berlin")
	tenant.TenantID = "tenant-1"
	tenant.Entities["acme.de"] = "entity-1"
	tenant.Workflows["acme.w1"] = workflowOutput{
		ID: "wf-1", Name: "DE VAT 2026", Category: "recurring", Status: "active", Started: true,
		Templates: map[string]string{"collect": "wt-1"},
		Instances: map[string]string{instanceKey("M1", "collect"): "ti-1"},
	}

	require.NoError(t, writeSeedOutput(path, seedOutput{
		GeneratedAt: formatInstant(time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)),
		SpecPath:    "seed/demo/dataset.json",
		APIBaseURL:  "http://localhost:3000",
		AsOf:        "2026-09-06",
		Warning:     outputWarning,
		Tenants:     []tenantOutput{*tenant},
	}))

	raw, err := os.ReadFile(path)
	require.NoError(t, err)

	var round seedOutput
	require.NoError(t, json.Unmarshal(raw, &round))
	assert.Equal(t, "2026-09-06T12:00:00.000Z", round.GeneratedAt)
	require.Len(t, round.Tenants, 1)
	assert.Equal(t, "entity-1", round.Tenants[0].Entities["acme.de"])
	assert.Equal(t, "ti-1", round.Tenants[0].Workflows["acme.w1"].Instances["M1|collect"])
	assert.Contains(t, round.Warning, "Demo credentials")
	// Empty maps must marshal as {} — verify and a browser tester index into
	// them without checking for null first.
	assert.Contains(t, string(raw), `"obligationTypes": {}`)
}

// --- the dry run writes nothing ------------------------------------------

func TestDryRunSeedDescribesThePlanWithoutWriting(t *testing.T) {
	out := &bytes.Buffer{}
	tenant := spec.Tenant{
		Key: "acme", Slug: "acme-demo", Timezone: "Europe/Berlin",
		Admin: spec.User{Key: "acme.admin", Role: "tenant_admin", Status: spec.StatusActive},
		Users: []spec.User{{Key: "acme.manager", Role: "manager", Status: spec.StatusActive}},
		Workflows: []spec.Workflow{{
			Key: "acme.w1", Category: spec.CategoryRecurring,
			SelectedPeriods: []string{"M1", "M2"},
			Lifecycle:       spec.Lifecycle{Start: true, FinalStatus: "active"},
			Templates:       []spec.TaskTemplate{{Key: "collect"}, {Key: "pay"}},
			Instances: []spec.Instance{
				{Period: "M1", Task: "collect", Via: spec.ViaApprove, CompletedOn: dateonly.New(2026, 2, 2)},
			},
			Documents: []spec.Document{{Period: "M1", Task: "pay", Kind: "pdf", NewVersion: true}},
		}},
	}
	outputPath := filepath.Join(t.TempDir(), "should-not-exist.json")
	d := &deps{
		Spec:       &spec.Spec{AsOf: dateonly.New(2026, 9, 6)},
		SpecPath:   "seed/demo/dataset.json",
		API:        apiclient.New("http://localhost:3000"),
		OutputPath: outputPath,
		Tenants:    []spec.Tenant{tenant},
		DryRun:     true,
		Out:        out,
		Err:        &bytes.Buffer{},
		Now:        time.Now,
	}

	require.NoError(t, runSeed(t.Context(), d))

	printed := out.String()
	assert.Contains(t, printed, "dry run")
	assert.Contains(t, printed, "setup writes as acme.manager")
	// 2 periods × 2 templates.
	assert.Contains(t, printed, "4 task instances")
	// via=approve is one PUT, one submit and one approve; the document adds a
	// second version.
	assert.Contains(t, printed, "1 PUT, 1 submit, 1 approve; 1 documents (2 versions)")
	assert.Contains(t, printed, "completion instants to back-date: 1")

	_, err := os.Stat(outputPath)
	assert.True(t, os.IsNotExist(err), "a dry run must not write the output file")
}
