package spec

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// datasetPath is the real dataset, relative to this package.
const datasetPath = "../dataset.json"

// TestLoadRealDataset is the golden test: the shipped dataset must decode
// strictly and satisfy its own invariants. It skips (rather than fails) when
// the file is absent, so the suite stays green if the dataset moves.
func TestLoadRealDataset(t *testing.T) {
	t.Parallel()
	if _, err := os.Stat(datasetPath); errors.Is(err, os.ErrNotExist) {
		t.Skip("seed/demo/dataset.json is not present")
	}

	spec, err := Load(datasetPath)
	require.NoError(t, err, "the dataset must decode with DisallowUnknownFields")
	require.NoError(t, spec.Validate())

	require.NotEmpty(t, spec.Tenants)
	assert.False(t, spec.AsOf.IsZero())
	assert.NotEmpty(t, spec.ExpectedAsOf)

	for _, tenant := range spec.Tenants {
		assert.NotEmpty(t, tenant.Slug, "tenant %s", tenant.Key)
		assert.NotEmpty(t, tenant.Admin.Email, "tenant %s has no admin", tenant.Key)
		assert.NotEmpty(t, tenant.Workflows, "tenant %s has no workflows", tenant.Key)
	}
}

// TestLoadAndValidateRealDataset covers the convenience wrapper on the same file.
func TestLoadAndValidateRealDataset(t *testing.T) {
	t.Parallel()
	if _, err := os.Stat(datasetPath); errors.Is(err, os.ErrNotExist) {
		t.Skip("seed/demo/dataset.json is not present")
	}
	spec, err := LoadAndValidate(datasetPath)
	require.NoError(t, err)
	assert.NotEmpty(t, spec.Tenants)
}

func TestLoad_MissingFile(t *testing.T) {
	t.Parallel()
	_, err := Load("does-not-exist.json")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "load dataset")
}

func TestDecode_RejectsUnknownFields(t *testing.T) {
	t.Parallel()
	_, err := Decode(strings.NewReader(`{"asOf":"2026-09-06","newThing":1}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown field")
	assert.Contains(t, err.Error(), "newThing")
}

func TestDecode_RejectsUnknownNestedFields(t *testing.T) {
	t.Parallel()
	_, err := Decode(strings.NewReader(`{"tenants":[{"key":"acme","surprise":true}]}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "surprise")
}

func TestDecode_RejectsTrailingData(t *testing.T) {
	t.Parallel()
	_, err := Decode(strings.NewReader(`{"asOf":"2026-09-06"} {"asOf":"2026-09-07"}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "trailing data")
}

func TestDecode_SchemaNotesAreOpaque(t *testing.T) {
	t.Parallel()
	// The prose in $schemaNotes may be rewritten freely; it must not have to
	// match a struct.
	spec, err := Decode(strings.NewReader(
		`{"$schemaNotes":{"anything":{"nested":[1,2,3]}},"asOf":"2026-09-06"}`))
	require.NoError(t, err)
	assert.JSONEq(t, `{"anything":{"nested":[1,2,3]}}`, string(spec.SchemaNotes))
}

func TestDecode_TaxDataKeepsExactNumbers(t *testing.T) {
	t.Parallel()
	// 9007199254740993 = 2^53+1: a float64 would round it. taxData is written
	// back into a PUT body verbatim, so the literal has to survive.
	spec, err := Decode(strings.NewReader(`{"tenants":[{"key":"t","slug":"t","name":"T","timezone":"UTC",
		"admin":{"key":"a","email":"a@t.test","name":"A","role":"tenant_admin","scopeEntity":null,
		         "password":"demo-password-1","status":"active"},
		"users":[],"entities":[],"obligationTypes":[],"entityObligations":[],
		"workflows":[{"key":"w","name":"W","description":"","category":"project","projectType":"dispute",
		  "entity":null,"obligationType":null,"entityObligation":null,"financialYear":"2026","periodicity":null,
		  "selectedPeriods":[],"dueDateRule":{"reference":"period_end","offsetUnit":"days","offsetValue":0,
		    "offsetDirection":"after","weekendAdjustment":"none"},
		  "startDate":"2026-01-01","endDate":"2026-06-30","tasksSequential":false,
		  "writer":"a","approver":"a","lifecycle":{"start":true,"finalStatus":"active"},
		  "templates":[],"documents":[],
		  "instances":[{"period":"PROJECT","task":"t1","status":"completed","via":"put","assignee":"a",
		    "completedOn":"2026-02-01","taxData":{"amount":9007199254740993,"rate":19.5},"taxDataStatus":"final"}]}]}]}`))
	require.NoError(t, err)

	taxData := spec.Tenants[0].Workflows[0].Instances[0].TaxData
	assert.Equal(t, json.Number("9007199254740993"), taxData["amount"])
	assert.Equal(t, json.Number("19.5"), taxData["rate"])

	// and it re-marshals to the same literals
	encoded, err := json.Marshal(taxData)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"amount":9007199254740993`)
	assert.Contains(t, string(encoded), `"rate":19.5`)
}

func TestDecode_PointerFieldsDistinguishNullFromValue(t *testing.T) {
	t.Parallel()
	spec, err := Decode(strings.NewReader(`{"tenants":[{"key":"t","slug":"t","name":"T","timezone":"UTC",
		"admin":{"key":"a","email":"a@t.test","name":"A","role":"tenant_admin","scopeEntity":null,
		         "password":"demo-password-1","status":"active"},
		"users":[{"key":"m","email":"m@t.test","name":"M","role":"preparer","scopeEntity":"e1",
		          "password":"demo-password-1","status":"active"}],
		"entities":[{"key":"e1","name":"E","legalName":"E SA","country":"France","taxResidency":"FR",
		             "parent":null,"fiscalCalendarPattern":"standard","financialYearEnd":"12-31"}],
		"obligationTypes":[],"entityObligations":[],"workflows":[]}]}`))
	require.NoError(t, err)

	tenant := spec.Tenants[0]
	assert.Nil(t, tenant.Admin.ScopeEntity, "a tenant-wide grant is null, not an empty string")
	require.NotNil(t, tenant.Users[0].ScopeEntity)
	assert.Equal(t, "e1", *tenant.Users[0].ScopeEntity)
	assert.Nil(t, tenant.Entities[0].Parent)
	assert.Equal(t, "12-31", tenant.Entities[0].FinancialYearEnd)
}
