package documents_test

import "encoding/json"

func jsonUnmarshal(body string, v any) error {
	return json.Unmarshal([]byte(body), v)
}
