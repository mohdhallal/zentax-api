package acceptance

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// MultipartFile describes the file part of a multipart upload.
type MultipartFile struct {
	Field       string // form field name (the API expects "file")
	FileName    string
	ContentType string // declared part Content-Type; "" = none
	Content     []byte
}

// POSTMultipart sends a multipart/form-data request with the given text
// fields and one file part (document uploads, ADR-0022).
func (b *RequestBuilder) POSTMultipart(t *testing.T, path string, fields map[string]string, file MultipartFile) *TestResponse {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		require.NoError(t, mw.WriteField(k, v))
	}
	field := file.Field
	if field == "" {
		field = "file"
	}
	hdr := textproto.MIMEHeader{}
	hdr.Set("Content-Disposition", `form-data; name="`+field+`"; filename="`+escapeQuotes(file.FileName)+`"`)
	if file.ContentType != "" {
		hdr.Set("Content-Type", file.ContentType)
	}
	part, err := mw.CreatePart(hdr)
	require.NoError(t, err)
	_, err = part.Write(file.Content)
	require.NoError(t, err)
	require.NoError(t, mw.Close())

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, b.baseURL+path, &buf)
	require.NoError(t, err)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	for k, vals := range b.headers {
		for _, v := range vals {
			req.Header.Set(k, v)
		}
	}

	resp, err := b.client.Do(req)
	require.NoError(t, err)
	respBody, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.NoError(t, err)
	return &TestResponse{Response: resp, body: respBody}
}

// Bytes returns the raw response body (binary downloads).
func (r *TestResponse) Bytes() []byte {
	return r.body
}

func escapeQuotes(s string) string {
	return strings.NewReplacer("\\", "\\\\", `"`, "\\\"").Replace(s)
}
