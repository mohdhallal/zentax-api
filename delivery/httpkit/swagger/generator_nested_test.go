package swagger

import (
	"testing"
	"time"
)

// JSONB-backed value types (period lists, rule structs, requirement lists) must
// surface with their real shape; a generated client typed them as "string"
// before this was covered.
func TestStructToSchemaFollowsNestedTypes(t *testing.T) {
	type rule struct {
		OffsetUnit  string `json:"offsetUnit,omitempty" validate:"omitempty,oneof=days weeks months"`
		OffsetValue int    `json:"offsetValue,omitempty"`
	}
	type requirement struct {
		Name     string `json:"name" validate:"required,max=200"`
		Required bool   `json:"required"`
	}
	type periods []string
	type requirements []requirement
	type body struct {
		Periods      periods      `json:"periods" validate:"omitempty,dive,max=10"`
		Rule         rule         `json:"rule" validate:"omitempty"`
		RulePtr      *rule        `json:"rulePtr"`
		Requirements requirements `json:"requirements" validate:"omitempty,dive"`
		Raw          []byte       `json:"raw"`
		At           time.Time    `json:"at"`
	}

	s := structToSchema(body{})

	periodsSchema := s.Properties["periods"]
	if periodsSchema.Type != "array" || periodsSchema.Items == nil || periodsSchema.Items.Type != "string" {
		t.Fatalf("periods: want array of string, got %+v", periodsSchema)
	}
	if periodsSchema.Items.MaxLength == nil || *periodsSchema.Items.MaxLength != 10 {
		t.Fatalf("periods: post-dive max=10 must land on the items, got %+v", periodsSchema.Items)
	}

	for _, name := range []string{"rule", "rulePtr"} {
		rs := s.Properties[name]
		if rs.Type != "object" || rs.Properties["offsetUnit"].Type != "string" || rs.Properties["offsetValue"].Type != "integer" {
			t.Fatalf("%s: want nested object schema, got %+v", name, rs)
		}
		if got := rs.Properties["offsetUnit"].Enum; len(got) != 3 {
			t.Fatalf("%s: nested oneof must become an enum, got %v", name, got)
		}
	}

	reqs := s.Properties["requirements"]
	if reqs.Type != "array" || reqs.Items == nil || reqs.Items.Type != "object" {
		t.Fatalf("requirements: want array of object, got %+v", reqs)
	}
	if reqs.Items.Properties["required"].Type != "boolean" || len(reqs.Items.Required) != 1 || reqs.Items.Required[0] != "name" {
		t.Fatalf("requirements: item schema must carry its own properties/required, got %+v", reqs.Items)
	}

	if s.Properties["raw"].Type != "string" {
		t.Fatalf("[]byte must stay a string, got %+v", s.Properties["raw"])
	}
	if at := s.Properties["at"]; at.Type != "string" || at.Format != "date-time" {
		t.Fatalf("time.Time must be string/date-time, got %+v", at)
	}
}
