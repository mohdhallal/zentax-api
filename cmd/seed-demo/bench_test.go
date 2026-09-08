package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mohamadhallal/zentax-api/seed/demo/scale"
	"github.com/mohamadhallal/zentax-api/shared/apiclient"
)

func TestBenchPercentileIsNearestRank(t *testing.T) {
	samples := []float64{5, 1, 4, 2, 3, 10, 9, 8, 7, 6} // 1..10
	assert.Equal(t, 5.0, benchPercentile(samples, 0.50))
	assert.Equal(t, 10.0, benchPercentile(samples, 0.95))
	assert.Equal(t, 10.0, benchPercentile(samples, 1))
	assert.Equal(t, 0.0, benchPercentile(nil, 0.5))
	assert.Equal(t, 7.0, benchPercentile([]float64{7}, 0.95))
}

func TestBenchCredentials(t *testing.T) {
	email, password, err := benchCredentials(BenchDeps{Tenant: scale.DefaultSlug})
	require.NoError(t, err)
	assert.Equal(t, scale.AdminEmail, email)
	assert.Equal(t, scale.AdminPassword, password)

	email, password, err = benchCredentials(BenchDeps{Tenant: "globex", SpecPath: "../../seed/demo/dataset.json"})
	require.NoError(t, err)
	assert.Equal(t, "admin@globex.test", email)
	assert.NotEmpty(t, password)

	email, password, err = benchCredentials(BenchDeps{Tenant: "x", Email: "a@b.test", Password: "pw"})
	require.NoError(t, err)
	assert.Equal(t, "a@b.test", email)
	assert.Equal(t, "pw", password)

	_, _, err = benchCredentials(BenchDeps{Tenant: "x", Email: "a@b.test"})
	assert.Error(t, err, "--email without --password")
	_, _, err = benchCredentials(BenchDeps{Tenant: "nobody", SpecPath: "../../seed/demo/dataset.json"})
	assert.Error(t, err)
}

// fakeBenchAPI is just enough of the API for a bench run: login, /auth/me,
// the two id-resolution lists, and every target — task-summary absent (404),
// entity search ignored (same total either way).
func fakeBenchAPI(t *testing.T, slow map[string]time.Duration) *httptest.Server {
	t.Helper()
	envelope := func(w http.ResponseWriter, data any, pagination map[string]int) {
		body := map[string]any{"status": true, "data": data}
		if pagination != nil {
			body["pagination"] = pagination
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health/ready", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	mux.HandleFunc("/auth/login", func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: apiclient.SessionCookieName, Value: "tok"})
		envelope(w, map[string]any{"mfaRequired": false, "user": map[string]any{"id": "u1", "email": scale.AdminEmail}}, nil)
	})
	mux.HandleFunc("/auth/me", func(w http.ResponseWriter, _ *http.Request) {
		envelope(w, map[string]any{"id": "u1", "email": scale.AdminEmail,
			"tenant": map[string]any{"id": "t1", "slug": "scale", "name": "Scale", "timezone": "Europe/Berlin"}}, nil)
	})
	mux.HandleFunc("/obligation-types", func(w http.ResponseWriter, _ *http.Request) {
		envelope(w, []map[string]any{{"id": "ot-vat", "code": "VAT", "template": "VAT"}}, map[string]int{"total": 1, "limit": 100, "offset": 0})
	})
	mux.HandleFunc("/workflows", func(w http.ResponseWriter, _ *http.Request) {
		envelope(w, []map[string]any{
			{"id": "wf-draft", "status": "draft", "obligationTypeId": "ot-vat"},
			{"id": "wf-vat", "status": "active", "obligationTypeId": "ot-vat"},
		}, map[string]int{"total": 2, "limit": 100, "offset": 0})
	})
	mux.HandleFunc("/entities", func(w http.ResponseWriter, _ *http.Request) {
		envelope(w, []map[string]any{}, map[string]int{"total": 48, "limit": 20, "offset": 0})
	})
	mux.HandleFunc("/reports/task-summary", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"status":false,"error":{"code":"NOT_FOUND","message":"route not found"}}`))
	})
	for _, path := range []string{"/reports/task-instances", "/reports/workflow-stats", "/reports/compliance-heatmap",
		"/reports/compliance-status", "/task-instances", "/reports/tax-financial"} {
		path := path
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			if d, ok := slow[path]; ok {
				time.Sleep(d)
			}
			envelope(w, []map[string]any{}, map[string]int{"total": 0, "limit": 50, "offset": 0})
		})
	}
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func TestRunBenchAgainstAFakeAPI(t *testing.T) {
	server := fakeBenchAPI(t, nil)
	out := &bytes.Buffer{}
	outFile := t.TempDir() + "/bench.json"
	err := runBench(context.Background(), BenchDeps{
		Stdout: out, Stderr: &bytes.Buffer{},
		Now:  func() time.Time { return time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC) },
		Args: []string{"--api", server.URL, "--n", "3", "--warmup", "1", "--json", "--out", outFile},
	})
	require.NoError(t, err)

	var report benchReport
	require.NoError(t, json.Unmarshal(out.Bytes(), &report))
	require.Len(t, report.Results, 11)
	assert.Equal(t, 0, report.Failed)
	assert.Equal(t, "scale", report.Tenant)
	assert.Equal(t, 3, report.N)
	assert.Equal(t, 1, report.Warmup)

	byName := map[string]benchResult{}
	for _, r := range report.Results {
		byName[r.Name] = r
	}
	summary := byName["task summary"]
	assert.Equal(t, "SKIP", summary.Result)
	assert.Contains(t, summary.Note, "404")
	assert.Equal(t, 0, summary.N)

	search := byName["entity search (Mül, 20)"]
	assert.Equal(t, "PASS", search.Result)
	assert.Contains(t, search.Note, "not honoured")
	assert.Contains(t, search.Path, "search=M%C3%BCl")

	instances := byName["workflow instances (VAT workflow, 50/page)"]
	assert.Contains(t, instances.Path, "workflowId=wf-vat", "the active VAT workflow, not the draft")

	deep := byName["task feed p200 (dueDate:asc, offset 9950)"]
	assert.Contains(t, deep.Path, "offset=9950")
	assert.Equal(t, 3, deep.N)
	assert.Equal(t, benchListBudgetMs, deep.BudgetMs)
	assert.LessOrEqual(t, deep.P50Ms, deep.P95Ms)
	assert.LessOrEqual(t, deep.P95Ms, deep.MaxMs)
	assert.Equal(t, benchAggregateBudgetMs, byName["compliance heatmap (FY 2026, period view)"].BudgetMs)

	// The --out file carries the same report.
	var written benchReport
	data, err := os.ReadFile(outFile)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &written))
	assert.Equal(t, report, written)
}

// A budget miss fails the run — unless --no-fail.
func TestBenchMeasureFlagsAMissAndAnError(t *testing.T) {
	server := fakeBenchAPI(t, map[string]time.Duration{"/reports/workflow-stats": 20 * time.Millisecond})
	api := apiclient.New(server.URL)

	miss := benchMeasure(context.Background(), api, benchTarget{Name: "stats", Path: "/reports/workflow-stats", BudgetMs: 5}, 2, 0)
	assert.Equal(t, "FAIL", miss.Result)
	assert.GreaterOrEqual(t, miss.P95Ms, 20.0)

	skip := benchMeasure(context.Background(), api, benchTarget{Name: "summary", Path: "/reports/task-summary", BudgetMs: 500, SkipOn404: true}, 2, 0)
	assert.Equal(t, "SKIP", skip.Result)

	broken := benchMeasure(context.Background(), api, benchTarget{Name: "missing", Path: "/nope", BudgetMs: 500}, 2, 0)
	assert.Equal(t, "ERROR", broken.Result)
	assert.Contains(t, broken.Note, "404")
}

func TestBenchTableRendersEveryRow(t *testing.T) {
	out := &bytes.Buffer{}
	benchPrintTable(out, benchReport{
		API: "http://x", Tenant: "scale", N: 30, Warmup: 3, Failed: 1,
		Results: []benchResult{
			{Name: "a", N: 30, P50Ms: 12.3, P95Ms: 18, MaxMs: 25.1, BudgetMs: 500, Result: "PASS"},
			{Name: "b", N: 30, P50Ms: 700, P95Ms: 900, MaxMs: 1000, BudgetMs: 500, Result: "FAIL"},
			{Name: "c", BudgetMs: 500, Result: "SKIP", Note: "not there"},
		},
	})
	text := out.String()
	assert.Contains(t, text, "TARGET")
	assert.Contains(t, text, "P95 MS")
	assert.Contains(t, text, "PASS")
	assert.Contains(t, text, "FAIL")
	assert.Contains(t, text, "SKIP")
	assert.Contains(t, text, "- c: not there")
	assert.Contains(t, text, "2 of 3 targets within budget, 1 missed")
	assert.True(t, strings.Contains(text, "900.0"), "p95 rendered with one decimal")
}
