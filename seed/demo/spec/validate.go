package spec

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mohamadhallal/zentax-api/shared/dateonly"
)

// Domain values the dataset uses. They are the API's own vocabulary; a value
// outside these sets would be rejected by the server halfway through a seed
// run, which is far more expensive to diagnose than a failed Validate.
const (
	CategoryRecurring = "recurring"
	CategoryProject   = "project"

	StatusActive   = "active"
	StatusDisabled = "disabled"

	ViaPut     = "put"
	ViaSubmit  = "submit"
	ViaApprove = "approve"

	InstanceCompleted       = "completed"
	InstancePendingApproval = "pending_approval"
)

var (
	memberRoles       = []string{"tenant_admin", "manager", "preparer", "reviewer", "viewer"}
	memberStatuses    = []string{StatusActive, StatusDisabled}
	obligationTmpls   = []string{"VAT", "CIT", "WHT", "TP", "Custom"}
	dataTemplateTypes = []string{"VAT", "CIT", "WHT"}
	workflowStatuses  = []string{"draft", "active", "completed", "archived"}
	instanceStatuses  = []string{
		"not_started", "in_progress", "in_review", "blocked", InstancePendingApproval, InstanceCompleted,
	}
	instanceVias  = []string{ViaPut, ViaSubmit, ViaApprove}
	taxDataStates = []string{"draft", "final"}
	documentKinds = []string{"pdf", "text", "csv"}

	// Column widths the API's schema imposes.
	maxPeriodCodeLen   = 10 // task_instances.period_code VARCHAR(10)
	maxFinancialYear   = 9  // workflows.financial_year VARCHAR(9)
	minPasswordRuneLen = 12 // the accept-invite policy (NIST 800-63B)
)

// ValidationError collects every invariant the dataset breaks, so one run
// reports all of them instead of the first.
type ValidationError struct {
	Problems []string
}

func (e *ValidationError) Error() string {
	if len(e.Problems) == 1 {
		return "invalid dataset: " + e.Problems[0]
	}
	return fmt.Sprintf("invalid dataset: %d problems:\n  - %s",
		len(e.Problems), strings.Join(e.Problems, "\n  - "))
}

// Validate enforces the dataset's own invariants: unique keys, resolvable
// cross-references, one entity obligation per (entity, obligation type), unique
// e-mails across tenants, passwords long enough for the accept-invite policy,
// period codes that fit the column, project workflows with an end date, active
// assignees, writer ≠ approver (segregation of duties), and instances that
// reference declared templates and selected periods.
//
// It checks structure, never arithmetic: whether a report number is right is
// the verifier's question, and the numbers in this file are revised
// independently of its shape.
func (s *Spec) Validate() error {
	v := &validator{}
	s.validateGlobals(v)

	emails := map[string]string{} // email -> "tenantKey.userKey"
	tenantKeys := map[string]bool{}
	slugs := map[string]bool{}

	for i := range s.Tenants {
		tenant := &s.Tenants[i]
		where := "tenants[" + strconv.Itoa(i) + "]"
		if tenant.Key == "" {
			v.addf("%s: key is required", where)
		} else {
			if tenantKeys[tenant.Key] {
				v.addf("%s: duplicate tenant key %q", where, tenant.Key)
			}
			tenantKeys[tenant.Key] = true
			where = "tenant " + tenant.Key
		}
		if tenant.Slug == "" {
			v.addf("%s: slug is required", where)
		} else if slugs[tenant.Slug] {
			v.addf("%s: duplicate tenant slug %q", where, tenant.Slug)
		}
		slugs[tenant.Slug] = true
		if tenant.Name == "" {
			v.addf("%s: name is required", where)
		}
		if tenant.Timezone == "" {
			v.addf("%s: timezone is required (the reports compute \"today\" in it)", where)
		}

		validateUsers(v, tenant, emails)
		validateEntities(v, tenant)
		validateObligations(v, tenant)
		validateWorkflows(v, tenant, s.CompletionLocalTime())
	}

	s.validateExpectations(v, tenantKeys)
	return v.err()
}

func (s *Spec) validateGlobals(v *validator) {
	if s.AsOf.IsZero() {
		v.addf("asOf is required")
	}
	if s.ValidityWindow.From.IsZero() || s.ValidityWindow.To.IsZero() {
		v.addf("validityWindow needs both from and to")
		return
	}
	if s.ValidityWindow.To.Time().Before(s.ValidityWindow.From.Time()) {
		v.addf("validityWindow: to (%s) is before from (%s)", s.ValidityWindow.To, s.ValidityWindow.From)
	}
	if !s.AsOf.IsZero() &&
		(s.AsOf.Time().Before(s.ValidityWindow.From.Time()) || s.AsOf.Time().After(s.ValidityWindow.To.Time())) {
		v.addf("asOf (%s) is outside the validity window %s..%s",
			s.AsOf, s.ValidityWindow.From, s.ValidityWindow.To)
	}
	if len(s.Tenants) == 0 {
		v.addf("no tenants declared")
	}
}

func validateUsers(v *validator, tenant *Tenant, emails map[string]string) {
	if tenant.Admin.Role != "tenant_admin" {
		v.addf("tenant %s: admin role is %q, want tenant_admin", tenant.Key, tenant.Admin.Role)
	}
	if tenant.Admin.Status != StatusActive {
		v.addf("tenant %s: the admin must be active", tenant.Key)
	}
	if tenant.Admin.ScopeEntity != nil {
		v.addf("tenant %s: the admin grant is tenant-wide, it cannot be scoped to an entity", tenant.Key)
	}

	seen := map[string]bool{}
	for _, user := range tenant.AllUsers() {
		where := fmt.Sprintf("tenant %s user %s", tenant.Key, user.Key)
		if user.Key == "" {
			v.addf("tenant %s: a user has no key", tenant.Key)
			continue
		}
		if seen[user.Key] {
			v.addf("%s: duplicate user key", where)
		}
		seen[user.Key] = true

		if user.Email == "" {
			v.addf("%s: email is required", where)
		} else if owner, taken := emails[user.Email]; taken {
			v.addf("%s: e-mail %s is already used by %s (users.email is globally unique)",
				where, user.Email, owner)
		} else {
			emails[user.Email] = tenant.Key + "." + user.Key
		}
		if user.Name == "" {
			v.addf("%s: name is required", where)
		}
		if n := utf8.RuneCountInString(user.Password); n < minPasswordRuneLen {
			v.addf("%s: password is %d characters, the minimum is %d", where, n, minPasswordRuneLen)
		}
		v.oneOf(where+": role", user.Role, memberRoles)
		v.oneOf(where+": status", user.Status, memberStatuses)
		if user.ScopeEntity != nil {
			if _, ok := tenant.Entity(*user.ScopeEntity); !ok {
				v.addf("%s: scopeEntity %q does not resolve to an entity of this tenant", where, *user.ScopeEntity)
			}
		}
	}
}

func validateEntities(v *validator, tenant *Tenant) {
	seen := map[string]bool{}
	for _, entity := range tenant.Entities {
		where := fmt.Sprintf("tenant %s entity %s", tenant.Key, entity.Key)
		if entity.Key == "" {
			v.addf("tenant %s: an entity has no key", tenant.Key)
			continue
		}
		if seen[entity.Key] {
			v.addf("%s: duplicate entity key", where)
		}
		seen[entity.Key] = true

		if entity.Name == "" {
			v.addf("%s: name is required", where)
		}
		if entity.Country == "" {
			v.addf("%s: country is required (the reports group by it)", where)
		}
		if entity.Parent != nil {
			switch {
			case *entity.Parent == entity.Key:
				v.addf("%s: is its own parent", where)
			default:
				if _, ok := tenant.Entity(*entity.Parent); !ok {
					v.addf("%s: parent %q does not resolve", where, *entity.Parent)
				}
			}
		}
		if entity.FinancialYearEnd != "" && !isMonthDay(entity.FinancialYearEnd) {
			v.addf("%s: financialYearEnd %q is not MM-DD", where, entity.FinancialYearEnd)
		}
	}
	validateNoEntityCycles(v, tenant)
}

// validateNoEntityCycles walks each entity up to a root; a cycle would make the
// closure table unbuildable.
func validateNoEntityCycles(v *validator, tenant *Tenant) {
	for _, entity := range tenant.Entities {
		seen := map[string]bool{entity.Key: true}
		current := entity
		for current.Parent != nil {
			parent, ok := tenant.Entity(*current.Parent)
			if !ok {
				break // already reported
			}
			if seen[parent.Key] {
				v.addf("tenant %s: entity hierarchy has a cycle through %s", tenant.Key, parent.Key)
				break
			}
			seen[parent.Key] = true
			current = parent
		}
	}
}

func validateObligations(v *validator, tenant *Tenant) {
	seenKeys := map[string]bool{}
	seenCodes := map[string]bool{}
	for _, obligation := range tenant.ObligationTypes {
		where := fmt.Sprintf("tenant %s obligation type %s", tenant.Key, obligation.Key)
		if obligation.Key == "" {
			v.addf("tenant %s: an obligation type has no key", tenant.Key)
			continue
		}
		if seenKeys[obligation.Key] {
			v.addf("%s: duplicate obligation-type key", where)
		}
		seenKeys[obligation.Key] = true

		if obligation.Code == "" {
			v.addf("%s: code is required", where)
		} else if seenCodes[obligation.Code] {
			v.addf("%s: code %q is used twice (codes are unique per tenant)", where, obligation.Code)
		}
		seenCodes[obligation.Code] = true
		if obligation.Name == "" {
			v.addf("%s: name is required", where)
		}
		v.oneOf(where+": template", obligation.Template, obligationTmpls)
	}

	seenPairs := map[string]string{}
	seenKeys = map[string]bool{}
	for _, entityObligation := range tenant.EntityObligations {
		where := fmt.Sprintf("tenant %s entity obligation %s", tenant.Key, entityObligation.Key)
		if entityObligation.Key == "" {
			v.addf("tenant %s: an entity obligation has no key", tenant.Key)
			continue
		}
		if seenKeys[entityObligation.Key] {
			v.addf("%s: duplicate entity-obligation key", where)
		}
		seenKeys[entityObligation.Key] = true

		if _, ok := tenant.Entity(entityObligation.Entity); !ok {
			v.addf("%s: entity %q does not resolve", where, entityObligation.Entity)
		}
		if _, ok := tenant.ObligationType(entityObligation.ObligationType); !ok {
			v.addf("%s: obligationType %q does not resolve", where, entityObligation.ObligationType)
		}
		pair := entityObligation.Entity + "|" + entityObligation.ObligationType
		if first, taken := seenPairs[pair]; taken {
			v.addf("%s: a second obligation for (%s, %s) — %s already declares it; exactly one is allowed",
				where, entityObligation.Entity, entityObligation.ObligationType, first)
		} else {
			seenPairs[pair] = entityObligation.Key
		}
		if entityObligation.Periodicity == "" {
			v.addf("%s: periodicity is required", where)
		}
	}
}

func validateWorkflows(v *validator, tenant *Tenant, defaultLocalTime string) {
	seen := map[string]bool{}
	for i := range tenant.Workflows {
		workflow := &tenant.Workflows[i]
		where := fmt.Sprintf("tenant %s workflow %s", tenant.Key, workflow.Key)
		if workflow.Key == "" {
			v.addf("tenant %s: a workflow has no key", tenant.Key)
			continue
		}
		if seen[workflow.Key] {
			v.addf("%s: duplicate workflow key", where)
		}
		seen[workflow.Key] = true

		if workflow.Name == "" {
			v.addf("%s: name is required", where)
		}
		if n := utf8.RuneCountInString(workflow.FinancialYear); n > maxFinancialYear {
			v.addf("%s: financialYear %q is %d characters, the column holds %d",
				where, workflow.FinancialYear, n, maxFinancialYear)
		}
		if workflow.StartDate.IsZero() {
			v.addf("%s: startDate is required", where)
		}

		validateWorkflowShape(v, tenant, workflow, where)
		validateWorkflowActors(v, tenant, workflow, where)
		validateWorkflowLifecycle(v, workflow, where)
		validateTemplates(v, workflow, where)
		validateInstances(v, tenant, workflow, where, defaultLocalTime)
		validateDocuments(v, workflow, where)
	}
}

func validateWorkflowShape(v *validator, tenant *Tenant, workflow *Workflow, where string) {
	switch workflow.Category {
	case CategoryRecurring:
		if workflow.Entity == nil {
			v.addf("%s: a recurring workflow needs an entity", where)
		} else if _, ok := tenant.Entity(*workflow.Entity); !ok {
			v.addf("%s: entity %q does not resolve", where, *workflow.Entity)
		}
		if workflow.ObligationType == nil {
			v.addf("%s: a recurring workflow needs an obligationType", where)
		} else if _, ok := tenant.ObligationType(*workflow.ObligationType); !ok {
			v.addf("%s: obligationType %q does not resolve", where, *workflow.ObligationType)
		}
		if workflow.Periodicity == nil || *workflow.Periodicity == "" {
			v.addf("%s: a recurring workflow needs a periodicity", where)
		}
		if len(workflow.SelectedPeriods) == 0 {
			v.addf("%s: a recurring workflow needs at least one selected period", where)
		}
		validateEntityObligationLink(v, tenant, workflow, where)
	case CategoryProject:
		if workflow.ProjectType == nil || *workflow.ProjectType == "" {
			v.addf("%s: a project workflow needs a projectType", where)
		}
		if workflow.EndDate.IsZero() {
			v.addf("%s: a project workflow needs an endDate (it is the period end, "+
				"the filing deadline and the payment deadline of its single instance)", where)
		}
		for _, unexpected := range []struct {
			name  string
			value *string
		}{
			{"obligationType", workflow.ObligationType},
			{"entityObligation", workflow.EntityObligation},
			{"periodicity", workflow.Periodicity},
		} {
			if unexpected.value != nil {
				v.addf("%s: a project workflow must not set %s", where, unexpected.name)
			}
		}
		if workflow.Entity != nil {
			if _, ok := tenant.Entity(*workflow.Entity); !ok {
				v.addf("%s: entity %q does not resolve", where, *workflow.Entity)
			}
		}
		if len(workflow.SelectedPeriods) > 0 {
			v.addf("%s: a project workflow declares no selectedPeriods (its period is %q)",
				where, ProjectPeriodCode)
		}
	default:
		v.addf("%s: category %q is neither %s nor %s", where, workflow.Category, CategoryRecurring, CategoryProject)
	}

	seenPeriods := map[string]bool{}
	for _, period := range workflow.SelectedPeriods {
		if seenPeriods[period] {
			v.addf("%s: period %q is selected twice", where, period)
		}
		seenPeriods[period] = true
		if n := utf8.RuneCountInString(period); n > maxPeriodCodeLen {
			v.addf("%s: period code %q is %d characters, the column holds %d",
				where, period, n, maxPeriodCodeLen)
		}
	}
}

func validateEntityObligationLink(v *validator, tenant *Tenant, workflow *Workflow, where string) {
	if workflow.EntityObligation == nil {
		v.addf("%s: a recurring workflow needs an entityObligation", where)
		return
	}
	entityObligation, ok := tenant.EntityObligation(*workflow.EntityObligation)
	if !ok {
		v.addf("%s: entityObligation %q does not resolve", where, *workflow.EntityObligation)
		return
	}
	if workflow.Entity != nil && entityObligation.Entity != *workflow.Entity {
		v.addf("%s: entityObligation %s belongs to entity %s, not %s",
			where, entityObligation.Key, entityObligation.Entity, *workflow.Entity)
	}
	if workflow.ObligationType != nil && entityObligation.ObligationType != *workflow.ObligationType {
		v.addf("%s: entityObligation %s is for obligation type %s, not %s",
			where, entityObligation.Key, entityObligation.ObligationType, *workflow.ObligationType)
	}
	if workflow.Periodicity != nil && entityObligation.Periodicity != *workflow.Periodicity {
		v.addf("%s: periodicity %s differs from the obligation's %s",
			where, *workflow.Periodicity, entityObligation.Periodicity)
	}
}

func validateWorkflowActors(v *validator, tenant *Tenant, workflow *Workflow, where string) {
	writer, writerOK := tenant.User(workflow.Writer)
	if !writerOK {
		v.addf("%s: writer %q does not resolve to a user of this tenant", where, workflow.Writer)
	} else if !writer.IsActive() {
		v.addf("%s: writer %s is %s; a disabled member cannot write", where, writer.Key, writer.Status)
	}

	approver, approverOK := tenant.User(workflow.Approver)
	if !approverOK {
		v.addf("%s: approver %q does not resolve to a user of this tenant", where, workflow.Approver)
	} else if !approver.IsActive() {
		v.addf("%s: approver %s is %s; a disabled member cannot approve", where, approver.Key, approver.Status)
	}

	if writerOK && approverOK && workflow.Writer == workflow.Approver {
		v.addf("%s: writer and approver are both %s — an approval requires two people (SoD, ADR-0012)",
			where, workflow.Writer)
	}
}

func validateWorkflowLifecycle(v *validator, workflow *Workflow, where string) {
	v.oneOf(where+": lifecycle.finalStatus", workflow.Lifecycle.FinalStatus, workflowStatuses)
	if workflow.Lifecycle.Start {
		if workflow.Lifecycle.FinalStatus == "draft" {
			v.addf("%s: a started workflow cannot end as a draft", where)
		}
		return
	}
	if workflow.Lifecycle.FinalStatus != "draft" {
		v.addf("%s: an unstarted workflow ends as a draft, not %q", where, workflow.Lifecycle.FinalStatus)
	}
	if len(workflow.Instances) > 0 || len(workflow.Documents) > 0 {
		v.addf("%s: it is never started, so it has no instances to act on", where)
	}
}

func validateTemplates(v *validator, workflow *Workflow, where string) {
	if len(workflow.Templates) == 0 {
		v.addf("%s: no task templates declared", where)
		return
	}
	seenKeys := map[string]bool{}
	seenOrder := map[int]bool{}
	for _, template := range workflow.Templates {
		templateWhere := where + " template " + template.Key
		if template.Key == "" {
			v.addf("%s: a task template has no key", where)
			continue
		}
		if seenKeys[template.Key] {
			v.addf("%s: duplicate template key", templateWhere)
		}
		seenKeys[template.Key] = true
		if template.Name == "" {
			v.addf("%s: name is required", templateWhere)
		}
		if seenOrder[template.OrderIndex] {
			v.addf("%s: orderIndex %d is used twice", templateWhere, template.OrderIndex)
		}
		seenOrder[template.OrderIndex] = true
		if template.DataTemplate != nil {
			v.oneOf(templateWhere+": dataTemplate", *template.DataTemplate, dataTemplateTypes)
		}
	}
}

func validateInstances(v *validator, tenant *Tenant, workflow *Workflow, where, defaultLocalTime string) {
	periods := workflow.PeriodCodes()
	seen := map[string]bool{}
	// A zone that will not load (no tzdata in this binary) is not the
	// dataset's fault: the instant cross-check is then skipped.
	zone, _ := time.LoadLocation(tenant.Timezone)

	for _, instance := range workflow.Instances {
		instanceWhere := fmt.Sprintf("%s instance %s/%s", where, instance.Period, instance.Task)

		if key := instance.Period + "|" + instance.Task; seen[key] {
			v.addf("%s: declared twice", instanceWhere)
		} else {
			seen[key] = true
		}
		if !slices.Contains(periods, instance.Period) {
			v.addf("%s: period %q is not one of the workflow's periods (%s)",
				instanceWhere, instance.Period, strings.Join(periods, ", "))
		}
		if _, ok := workflow.Template(instance.Task); !ok {
			v.addf("%s: task %q is not a template of this workflow", instanceWhere, instance.Task)
		}

		v.oneOf(instanceWhere+": status", instance.Status, instanceStatuses)
		v.oneOf(instanceWhere+": via", instance.Via, instanceVias)

		if assignee, ok := tenant.User(instance.Assignee); !ok {
			v.addf("%s: assignee %q does not resolve to a user of this tenant", instanceWhere, instance.Assignee)
		} else if !assignee.IsActive() {
			v.addf("%s: assignee %s is %s; only an active member can hold a task",
				instanceWhere, assignee.Key, assignee.Status)
		}

		validateInstanceState(v, instance, instanceWhere)
		validateCompletionInstant(v, instance, zone, defaultLocalTime, instanceWhere)
	}
}

// validateCompletionInstant checks the declared completedAtUtc against the rule
// that produced it: completedOn at the local completion time, in the tenant's
// zone. The verifier asserts the stored instant, so a wrong literal here would
// only surface as a failing report run.
func validateCompletionInstant(v *validator, instance Instance, zone *time.Location, defaultLocalTime, where string) {
	if instance.CompletedAtUtc.IsZero() {
		return
	}
	if instance.CompletedOn.IsZero() {
		v.addf("%s: completedAtUtc without completedOn", where)
		return
	}
	if _, offset := instance.CompletedAtUtc.Zone(); offset != 0 {
		v.addf("%s: completedAtUtc %s is not expressed in UTC",
			where, instance.CompletedAtUtc.Format(time.RFC3339))
	}
	want, ok := instance.CompletionInstant(zone, defaultLocalTime)
	if !ok {
		return // no zone available, or a malformed local time already reported
	}
	if !instance.CompletedAtUtc.Equal(want) {
		v.addf("%s: completedAtUtc is %s, but %s %s in %s is %s",
			where, instance.CompletedAtUtc.UTC().Format(time.RFC3339),
			instance.CompletedOn, instance.LocalCompletionTime(defaultLocalTime),
			zone, want.Format(time.RFC3339))
	}
}

func validateInstanceState(v *validator, instance Instance, where string) {
	completed := instance.Status == InstanceCompleted
	switch {
	case completed && instance.CompletedOn.IsZero():
		v.addf("%s: completed, but no completedOn (the seeder needs it to stamp completed_at)", where)
	case !completed && !instance.CompletedOn.IsZero():
		v.addf("%s: has completedOn but status %q", where, instance.Status)
	}

	switch instance.Via {
	case ViaSubmit:
		if instance.Status != InstancePendingApproval {
			v.addf("%s: via=submit leaves the instance %s, not %q", where, InstancePendingApproval, instance.Status)
		}
	case ViaApprove:
		if !completed {
			v.addf("%s: via=approve completes the instance, so status must be %s", where, InstanceCompleted)
		}
		if instance.SubmittedOn.IsZero() {
			v.addf("%s: via=approve needs submittedOn (it stamps submitted_at)", where)
		}
	case ViaPut:
		if !instance.SubmittedOn.IsZero() {
			v.addf("%s: via=put never submits, so submittedOn does not apply", where)
		}
	}

	if len(instance.TaxData) > 0 && instance.TaxDataStatus == "" {
		v.addf("%s: taxData without a taxDataStatus", where)
	}
	if instance.TaxDataStatus != "" {
		v.oneOf(where+": taxDataStatus", instance.TaxDataStatus, taxDataStates)
		if len(instance.TaxData) == 0 {
			v.addf("%s: taxDataStatus without taxData", where)
		}
	}
	if instance.CompletedAtLocalTime != "" {
		if _, _, ok := parseClockTime(instance.CompletedAtLocalTime); !ok {
			v.addf("%s: completedAtLocalTime %q is not HH:MM", where, instance.CompletedAtLocalTime)
		}
	}
}

func validateDocuments(v *validator, workflow *Workflow, where string) {
	periods := workflow.PeriodCodes()
	for _, document := range workflow.Documents {
		documentWhere := fmt.Sprintf("%s document %q", where, document.Label)
		if !slices.Contains(periods, document.Period) {
			v.addf("%s: period %q is not one of the workflow's periods", documentWhere, document.Period)
		}
		if _, ok := workflow.Template(document.Task); !ok {
			v.addf("%s: task %q is not a template of this workflow", documentWhere, document.Task)
		}
		if document.FileName == "" {
			v.addf("%s: fileName is required", documentWhere)
		}
		if document.DocumentType == "" {
			v.addf("%s: documentType is required", documentWhere)
		}
		v.oneOf(documentWhere+": kind", document.Kind, documentKinds)
		v.oneOf(documentWhere+": category", document.Category, []string{"compliance", "project"})
	}
}

// validateExpectations checks the shape of the expectation tables — the keys
// resolve and the dates parse. The numbers themselves are the verifier's job.
func (s *Spec) validateExpectations(v *validator, tenantKeys map[string]bool) {
	for date, byTenant := range s.ExpectedAsOf {
		if _, err := dateonly.Parse(date); err != nil {
			v.addf("expectedAsOf: %q is not a YYYY-MM-DD date", date)
		}
		for tenantKey, expectations := range byTenant {
			where := fmt.Sprintf("expectedAsOf[%s][%s]", date, tenantKey)
			if !tenantKeys[tenantKey] {
				v.addf("%s: no tenant with that key", where)
				continue
			}
			tenant, _ := s.Tenant(tenantKey)
			for workflowKey := range expectations.WorkflowStats {
				if _, ok := tenant.Workflow(workflowKey); !ok {
					v.addf("%s: workflowStats mentions unknown workflow %q", where, workflowKey)
				}
			}
		}
	}
}

// --- helpers ---------------------------------------------------------------

type validator struct {
	problems []string
}

func (v *validator) addf(format string, args ...any) {
	v.problems = append(v.problems, fmt.Sprintf(format, args...))
}

func (v *validator) oneOf(what, value string, allowed []string) {
	if !slices.Contains(allowed, value) {
		v.addf("%s: %q is not one of [%s]", what, value, strings.Join(allowed, " "))
	}
}

func (v *validator) err() error {
	if len(v.problems) == 0 {
		return nil
	}
	return &ValidationError{Problems: v.problems}
}

// isMonthDay reports whether s is a legal MM-DD value.
func isMonthDay(s string) bool {
	if len(s) != 5 || s[2] != '-' {
		return false
	}
	month, err := strconv.Atoi(s[:2])
	if err != nil || month < 1 || month > 12 {
		return false
	}
	day, err := strconv.Atoi(s[3:])
	return err == nil && day >= 1 && day <= 31
}
