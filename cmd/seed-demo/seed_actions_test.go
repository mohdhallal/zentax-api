package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mohamadhallal/zentax-api/seed/demo/spec"
	"github.com/mohamadhallal/zentax-api/shared/apiclient"
)

// The action mapping is the part of the seeder most likely to go quietly wrong
// — a submit before an upload, a PUT that sets the final status on an instance
// that has to reach it through an approval, the wrong user's session on an
// approve — and none of those show up as an error at runtime. So the mapping is
// exercised here end to end against a fake transport: no server, no database,
// just "this instance spec produces exactly these requests, in this order, with
// these bodies, on these sessions".

// recordedCall is one request the fake transport saw.
type recordedCall struct {
	Method      string
	Path        string
	Cookie      string
	ContentType string
	Body        []byte
}

// fakeTransport answers every request with a success envelope and records it.
type fakeTransport struct {
	mu   sync.Mutex
	seen []recordedCall
	// data is the JSON written into the envelope's "data" member.
	data string
}

func (f *fakeTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	var body []byte
	if r.Body != nil {
		body, _ = io.ReadAll(r.Body)
		_ = r.Body.Close()
	}
	f.mu.Lock()
	f.seen = append(f.seen, recordedCall{
		Method:      r.Method,
		Path:        r.URL.Path,
		Cookie:      r.Header.Get("Cookie"),
		ContentType: r.Header.Get("Content-Type"),
		Body:        body,
	})
	f.mu.Unlock()

	data := f.data
	if data == "" {
		data = "{}"
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"status":true,"data":` + data + `}`)),
		Request:    r,
	}, nil
}

func (f *fakeTransport) calls() []recordedCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]recordedCall, len(f.seen))
	copy(out, f.seen)
	return out
}

// fakeSessions builds one apiclient session per user key, each with its own
// cookie value, so a recorded call identifies who made it.
func fakeSessions(transport http.RoundTripper, keys ...string) map[string]*apiclient.Session {
	client := apiclient.New("http://api.test", apiclient.WithHTTPClient(&http.Client{Transport: transport}))
	sessions := make(map[string]*apiclient.Session, len(keys))
	for _, key := range keys {
		token := "session-" + key
		sessions[key] = &apiclient.Session{Client: client.WithSession(token), Token: token}
	}
	return sessions
}

// demoWorkflow is a two-template workflow whose instances cover the three
// `via` values plus a document with a second version.
func demoWorkflow() spec.Workflow {
	return spec.Workflow{
		Key:             "acme.w1",
		Name:            "DE VAT 2026",
		Category:        spec.CategoryRecurring,
		SelectedPeriods: []string{"M1", "M2"},
		Writer:          "acme.preparer",
		Approver:        "acme.reviewer",
		Lifecycle:       spec.Lifecycle{Start: true, FinalStatus: "active"},
		Templates: []spec.TaskTemplate{
			{Key: "prepare", Name: "Prepare return", TaskType: "preparation", OrderIndex: 0},
			{Key: "review", Name: "Review", TaskType: "review", ApprovalRequired: true, OrderIndex: 1},
		},
		Instances: []spec.Instance{
			{
				Period: "M1", Task: "prepare", Status: "completed", Via: spec.ViaPut,
				Assignee:      "acme.preparer",
				TaxData:       map[string]any{"outputVat": json.Number("76000")},
				TaxDataStatus: "final",
			},
			{Period: "M1", Task: "review", Status: "completed", Via: spec.ViaApprove, Assignee: "acme.reviewer"},
			{Period: "M2", Task: "review", Status: "pending_approval", Via: spec.ViaSubmit, Assignee: "acme.reviewer"},
		},
		Documents: []spec.Document{
			{
				Period: "M1", Task: "prepare", DocumentType: "final_return", Label: "VAT return Jan",
				Kind: "pdf", FileName: "vat-jan.pdf", Category: "compliance", NewVersion: true,
			},
		},
	}
}

func demoRefs() workflowRefs {
	return workflowRefs{
		WorkflowID: "wf-1",
		InstanceIDs: map[string]string{
			instanceKey("M1", "prepare"): "ti-m1-prepare",
			instanceKey("M1", "review"):  "ti-m1-review",
			instanceKey("M2", "prepare"): "ti-m2-prepare",
			instanceKey("M2", "review"):  "ti-m2-review",
		},
		UserIDs: map[string]string{
			"acme.preparer": "user-preparer",
			"acme.reviewer": "user-reviewer",
		},
	}
}

func TestPlanWorkflowActionsOrdersPhases(t *testing.T) {
	actions, err := planWorkflowActions(demoWorkflow(), demoRefs())
	require.NoError(t, err)

	type step struct{ method, path, actor string }
	got := make([]step, 0, len(actions))
	for _, a := range actions {
		got = append(got, step{a.Method, a.Path, a.Actor})
	}

	// Every PUT first, then the documents (an instance freezes once it is
	// submitted), then the submits, then the approvals.
	assert.Equal(t, []step{
		{http.MethodPut, "/task-instances/ti-m1-prepare", "acme.preparer"},
		{http.MethodPut, "/task-instances/ti-m1-review", "acme.preparer"},
		{http.MethodPut, "/task-instances/ti-m2-review", "acme.preparer"},
		{http.MethodPost, "/workflows/wf-1/documents", "acme.preparer"},
		{http.MethodPost, "/task-instances/ti-m1-review/submit-for-approval", "acme.preparer"},
		{http.MethodPost, "/task-instances/ti-m2-review/submit-for-approval", "acme.preparer"},
		{http.MethodPost, "/task-instances/ti-m1-review/approve", "acme.reviewer"},
	}, got)
}

func TestPlanInstancePutBodies(t *testing.T) {
	actions, err := planWorkflowActions(demoWorkflow(), demoRefs())
	require.NoError(t, err)

	// via=put carries the dataset's own status, the assignee and the tax data.
	body, ok := actions[0].Body.(updateTaskInstanceBody)
	require.True(t, ok)
	assert.Equal(t, "completed", body.Status)
	require.NotNil(t, body.AssigneeID)
	assert.Equal(t, "user-preparer", *body.AssigneeID)
	assert.Equal(t, "final", body.TaxDataStatus)
	assert.Equal(t, map[string]any{"outputVat": json.Number("76000")}, body.TaxData)
	assert.Nil(t, body.Notes)

	// via=approve / via=submit set in_progress: pending_approval and the
	// approved completion are only reachable through the dedicated actions.
	for _, i := range []int{1, 2} {
		body, ok := actions[i].Body.(updateTaskInstanceBody)
		require.True(t, ok, "action %d", i)
		assert.Equal(t, "in_progress", body.Status, "action %d", i)
		require.NotNil(t, body.AssigneeID)
		assert.Equal(t, "user-reviewer", *body.AssigneeID)
	}
}

func TestPlanInstancePutSendsNotesAndOmitsEmptyTaxData(t *testing.T) {
	workflow := spec.Workflow{
		Key: "acme.w1", Category: spec.CategoryRecurring, Writer: "acme.preparer", Approver: "acme.reviewer",
		Templates: []spec.TaskTemplate{{Key: "collect", OrderIndex: 0}},
		Instances: []spec.Instance{{
			Period: "M9", Task: "collect", Status: "blocked", Via: spec.ViaPut,
			Assignee: "acme.preparer", Notes: "Waiting for ERP export",
		}},
	}
	refs := workflowRefs{
		WorkflowID:  "wf-1",
		InstanceIDs: map[string]string{instanceKey("M9", "collect"): "ti-x"},
		UserIDs:     map[string]string{"acme.preparer": "user-preparer"},
	}

	actions, err := planWorkflowActions(workflow, refs)
	require.NoError(t, err)
	require.Len(t, actions, 1)

	body := actions[0].Body.(updateTaskInstanceBody)
	assert.Equal(t, "blocked", body.Status)
	require.NotNil(t, body.Notes)
	assert.Equal(t, "Waiting for ERP export", *body.Notes)
	assert.Nil(t, body.TaxData)

	// The wire body must carry no key the API does not know: an unknown field
	// is a 400 (the contract is strict), and an empty taxData would clear the
	// instance's data.
	encoded, err := json.Marshal(body)
	require.NoError(t, err)
	var wire map[string]any
	require.NoError(t, json.Unmarshal(encoded, &wire))
	assert.ElementsMatch(t, []string{"status", "assigneeId", "notes"}, keysOf(wire))
}

func TestPlanWorkflowActionsFailsOnMissingIDs(t *testing.T) {
	t.Run("unknown instance", func(t *testing.T) {
		refs := demoRefs()
		delete(refs.InstanceIDs, instanceKey("M1", "review"))
		_, err := planWorkflowActions(demoWorkflow(), refs)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "acme.w1 M1/review")
	})

	t.Run("unknown assignee", func(t *testing.T) {
		refs := demoRefs()
		delete(refs.UserIDs, "acme.reviewer")
		_, err := planWorkflowActions(demoWorkflow(), refs)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "acme.w1 M1/review")
		assert.Contains(t, err.Error(), "acme.reviewer")
	})

	t.Run("unknown document instance", func(t *testing.T) {
		refs := demoRefs()
		delete(refs.InstanceIDs, instanceKey("M1", "prepare"))
		_, err := planWorkflowActions(demoWorkflow(), refs)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "M1/prepare")
	})

	t.Run("no workflow id", func(t *testing.T) {
		_, err := planWorkflowActions(demoWorkflow(), workflowRefs{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "acme.w1")
	})
}

func TestExecuteActionsIssuesTheRequestsAsTheRightUsers(t *testing.T) {
	transport := &fakeTransport{data: `{"id":"doc-1","versionId":"ver-1","version":1}`}
	sessions := fakeSessions(transport, "acme.preparer", "acme.reviewer")

	actions, err := planWorkflowActions(demoWorkflow(), demoRefs())
	require.NoError(t, err)
	results, err := executeActions(context.Background(), sessions, actions)
	require.NoError(t, err)
	require.Len(t, results, len(actions))

	calls := transport.calls()
	// One extra call: the document's second version.
	require.Len(t, calls, len(actions)+1)

	assert.Equal(t, "zentax_session=session-acme.preparer", calls[0].Cookie)
	assert.Equal(t, "application/json", calls[0].ContentType)
	assert.JSONEq(t,
		`{"status":"completed","assigneeId":"user-preparer","taxData":{"outputVat":76000},"taxDataStatus":"final"}`,
		string(calls[0].Body))

	// The upload and its second version, then the submits, then the approve —
	// and the approve is the reviewer's, never the writer's (separation of
	// duties: the API refuses an approver who is the submitter).
	assert.Equal(t, "/workflows/wf-1/documents", calls[3].Path)
	assert.Equal(t, "/documents/doc-1/versions", calls[4].Path)
	assert.Equal(t, "/task-instances/ti-m1-review/submit-for-approval", calls[5].Path)
	assert.Equal(t, "zentax_session=session-acme.preparer", calls[5].Cookie)
	assert.Equal(t, "/task-instances/ti-m1-review/approve", calls[7].Path)
	assert.Equal(t, "zentax_session=session-acme.reviewer", calls[7].Cookie)

	// The upload's own result carries the ids the summary and the output file
	// record; the version call replaces the version id.
	require.Equal(t, actionUpload, results[3].Action.Kind)
	assert.Equal(t, "doc-1", results[3].DocumentID)
	assert.Equal(t, "ver-1", results[3].VersionID)
	assert.Equal(t, "M1", results[3].Action.Period)
	assert.Equal(t, "prepare", results[3].Action.Task)
}

func TestExecuteActionsUploadsMultipartWithTheDatasetsFields(t *testing.T) {
	transport := &fakeTransport{data: `{"id":"doc-1","versionId":"ver-1","version":1}`}
	sessions := fakeSessions(transport, "acme.preparer", "acme.reviewer")

	actions, err := planWorkflowActions(demoWorkflow(), demoRefs())
	require.NoError(t, err)
	_, err = executeActions(context.Background(), sessions, actions)
	require.NoError(t, err)

	upload := transport.calls()[3]
	fields, fileName, fileType, content := parseMultipart(t, upload)
	assert.Equal(t, map[string]string{
		"documentType":   "final_return",
		"taskInstanceId": "ti-m1-prepare",
		"category":       "compliance",
		"label":          "VAT return Jan",
	}, fields)
	assert.Equal(t, "vat-jan.pdf", fileName)
	assert.Equal(t, "application/pdf", fileType)
	// The upload sniffs the first bytes and refuses a declared type the content
	// contradicts, so a "pdf" really has to be one.
	assert.True(t, bytes.HasPrefix(content, []byte("%PDF-")))
	assert.Equal(t, "application/pdf", http.DetectContentType(content))

	// Version 2 differs from version 1: a document version is content-addressed
	// by sha256, so identical bytes would be a meaningless second version.
	_, _, _, second := parseMultipart(t, transport.calls()[4])
	assert.NotEqual(t, content, second)
}

func TestExecuteActionsReportsWhichActionFailed(t *testing.T) {
	failing := &failingTransport{
		fakeTransport: fakeTransport{data: `{"id":"doc-1","versionId":"ver-1","version":1}`},
		failOn:        "/task-instances/ti-m1-review/approve",
	}
	sessions := fakeSessions(failing, "acme.preparer", "acme.reviewer")

	actions, err := planWorkflowActions(demoWorkflow(), demoRefs())
	require.NoError(t, err)
	_, err = executeActions(context.Background(), sessions, actions)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "acme.w1 M1/review")
	assert.Contains(t, err.Error(), "POST approve")
	assert.Contains(t, err.Error(), "acme.reviewer")
	assert.Contains(t, err.Error(), "approver cannot be the submitter")
}

func TestExecuteActionsRefusesAnActorWithNoSession(t *testing.T) {
	transport := &fakeTransport{data: `{"id":"doc-1","versionId":"ver-1","version":1}`}
	sessions := fakeSessions(transport, "acme.preparer") // no reviewer

	actions, err := planWorkflowActions(demoWorkflow(), demoRefs())
	require.NoError(t, err)
	_, err = executeActions(context.Background(), sessions, actions)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "acme.reviewer")
}

// failingTransport answers one path with a failure envelope and everything else
// with success.
type failingTransport struct {
	fakeTransport
	failOn string
}

func (f *failingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Path != f.failOn {
		return f.fakeTransport.RoundTrip(r)
	}
	return &http.Response{
		StatusCode: http.StatusForbidden,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"status":false,"error":{"code":"FORBIDDEN","message":"approver cannot be the submitter"}}`)),
		Request: r,
	}, nil
}

// parseMultipart splits a recorded multipart request into its text fields and
// its single file part.
func parseMultipart(t *testing.T, call recordedCall) (fields map[string]string, fileName, fileType string, content []byte) {
	t.Helper()
	mediaType, params, err := mime.ParseMediaType(call.ContentType)
	require.NoError(t, err)
	require.Equal(t, "multipart/form-data", mediaType)

	reader := multipart.NewReader(bytes.NewReader(call.Body), params["boundary"])
	fields = map[string]string{}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		body, err := io.ReadAll(part)
		require.NoError(t, err)
		if part.FileName() != "" {
			fileName = part.FileName()
			fileType, _, _ = strings.Cut(part.Header.Get("Content-Type"), ";")
			content = body
			continue
		}
		fields[part.FormName()] = string(body)
	}
	return fields, fileName, fileType, content
}

func keysOf(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}
