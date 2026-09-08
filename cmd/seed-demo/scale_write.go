package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/platform/crypto"
	platformseed "github.com/mohamadhallal/zentax-api/platform/seed"
	"github.com/mohamadhallal/zentax-api/seed/demo/scale"
	"github.com/mohamadhallal/zentax-api/seed/demo/spec"
	"github.com/mohamadhallal/zentax-api/shared/dateonly"
)

// The scale writer: the generated tenant, bulk-inserted.
//
// The demo seeder drives everything through the API because a demo must
// prove the product. The scale fixture proves something else — that the
// lists and reports hold up at 10⁵ instances — and 97,152 PUT calls would
// take an hour. So it writes the rows directly, in the shape the API would
// have written them:
//
//   - the tenant, its admin and the predefined data templates go through
//     platform/seed.CreateTenant, exactly as `seed` and cmd/seed-admin do;
//   - the members are plain users rows (not RLS-scoped) sharing ONE argon2id
//     hash, computed once — hashing is deliberately slow;
//   - everything else lands in ONE transaction bound to the tenant
//     (app.WithTenantID → SELECT set_config('app.tenant_id', …, true), the
//     seed_backdate.go pattern), with the admin as the acting user so
//     created_by / updated_by default from app.user_id: entities →
//     user_grants → obligation types → entity obligations → workflows →
//     workflow_tasks → task_instances, each as INSERT … SELECT FROM unnest(…)
//     over parallel arrays in batches of scaleBatchRows. Ids are generated in
//     Go, so no child ever waits on RETURNING. The parent tables are
//     addressed, never a hash partition (ADR-0020).
//
// The instance rows are the verify oracle's own plan (buildOracleTenant over
// the generated spec): dates, statuses, completion instants, submitted /
// approved stamps. Whatever `verify --scale` will recompute is, by
// construction, what was written.
//
// What is deliberately NOT written: documents, audit_log entries (append-only
// and hash-chained — ADR-0007/0008 — so the trail simply has no entry for the
// fixture's history), sessions. created_at is back-dated to the workflow's
// fiscal-year start so `sort=createdAt` orders eight years of history the
// way a real tenant's would.

// scaleBatchRows is the unnest batch size: large enough that 97k rows are ~20
// statements, small enough to keep each statement's parameter arrays modest.
const scaleBatchRows = 5000

// scaleCounts is what the writer reports.
type scaleCounts struct {
	TenantID          string
	AdminID           string
	Users             int
	Grants            int
	Entities          int
	ObligationTypes   int
	EntityObligations int
	Workflows         int
	WorkflowTasks     int
	Instances         int
	Templates         int
	Elapsed           time.Duration
}

// scaleRows are the parallel column arrays of one bulk insert, keyed by
// column expression order. Text arrays throughout, cast in SQL — the only
// encoding the pgx stdlib driver has to know is text[], bool[] and int4[].
type scaleRows struct {
	text  map[string][]string
	bools map[string][]bool
	ints  map[string][]int32
	n     int
}

func newScaleRows() *scaleRows {
	return &scaleRows{text: map[string][]string{}, bools: map[string][]bool{}, ints: map[string][]int32{}}
}

// scaleColumn describes one inserted column: its name, the array kind it is
// passed as, and the SQL expression that casts the unnested text into it.
type scaleColumn struct {
	name string
	kind string // text | bool | int
	// cast wraps the unnested value; "%s" is the column reference.
	cast string
}

// scaleInsert renders one INSERT … SELECT … FROM unnest(...) for a batch of
// rows and the positional arguments to bind. Rows [from, to) are taken.
func scaleInsert(table string, columns []scaleColumn, rows *scaleRows, from, to int) (string, []any) {
	names := make([]string, len(columns))
	selects := make([]string, len(columns))
	params := make([]string, len(columns))
	aliases := make([]string, len(columns))
	args := make([]any, len(columns))
	for i, col := range columns {
		names[i] = col.name
		alias := "c" + fmt.Sprint(i)
		aliases[i] = alias
		cast := col.cast
		if cast == "" {
			cast = "%s"
		}
		selects[i] = strings.ReplaceAll(cast, "%s", "t."+alias)
		switch col.kind {
		case "bool":
			params[i] = fmt.Sprintf("$%d::bool[]", i+1)
			args[i] = rows.bools[col.name][from:to]
		case "int":
			params[i] = fmt.Sprintf("$%d::int4[]", i+1)
			args[i] = rows.ints[col.name][from:to]
		default:
			params[i] = fmt.Sprintf("$%d::text[]", i+1)
			args[i] = rows.text[col.name][from:to]
		}
	}
	return fmt.Sprintf("INSERT INTO %s (%s)\nSELECT %s\nFROM unnest(%s) AS t(%s)",
		table, strings.Join(names, ", "), strings.Join(selects, ", "),
		strings.Join(params, ", "), strings.Join(aliases, ", ")), args
}

// Cast expressions.
const (
	castUUID        = "%s::uuid"
	castUUIDNull    = "NULLIF(%s, '')::uuid"
	castText        = "%s"
	castTextNull    = "NULLIF(%s, '')"
	castDate        = "%s::date"
	castDateNull    = "NULLIF(%s, '')::date"
	castInstant     = "%s::timestamptz"
	castInstantNull = "NULLIF(%s, '')::timestamptz"
	castJSONB       = "%s::jsonb"
	castJSONBNull   = "NULLIF(%s, '')::jsonb"
)

// scaleBatches splits [0, n) into [from, to) ranges of at most size rows.
func scaleBatches(n, size int) [][2]int {
	if size < 1 {
		size = 1
	}
	var out [][2]int
	for from := 0; from < n; from += size {
		to := from + size
		if to > n {
			to = n
		}
		out = append(out, [2]int{from, to})
	}
	return out
}

// scaleExec runs the batches of one table.
func scaleExec(ctx context.Context, db platformseed.DB, table string, columns []scaleColumn, rows *scaleRows) error {
	for _, b := range scaleBatches(rows.n, scaleBatchRows) {
		stmt, args := scaleInsert(table, columns, rows, b[0], b[1])
		if _, err := db.ExecContext(ctx, stmt, args...); err != nil {
			return fmt.Errorf("insert %s rows %d..%d: %w", table, b[0], b[1], err)
		}
	}
	return nil
}

// scaleIDs is the key → id map the writer builds as it goes.
type scaleIDs struct {
	tenantID string
	adminID  string
	users    map[string]string // user key → id (admin included)
	entities map[string]string
	types    map[string]string
	eos      map[string]string
	wfs      map[string]string
	tmpls    map[string]string // workflow key + "|" + template key → id
	dts      map[string]string // template type → predefined data template id
}

func newID() string { return uuid.NewString() }

// instantText renders an instant for a timestamptz column ("" = NULL).
func instantText(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// scaleWrite materializes the generated tenant. `world` is the oracle's
// recomputation of `res.Tenant` (dates and instants); it must have been built
// from the same spec.
func scaleWrite(ctx context.Context, db platformseed.DB, res *scale.Result, world *oracleTenant) (scaleCounts, error) {
	started := time.Now()
	tenant := res.Tenant
	var counts scaleCounts

	// 1. Tenant + admin + predefined data templates: the supported bootstrap.
	ids, err := platformseed.CreateTenant(ctx, db, platformseed.Params{
		Slug: tenant.Slug, Name: tenant.Name, Email: tenant.Admin.Email,
		Password: tenant.Admin.Password, UserName: tenant.Admin.Name, Timezone: tenant.Timezone,
	})
	if err != nil {
		return counts, err
	}
	m := &scaleIDs{
		tenantID: ids.TenantID, adminID: ids.UserID,
		users:    map[string]string{tenant.Admin.Key: ids.UserID},
		entities: map[string]string{}, types: map[string]string{}, eos: map[string]string{},
		wfs: map[string]string{}, tmpls: map[string]string{}, dts: map[string]string{},
	}
	counts.TenantID, counts.AdminID, counts.Templates = ids.TenantID, ids.UserID, ids.Templates

	// 2. Members: one hash, reused (every member shares the demo password).
	if err := scaleWriteUsers(ctx, db, tenant, m); err != nil {
		return counts, err
	}
	counts.Users = len(tenant.AllUsers())

	// 3. Everything tenant-scoped, in one transaction bound to the tenant with
	//    the admin as the acting user.
	txCtx := app.WithRequester(app.WithTenantID(ctx, m.tenantID),
		&app.Requester{Kind: app.RequesterUser, ID: m.adminID})
	err = db.WithinTransaction(txCtx, func(ctx context.Context) error {
		if err := scaleWriteEntities(ctx, db, tenant, m); err != nil {
			return err
		}
		counts.Entities = len(tenant.Entities)
		n, err := scaleWriteGrants(ctx, db, tenant, m)
		if err != nil {
			return err
		}
		counts.Grants = n
		if err := scaleLoadDataTemplates(ctx, db, m); err != nil {
			return err
		}
		if err := scaleWriteObligationTypes(ctx, db, tenant, m); err != nil {
			return err
		}
		counts.ObligationTypes = len(tenant.ObligationTypes)
		if err := scaleWriteEntityObligations(ctx, db, tenant, m); err != nil {
			return err
		}
		counts.EntityObligations = len(tenant.EntityObligations)
		tasks, err := scaleWriteWorkflows(ctx, db, world, m)
		if err != nil {
			return err
		}
		counts.Workflows, counts.WorkflowTasks = len(world.workflows), tasks
		n, err = scaleWriteInstances(ctx, db, res.Spec, world, m)
		if err != nil {
			return err
		}
		counts.Instances = n
		return nil
	})
	if err != nil {
		return counts, fmt.Errorf("write the scale tenant: %w", err)
	}
	counts.Elapsed = time.Since(started)
	return counts, nil
}

var scaleUserColumns = []scaleColumn{
	{"id", "text", castUUID}, {"tenant_id", "text", castUUID}, {"email", "text", castText},
	{"name", "text", castText}, {"password_hash", "text", castText}, {"status", "text", castText},
}

func scaleWriteUsers(ctx context.Context, db platformseed.DB, tenant *spec.Tenant, m *scaleIDs) error {
	if len(tenant.Users) == 0 {
		return nil
	}
	// One hash: every member shares the demo password, and argon2id is
	// deliberately slow.
	hash, err := crypto.HashPassword(tenant.Users[0].Password)
	if err != nil {
		return fmt.Errorf("hash the member password: %w", err)
	}
	rows := newScaleRows()
	for _, u := range tenant.Users {
		if u.Password != tenant.Users[0].Password {
			return fmt.Errorf("user %s: the scale fixture shares one member password (one hash); got a different one", u.Key)
		}
		id := newID()
		m.users[u.Key] = id
		rows.text["id"] = append(rows.text["id"], id)
		rows.text["tenant_id"] = append(rows.text["tenant_id"], m.tenantID)
		rows.text["email"] = append(rows.text["email"], strings.ToLower(strings.TrimSpace(u.Email)))
		rows.text["name"] = append(rows.text["name"], u.Name)
		rows.text["password_hash"] = append(rows.text["password_hash"], hash)
		rows.text["status"] = append(rows.text["status"], u.Status)
		rows.n++
	}
	return scaleExec(ctx, db, "users", scaleUserColumns, rows)
}

var scaleEntityColumns = []scaleColumn{
	{"id", "text", castUUID}, {"parent_entity_id", "text", castUUIDNull}, {"name", "text", castText},
	{"legal_name", "text", castTextNull}, {"country", "text", castText}, {"tax_residency", "text", castTextNull},
	{"fiscal_calendar_pattern", "text", castText}, {"financial_year_end", "text", castTextNull},
	{"status", "text", castText}, {"created_at", "text", castInstant}, {"updated_at", "text", castInstant},
}

func scaleWriteEntities(ctx context.Context, db platformseed.DB, tenant *spec.Tenant, m *scaleIDs) error {
	for _, e := range tenant.Entities {
		m.entities[e.Key] = newID()
	}
	created := instantText(ptrTime(scaleTenantEpoch(tenant)))
	rows := newScaleRows()
	// Parents before children: the spec lists them that way, and one
	// statement inserts them all (RI checks run at statement end).
	for _, e := range tenant.Entities {
		parent := ""
		if e.Parent != nil {
			parent = m.entities[*e.Parent]
		}
		rows.text["id"] = append(rows.text["id"], m.entities[e.Key])
		rows.text["parent_entity_id"] = append(rows.text["parent_entity_id"], parent)
		rows.text["name"] = append(rows.text["name"], e.Name)
		rows.text["legal_name"] = append(rows.text["legal_name"], e.LegalName)
		rows.text["country"] = append(rows.text["country"], e.Country)
		rows.text["tax_residency"] = append(rows.text["tax_residency"], e.TaxResidency)
		rows.text["fiscal_calendar_pattern"] = append(rows.text["fiscal_calendar_pattern"], e.FiscalCalendarPattern)
		rows.text["financial_year_end"] = append(rows.text["financial_year_end"], e.FinancialYearEnd)
		rows.text["status"] = append(rows.text["status"], "active")
		rows.text["created_at"] = append(rows.text["created_at"], created)
		rows.text["updated_at"] = append(rows.text["updated_at"], created)
		rows.n++
	}
	return scaleExec(ctx, db, "entities", scaleEntityColumns, rows)
}

var scaleGrantColumns = []scaleColumn{
	{"id", "text", castUUID}, {"user_id", "text", castUUID}, {"role", "text", castText},
	{"scope_entity_id", "text", castUUIDNull},
}

func scaleWriteGrants(ctx context.Context, db platformseed.DB, tenant *spec.Tenant, m *scaleIDs) (int, error) {
	rows := newScaleRows()
	for _, u := range tenant.Users {
		scope := ""
		if u.ScopeEntity != nil {
			id, ok := m.entities[*u.ScopeEntity]
			if !ok {
				return 0, fmt.Errorf("user %s: scope entity %s has no id", u.Key, *u.ScopeEntity)
			}
			scope = id
		}
		rows.text["id"] = append(rows.text["id"], newID())
		rows.text["user_id"] = append(rows.text["user_id"], m.users[u.Key])
		rows.text["role"] = append(rows.text["role"], u.Role)
		rows.text["scope_entity_id"] = append(rows.text["scope_entity_id"], scope)
		rows.n++
	}
	if rows.n == 0 {
		return 0, nil
	}
	return rows.n, scaleExec(ctx, db, "user_grants", scaleGrantColumns, rows)
}

// scaleLoadDataTemplates reads the tenant's predefined templates (RLS scopes
// the query to the bound tenant) so a Prepare template can attach its own.
func scaleLoadDataTemplates(ctx context.Context, db platformseed.DB, m *scaleIDs) error {
	var rows []struct {
		ID           string `db:"id"`
		TemplateType string `db:"template_type"`
	}
	if err := db.SelectContext(ctx, &rows,
		`SELECT id, template_type FROM data_templates WHERE category = 'predefined' ORDER BY template_type`); err != nil {
		return fmt.Errorf("load predefined data templates: %w", err)
	}
	for _, r := range rows {
		if _, seen := m.dts[r.TemplateType]; !seen {
			m.dts[r.TemplateType] = r.ID
		}
	}
	return nil
}

var scaleObligationTypeColumns = []scaleColumn{
	{"id", "text", castUUID}, {"name", "text", castText}, {"code", "text", castText},
	{"category", "text", castText}, {"template", "text", castText}, {"status", "text", castText},
	{"description", "text", castTextNull}, {"created_at", "text", castInstant}, {"updated_at", "text", castInstant},
}

func scaleWriteObligationTypes(ctx context.Context, db platformseed.DB, tenant *spec.Tenant, m *scaleIDs) error {
	created := instantText(ptrTime(scaleTenantEpoch(tenant)))
	rows := newScaleRows()
	for _, ot := range tenant.ObligationTypes {
		m.types[ot.Key] = newID()
		rows.text["id"] = append(rows.text["id"], m.types[ot.Key])
		rows.text["name"] = append(rows.text["name"], ot.Name)
		rows.text["code"] = append(rows.text["code"], ot.Code)
		rows.text["category"] = append(rows.text["category"], ot.Category)
		rows.text["template"] = append(rows.text["template"], ot.Template)
		rows.text["status"] = append(rows.text["status"], "active")
		rows.text["description"] = append(rows.text["description"], ot.Description)
		rows.text["created_at"] = append(rows.text["created_at"], created)
		rows.text["updated_at"] = append(rows.text["updated_at"], created)
		rows.n++
	}
	return scaleExec(ctx, db, "obligation_types", scaleObligationTypeColumns, rows)
}

var scaleEntityObligationColumns = []scaleColumn{
	{"id", "text", castUUID}, {"entity_id", "text", castUUID}, {"obligation_type_id", "text", castUUID},
	{"jurisdiction", "text", castTextNull}, {"periodicity", "text", castText}, {"deadline_rule", "text", castJSONB},
	{"status", "text", castText}, {"tax_reference_number", "text", castTextNull}, {"currency", "text", castTextNull},
	{"created_at", "text", castInstant}, {"updated_at", "text", castInstant},
}

func scaleWriteEntityObligations(ctx context.Context, db platformseed.DB, tenant *spec.Tenant, m *scaleIDs) error {
	created := instantText(ptrTime(scaleTenantEpoch(tenant)))
	rows := newScaleRows()
	for _, eo := range tenant.EntityObligations {
		rule, err := json.Marshal(eo.DeadlineRule)
		if err != nil {
			return fmt.Errorf("entity obligation %s: encode deadline rule: %w", eo.Key, err)
		}
		ref := ""
		if eo.TaxReferenceNumber != nil {
			ref = *eo.TaxReferenceNumber
		}
		m.eos[eo.Key] = newID()
		rows.text["id"] = append(rows.text["id"], m.eos[eo.Key])
		rows.text["entity_id"] = append(rows.text["entity_id"], m.entities[eo.Entity])
		rows.text["obligation_type_id"] = append(rows.text["obligation_type_id"], m.types[eo.ObligationType])
		rows.text["jurisdiction"] = append(rows.text["jurisdiction"], eo.Jurisdiction)
		rows.text["periodicity"] = append(rows.text["periodicity"], eo.Periodicity)
		rows.text["deadline_rule"] = append(rows.text["deadline_rule"], string(rule))
		rows.text["status"] = append(rows.text["status"], "active")
		rows.text["tax_reference_number"] = append(rows.text["tax_reference_number"], ref)
		rows.text["currency"] = append(rows.text["currency"], strings.ToUpper(eo.Currency))
		rows.text["created_at"] = append(rows.text["created_at"], created)
		rows.text["updated_at"] = append(rows.text["updated_at"], created)
		rows.n++
	}
	return scaleExec(ctx, db, "entity_obligations", scaleEntityObligationColumns, rows)
}

var scaleWorkflowColumns = []scaleColumn{
	{"id", "text", castUUID}, {"name", "text", castText}, {"description", "text", castTextNull},
	{"workflow_category", "text", castText}, {"financial_year", "text", castTextNull},
	{"periodicity", "text", castTextNull}, {"selected_periods", "text", castJSONB},
	{"entity_id", "text", castUUIDNull}, {"obligation_type_id", "text", castUUIDNull},
	{"due_date_rule", "text", castJSONB}, {"start_date", "text", castTextNull}, {"end_date", "text", castTextNull},
	{"tasks_sequential", "bool", ""}, {"status", "text", castText},
	{"created_at", "text", castInstant}, {"updated_at", "text", castInstant},
}

var scaleWorkflowTaskColumns = []scaleColumn{
	{"id", "text", castUUID}, {"workflow_id", "text", castUUID}, {"name", "text", castText},
	{"task_type", "text", castText}, {"role_label", "text", castTextNull}, {"approval_required", "bool", ""},
	{"due_date_reference", "text", castText}, {"due_date_offset_value", "int", ""},
	{"due_date_offset_unit", "text", castText}, {"due_date_offset_direction", "text", castText},
	{"order_index", "int", ""}, {"data_template_id", "text", castUUIDNull},
	{"created_at", "text", castInstant}, {"updated_at", "text", castInstant},
}

// scaleWriteWorkflows inserts the workflows and their task templates. It
// returns the number of templates written.
func scaleWriteWorkflows(ctx context.Context, db platformseed.DB, world *oracleTenant, m *scaleIDs) (int, error) {
	wfRows, taskRows := newScaleRows(), newScaleRows()
	for _, w := range world.workflows {
		sw := w.spec
		periods, err := json.Marshal(sw.SelectedPeriods)
		if err != nil {
			return 0, fmt.Errorf("workflow %s: encode periods: %w", sw.Key, err)
		}
		rule, err := json.Marshal(sw.DueDateRule)
		if err != nil {
			return 0, fmt.Errorf("workflow %s: encode due-date rule: %w", sw.Key, err)
		}
		entityID, typeID := "", ""
		if sw.Entity != nil {
			entityID = m.entities[*sw.Entity]
		}
		if sw.ObligationType != nil {
			typeID = m.types[*sw.ObligationType]
		}
		id := newID()
		m.wfs[sw.Key] = id
		created := instantText(ptrTime(scaleWorkflowCreatedAt(world, sw)))
		status := w.status
		if status == "" {
			status = "draft"
		}
		wfRows.text["id"] = append(wfRows.text["id"], id)
		wfRows.text["name"] = append(wfRows.text["name"], sw.Name)
		wfRows.text["description"] = append(wfRows.text["description"], sw.Description)
		wfRows.text["workflow_category"] = append(wfRows.text["workflow_category"], sw.Category)
		wfRows.text["financial_year"] = append(wfRows.text["financial_year"], sw.FinancialYear)
		wfRows.text["periodicity"] = append(wfRows.text["periodicity"], derefString(sw.Periodicity))
		wfRows.text["selected_periods"] = append(wfRows.text["selected_periods"], string(periods))
		wfRows.text["entity_id"] = append(wfRows.text["entity_id"], entityID)
		wfRows.text["obligation_type_id"] = append(wfRows.text["obligation_type_id"], typeID)
		wfRows.text["due_date_rule"] = append(wfRows.text["due_date_rule"], string(rule))
		wfRows.text["start_date"] = append(wfRows.text["start_date"], dateText(sw.StartDate))
		wfRows.text["end_date"] = append(wfRows.text["end_date"], dateText(sw.EndDate))
		wfRows.bools["tasks_sequential"] = append(wfRows.bools["tasks_sequential"], sw.TasksSequential)
		wfRows.text["status"] = append(wfRows.text["status"], status)
		wfRows.text["created_at"] = append(wfRows.text["created_at"], created)
		wfRows.text["updated_at"] = append(wfRows.text["updated_at"], created)
		wfRows.n++

		for _, tmpl := range oracleSortedTemplates(sw.Templates) {
			dataTemplate := ""
			if tmpl.DataTemplate != nil {
				dt, ok := m.dts[*tmpl.DataTemplate]
				if !ok {
					return 0, fmt.Errorf("workflow %s template %s: the tenant has no predefined %s data template",
						sw.Key, tmpl.Key, *tmpl.DataTemplate)
				}
				dataTemplate = dt
			}
			tid := newID()
			m.tmpls[sw.Key+"|"+tmpl.Key] = tid
			taskRows.text["id"] = append(taskRows.text["id"], tid)
			taskRows.text["workflow_id"] = append(taskRows.text["workflow_id"], id)
			taskRows.text["name"] = append(taskRows.text["name"], tmpl.Name)
			taskRows.text["task_type"] = append(taskRows.text["task_type"], tmpl.TaskType)
			taskRows.text["role_label"] = append(taskRows.text["role_label"], tmpl.RoleLabel)
			taskRows.bools["approval_required"] = append(taskRows.bools["approval_required"], tmpl.ApprovalRequired)
			taskRows.text["due_date_reference"] = append(taskRows.text["due_date_reference"], tmpl.DueDateReference)
			taskRows.ints["due_date_offset_value"] = append(taskRows.ints["due_date_offset_value"], int32(tmpl.DueDateOffsetValue))
			taskRows.text["due_date_offset_unit"] = append(taskRows.text["due_date_offset_unit"], tmpl.DueDateOffsetUnit)
			taskRows.text["due_date_offset_direction"] = append(taskRows.text["due_date_offset_direction"], tmpl.DueDateOffsetDirection)
			taskRows.ints["order_index"] = append(taskRows.ints["order_index"], int32(tmpl.OrderIndex))
			taskRows.text["data_template_id"] = append(taskRows.text["data_template_id"], dataTemplate)
			taskRows.text["created_at"] = append(taskRows.text["created_at"], created)
			taskRows.text["updated_at"] = append(taskRows.text["updated_at"], created)
			taskRows.n++
		}
	}
	if err := scaleExec(ctx, db, "workflows", scaleWorkflowColumns, wfRows); err != nil {
		return 0, err
	}
	if err := scaleExec(ctx, db, "workflow_tasks", scaleWorkflowTaskColumns, taskRows); err != nil {
		return 0, err
	}
	return taskRows.n, nil
}

var scaleInstanceColumns = []scaleColumn{
	{"id", "text", castUUID}, {"workflow_id", "text", castUUID}, {"workflow_task_id", "text", castUUID},
	{"period_code", "text", castText}, {"name", "text", castText}, {"task_type", "text", castText},
	{"status", "text", castText}, {"assignee_id", "text", castUUIDNull},
	{"due_date", "text", castDate}, {"period_end_date", "text", castDate}, {"filing_deadline", "text", castDate},
	{"payment_deadline", "text", castDateNull}, {"approval_required", "bool", ""},
	{"approved_by", "text", castUUIDNull}, {"approved_at", "text", castInstantNull},
	{"completed_at", "text", castInstantNull}, {"submitted_by", "text", castUUIDNull},
	{"submitted_at", "text", castInstantNull}, {"order_index", "int", ""},
	{"notes", "text", castTextNull}, {"data_template_id", "text", castUUIDNull},
	{"tax_data", "text", castJSONBNull}, {"tax_data_status", "text", castText},
	{"created_at", "text", castInstant}, {"updated_at", "text", castInstant},
	// The assignee touched the row last; an untouched row's updater is the
	// acting user, as the column default would have made it.
	{"updated_by", "text", "COALESCE(NULLIF(%s, '')::uuid, NULLIF(current_setting('app.user_id', true), '')::uuid)"},
}

// scaleWriteInstances inserts every started workflow's instances from the
// oracle's plan, with the instants the demo seeder's escape hatch would have
// written (instantsFor: completion at the local completion time in the
// tenant's zone; approved_at = completed_at; submitted_at from submittedOn).
func scaleWriteInstances(ctx context.Context, db platformseed.DB, s *spec.Spec, world *oracleTenant, m *scaleIDs) (int, error) {
	rows := newScaleRows()
	add := func(col, v string) { rows.text[col] = append(rows.text[col], v) }
	for _, w := range world.workflows {
		if !w.started {
			continue
		}
		sw := w.spec
		wfID := m.wfs[sw.Key]
		created := scaleWorkflowCreatedAt(world, sw).Add(time.Minute) // started right after creation
		writerID, approverID := m.users[sw.Writer], m.users[sw.Approver]
		for _, inst := range w.instances {
			taskID, ok := m.tmpls[sw.Key+"|"+inst.TemplateKey]
			if !ok {
				return 0, fmt.Errorf("workflow %s: template %s has no id", sw.Key, inst.TemplateKey)
			}
			dev := spec.Instance{
				Period: inst.PeriodCode, Task: inst.TemplateKey, Status: inst.Status, Via: inst.Via,
				CompletedOn: inst.CompletedOn, SubmittedOn: inst.SubmittedOn,
			}
			stamps, _, err := instantsFor(dev, world.zone, s.CompletionLocalTime())
			if err != nil {
				return 0, fmt.Errorf("%s: %w", inst.ref(), err)
			}
			if !inst.CompletedAt.IsZero() && (stamps.CompletedAt == nil || !stamps.CompletedAt.Equal(inst.CompletedAt)) {
				return 0, fmt.Errorf("%s: the oracle's completion instant %s disagrees with the seeder's rule",
					inst.ref(), inst.CompletedAt.Format(time.RFC3339))
			}
			assignee := ""
			if inst.AssigneeKey != "" {
				id, ok := m.users[inst.AssigneeKey]
				if !ok {
					return 0, fmt.Errorf("%s: assignee %s has no id", inst.ref(), inst.AssigneeKey)
				}
				assignee = id
			}
			approvedBy, submittedBy := "", ""
			switch inst.Via {
			case spec.ViaApprove:
				approvedBy, submittedBy = approverID, writerID
			case spec.ViaSubmit:
				submittedBy = writerID
			}
			dataTemplate := ""
			if inst.DataTemplate != "" {
				dataTemplate = m.dts[inst.DataTemplate]
			}
			taxData := ""
			if len(inst.TaxData) > 0 {
				encoded, err := json.Marshal(inst.TaxData)
				if err != nil {
					return 0, fmt.Errorf("%s: encode tax data: %w", inst.ref(), err)
				}
				taxData = string(encoded)
			}
			updated := created
			if at := stamps.updatedAt(); !at.IsZero() {
				updated = at
			}

			add("id", newID())
			add("workflow_id", wfID)
			add("workflow_task_id", taskID)
			add("period_code", inst.PeriodCode)
			add("name", inst.Name)
			add("task_type", inst.TaskType)
			add("status", inst.Status)
			add("assignee_id", assignee)
			add("due_date", inst.DueDate.String())
			add("period_end_date", inst.PeriodEnd.String())
			add("filing_deadline", inst.FilingDeadline.String())
			add("payment_deadline", datePtrText(inst.PaymentDeadline))
			rows.bools["approval_required"] = append(rows.bools["approval_required"], inst.ApprovalRequired)
			add("approved_by", approvedBy)
			add("approved_at", instantText(stamps.ApprovedAt))
			add("completed_at", instantText(stamps.CompletedAt))
			add("submitted_by", submittedBy)
			add("submitted_at", instantText(stamps.SubmittedAt))
			rows.ints["order_index"] = append(rows.ints["order_index"], int32(inst.OrderIndex))
			add("notes", inst.Notes)
			add("data_template_id", dataTemplate)
			add("tax_data", taxData)
			add("tax_data_status", inst.TaxDataStatus)
			add("created_at", instantText(&created))
			add("updated_at", instantText(&updated))
			add("updated_by", assignee)
			rows.n++
		}
	}
	return rows.n, scaleExec(ctx, db, "task_instances", scaleInstanceColumns, rows)
}

// scaleTenantEpoch is the instant the tenant's setup rows are dated: the
// morning of the earliest workflow start in the tenant, so the setup precedes
// every workflow. Falls back to the spec's asOf.
func scaleTenantEpoch(tenant *spec.Tenant) time.Time {
	zone, err := time.LoadLocation(tenant.Timezone)
	if err != nil {
		zone = time.UTC
	}
	var earliest time.Time
	for _, w := range tenant.Workflows {
		if w.StartDate.IsZero() {
			continue
		}
		t := time.Date(w.StartDate.Year, time.Month(w.StartDate.Month), w.StartDate.Day, 8, 0, 0, 0, zone)
		if earliest.IsZero() || t.Before(earliest) {
			earliest = t
		}
	}
	if earliest.IsZero() {
		return time.Now().UTC()
	}
	return earliest.AddDate(0, 0, -30).UTC()
}

// scaleWorkflowCreatedAt dates a workflow's creation: 20 days before its
// fiscal year starts, at 09:00 in the tenant's zone, plus a per-workflow
// minute so a tenant's rows are not all created "at once" — but never after
// the tenant's asOf (a draft for next year is created three days before it).
func scaleWorkflowCreatedAt(world *oracleTenant, w spec.Workflow) time.Time {
	zone := world.zone
	created := time.Date(w.StartDate.Year, time.Month(w.StartDate.Month), w.StartDate.Day, 9, 0, 0, 0, zone).
		AddDate(0, 0, -20).
		Add(time.Duration(scaleMinuteOf(w.Key)) * time.Minute)
	latest := time.Date(world.today.Year, time.Month(world.today.Month), world.today.Day, 9, 0, 0, 0, zone).
		AddDate(0, 0, -3)
	if created.After(latest) {
		created = latest.Add(time.Duration(scaleMinuteOf(w.Key)) * time.Minute)
	}
	return created.UTC()
}

// scaleMinuteOf spreads workflow creation instants over a working day,
// deterministically from the key.
func scaleMinuteOf(key string) int {
	h := 0
	for _, r := range key {
		h = (h*31 + int(r)) % 480
	}
	return h
}

func ptrTime(t time.Time) *time.Time { return &t }

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func dateText(d dateonly.Date) string {
	if d.IsZero() {
		return ""
	}
	return d.String()
}

func datePtrText(d *dateonly.Date) string {
	if d == nil {
		return ""
	}
	return dateText(*d)
}
