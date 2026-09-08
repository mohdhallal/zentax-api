package routing

import (
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"

	gochi "github.com/go-chi/chi/v5"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	sharedtypes "github.com/mohamadhallal/zentax-api/shared/types"
)

func writeResponse(w http.ResponseWriter, r *http.Request, response *types.HttpResponse) {
	// A nil response means the handler wrote the reply itself (streamed
	// download): nothing to envelope.
	if response == nil {
		return
	}
	requestId := app.GetRequestId(r.Context())
	if requestId != "" {
		w.Header().Set("X-Request-Id", requestId)
	}

	if response.Headers != nil {
		for key, value := range response.Headers {
			w.Header().Set(key, value)
		}
	}

	if response.Data == nil {
		w.WriteHeader(response.Status)
		return
	}

	body := map[string]any{
		"status": true,
		"data":   response.Data,
	}
	if response.Pagination != nil {
		body["pagination"] = response.Pagination
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(response.Status)
	_ = json.NewEncoder(w).Encode(body)
}

func extractQuery(r *http.Request) map[string][]string {
	return r.URL.Query()
}

func extractPagination(query any, sortColMap map[string]string) *sharedtypes.ListArgs {
	v := reflect.ValueOf(query)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return &sharedtypes.ListArgs{}
	}

	args := &sharedtypes.ListArgs{}
	t := v.Type()

	for i := range t.NumField() {
		field := t.Field(i)
		fieldVal := v.Field(i)
		jsonTag := strings.SplitN(field.Tag.Get("json"), ",", 2)[0]

		switch jsonTag {
		case "limit":
			if fieldVal.Kind() == reflect.Int {
				args.Limit = int(fieldVal.Int())
			}
		case "offset":
			if fieldVal.Kind() == reflect.Int {
				args.Offset = int(fieldVal.Int())
			}
		case "sort":
			if fieldVal.Kind() == reflect.Slice {
				args.Sort = parseSortFields(fieldVal, sortColMap)
			}
		}

		if col := field.Tag.Get("filter"); col != "" {
			switch fieldVal.Kind() {
			case reflect.Ptr:
				if fieldVal.IsNil() {
					continue
				}
				fieldVal = fieldVal.Elem()
			case reflect.Slice:
				// A []string bound from repeated query params (?status=a&status=b)
				// travels as ONE multi-value filter whose value is the slice; the
				// repository renders it as `col = ANY($n)`. Nothing bound → no filter.
				if fieldVal.Len() == 0 {
					continue
				}
			default:
				if fieldVal.IsZero() {
					continue
				}
			}
			args.Filters = append(args.Filters, sharedtypes.Filter{Column: col, Value: fieldVal.Interface()})
		}
	}

	return args
}

func parseSortFields(sliceVal reflect.Value, colMap map[string]string) []sharedtypes.SortField {
	var result []sharedtypes.SortField
	for i := range sliceVal.Len() {
		entry := sliceVal.Index(i).String()
		field, dir, _ := strings.Cut(entry, ":")
		col, ok := colMap[field]
		if !ok {
			continue
		}
		result = append(result, sharedtypes.SortField{Column: col, Desc: dir != "asc"})
	}
	return result
}

func extractParams(r *http.Request) map[string]string {
	params := make(map[string]string)
	rctx := gochi.RouteContext(r.Context())
	if rctx != nil {
		for i, key := range rctx.URLParams.Keys {
			if i < len(rctx.URLParams.Values) {
				params[key] = rctx.URLParams.Values[i]
			}
		}
	}
	return params
}

func parseAndSanitizeBody(r *http.Request) any {
	if r.Body == nil || r.ContentLength == 0 {
		return nil
	}
	data, err := io.ReadAll(r.Body)
	if err != nil || len(data) == 0 {
		return nil
	}
	var body any
	_ = json.Unmarshal(data, &body)
	return sanitize(body)
}

func sanitize(data any) any {
	if data == nil {
		return nil
	}
	switch v := data.(type) {
	case string:
		return strings.TrimSpace(v)
	case map[string]any:
		result := make(map[string]any, len(v))
		for key, val := range v {
			result[key] = sanitize(val)
		}
		return result
	case []any:
		result := make([]any, len(v)) //nolint:makezero // indexed assignment
		for i, val := range v {
			result[i] = sanitize(val)
		}
		return result
	default:
		return data
	}
}
