package domain

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckContentType(t *testing.T) {
	t.Parallel()
	pdf := []byte("%PDF-1.7\n%âãÏÓ\n1 0 obj")
	png := append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, make([]byte, 20)...)
	zip := append([]byte{'P', 'K', 0x03, 0x04}, make([]byte, 30)...)
	text := []byte("name,amount\nVAT,100\n")
	xml := []byte(`<?xml version="1.0"?><root/>`)
	binary := append([]byte{0x00, 0x01, 0x02, 0xff}, make([]byte, 40)...)

	cases := []struct {
		name     string
		declared string
		head     []byte
		want     string
		wantErr  bool
		sniffed  string
	}{
		{"pdf ok", "application/pdf", pdf, "application/pdf", false, ""},
		{"pdf with params", "Application/PDF; charset=binary", pdf, "application/pdf", false, ""},
		{"png ok", "image/png", png, "image/png", false, ""},
		{"png declared as jpeg", "image/jpeg", png, "", true, "image/png"},
		{"pdf declared as text", "text/plain", pdf, "", true, "application/pdf"},
		{"docx sniffs as zip (generic)", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", zip, "application/vnd.openxmlformats-officedocument.wordprocessingml.document", false, ""},
		{"csv sniffs as text (generic)", "text/csv", text, "text/csv", false, ""},
		{"json sniffs as text (generic)", "application/json", []byte(`{"a":1}`), "application/json", false, ""},
		{"xml declared application/xml", "application/xml", xml, "application/xml", false, ""},
		{"xml declared text/xml", "text/xml", xml, "text/xml", false, ""},
		{"empty declared → octet-stream", "", binary, "application/octet-stream", false, ""},
		{"octet-stream with binary", "application/octet-stream", binary, "application/octet-stream", false, ""},
		{"not allowlisted", "application/x-msdownload", binary, "", true, ""},
		{"html not allowlisted", "text/html", []byte("<html>"), "", true, ""},
		{"empty body", "text/plain", nil, "text/plain", false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := CheckContentType(tc.declared, tc.head)
			if tc.wantErr {
				require.Error(t, err)
				var umt *UnsupportedMediaTypeError
				require.True(t, errors.As(err, &umt))
				assert.Equal(t, tc.sniffed, umt.Sniffed)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestVocabulary(t *testing.T) {
	t.Parallel()
	assert.Len(t, DocumentTypes, 10)
	assert.True(t, IsDocumentType("draft_return"))
	assert.True(t, IsDocumentType("other"))
	assert.False(t, IsDocumentType("invoice"))
	assert.True(t, IsCategory("compliance"))
	assert.True(t, IsCategory("project"))
	assert.False(t, IsCategory("misc"))
}
