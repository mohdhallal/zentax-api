package swagger

import (
	"net/http"
	"reflect"
	"strings"

	"github.com/mohamadhallal/zentax-api/delivery/httpkit/routing"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
)

const (
	schemaTypeString  = "string"
	schemaTypeInteger = "integer"
	schemaTypeObject  = "object"
	schemaTypeNumber  = "number"
	schemaTypeBoolean = "boolean"
)

func Generate(routes []routing.RouteMeta, mode types.ServerMode, cfg Config) Spec {
	spec := Spec{
		OpenAPI: "3.0.3",
		Info:    buildInfo(mode, cfg),
		Paths:   make(map[string]PathItem),
	}

	if cfg.BaseURL != "" {
		spec.Servers = []Server{{URL: cfg.BaseURL}}
	}

	if mode == types.ModeExternal {
		spec.Components = &Components{
			SecuritySchemes: map[string]SecurityScheme{
				"gatewayAccountId":     gatewayHeaderSecurityScheme("X-Account-Id", "Authenticated account ID injected by the upstream auth gateway"),
				"gatewayAPIKeyId":      gatewayHeaderSecurityScheme("X-API-Key-Id", "API key ID injected by the upstream auth gateway"),
				"gatewayCustomerId":    gatewayHeaderSecurityScheme("X-Customer-Id", "Customer ID injected by the upstream auth gateway"),
				"gatewayCorrelationId": gatewayHeaderSecurityScheme("X-Correlation-Id", "Correlation ID injected by the upstream auth gateway"),
				"gatewayClientIP":      gatewayHeaderSecurityScheme("X-Client-IP", "Client IP injected by the upstream auth gateway"),
			},
		}
	} else {
		spec.Components = &Components{
			SecuritySchemes: map[string]SecurityScheme{
				"basicAuth": {Type: "http", Scheme: "basic", Description: "API key as username, SHA512-hashed secret as password"},
			},
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
			Tags:        []string{tag},
			Summary:     summary,
			OperationID: buildOperationID(rm.Definition.Method, rm.FullPath),
			Responses:   defaultResponses(rm.Definition.Method),
		}

		if rm.Definition.Auth {
			if mode == types.ModeExternal {
				op.Security = []SecurityReq{gatewayHeaderSecurityReq()}
			} else {
				op.Security = []SecurityReq{{"basicAuth": {}}}
			}
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
			desc = "Public-facing API for client applications. Authenticated by the upstream gateway using injected request headers."
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

func defaultResponses(method string) map[string]Response {
	responses := map[string]Response{
		"400": {Description: "Bad request"},
		"500": {Description: "Internal server error"},
	}

	switch method {
	case http.MethodPost:
		responses["201"] = Response{
			Description: "Created",
			Content:     jsonResponseContent(),
		}
	case http.MethodDelete:
		responses["204"] = Response{Description: "No content"}
	default:
		responses["200"] = Response{
			Description: "Success",
			Content:     jsonResponseContent(),
		}
	}

	return responses
}

func jsonResponseContent() map[string]Content {
	return map[string]Content{
		"application/json": {
			Schema: Schema{
				Type: schemaTypeObject,
				Properties: map[string]Schema{
					"status": {Type: schemaTypeBoolean},
					"data":   {Type: schemaTypeObject},
				},
			},
		},
	}
}

func gatewayHeaderSecurityScheme(name, description string) SecurityScheme {
	return SecurityScheme{
		Type:        "apiKey",
		In:          "header",
		Name:        name,
		Description: description,
	}
}

func gatewayHeaderSecurityReq() SecurityReq {
	return SecurityReq{
		"gatewayAccountId":     {},
		"gatewayAPIKeyId":      {},
		"gatewayCustomerId":    {},
		"gatewayCorrelationId": {},
		"gatewayClientIP":      {},
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

func fieldToSchema(field reflect.StructField) Schema {
	s := Schema{}
	ft := field.Type
	isPtr := false
	if ft.Kind() == reflect.Ptr {
		ft = ft.Elem()
		isPtr = true
	}

	switch ft.Kind() {
	case reflect.String:
		s.Type = schemaTypeString
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		s.Type = schemaTypeInteger
		if ft.Kind() == reflect.Int64 {
			s.Format = "int64"
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		s.Type = schemaTypeInteger
	case reflect.Float32, reflect.Float64:
		s.Type = schemaTypeNumber
	case reflect.Bool:
		s.Type = schemaTypeBoolean
	default:
		s.Type = schemaTypeString
	}
	_ = isPtr

	validate := field.Tag.Get("validate")
	applyValidationConstraints(&s, validate)

	return s
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
