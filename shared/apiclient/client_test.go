package apiclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordedRequest is what the stub server saw.
type recordedRequest struct {
	Method string
	Path   string
	Query  string
	Header http.Header
	Body   []byte
}

// newStub starts an httptest server that records every request and replies
// with what handler writes.
func newStub(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) (*Client, *[]recordedRequest) {
	t.Helper()
	var (
		mu   sync.Mutex
		seen []recordedRequest
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		seen = append(seen, recordedRequest{
			Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery,
			Header: r.Header.Clone(), Body: body,
		})
		mu.Unlock()
		r.Body = io.NopCloser(strings.NewReader(string(body)))
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL), &seen
}

func writeEnvelope(t *testing.T, w http.ResponseWriter, status int, body map[string]any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	require.NoError(t, json.NewEncoder(w).Encode(body))
}

// --- envelope unwrapping ---------------------------------------------------

func TestGET_UnwrapsEnvelope(t *testing.T) {
	t.Parallel()
	client, seen := newStub(t, func(w http.ResponseWriter, _ *http.Request) {
		writeEnvelope(t, w, http.StatusOK, map[string]any{
			"status": true,
			"data":   map[string]any{"id": "e-1", "name": "Acme GmbH"},
		})
	})

	var entity struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	require.NoError(t, client.GET(t.Context(), "/entities/e-1", nil, &entity))
	assert.Equal(t, "e-1", entity.ID)
	assert.Equal(t, "Acme GmbH", entity.Name)

	require.Len(t, *seen, 1)
	assert.Equal(t, http.MethodGet, (*seen)[0].Method)
	assert.Equal(t, "/entities/e-1", (*seen)[0].Path)
	assert.Empty(t, (*seen)[0].Body)
}

func TestGET_QueryStringIsPassedThrough(t *testing.T) {
	t.Parallel()
	client, seen := newStub(t, func(w http.ResponseWriter, _ *http.Request) {
		writeEnvelope(t, w, http.StatusOK, map[string]any{"status": true, "data": []any{}})
	})
	require.NoError(t, client.GET(t.Context(), "/reports/compliance-heatmap?year=2026&viewMode=period", nil, nil))
	assert.Equal(t, "year=2026&viewMode=period", (*seen)[0].Query)
}

func TestPOST_SendsJSONBodyAndDecodesData(t *testing.T) {
	t.Parallel()
	client, seen := newStub(t, func(w http.ResponseWriter, _ *http.Request) {
		writeEnvelope(t, w, http.StatusCreated, map[string]any{
			"status": true,
			"data":   map[string]any{"id": "w-9"},
		})
	})

	var created struct {
		ID string `json:"id"`
	}
	require.NoError(t, client.POST(t.Context(), "/workflows", map[string]any{"name": "VAT DE"}, &created))
	assert.Equal(t, "w-9", created.ID)

	require.Len(t, *seen, 1)
	assert.Equal(t, "application/json", (*seen)[0].Header.Get("Content-Type"))
	assert.JSONEq(t, `{"name":"VAT DE"}`, string((*seen)[0].Body))
}

func TestPUT_And_DELETE(t *testing.T) {
	t.Parallel()
	client, seen := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeEnvelope(t, w, http.StatusOK, map[string]any{"status": true, "data": map[string]any{"id": "t-1"}})
	})

	var updated struct {
		ID string `json:"id"`
	}
	require.NoError(t, client.PUT(t.Context(), "/task-instances/t-1", map[string]any{"status": "completed"}, &updated))
	assert.Equal(t, "t-1", updated.ID)

	require.NoError(t, client.DELETE(t.Context(), "/task-instances/t-1", nil, nil))
	require.Len(t, *seen, 2)
	assert.Equal(t, http.MethodPut, (*seen)[0].Method)
	assert.Equal(t, http.MethodDelete, (*seen)[1].Method)
}

func TestDecode_EmptyBodyLeavesOutUntouched(t *testing.T) {
	t.Parallel()
	client, _ := newStub(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	out := map[string]any{"untouched": true}
	require.NoError(t, client.POST(t.Context(), "/auth/logout", nil, &out))
	assert.Equal(t, map[string]any{"untouched": true}, out)
}

func TestDecode_NumbersInAnyStayExact(t *testing.T) {
	t.Parallel()
	// 9007199254740993 = 2^53+1: it does not survive a float64 round trip.
	client, _ := newStub(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":true,"data":{"totalOutputVat":9007199254740993}}`)
	})
	var out map[string]any
	require.NoError(t, client.GET(t.Context(), "/reports/tax-financial", nil, &out))
	assert.Equal(t, json.Number("9007199254740993"), out["totalOutputVat"])
}

func TestDecode_MissingStatusFieldIsAProtocolError(t *testing.T) {
	t.Parallel()
	client, _ := newStub(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{"id":"x"}}`)
	})
	err := client.GET(t.Context(), "/entities", nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `no "status" field`)
	_, isAPI := AsAPIError(err)
	assert.False(t, isAPI, "a malformed success body is not an APIError")
}

func TestDecode_NonJSONSuccessBodyIsAnError(t *testing.T) {
	t.Parallel()
	client, _ := newStub(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "not json at all")
	})
	err := client.GET(t.Context(), "/entities", nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not JSON")
	assert.Contains(t, err.Error(), "not json at all")
}

// --- pagination ------------------------------------------------------------

func TestGetPaginated(t *testing.T) {
	t.Parallel()
	client, _ := newStub(t, func(w http.ResponseWriter, _ *http.Request) {
		writeEnvelope(t, w, http.StatusOK, map[string]any{
			"status":     true,
			"data":       []map[string]any{{"id": "e-1"}, {"id": "e-2"}},
			"pagination": map[string]any{"total": 42, "limit": 2, "offset": 0},
		})
	})

	var entities []struct {
		ID string `json:"id"`
	}
	page, err := client.GetPaginated(t.Context(), "/entities?limit=2", &entities)
	require.NoError(t, err)
	assert.Len(t, entities, 2)
	assert.Equal(t, Pagination{Total: 42, Limit: 2, Offset: 0, Present: true}, page)
	assert.True(t, page.HasMore())
}

func TestGetPaginated_AbsentPaginationIsNotPresent(t *testing.T) {
	t.Parallel()
	client, _ := newStub(t, func(w http.ResponseWriter, _ *http.Request) {
		writeEnvelope(t, w, http.StatusOK, map[string]any{"status": true, "data": []any{}})
	})
	page, err := client.GetPaginated(t.Context(), "/data-templates", nil)
	require.NoError(t, err)
	assert.False(t, page.Present)
	assert.False(t, page.HasMore())
}

func TestPagination_HasMore(t *testing.T) {
	t.Parallel()
	assert.True(t, Pagination{Total: 30, Limit: 10, Offset: 10, Present: true}.HasMore())
	assert.False(t, Pagination{Total: 30, Limit: 10, Offset: 20, Present: true}.HasMore())
	assert.False(t, Pagination{Total: 30, Limit: 10, Offset: 0}.HasMore(), "absent pagination never has more")
}

// --- error mapping ---------------------------------------------------------

func TestErrorEnvelope_BecomesAPIError(t *testing.T) {
	t.Parallel()
	client, _ := newStub(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Request-Id", "req-123")
		writeEnvelope(t, w, http.StatusConflict, map[string]any{
			"status": false,
			"error": map[string]any{
				"code":    "CONFLICT",
				"message": "slug already exists",
				"details": map[string]any{"field": "slug"},
			},
		})
	})

	err := client.POST(t.Context(), "/entities", map[string]any{"name": "Acme"}, nil)
	require.Error(t, err)

	apiErr, ok := AsAPIError(err)
	require.True(t, ok)
	assert.Equal(t, http.StatusConflict, apiErr.Status)
	assert.Equal(t, "CONFLICT", apiErr.Code)
	assert.Equal(t, "slug already exists", apiErr.Message)
	assert.Equal(t, http.MethodPost, apiErr.Method)
	assert.Equal(t, "/entities", apiErr.Path)
	assert.Equal(t, "req-123", apiErr.RequestID)
	assert.JSONEq(t, `{"field":"slug"}`, string(apiErr.Details))

	assert.True(t, IsStatus(err, http.StatusConflict))
	assert.True(t, IsConflict(err))
	assert.True(t, IsCode(err, "CONFLICT"))
	assert.False(t, IsStatus(err, http.StatusNotFound))
	assert.False(t, IsCode(err, "VALIDATION"))

	// the message carries method, path, status, code and request id
	for _, want := range []string{"POST", "/entities", "409", "CONFLICT", "slug already exists", "req-123"} {
		assert.Contains(t, err.Error(), want)
	}
}

func TestErrorHelpers_CoverTheCommonStatuses(t *testing.T) {
	t.Parallel()
	for status, check := range map[int]func(error) bool{
		http.StatusNotFound:     IsNotFound,
		http.StatusUnauthorized: IsUnauthorized,
		http.StatusForbidden:    IsForbidden,
		http.StatusConflict:     IsConflict,
	} {
		err := &APIError{Status: status}
		assert.True(t, check(err), "helper for %d", status)
		assert.False(t, check(fmt.Errorf("transport: %w", io.EOF)), "helper for %d on a plain error", status)
	}
}

func TestNonEnvelopeErrorBody_KeepsSnippet(t *testing.T) {
	t.Parallel()
	client, _ := newStub(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, "<html>bad gateway</html>")
	})
	err := client.GET(t.Context(), "/entities", nil, nil)
	apiErr, ok := AsAPIError(err)
	require.True(t, ok)
	assert.Equal(t, http.StatusBadGateway, apiErr.Status)
	assert.Empty(t, apiErr.Code)
	assert.Equal(t, "Bad Gateway", apiErr.Message, "falls back to the HTTP status text")
	assert.Equal(t, "<html>bad gateway</html>", apiErr.BodySnippet)
}

func TestErrorBodySnippetIsTruncated(t *testing.T) {
	t.Parallel()
	client, _ := newStub(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, strings.Repeat("x", bodySnippetMax*3))
	})
	err := client.GET(t.Context(), "/entities", nil, nil)
	apiErr, ok := AsAPIError(err)
	require.True(t, ok)
	assert.Equal(t, strings.Repeat("x", bodySnippetMax)+"…", apiErr.BodySnippet)
}

func TestEmptyErrorBody_StillAnAPIError(t *testing.T) {
	t.Parallel()
	client, _ := newStub(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	err := client.GET(t.Context(), "/auth/me", nil, nil)
	require.True(t, IsUnauthorized(err))
}

func TestTransportFailureIsNotAnAPIError(t *testing.T) {
	t.Parallel()
	client := New("http://127.0.0.1:1") // nothing listens on port 1
	err := client.GET(t.Context(), "/entities", nil, nil)
	require.Error(t, err)
	_, ok := AsAPIError(err)
	assert.False(t, ok, "connection refused must not look like an HTTP status")
	assert.False(t, IsStatus(err, http.StatusConflict))
}

// --- headers: session cookie, and never an Origin --------------------------

func TestSessionCookieIsSentByHand(t *testing.T) {
	t.Parallel()
	client, seen := newStub(t, func(w http.ResponseWriter, _ *http.Request) {
		writeEnvelope(t, w, http.StatusOK, map[string]any{"status": true, "data": map[string]any{}})
	})

	bound := client.WithSession("tok-abc")
	require.NoError(t, bound.GET(t.Context(), "/entities", nil, nil))
	require.NoError(t, client.GET(t.Context(), "/entities", nil, nil))

	assert.Equal(t, SessionCookieName+"=tok-abc", (*seen)[0].Header.Get("Cookie"))
	assert.Empty(t, (*seen)[1].Header.Get("Cookie"), "the original client stays anonymous")
	assert.Equal(t, "tok-abc", bound.SessionToken())
	assert.Empty(t, client.SessionToken())
	assert.Equal(t, client.BaseURL(), bound.BaseURL())
}

func TestNoOriginHeaderIsEverSent(t *testing.T) {
	t.Parallel()
	client, seen := newStub(t, func(w http.ResponseWriter, _ *http.Request) {
		writeEnvelope(t, w, http.StatusOK, map[string]any{"status": true, "data": map[string]any{}})
	})
	session := client.WithSession("tok")

	require.NoError(t, session.GET(t.Context(), "/entities", nil, nil))
	require.NoError(t, session.POST(t.Context(), "/entities", map[string]any{"a": 1}, nil))
	require.NoError(t, session.PUT(t.Context(), "/entities/e-1", map[string]any{"a": 1}, nil))
	require.NoError(t, session.DELETE(t.Context(), "/entities/e-1", nil, nil))
	require.NoError(t, session.PostMultipart(t.Context(), "/workflows/w-1/documents",
		map[string]string{"label": "x"}, "file", "a.txt", []byte("hi"), nil))
	_, _, err := session.GetRaw(t.Context(), "/documents/d-1/download")
	require.NoError(t, err)

	require.Len(t, *seen, 6)
	for i, req := range *seen {
		assert.Empty(t, req.Header.Get("Origin"), "request %d (%s %s) must not carry an Origin", i, req.Method, req.Path)
		assert.Equal(t, DefaultUserAgent, req.Header.Get("User-Agent"))
	}
}

func TestWithUserAgentAndTimeout(t *testing.T) {
	t.Parallel()
	var seenAgent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAgent = r.Header.Get("User-Agent")
		writeEnvelope(t, w, http.StatusOK, map[string]any{"status": true, "data": map[string]any{}})
	}))
	defer srv.Close()

	client := New(srv.URL+"/", WithUserAgent("seed-demo/1"), WithTimeout(5*time.Second))
	assert.Equal(t, srv.URL, client.BaseURL(), "a trailing slash is trimmed")
	require.NoError(t, client.GET(t.Context(), "/entities", nil, nil))
	assert.Equal(t, "seed-demo/1", seenAgent)
	assert.Equal(t, 5*time.Second, client.http.Timeout)
}

func TestContextCancellationIsReported(t *testing.T) {
	t.Parallel()
	client, _ := newStub(t, func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(300 * time.Millisecond)
		writeEnvelope(t, w, http.StatusOK, map[string]any{"status": true, "data": map[string]any{}})
	})
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	err := client.GET(ctx, "/entities", nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "/entities")
}

// --- raw download ----------------------------------------------------------

func TestGetRaw(t *testing.T) {
	t.Parallel()
	client, _ := newStub(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("%PDF-1.4 fake"))
	})
	body, header, err := client.GetRaw(t.Context(), "/documents/d-1/download")
	require.NoError(t, err)
	assert.Equal(t, "%PDF-1.4 fake", string(body))
	assert.Equal(t, "application/pdf", header.Get("Content-Type"))
}

func TestGetRaw_ErrorStatus(t *testing.T) {
	t.Parallel()
	client, _ := newStub(t, func(w http.ResponseWriter, _ *http.Request) {
		writeEnvelope(t, w, http.StatusNotFound, map[string]any{
			"status": false,
			"error":  map[string]any{"code": "NOT_FOUND", "message": "document not found"},
		})
	})
	_, _, err := client.GetRaw(t.Context(), "/documents/nope/download")
	require.True(t, IsNotFound(err))
	assert.True(t, IsCode(err, "NOT_FOUND"))
}

// --- multipart -------------------------------------------------------------

func TestPostMultipart(t *testing.T) {
	t.Parallel()
	type parsed struct {
		fields      map[string]string
		fileName    string
		contentType string
		content     string
	}
	var got parsed

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseMultipartForm(1<<20))
		got.fields = map[string]string{}
		for k, v := range r.MultipartForm.Value {
			got.fields[k] = v[0]
		}
		file, hdr, err := r.FormFile("file")
		require.NoError(t, err)
		defer func() { _ = file.Close() }()
		content, err := io.ReadAll(file)
		require.NoError(t, err)
		got.fileName = hdr.Filename
		got.contentType = hdr.Header.Get("Content-Type")
		got.content = string(content)

		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		require.NoError(t, err)
		assert.Equal(t, "multipart/form-data", mediaType)
		assert.Empty(t, r.Header.Get("Origin"))
		assert.Equal(t, SessionCookieName+"=tok", r.Header.Get("Cookie"))

		writeEnvelope(t, w, http.StatusCreated, map[string]any{
			"status": true,
			"data":   map[string]any{"id": "doc-1"},
		})
	}))
	defer srv.Close()

	client := New(srv.URL).WithSession("tok")
	var out struct {
		ID string `json:"id"`
	}
	require.NoError(t, client.PostMultipart(t.Context(), "/workflows/w-1/documents",
		map[string]string{"documentType": "final_return", "label": `VAT "Q1"`},
		"", "de-vat-2026-m1-return.pdf", []byte("%PDF-1.4 fake"), &out))

	assert.Equal(t, "doc-1", out.ID)
	assert.Equal(t, map[string]string{"documentType": "final_return", "label": `VAT "Q1"`}, got.fields)
	assert.Equal(t, "de-vat-2026-m1-return.pdf", got.fileName)
	assert.Equal(t, "application/pdf", got.contentType)
	assert.Equal(t, "%PDF-1.4 fake", got.content)
}

func TestPostMultipart_CustomFieldNameAndQuotedFileName(t *testing.T) {
	t.Parallel()
	var partName, fileName string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reader, err := r.MultipartReader()
		require.NoError(t, err)
		part, err := reader.NextPart()
		require.NoError(t, err)
		partName, fileName = part.FormName(), part.FileName()
		writeEnvelope(t, w, http.StatusOK, map[string]any{"status": true, "data": map[string]any{}})
	}))
	defer srv.Close()

	client := New(srv.URL)
	require.NoError(t, client.PostMultipart(t.Context(), "/documents/d-1/versions",
		nil, "attachment", `weird "name".csv`, []byte("a,b\n1,2\n"), nil))
	assert.Equal(t, "attachment", partName)
	assert.Equal(t, `weird "name".csv`, fileName)
}

func TestPostMultipart_ErrorResponse(t *testing.T) {
	t.Parallel()
	client, _ := newStub(t, func(w http.ResponseWriter, _ *http.Request) {
		writeEnvelope(t, w, http.StatusBadRequest, map[string]any{
			"status": false,
			"error":  map[string]any{"code": "INVALID_BODY", "message": "file is required"},
		})
	})
	err := client.PostMultipart(t.Context(), "/workflows/w-1/documents", nil, "file", "x.txt", nil, nil)
	require.Error(t, err)
	assert.True(t, IsCode(err, "INVALID_BODY"))
	assert.True(t, IsStatus(err, http.StatusBadRequest))
}

func TestContentTypeFor(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "application/pdf", ContentTypeFor("a.PDF", nil))
	assert.Equal(t, "text/plain; charset=utf-8", ContentTypeFor("a.txt", []byte("hi")))
	assert.Equal(t, "text/csv; charset=utf-8", ContentTypeFor("a.csv", []byte("a,b")))
	assert.Equal(t, "application/octet-stream", ContentTypeFor("noext", nil))
	// unknown extension with content falls back to sniffing
	assert.True(t, strings.HasPrefix(ContentTypeFor("a.weird", []byte("plain text")), "text/plain"))
}

func TestMultipartBodyIsWellFormed(t *testing.T) {
	t.Parallel()
	var body []byte
	var contentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		contentType = r.Header.Get("Content-Type")
		writeEnvelope(t, w, http.StatusOK, map[string]any{"status": true, "data": map[string]any{}})
	}))
	defer srv.Close()

	require.NoError(t, New(srv.URL).PostMultipart(t.Context(), "/x",
		map[string]string{"label": "L"}, "file", "f.txt", []byte("payload"), nil))

	_, params, err := mime.ParseMediaType(contentType)
	require.NoError(t, err)
	reader := multipart.NewReader(strings.NewReader(string(body)), params["boundary"])
	form, err := reader.ReadForm(1 << 20)
	require.NoError(t, err)
	assert.Equal(t, "L", form.Value["label"][0])
	require.Len(t, form.File["file"], 1)
	assert.Equal(t, "f.txt", form.File["file"][0].Filename)
}
