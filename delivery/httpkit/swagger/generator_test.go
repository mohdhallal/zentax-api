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

func TestGenerateExternalAuthUsesGatewayHeaders(t *testing.T) {
	spec := Generate([]routing.RouteMeta{
		{
			FullPath: "/users",
			Definition: types.RouteDefinition{
				Method: http.MethodGet,
				Path:   "/users",
				Auth:   true,
			},
		},
	}, types.ModeExternal, Config{Title: "Test API"})

	scheme := spec.Components.SecuritySchemes["gatewayAccountId"]
	if scheme.Type != "apiKey" || scheme.In != "header" || scheme.Name != "X-Account-Id" {
		t.Fatalf("external auth scheme mismatch: %#v", scheme)
	}

	for schemeName, headerName := range map[string]string{
		"gatewayAPIKeyId":      "X-API-Key-Id",
		"gatewayCustomerId":    "X-Customer-Id",
		"gatewayCorrelationId": "X-Correlation-Id",
		"gatewayClientIP":      "X-Client-IP",
	} {
		scheme := spec.Components.SecuritySchemes[schemeName]
		if scheme.Type != "apiKey" || scheme.In != "header" || scheme.Name != headerName {
			t.Fatalf("external auth scheme %s mismatch: %#v", schemeName, scheme)
		}
	}

	op := spec.Paths["/users"]["get"]
	assertSecurity(t, op.Security, "gatewayAccountId")
	assertSecurity(t, op.Security, "gatewayAPIKeyId")
	assertSecurity(t, op.Security, "gatewayCustomerId")
	assertSecurity(t, op.Security, "gatewayCorrelationId")
	assertSecurity(t, op.Security, "gatewayClientIP")

	if len(op.Parameters) != 0 {
		t.Fatalf("expected external auth headers to be configured via security schemes, got parameters %#v", op.Parameters)
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
