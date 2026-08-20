package acceptance

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type TestResponse struct {
	*http.Response
	body []byte
}

func (r *TestResponse) AssertStatus(t *testing.T, code int) {
	t.Helper()
	require.Equal(t, code, r.StatusCode,
		"unexpected status; body: %s", string(r.body))
}

func (r *TestResponse) DecodeData(t *testing.T, v any) {
	t.Helper()
	var env struct {
		Status bool            `json:"status"`
		Data   json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(r.body, &env))
	require.True(t, env.Status, "response status=false; body: %s", string(r.body))
	require.NoError(t, json.Unmarshal(env.Data, v))
}

func (r *TestResponse) AssertErrorCode(t *testing.T, code string) {
	t.Helper()
	var env struct {
		Status bool `json:"status"`
		Error  struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(r.body, &env))
	require.False(t, env.Status, "expected error response; body: %s", string(r.body))
	require.Equal(t, code, env.Error.Code,
		"unexpected error code; body: %s", string(r.body))
}

func (r *TestResponse) BodyString() string {
	return strings.TrimSpace(string(r.body))
}
