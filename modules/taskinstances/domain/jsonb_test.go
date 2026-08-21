package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaxData_ValueScanRoundTrip(t *testing.T) {
	t.Parallel()

	original := TaxData{"vatAmount": float64(5000), "currency": "EUR", "filed": true}

	v, err := original.Value()
	require.NoError(t, err)

	var scanned TaxData
	require.NoError(t, scanned.Scan(v))
	assert.Equal(t, original, scanned)
}

func TestTaxData_NilValueIsNull(t *testing.T) {
	t.Parallel()

	var d TaxData
	v, err := d.Value()
	require.NoError(t, err)
	assert.Nil(t, v)

	require.NoError(t, d.Scan(nil))
	assert.Nil(t, d)
}

func TestTaxData_ScanUnsupportedType(t *testing.T) {
	t.Parallel()

	var d TaxData
	assert.Error(t, d.Scan(3.14))
}
