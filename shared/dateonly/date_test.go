package dateonly

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDate_String(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "2025-01-31", New(2025, 1, 31).String())
	assert.Equal(t, "2025-03-05", New(2025, 3, 5).String())
}

func TestDate_ParseAndZero(t *testing.T) {
	t.Parallel()
	d, err := Parse("2025-01-31")
	require.NoError(t, err)
	assert.Equal(t, New(2025, 1, 31), d)

	// tolerate a time suffix (ADR-0002: keep only the calendar date)
	d2, err := Parse("2025-01-31T00:00:00Z")
	require.NoError(t, err)
	assert.Equal(t, New(2025, 1, 31), d2)

	_, err = Parse("nope")
	assert.Error(t, err)

	assert.True(t, Date{}.IsZero())
}

func TestDate_JSON(t *testing.T) {
	t.Parallel()
	b, err := json.Marshal(New(2025, 12, 31))
	require.NoError(t, err)
	assert.Equal(t, `"2025-12-31"`, string(b))

	var d Date
	require.NoError(t, json.Unmarshal([]byte(`"2025-12-31"`), &d))
	assert.Equal(t, New(2025, 12, 31), d)

	var d2 Date
	require.NoError(t, json.Unmarshal([]byte(`null`), &d2))
	assert.True(t, d2.IsZero())
}

func TestDate_ValueScan(t *testing.T) {
	t.Parallel()
	v, err := New(2025, 1, 31).Value()
	require.NoError(t, err)
	assert.Equal(t, "2025-01-31", v)

	// a Postgres `date` comes back from pgx as time.Time
	var d Date
	require.NoError(t, d.Scan(time.Date(2025, 1, 31, 0, 0, 0, 0, time.UTC)))
	assert.Equal(t, New(2025, 1, 31), d)

	// or a string
	var d2 Date
	require.NoError(t, d2.Scan("2025-06-30"))
	assert.Equal(t, New(2025, 6, 30), d2)

	// nil
	var d3 Date
	require.NoError(t, d3.Scan(nil))
	assert.True(t, d3.IsZero())
}
