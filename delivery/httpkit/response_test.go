package httpkit

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOk(t *testing.T) {
	t.Parallel()

	data := map[string]string{"id": "1"}
	resp := Ok(data)

	require.NotNil(t, resp)
	assert.Equal(t, 200, resp.Status)
	assert.Equal(t, data, resp.Data)
	assert.Nil(t, resp.Pagination)
}

func TestCreated(t *testing.T) {
	t.Parallel()

	data := map[string]string{"id": "2"}
	resp := Created(data)

	require.NotNil(t, resp)
	assert.Equal(t, 201, resp.Status)
	assert.Equal(t, data, resp.Data)
}

func TestAccepted(t *testing.T) {
	t.Parallel()

	data := "queued"
	resp := Accepted(data)

	require.NotNil(t, resp)
	assert.Equal(t, 202, resp.Status)
	assert.Equal(t, "queued", resp.Data)
}

func TestNoContent(t *testing.T) {
	t.Parallel()

	resp := NoContent()

	require.NotNil(t, resp)
	assert.Equal(t, 204, resp.Status)
	assert.Nil(t, resp.Data)
}

func TestOk_NilData(t *testing.T) {
	t.Parallel()

	resp := Ok(nil)
	assert.Equal(t, 200, resp.Status)
	assert.Nil(t, resp.Data)
}
