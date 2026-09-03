package swagger

import (
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/mohamadhallal/zentax-api/delivery/httpkit/routing"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
)

const (
	schemaTypeString  = "string"
	schemaTypeInteger = "integer"
	schemaTypeObject  = "object"
	schemaTypeNumber  = "number"
	schemaTypeBoolean = "boolean"
	schemaTypeArray   = "array"
)

var timeType = reflect.TypeOf(time.Time{})

func Generate(routes []routing.RouteMeta, mode types.ServerMode, cfg Config) Spec {
	spec := Spec{
		OpenAPI: "3.0.3",
		Info:    buildInfo(mode, cfg),
		Paths:   make(map[string]PathItem),
	}

	if cfg.BaseURL != "" {
		spec.Servers = []Server{{URL: cfg.BaseURL}}
	}

	// Security schemes mirror the real auth middleware (RequireAuth): humans
	// authenticate with the server-side session cookie (ADR-0011), machines
	// with a bearer API token (agentic-AI B1). Internal mode keeps the
	// boilerplate's basic-auth service credentials.
	cookieName := cfg.SessionCookieName
	if cookieName == "" {
		cookieName = "zentax_session"
	}
	if mode == types.ModeExternal {
		spec.Components = &Components{
			SecuritySchemes: map[string]SecurityScheme{
				"sessionCookie": {
					Type: "apiKey", In: "cookie", Name: cookieName,
					Description: "Server-side session for human users, set by POST /auth/login (httpOnly; MFA-gated when enrolled).",
				},
				"bearerToken": {
					Type: "http", Scheme: "bearer", BearerFormat: "ztx_...",
					Description: "Service-account API token (machine identity), issued via POST /service-accounts/{id}/tokens. Machines are denied human-only capabilities (task:approve).",
				},
			},
			Schemas: envelopeSchemas(),
		}
	} else {
		spec.Components = &Components{
			SecuritySchemes: map[string]SecurityScheme{
				"basicAuth": {Type: "http", Scheme: "basic", Description: "API key as username, SHA512-hashed secret as password"},
			},
			Schemas: envelopeSchemas(),
		}
	}

	tagSeen := map[string]bool{}

	for _, rm := range routes {
		path := chiPathToOpenAPI(rm.FullPath)
		method := strings.ToLower(rm.Definition.Method)

		tag := extractTag(rm.FullPath)
		summary := buildSummary(rm.Definition.Method, rm.FullPath)

		if !tagSeen[tag] {
			tagSeen[tag] = true
			spec.Tags = append(spec.Tags, Tag{Name: tag})
		}

		op := &Operation{
			Tags:                []string{tag},
			Summary:             summary,
			OperationID:         buildOperationID(rm.Definition.Method, rm.FullPath),
			Responses:           buildResponses(rm.Definition),
			XRequiredCapability: string(rm.Definition.Capability),
		}

		// Tenant routes require an authenticated principal: a human session
		// cookie OR a service-account bearer token (alternatives, not both).
		if rm.Definition.Tenant && mode == types.ModeExternal {
			op.Security = []SecurityReq{{"sessionCookie": {}}, {"bearerToken": {}}}
		}
		if rm.Definition.Auth && mode == types.ModeInternal {
			op.Security = []SecurityReq{{"basicAuth": {}}}
		}

		if rm.Schema != nil {
			if rm.Schema.Params != nil {
				op.Parameters = append(op.Parameters, structToParams(rm.Schema.Params, "path")...)
			}
			if rm.Schema.Query != nil {
				op.Parameters = append(op.Parameters, structToParams(rm.Schema.Query, "query")...)
			}
			if rm.Schema.Body != nil {
				op.RequestBody = &RequestBody{
					Required: true,
					Content: map[string]Content{
						"application/json": {Schema: structToSchema(rm.Schema.Body)},
					},
				}
			}
		} else {
			params := extractPathParams(rm.FullPath)
			for _, p := range params {
				op.Parameters = append(op.Parameters, Parameter{
					Name: p, In: "path", Required: true,
					Schema: Schema{Type: schemaTypeString},
				})
			}
		}

		if _, ok := spec.Paths[path]; !ok {
			spec.Paths[path] = make(PathItem)
		}
		spec.Paths[path][method] = op
	}

	return spec
}

func buildInfo(mode types.ServerMode, cfg Config) Info {
	title := cfg.Title
	if title == "" {
		title = "API"
	}

	desc := cfg.Description
	if desc == "" {
		switch mode {
		case types.ModeExternal:
			desc = "ZenTax tenant API. Humans authenticate with a server-side session cookie (POST /auth/login, MFA-gated); " +
				"machines with a service-account bearer token (ztx_...). Every operation lists its required RBAC capability " +
				"as x-required-capability; writes are additionally scoped to the principal's entity subtree, and approval " +
				"actions are human-only."
		case types.ModeInternal:
			desc = "Internal service-to-service API. Authenticated via HTTP Basic using API key and hashed secret."
		}
	}

	version := cfg.Version
	if version == "" {
		version = "1.0.0"
	}

	return Info{
		Title:       title + " (" + string(mode) + ")",
		Description: desc,
		Version:     version,
	}
}

func chiPathToOpenAPI(path string) string {
	result := strings.Builder{}
	i := 0
	for i < len(path) {
		if path[i] == '{' {
			end := strings.IndexByte(path[i:], '}')
			if end == -1 {
				result.WriteByte(path[i])
				i++
				continue
			}
			result.WriteString(path[i : i+end+1])
			i += end + 1
		} else {
			result.WriteByte(path[i])
			i++
		}
	}
	return result.String()
}

func extractPathParams(path string) []string {
	var params []string
	for _, seg := range strings.Split(path, "/") {
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			params = append(params, seg[1:len(seg)-1])
		}
	}
	return params
}

func extractTag(path string) string {
	trimmed := strings.TrimPrefix(path, "/")
	if trimmed == "" {
		return "root"
	}
	parts := strings.SplitN(trimmed, "/", 2)
	return capitalize(parts[0])
}

func buildSummary(method, path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	var meaningful []string
	for _, p := range parts {
		if !strings.HasPrefix(p, "{") {
			meaningful = append(meaningful, p)
		}
	}
	resource := strings.Join(meaningful, " ")
	if resource == "" {
		resource = "root"
	}

	switch method {
	case http.MethodGet:
		if hasPathParam(path) {
			return "Get " + resource
		}
		return "List " + resource
	case http.MethodPost:
		return "Create " + resource
	case http.MethodPut:
		return "Replace " + resource
	case http.MethodPatch:
		return "Update " + resource
	case http.MethodDelete:
		return "Delete " + resource
	}
	return method + " " + resource
}

func hasPathParam(path string) bool {
	return strings.Contains(path, "{")
}

func buildOperationID(method, path string) string {
	clean := strings.NewReplacer("/", "_", "{", "", "}", "").Replace(path)
	clean = strings.Trim(clean, "_")
	return strings.ToLower(method) + "_" + clean
}

// buildResponses declares the response contract per route. Success bodies use
// the shared envelope component schemas; error statuses reflect the middleware
// chain (401/403 on authenticated routes, 404 on id-addressed routes, 409 on
// tenant mutations — duplicates, already-started, approval-locked). Success
// codes follow the handler convention: POST creates → 201, action-style POSTs
// on an id (submit/approve/start) may return 200, DELETE → 204, else 200.
func buildResponses(def types.RouteDefinition) map[string]Response {
	success := envelopeContent("SuccessEnvelope")
	if def.Paginated {
		success = envelopeContent("PaginatedEnvelope")
	}

	responses := map[string]Response{
		"400": errorResponse("Bad request (validation or malformed input)"),
		"500": errorResponse("Internal server error"),
	}

	switch def.Method {
	case http.MethodPost:
		responses["201"] = Response{Description: "Created", Content: success}
		if hasPathParam(def.Path) {
			// Action-style POSTs (…/{id}/approve, …/{id}/start) return 200.
			responses["200"] = Response{Description: "Success", Content: success}
		}
	case http.MethodDelete:
		responses["204"] = Response{Description: "No content"}
	default:
		responses["200"] = Response{Description: "Success", Content: success}
	}

	if def.Tenant {
		responses["401"] = errorResponse("Not authenticated (missing or invalid session / API token)")
		responses["403"] = errorResponse("Not authorized (missing capability, out-of-scope entity, or human-only action)")
		switch def.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch:
			responses["409"] = errorResponse("Conflict (duplicate, already started, or locked by approval)")
		}
	}
	if hasPathParam(def.Path) {
		responses["404"] = errorResponse("Not found (or not visible in this tenant)")
	}

	return responses
}

func envelopeContent(name string) map[string]Content {
	return map[string]Content{
		"application/json": {Schema: Schema{Ref: "#/components/schemas/" + name}},
	}
}

func errorResponse(description string) Response {
	return Response{
		Description: description,
		Content:     envelopeContent("ErrorEnvelope"),
	}
}

// envelopeSchemas are the shared response envelopes every endpoint uses. The
// `data` payload is deliberately untyped at this stage — typed response DTOs
// land with the client-generation pass.
func envelopeSchemas() map[string]Schema {
	intSchema := Schema{Type: schemaTypeInteger}
	return map[string]Schema{
		"SuccessEnvelope": {
			Type: schemaTypeObject,
			Properties: map[string]Schema{
				"status": {Type: schemaTypeBoolean, Example: true},
				"data":   {}, // any
			},
			Required: []string{"status", "data"},
		},
		"PaginatedEnvelope": {
			Type: schemaTypeObject,
			Properties: map[string]Schema{
				"status": {Type: schemaTypeBoolean, Example: true},
				"data":   {Type: "array", Items: &Schema{}},
				"pagination": {
					Type: schemaTypeObject,
					Properties: map[string]Schema{
						"total": intSchema, "limit": intSchema, "offset": intSchema,
					},
					Required: []string{"total", "limit", "offset"},
				},
			},
			Required: []string{"status", "data", "pagination"},
		},
		"ErrorEnvelope": {
			Type: schemaTypeObject,
			Properties: map[string]Schema{
				"status": {Type: schemaTypeBoolean, Example: false},
				"error": {
					Type: schemaTypeObject,
					Properties: map[string]Schema{
						"code":    {Type: schemaTypeString, Example: "FORBIDDEN"},
						"message": {Type: schemaTypeString},
					},
					Required: []string{"code", "message"},
				},
			},
			Required: []string{"status", "error"},
		},
	}
}

func structToSchema(v any) Schema {
	t := reflect.TypeOf(v)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return Schema{Type: schemaTypeObject}
	}
	return structTypeToSchema(t)
}

func structTypeToSchema(t reflect.Type) Schema {
	schema := Schema{
		Type:       schemaTypeObject,
		Properties: make(map[string]Schema),
	}

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		name := fieldName(field)
		if name == "-" {
			continue
		}

		prop := fieldToSchema(field)
		if ex := field.Tag.Get("example"); ex != "" {
			prop.Example = coerceExample(ex, prop.Type)
		}
		schema.Properties[name] = prop

		validate := field.Tag.Get("validate")
		if isRequired(validate) {
			schema.Required = append(schema.Required, name)
		}
	}

	return schema
}

func structToParams(v any, in string) []Parameter {
	t := reflect.TypeOf(v)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}

	explodeTrue := true

	var params []Parameter
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		name := fieldName(field)
		if name == "-" {
			continue
		}

		ft := field.Type
		if ft.Kind() == reflect.Ptr {
			ft = ft.Elem()
		}

		validate := field.Tag.Get("validate")

		if ft.Kind() == reflect.Slice && ft.Elem().Kind() == reflect.String {
			itemSchema := Schema{Type: schemaTypeString}
			applyValidationConstraints(&itemSchema, validate)
			p := Parameter{
				Name:     name,
				In:       in,
				Required: in == "path" || isRequired(validate),
				Style:    "form",
				Explode:  &explodeTrue,
				Schema:   Schema{Type: "array", Items: &itemSchema},
			}
			if ex := field.Tag.Get("example"); ex != "" {
				p.Example = ex
			}
			params = append(params, p)
			continue
		}

		p := Parameter{
			Name:     name,
			In:       in,
			Required: in == "path" || isRequired(validate),
			Schema:   fieldToSchema(field),
		}

		if def := field.Tag.Get("default"); def != "" {
			p.Schema.Default = def
		}

		if ex := field.Tag.Get("example"); ex != "" {
			p.Example = coerceExample(ex, p.Schema.Type)
		}

		params = append(params, p)
	}

	return params
}

// fieldToSchema renders a struct field, following the Go type into nested
// objects and arrays so JSONB-backed value types (a []string of period codes,
// a due-date-rule struct, a list of document requirements) surface with their
// real shape instead of collapsing to "string". Validator rules before `dive`
// describe the field itself; rules after `dive` describe each array element.
func fieldToSchema(field reflect.StructField) Schema {
	s := typeToSchema(field.Type)

	validate := field.Tag.Get("validate")
	if s.Type == schemaTypeArray && s.Items != nil {
		// Pre-dive min=/max= are item counts, which Schema does not model.
		if _, inner, hasDive := strings.Cut(validate, "dive"); hasDive {
			applyValidationConstraints(s.Items, strings.Trim(inner, ", "))
		}
		return s
	}
	applyValidationConstraints(&s, validate)
	return s
}

func typeToSchema(t reflect.Type) Schema {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t == timeType {
		return Schema{Type: schemaTypeString, Format: "date-time"}
	}

	switch t.Kind() {
	case reflect.String:
		return Schema{Type: schemaTypeString}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32:
		return Schema{Type: schemaTypeInteger}
	case reflect.Int64:
		return Schema{Type: schemaTypeInteger, Format: "int64"}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return Schema{Type: schemaTypeInteger}
	case reflect.Float32, reflect.Float64:
		return Schema{Type: schemaTypeNumber}
	case reflect.Bool:
		return Schema{Type: schemaTypeBoolean}
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 { // []byte travels as a string
			return Schema{Type: schemaTypeString}
		}
		items := typeToSchema(t.Elem())
		return Schema{Type: schemaTypeArray, Items: &items}
	case reflect.Struct:
		return structTypeToSchema(t)
	case reflect.Map:
		values := typeToSchema(t.Elem())
		return Schema{Type: schemaTypeObject, AdditionalProperties: &values}
	case reflect.Interface:
		return Schema{Type: schemaTypeObject}
	default:
		return Schema{Type: schemaTypeString}
	}
}

func applyValidationConstraints(s *Schema, validate string) {
	if validate == "" {
		return
	}
	rules := strings.Split(validate, ",")
	for _, rule := range rules {
		rule = strings.TrimSpace(rule)
		if strings.HasPrefix(rule, "oneof=") {
			values := strings.Fields(strings.TrimPrefix(rule, "oneof="))
			s.Enum = values
		}
		if strings.HasPrefix(rule, "max=") {
			if v := parseIntTag(rule, "max="); v != nil {
				if s.Type == schemaTypeString {
					s.MaxLength = v
				} else {
					f := float64(*v)
					s.Maximum = &f
				}
			}
		}
		if strings.HasPrefix(rule, "min=") {
			if v := parseIntTag(rule, "min="); v != nil {
				if s.Type == schemaTypeString {
					s.MinLength = v
				} else {
					f := float64(*v)
					s.Minimum = &f
				}
			}
		}
		if strings.HasPrefix(rule, "len=") {
			if v := parseIntTag(rule, "len="); v != nil {
				s.MinLength = v
				s.MaxLength = v
			}
		}
		if rule == "email" {
			s.Format = "email"
		}
		if rule == "uuid" {
			s.Format = "uuid"
		}
		if strings.HasPrefix(rule, "gt=") {
			if v := parseIntTag(rule, "gt="); v != nil {
				excl := float64(*v)
				s.Minimum = &excl
			}
		}
	}
}

func parseIntTag(rule, prefix string) *int {
	val := strings.TrimPrefix(rule, prefix)
	n := 0
	for _, ch := range val {
		if ch < '0' || ch > '9' {
			return nil
		}
		n = n*10 + int(ch-'0')
	}
	return &n
}

func fieldName(f reflect.StructField) string {
	tag := f.Tag.Get("json")
	if tag == "" {
		return f.Name
	}
	parts := strings.SplitN(tag, ",", 2)
	if parts[0] == "" {
		return f.Name
	}
	return parts[0]
}

func isRequired(validate string) bool {
	for _, rule := range strings.Split(validate, ",") {
		if rule == "required" {
			return true
		}
	}
	return false
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func coerceExample(raw, schemaType string) (result any) {
	defer func() {
		if recover() != nil {
			result = raw
		}
	}()

	switch schemaType {
	case schemaTypeInteger:
		n := 0
		for _, ch := range raw {
			if ch < '0' || ch > '9' {
				return raw
			}
			n = n*10 + int(ch-'0')
		}
		return n
	case schemaTypeNumber:
		for _, ch := range raw {
			if (ch < '0' || ch > '9') && ch != '.' && ch != '-' {
				return raw
			}
		}
		if v := parseIntTag(raw, ""); v != nil {
			return *v
		}
		return raw
	case schemaTypeBoolean:
		return raw == "true"
	default:
		return raw
	}
}
