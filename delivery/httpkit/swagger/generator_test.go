package swagger

import (
	"net/http"
	"testing"

	"github.com/mohamadhallal/zentax-api/delivery/httpkit/routing"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
)

func TestGenerateIncludesDTOExamples(t *testing.T) {
	type requestBody struct {
		Name   string `json:"name" validate:"required" example:"Alice"`
		Amount int    `json:"amount" validate:"required,min=1" example:"2500"`
		Active bool   `json:"active" example:"true"`
	}
	type requestQuery struct {
		Limit int    `json:"limit" default:"20" example:"50"`
		Sort  string `json:"sort" example:"createdAt"`
	}

	spec := Generate([]routing.RouteMeta{
		{
			FullPath: "/users",
			Definition: types.RouteDefinition{
				Method: http.MethodPost,
				Path:   "/users",
			},
			Schema: &types.SchemaDefinition{
				Body:  requestBody{},
				Query: requestQuery{},
			},
		},
	}, types.ModeExternal, Config{Title: "Test API"})

	op := spec.Paths["/users"]["post"]
	if op == nil {
		t.Fatal("expected POST /users operation")
	}

	bodySchema := op.RequestBody.Content["application/json"].Schema
	assertExample(t, bodySchema.Properties["name"].Example, "Alice")
	assertExample(t, bodySchema.Properties["amount"].Example, 2500)
	assertExample(t, bodySchema.Properties["active"].Example, true)

	paramsByName := make(map[string]Parameter, len(op.Parameters))
	for _, param := range op.Parameters {
		paramsByName[param.Name] = param
	}
	assertExample(t, paramsByName["limit"].Example, 50)
	assertExample(t, paramsByName["sort"].Example, "createdAt")
}

// A repeatable query parameter is an array in the spec, so its example must be
// an array too (OpenAPI 3.0.3: an example SHOULD match the schema; a bare
// string on an array schema trips spec linters).
func TestGenerateArrayParamExampleIsAnArray(t *testing.T) {
	type requestQuery struct {
		Status []string `json:"status" validate:"omitempty,dive,oneof=draft active" example:"active"`
	}
	spec := Generate([]routing.RouteMeta{
		{
			FullPath:   "/things",
			Definition: types.RouteDefinition{Method: http.MethodGet, Path: "/things"},
			Schema:     &types.SchemaDefinition{Query: requestQuery{}},
		},
	}, types.ModeExternal, Config{Title: "Test API"})
	op := spec.Paths["/things"]["get"]
	if op == nil {
		t.Fatal("expected GET /things operation")
	}
	for _, param := range op.Parameters {
		if param.Name != "status" {
			continue
		}
		if param.Schema.Type != "array" {
			t.Fatalf("status schema type = %q, want array", param.Schema.Type)
		}
		got, ok := param.Example.([]string)
		if !ok || len(got) != 1 || got[0] != "active" {
			t.Fatalf("status example = %#v, want []string{\"active\"}", param.Example)
		}
		return
	}
	t.Fatal("status parameter not generated")
}

func TestGenerateExternalSecurityIsSessionOrBearer(t *testing.T) {
	spec := Generate([]routing.RouteMeta{
		{
			FullPath: "/entities",
			Definition: types.RouteDefinition{
				Method:     http.MethodPost,
				Path:       "/",
				Tenant:     true,
				Capability: "entity:write",
			},
		},
	}, types.ModeExternal, Config{Title: "Test API", SessionCookieName: "zentax_session"})

	cookie := spec.Components.SecuritySchemes["sessionCookie"]
	if cookie.Type != "apiKey" || cookie.In != "cookie" || cookie.Name != "zentax_session" {
		t.Fatalf("session cookie scheme mismatch: %#v", cookie)
	}
	bearer := spec.Components.SecuritySchemes["bearerToken"]
	if bearer.Type != "http" || bearer.Scheme != "bearer" {
		t.Fatalf("bearer scheme mismatch: %#v", bearer)
	}
	if _, gone := spec.Components.SecuritySchemes["gatewayAccountId"]; gone {
		t.Fatal("gateway schemes must be gone — they never described this API's auth")
	}

	op := spec.Paths["/entities"]["post"]
	// Alternatives: cookie OR bearer — two separate requirement objects.
	if len(op.Security) != 2 {
		t.Fatalf("expected two alternative security requirements, got %#v", op.Security)
	}
	if _, ok := op.Security[0]["sessionCookie"]; !ok {
		t.Fatalf("first alternative must be sessionCookie: %#v", op.Security)
	}
	if _, ok := op.Security[1]["bearerToken"]; !ok {
		t.Fatalf("second alternative must be bearerToken: %#v", op.Security)
	}
}

func TestGenerateSurfacesCapabilityAndResponses(t *testing.T) {
	spec := Generate([]routing.RouteMeta{
		{
			FullPath: "/task-instances/{id}/approve",
			Definition: types.RouteDefinition{
				Method:     http.MethodPost,
				Path:       "/{id}/approve",
				Tenant:     true,
				Capability: "task:approve",
			},
		},
		{
			FullPath: "/entities",
			Definition: types.RouteDefinition{
				Method:     http.MethodGet,
				Path:       "/",
				Tenant:     true,
				Paginated:  true,
				Capability: "entity:read",
			},
		},
	}, types.ModeExternal, Config{Title: "Test API"})

	approve := spec.Paths["/task-instances/{id}/approve"]["post"]
	if approve.XRequiredCapability != "task:approve" {
		t.Fatalf("x-required-capability missing: %#v", approve.XRequiredCapability)
	}
	// Authenticated + id-addressed mutation: the full error contract.
	for _, code := range []string{"400", "401", "403", "404", "409", "500"} {
		if _, ok := approve.Responses[code]; !ok {
			t.Fatalf("approve must declare %s", code)
		}
	}
	// Action POST on an id declares 200 alongside 201.
	if _, ok := approve.Responses["200"]; !ok {
		t.Fatal("action-style POST must declare 200")
	}
	if got := approve.Responses["403"].Content["application/json"].Schema.Ref; got != "#/components/schemas/ErrorEnvelope" {
		t.Fatalf("errors must reference the ErrorEnvelope, got %q", got)
	}

	list := spec.Paths["/entities"]["get"]
	if got := list.Responses["200"].Content["application/json"].Schema.Ref; got != "#/components/schemas/PaginatedEnvelope" {
		t.Fatalf("paginated list must reference the PaginatedEnvelope, got %q", got)
	}
	if _, ok := spec.Components.Schemas["SuccessEnvelope"]; !ok {
		t.Fatal("SuccessEnvelope component schema missing")
	}
	if _, ok := spec.Components.Schemas["ErrorEnvelope"]; !ok {
		t.Fatal("ErrorEnvelope component schema missing")
	}
}

func TestGenerateInternalAuthStillUsesBasicAuth(t *testing.T) {
	spec := Generate([]routing.RouteMeta{
		{
			FullPath: "/users",
			Definition: types.RouteDefinition{
				Method: http.MethodGet,
				Path:   "/users",
				Auth:   true,
			},
		},
	}, types.ModeInternal, Config{Title: "Test API"})

	scheme := spec.Components.SecuritySchemes["basicAuth"]
	if scheme.Type != "http" || scheme.Scheme != "basic" {
		t.Fatalf("internal auth scheme mismatch: %#v", scheme)
	}

	op := spec.Paths["/users"]["get"]
	assertSecurity(t, op.Security, "basicAuth")
}

func assertExample(t *testing.T, got, want any) {
	t.Helper()
	if got != want {
		t.Fatalf("example mismatch: got %#v (%T), want %#v (%T)", got, got, want, want)
	}
}

func assertSecurity(t *testing.T, got []SecurityReq, want string) {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("expected one security requirement, got %#v", got)
	}
	if _, ok := got[0][want]; !ok {
		t.Fatalf("expected security requirement %q, got %#v", want, got)
	}
}
