package swagger

type Spec struct {
	OpenAPI    string              `json:"openapi"`
	Info       Info                `json:"info"`
	Servers    []Server            `json:"servers,omitempty"`
	Tags       []Tag               `json:"tags,omitempty"`
	Paths      map[string]PathItem `json:"paths"`
	Components *Components         `json:"components,omitempty"`
}

type Server struct {
	URL         string `json:"url"`
	Description string `json:"description,omitempty"`
}

type Tag struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type Config struct {
	Title       string
	Description string
	Version     string
	BaseURL     string
	// SessionCookieName names the session cookie in the cookie security scheme
	// (defaults to "zentax_session").
	SessionCookieName string
}

type Components struct {
	SecuritySchemes map[string]SecurityScheme `json:"securitySchemes,omitempty"`
	Schemas         map[string]Schema         `json:"schemas,omitempty"`
}

type SecurityScheme struct {
	Type         string `json:"type"`
	Scheme       string `json:"scheme,omitempty"`
	BearerFormat string `json:"bearerFormat,omitempty"`
	In           string `json:"in,omitempty"`
	Name         string `json:"name,omitempty"`
	Description  string `json:"description,omitempty"`
}

type Info struct {
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Version     string   `json:"version"`
	Contact     *Contact `json:"contact,omitempty"`
}

type Contact struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
	URL   string `json:"url,omitempty"`
}

type PathItem map[string]*Operation

type Operation struct {
	Tags        []string            `json:"tags"`
	Summary     string              `json:"summary"`
	OperationID string              `json:"operationId"`
	Parameters  []Parameter         `json:"parameters,omitempty"`
	RequestBody *RequestBody        `json:"requestBody,omitempty"`
	Responses   map[string]Response `json:"responses"`
	Security    []SecurityReq       `json:"security,omitempty"`
	// XRequiredCapability surfaces the RBAC capability the route demands
	// (RouteDefinition.Capability) so generated clients / MCP tooling can
	// annotate operations with the permission they need (ADR-0012).
	XRequiredCapability string `json:"x-required-capability,omitempty"`
}

type Parameter struct {
	Name     string `json:"name"`
	In       string `json:"in"`
	Required bool   `json:"required"`
	Style    string `json:"style,omitempty"`
	Explode  *bool  `json:"explode,omitempty"`
	Schema   Schema `json:"schema"`
	Example  any    `json:"example,omitempty"`
}

type RequestBody struct {
	Required bool               `json:"required"`
	Content  map[string]Content `json:"content"`
}

type Content struct {
	Schema Schema `json:"schema"`
}

type Response struct {
	Description string             `json:"description"`
	Content     map[string]Content `json:"content,omitempty"`
}

type Schema struct {
	Ref        string            `json:"$ref,omitempty"`
	Type       string            `json:"type,omitempty"`
	Format     string            `json:"format,omitempty"`
	Items      *Schema           `json:"items,omitempty"`
	Properties map[string]Schema `json:"properties,omitempty"`
	Required   []string          `json:"required,omitempty"`
	Enum       []string          `json:"enum,omitempty"`
	Minimum    *float64          `json:"minimum,omitempty"`
	Maximum    *float64          `json:"maximum,omitempty"`
	MaxLength  *int              `json:"maxLength,omitempty"`
	MinLength  *int              `json:"minLength,omitempty"`
	Default    any               `json:"default,omitempty"`
	Example    any               `json:"example,omitempty"`
}

type SecurityReq map[string][]string
