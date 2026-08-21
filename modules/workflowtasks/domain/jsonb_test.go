package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDocumentRequirements_ValueScanRoundTrip(t *testing.T) {
	t.Parallel()

	desc := "Signed PDF"
	original := DocumentRequirements{
		{Name: "VAT ledger", Required: true},
		{Name: "Invoice", Description: &desc, Required: false},
	}

	v, err := original.Value()
	require.NoError(t, err)

	var scanned DocumentRequirements
	require.NoError(t, scanned.Scan(v))
	assert.Equal(t, original, scanned)
}

func TestDocumentRequirements_NilAndData(t *testing.T) {
	t.Parallel()

	var d DocumentRequirements // nil
	v, err := d.Value()
	require.NoError(t, err)
	assert.Equal(t, []byte("[]"), v)

	require.NoError(t, d.Scan(nil))
	assert.Equal(t, DocumentRequirements{}, d)

	require.NoError(t, d.Scan([]byte(`[{"name":"X","required":true}]`)))
	require.Len(t, d, 1)
	assert.Equal(t, "X", d[0].Name)
	assert.True(t, d[0].Required)
}

func TestDocumentRequirements_ScanUnsupportedType(t *testing.T) {
	t.Parallel()

	var d DocumentRequirements
	assert.Error(t, d.Scan(99))
}
