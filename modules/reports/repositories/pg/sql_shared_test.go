package pg

import (
	"strings"
	"testing"
)

// "Today" in the compliance classification is the TENANT's calendar date
// (ADR-0003 / ADR-0023 §6), never the session's or the server's.
func TestComplianceClass_TodayIsTheTenantsDay(t *testing.T) {
	for _, col := range []string{"ti.filing_deadline", "ti.due_date"} {
		sql := complianceClass(col)
		if strings.Contains(strings.ToUpper(sql), "CURRENT_DATE") {
			t.Fatalf("%s: CURRENT_DATE (server day) must not appear:\n%s", col, sql)
		}
		if !strings.Contains(sql, col+" < "+tenantToday) {
			t.Fatalf("%s: the missed rule must compare against tenantToday:\n%s", col, sql)
		}
	}
}

func TestTenantToday_ReadsTheRegistryRowOfTheRequestTenant(t *testing.T) {
	for _, want := range []string{
		"NOW() AT TIME ZONE",
		"FROM tenants t",
		"current_setting('app.tenant_id', true)::uuid",
		"::date",
	} {
		if !strings.Contains(tenantToday, want) {
			t.Fatalf("tenantToday lacks %q:\n%s", want, tenantToday)
		}
	}
	// No bind parameter and no interpolation slot: the zone comes from the
	// registry row only.
	if strings.Contains(tenantToday, "$") || strings.Contains(tenantToday, "%") {
		t.Fatalf("tenantToday must not take caller data:\n%s", tenantToday)
	}
}
