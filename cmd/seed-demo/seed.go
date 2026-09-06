package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	platformseed "github.com/mohamadhallal/zentax-api/platform/seed"
	"github.com/mohamadhallal/zentax-api/seed/demo/spec"
	"github.com/mohamadhallal/zentax-api/shared/apiclient"
)

// runSeed materializes the dataset.
//
// Everything below is an ordinary HTTP call made as one of the seeded users,
// in the dataset's own order, with exactly two exceptions that no endpoint
// exposes: the tenant bootstrap (platform/seed, the same path cmd/seed-admin
// takes) and the completion instants (seed_backdate.go). If a demo state can be
// reached through the API, the seeder reaches it through the API — otherwise
// the demo would prove nothing about the product.
//
// The stage order is $schemaNotes.conventions.creationOrder:
//
//	tenant + admin + predefined templates (SQL)
//	  → entities (parents first)   — a scoped grant needs its entity to exist
//	  → obligation types
//	  → members (invite → accept → login)
//	  → entity obligations
//	  → workflows + task templates
//	  → preview + start
//	  → per-instance actions (PUT → documents → submit → approve)
//	  → workflow final status
//	  → disable the members marked disabled
//	  → the one direct-database pass: completion instants
func runSeed(ctx context.Context, d *deps) error {
	if len(d.Tenants) == 0 {
		return errors.New("no tenants selected")
	}
	if d.DryRun {
		return dryRunSeed(d)
	}
	if d.Reset {
		if !d.Yes {
			return errors.New("`seed --reset` deletes the dataset's tenants before seeding; pass --yes to confirm")
		}
		if err := runReset(ctx, d); err != nil {
			return err
		}
		fmt.Fprintln(d.Out)
	}

	fmt.Fprintf(d.Out, "seeding %s into %s\n", d.SpecPath, d.API.BaseURL())
	fmt.Fprintf(d.Out, "  dataset asOf %s (classifications hold for a tenant-today in %s..%s)\n",
		d.Spec.AsOf, d.Spec.ValidityWindow.From, d.Spec.ValidityWindow.To)

	if err := waitForAPI(ctx, d.API, d.Out); err != nil {
		return err
	}
	if err := preflight(ctx, d); err != nil {
		return err
	}

	worlds := make([]*tenantWorld, 0, len(d.Tenants))
	for _, tenant := range d.Tenants {
		world, err := seedTenant(ctx, d, tenant)
		if err != nil {
			return fmt.Errorf("tenant %s (%s): %w", tenant.Key, tenant.Slug, err)
		}
		worlds = append(worlds, world)
	}

	// The one direct-database pass, deliberately last and deliberately separate:
	// everything the API can do has been done by now.
	for _, world := range worlds {
		count, err := backdateCompletions(ctx, d.DB, world.tenantID, world.backdates)
		if err != nil {
			return fmt.Errorf("tenant %s (%s): %w", world.spec.Key, world.spec.Slug, err)
		}
		world.out.Counts.BackdatedRows = count
		fmt.Fprintf(d.Out, "  [%s] back-dated %d completion instants (audit_log untouched)\n", world.spec.Key, count)
	}

	out := seedOutput{
		GeneratedAt: formatInstant(d.Now()),
		SpecPath:    d.SpecPath,
		APIBaseURL:  d.API.BaseURL(),
		AsOf:        d.Spec.AsOf.String(),
		Warning:     outputWarning,
	}
	for _, world := range worlds {
		out.Tenants = append(out.Tenants, *world.out)
	}
	if err := writeSeedOutput(d.OutputPath, out); err != nil {
		return err
	}

	printSummary(d, worlds)
	return nil
}

// waitForAPI blocks until the API is up.
//
// The readiness probe is GET /health/ready (modules/health/router.go). An API
// image built before that route existed answers 404 there while being perfectly
// alive on GET /health — and that is not hypothetical: the compose stack's api
// image is built from this repository and goes stale until it is rebuilt. So a
// 404 falls back to the liveness probe and says what it found, instead of
// reporting a running server as "not ready" and sending the operator to look at
// the wrong thing. A stale image is worth mentioning anyway: the dataset needs
// product fixes that may not be in it.
func waitForAPI(ctx context.Context, client *apiclient.Client, out io.Writer) error {
	err := client.WaitReady(ctx)
	switch {
	case err == nil:
		return nil
	case !apiclient.IsNotFound(err):
		return fmt.Errorf("the API at %s is not ready (start the stack, or pass --api): %w", client.BaseURL(), err)
	}
	if _, _, liveErr := client.GetRaw(ctx, "/health"); liveErr != nil {
		return fmt.Errorf("the API at %s answers neither /health/ready nor /health: %w", client.BaseURL(), err)
	}
	fmt.Fprintf(out, "  note: %s has no /health/ready, so its image predates the readiness probe. "+
		"It is alive — but it may also predate the product fixes this dataset needs "+
		"(F1 canonical tax-data keys, F6 project-workflow instances). Rebuild it if seeding fails.\n",
		client.BaseURL())
	return nil
}

// tenantWorld is one tenant's state while it is being built: every key → id map
// the later stages resolve against, the live sessions, and the rows the
// completion pass will rewrite.
type tenantWorld struct {
	spec     spec.Tenant
	tenantID string
	zone     *time.Location

	// setupKey is the user who performs the setup writes: the tenant's
	// manager, or the admin when it has none (the dataset's own rule —
	// "as the tenant manager (initech: admin)").
	setupKey string
	// sessions is every logged-in user, keyed by the dataset's user key.
	sessions map[string]*apiclient.Session

	userIDs             map[string]string
	entityIDs           map[string]string
	obligationTypeIDs   map[string]string
	entityObligationIDs map[string]string
	dataTemplateIDs     map[string]string
	workflowIDs         map[string]string

	backdates []backdateRow
	out       *tenantOutput
}

func (w *tenantWorld) admin() *apiclient.Session { return w.sessions[w.spec.Admin.Key] }
func (w *tenantWorld) setup() *apiclient.Session { return w.sessions[w.setupKey] }

// seedTenant builds one tenant end to end.
func seedTenant(ctx context.Context, d *deps, tenant spec.Tenant) (*tenantWorld, error) {
	zone, err := time.LoadLocation(tenant.Timezone)
	if err != nil {
		return nil, fmt.Errorf("load timezone %q: %w", tenant.Timezone, err)
	}
	world := &tenantWorld{
		spec:                tenant,
		zone:                zone,
		setupKey:            setupActorKey(tenant),
		sessions:            map[string]*apiclient.Session{},
		userIDs:             map[string]string{},
		entityIDs:           map[string]string{},
		obligationTypeIDs:   map[string]string{},
		entityObligationIDs: map[string]string{},
		dataTemplateIDs:     map[string]string{},
		workflowIDs:         map[string]string{},
		out:                 newTenantOutput(tenant.Key, tenant.Slug, tenant.Name, tenant.Timezone),
	}

	fmt.Fprintf(d.Out, "\n[%s] tenant %s (%s, %s)\n", tenant.Key, tenant.Name, tenant.Slug, tenant.Timezone)

	// 1. tenant + admin + predefined data templates, by direct SQL: there is no
	//    tenant endpoint (ADR-0011), and this is the supported bootstrap path.
	ids, err := platformseed.CreateTenant(ctx, d.DB, platformseed.Params{
		Slug:     tenant.Slug,
		Name:     tenant.Name,
		Email:    tenant.Admin.Email,
		Password: tenant.Admin.Password,
		UserName: tenant.Admin.Name,
		Timezone: tenant.Timezone,
	})
	if err != nil {
		return nil, err
	}
	world.tenantID = ids.TenantID
	world.out.TenantID = ids.TenantID
	world.userIDs[tenant.Admin.Key] = ids.UserID
	world.recordUser(tenant.Admin, ids.UserID)
	fmt.Fprintf(d.Out, "  tenant_id %s, admin %s, %d predefined data templates\n",
		ids.TenantID, ids.UserID, ids.Templates)

	// 2. log in as the admin — every later stage is an authenticated request.
	adminSession, err := login(ctx, d.API, tenant.Admin)
	if err != nil {
		return nil, err
	}
	world.sessions[tenant.Admin.Key] = adminSession

	// 3. entities, in dataset order (parents before children).
	if err := seedEntities(ctx, world); err != nil {
		return nil, err
	}
	fmt.Fprintf(d.Out, "  entities: %d\n", len(world.entityIDs))

	// 4. obligation types.
	if err := seedObligationTypes(ctx, world); err != nil {
		return nil, err
	}
	fmt.Fprintf(d.Out, "  obligation types: %d\n", len(world.obligationTypeIDs))

	// 5. members: invite → accept → log in. Entities exist by now, so a scoped
	//    grant can name one.
	if err := seedMembers(ctx, d, world); err != nil {
		return nil, err
	}
	fmt.Fprintf(d.Out, "  members: %d invited, accepted and signed in\n", len(tenant.Users))

	// 6. the tenant's predefined data templates, so a task template can attach
	//    the one its tax type calls for.
	if err := loadDataTemplates(ctx, world); err != nil {
		return nil, err
	}

	// 7. entity obligations, by the setup actor.
	if err := seedEntityObligations(ctx, world); err != nil {
		return nil, err
	}
	fmt.Fprintf(d.Out, "  entity obligations: %d\n", len(world.entityObligationIDs))

	// 8. workflows, task templates, start, and the per-instance deviations.
	for _, workflow := range tenant.Workflows {
		if err := seedWorkflow(ctx, d, world, workflow); err != nil {
			return nil, fmt.Errorf("workflow %s: %w", workflow.Key, err)
		}
	}

	// 9. disable the members the dataset marks disabled — after every instance
	//    that needed them (a disabled member cannot be assigned a task).
	if err := disableMembers(ctx, world); err != nil {
		return nil, err
	}

	world.out.Counts.Users = len(tenant.AllUsers())
	world.out.Counts.Entities = len(world.entityIDs)
	world.out.Counts.ObligationTypes = len(world.obligationTypeIDs)
	world.out.Counts.EntityObligations = len(world.entityObligationIDs)
	world.out.Counts.Workflows = len(tenant.Workflows)
	return world, nil
}

// setupActorKey picks the user who performs the setup writes. The dataset's
// rule is "the tenant manager, the admin where there is none".
func setupActorKey(tenant spec.Tenant) string {
	for _, user := range tenant.Users {
		if user.Role == "manager" && user.IsActive() && user.ScopeEntity == nil {
			return user.Key
		}
	}
	return tenant.Admin.Key
}

func login(ctx context.Context, client *apiclient.Client, user spec.User) (*apiclient.Session, error) {
	session, err := client.Login(ctx, user.Email, user.Password)
	if err != nil {
		return nil, fmt.Errorf("log in as %s (%s): %w", user.Key, user.Email, err)
	}
	if session.MFARequired {
		return nil, fmt.Errorf("log in as %s (%s): the account requires a second factor, "+
			"which no demo user should have", user.Key, user.Email)
	}
	return session, nil
}

// --- request bodies -------------------------------------------------------
//
// Each mirrors the module's Create*Body DTO. Only the fields the dataset sets
// are present: the API rejects an unknown body field with a 400, and an omitted
// optional field takes the use case's documented default.

type idResponse struct {
	ID string `json:"id"`
}

type createEntityBody struct {
	Name                  string  `json:"name"`
	LegalName             *string `json:"legalName,omitempty"`
	Country               string  `json:"country"`
	TaxResidency          *string `json:"taxResidency,omitempty"`
	ParentEntityID        *string `json:"parentEntityId,omitempty"`
	FiscalCalendarPattern string  `json:"fiscalCalendarPattern,omitempty"`
	FinancialYearEnd      *string `json:"financialYearEnd,omitempty"`
}

type createObligationTypeBody struct {
	Name        string  `json:"name"`
	Code        string  `json:"code"`
	Template    string  `json:"template"`
	Category    string  `json:"category,omitempty"`
	Description *string `json:"description,omitempty"`
}

type createEntityObligationBody struct {
	EntityID           string            `json:"entityId"`
	ObligationTypeID   string            `json:"obligationTypeId"`
	TaxReferenceNumber *string           `json:"taxReferenceNumber,omitempty"`
	Jurisdiction       *string           `json:"jurisdiction,omitempty"`
	Currency           *string           `json:"currency,omitempty"`
	Periodicity        string            `json:"periodicity"`
	DeadlineRule       spec.DeadlineRule `json:"deadlineRule"`
}

type workflowBody struct {
	Name             string           `json:"name"`
	Description      *string          `json:"description,omitempty"`
	WorkflowCategory string           `json:"workflowCategory"`
	ProjectType      *string          `json:"projectType,omitempty"`
	FinancialYear    *string          `json:"financialYear,omitempty"`
	Periodicity      *string          `json:"periodicity,omitempty"`
	SelectedPeriods  []string         `json:"selectedPeriods,omitempty"`
	EntityID         *string          `json:"entityId,omitempty"`
	ObligationTypeID *string          `json:"obligationTypeId,omitempty"`
	DueDateRule      spec.DueDateRule `json:"dueDateRule"`
	StartDate        *string          `json:"startDate,omitempty"`
	EndDate          *string          `json:"endDate,omitempty"`
	TasksSequential  bool             `json:"tasksSequential"`
	// Status is sent on the update only: PUT /workflows/{id} is a full
	// replacement and an omitted status resets the workflow to draft.
	Status string `json:"status,omitempty"`
}

type createWorkflowTaskBody struct {
	WorkflowID             string  `json:"workflowId"`
	Name                   string  `json:"name"`
	TaskType               string  `json:"taskType"`
	RoleLabel              *string `json:"roleLabel,omitempty"`
	ApprovalRequired       bool    `json:"approvalRequired"`
	DueDateReference       string  `json:"dueDateReference,omitempty"`
	DueDateOffsetValue     int     `json:"dueDateOffsetValue"`
	DueDateOffsetUnit      string  `json:"dueDateOffsetUnit,omitempty"`
	DueDateOffsetDirection string  `json:"dueDateOffsetDirection,omitempty"`
	OrderIndex             int     `json:"orderIndex"`
	DataTemplateID         *string `json:"dataTemplateId,omitempty"`
}

type createMemberBody struct {
	Email         string  `json:"email"`
	Name          string  `json:"name"`
	Role          string  `json:"role"`
	ScopeEntityID *string `json:"scopeEntityId,omitempty"`
}

type updateMemberBody struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

type inviteResponse struct {
	InviteToken string     `json:"inviteToken"`
	Member      idResponse `json:"member"`
}

type dataTemplateRow struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	TemplateType string `json:"templateType"`
	Category     string `json:"category"`
}

// --- stages ---------------------------------------------------------------

func seedEntities(ctx context.Context, world *tenantWorld) error {
	admin := world.admin()
	for _, entity := range world.spec.Entities {
		body := createEntityBody{
			Name:                  entity.Name,
			Country:               entity.Country,
			FiscalCalendarPattern: entity.FiscalCalendarPattern,
		}
		if entity.LegalName != "" {
			body.LegalName = &entity.LegalName
		}
		if entity.TaxResidency != "" {
			body.TaxResidency = &entity.TaxResidency
		}
		if entity.FinancialYearEnd != "" {
			body.FinancialYearEnd = &entity.FinancialYearEnd
		}
		if entity.Parent != nil {
			parentID, ok := world.entityIDs[*entity.Parent]
			if !ok {
				return fmt.Errorf("entity %s: parent %s has not been created yet "+
					"(the dataset must list parents before children)", entity.Key, *entity.Parent)
			}
			body.ParentEntityID = &parentID
		}

		var created idResponse
		if err := admin.POST(ctx, "/entities", body, &created); err != nil {
			return fmt.Errorf("entity %s (%s): POST /entities: %w", entity.Key, entity.Name, err)
		}
		world.entityIDs[entity.Key] = created.ID
		world.out.Entities[entity.Key] = created.ID
	}
	return nil
}

func seedObligationTypes(ctx context.Context, world *tenantWorld) error {
	admin := world.admin()
	for _, obligation := range world.spec.ObligationTypes {
		body := createObligationTypeBody{
			Name:     obligation.Name,
			Code:     obligation.Code,
			Template: obligation.Template,
			Category: obligation.Category,
		}
		if obligation.Description != "" {
			body.Description = &obligation.Description
		}
		var created idResponse
		if err := admin.POST(ctx, "/obligation-types", body, &created); err != nil {
			return fmt.Errorf("obligation type %s (%s): POST /obligation-types: %w",
				obligation.Key, obligation.Code, err)
		}
		world.obligationTypeIDs[obligation.Key] = created.ID
		world.out.ObligationTypes[obligation.Key] = created.ID
	}
	return nil
}

// seedMembers invites, activates and signs in every non-admin user. The invite
// token is returned exactly once, by POST /members, so it is redeemed straight
// away; the account is not usable — and cannot be assigned a task — until it is.
func seedMembers(ctx context.Context, d *deps, world *tenantWorld) error {
	admin := world.admin()
	for _, user := range world.spec.Users {
		body := createMemberBody{Email: user.Email, Name: user.Name, Role: user.Role}
		if user.ScopeEntity != nil {
			scopeID, ok := world.entityIDs[*user.ScopeEntity]
			if !ok {
				return fmt.Errorf("user %s: scope entity %s does not exist", user.Key, *user.ScopeEntity)
			}
			body.ScopeEntityID = &scopeID
		}

		var invite inviteResponse
		if err := admin.POST(ctx, "/members", body, &invite); err != nil {
			return fmt.Errorf("user %s (%s): POST /members: %w", user.Key, user.Email, err)
		}
		if invite.InviteToken == "" || invite.Member.ID == "" {
			return fmt.Errorf("user %s (%s): POST /members returned no invite token or member id",
				user.Key, user.Email)
		}
		// Redeemed anonymously: accepting an invite is a public route.
		if _, err := d.API.AcceptInvite(ctx, invite.InviteToken, user.Password, ""); err != nil {
			return fmt.Errorf("user %s (%s): POST /auth/accept-invite: %w", user.Key, user.Email, err)
		}
		session, err := login(ctx, d.API, user)
		if err != nil {
			return err
		}
		world.sessions[user.Key] = session
		world.userIDs[user.Key] = invite.Member.ID
		world.recordUser(user, invite.Member.ID)
	}
	return nil
}

// loadDataTemplates reads the tenant's predefined templates (created with the
// tenant) and indexes them by tax type, which is how the dataset names them.
func loadDataTemplates(ctx context.Context, world *tenantWorld) error {
	var templates []dataTemplateRow
	if err := world.admin().GET(ctx, "/data-templates?category=predefined", nil, &templates); err != nil {
		return fmt.Errorf("GET /data-templates: %w", err)
	}
	for _, template := range templates {
		if _, seen := world.dataTemplateIDs[template.TemplateType]; seen {
			continue
		}
		world.dataTemplateIDs[template.TemplateType] = template.ID
		world.out.DataTemplates[template.TemplateType] = template.ID
	}
	return nil
}

func seedEntityObligations(ctx context.Context, world *tenantWorld) error {
	setup := world.setup()
	for _, obligation := range world.spec.EntityObligations {
		entityID, ok := world.entityIDs[obligation.Entity]
		if !ok {
			return fmt.Errorf("entity obligation %s: unknown entity %s", obligation.Key, obligation.Entity)
		}
		typeID, ok := world.obligationTypeIDs[obligation.ObligationType]
		if !ok {
			return fmt.Errorf("entity obligation %s: unknown obligation type %s",
				obligation.Key, obligation.ObligationType)
		}
		body := createEntityObligationBody{
			EntityID:           entityID,
			ObligationTypeID:   typeID,
			TaxReferenceNumber: obligation.TaxReferenceNumber,
			Periodicity:        obligation.Periodicity,
			DeadlineRule:       obligation.DeadlineRule,
		}
		if obligation.Jurisdiction != "" {
			body.Jurisdiction = &obligation.Jurisdiction
		}
		if obligation.Currency != "" {
			body.Currency = &obligation.Currency
		}
		var created idResponse
		if err := setup.POST(ctx, "/entity-obligations", body, &created); err != nil {
			return fmt.Errorf("entity obligation %s (%s/%s, as %s): POST /entity-obligations: %w",
				obligation.Key, obligation.Entity, obligation.ObligationType, world.setupKey, err)
		}
		world.entityObligationIDs[obligation.Key] = created.ID
		world.out.EntityObligations[obligation.Key] = created.ID
	}
	return nil
}

// seedWorkflow creates one workflow, its task templates, optionally starts it
// (asserting that what was created is what the preview promised), applies the
// dataset's per-instance deviations, and finally moves the workflow to the
// status the dataset asks for.
func seedWorkflow(ctx context.Context, d *deps, world *tenantWorld, workflow spec.Workflow) error {
	setup := world.setup()

	body, err := world.workflowBody(workflow)
	if err != nil {
		return err
	}
	var created idResponse
	if err := setup.POST(ctx, "/workflows", body, &created); err != nil {
		return fmt.Errorf("POST /workflows (as %s): %w", world.setupKey, err)
	}
	world.workflowIDs[workflow.Key] = created.ID

	record := workflowOutput{
		ID:        created.ID,
		Name:      workflow.Name,
		Category:  workflow.Category,
		Status:    "draft",
		Templates: map[string]string{},
		Instances: map[string]string{},
	}

	// Task templates, in orderIndex order — the order the generator materializes
	// them in, and the order the UI lists them in.
	for _, template := range sortedTemplates(workflow.Templates) {
		taskBody := createWorkflowTaskBody{
			WorkflowID:             created.ID,
			Name:                   template.Name,
			TaskType:               template.TaskType,
			ApprovalRequired:       template.ApprovalRequired,
			DueDateReference:       template.DueDateReference,
			DueDateOffsetValue:     template.DueDateOffsetValue,
			DueDateOffsetUnit:      template.DueDateOffsetUnit,
			DueDateOffsetDirection: template.DueDateOffsetDirection,
			OrderIndex:             template.OrderIndex,
		}
		if template.RoleLabel != "" {
			taskBody.RoleLabel = &template.RoleLabel
		}
		if template.DataTemplate != nil {
			templateID, ok := world.dataTemplateIDs[*template.DataTemplate]
			if !ok {
				return fmt.Errorf("task template %s: the tenant has no predefined %s data template "+
					"(have: %s)", template.Key, *template.DataTemplate, strings.Join(sortedKeys(world.dataTemplateIDs), ", "))
			}
			taskBody.DataTemplateID = &templateID
		}
		var createdTask idResponse
		if err := setup.POST(ctx, "/workflow-tasks", taskBody, &createdTask); err != nil {
			return fmt.Errorf("task template %s (%s): POST /workflow-tasks: %w", template.Key, template.Name, err)
		}
		record.Templates[template.Key] = createdTask.ID
		world.out.Counts.WorkflowTasks++
	}

	if !workflow.Lifecycle.Start {
		world.out.Workflows[workflow.Key] = record
		return nil
	}

	// Preview first, then start, then check that what exists is what was shown.
	// The preview is a pure read of the same planner start uses; a divergence
	// means the calendar or the deadline rules moved under the dataset, and
	// every expected number in it would be wrong.
	instances, err := startWorkflow(ctx, setup, workflow, created.ID, record.Templates)
	if err != nil {
		return err
	}
	record.Started = true
	record.Status = "active"
	world.out.Counts.WorkflowsStarted++

	templateKeyByID := invert(record.Templates)
	refs := workflowRefs{
		WorkflowID:  created.ID,
		InstanceIDs: map[string]string{},
		UserIDs:     world.userIDs,
	}
	for _, instance := range instances {
		templateKey, ok := templateKeyByID[instance.WorkflowTaskID]
		if !ok {
			return fmt.Errorf("task instance %s references workflow task %s, which this workflow did not create",
				instance.ID, instance.WorkflowTaskID)
		}
		key := instanceKey(instance.PeriodCode, templateKey)
		refs.InstanceIDs[key] = instance.ID
		record.Instances[key] = instance.ID
	}
	world.out.Counts.Instances += len(instances)

	// The per-instance deviations, in the dataset's own order.
	actions, err := planWorkflowActions(workflow, refs)
	if err != nil {
		return err
	}
	results, err := executeActions(ctx, world.sessions, actions)
	if err != nil {
		return err
	}
	for _, result := range results {
		if result.Action.Kind != actionUpload {
			continue
		}
		versions := 1
		if result.Action.NewVersion {
			versions = 2
		}
		world.out.Documents = append(world.out.Documents, documentOutput{
			Workflow:  workflow.Key,
			Period:    result.Action.Period,
			Task:      result.Action.Task,
			FileName:  result.Action.FileName,
			ID:        result.DocumentID,
			VersionID: result.VersionID,
			Versions:  versions,
		})
		world.out.Counts.Documents++
		world.out.Counts.DocumentVersions += versions
	}

	// The completion instants this workflow contributes to the final SQL pass.
	for _, instance := range workflow.Instances {
		row, ok, err := instantsFor(instance, world.zone, d.Spec.CompletionLocalTime())
		if err != nil {
			return fmt.Errorf("%s: %w", instanceRef(workflow, instance), err)
		}
		if !ok {
			continue
		}
		row.InstanceID = refs.InstanceIDs[instanceKey(instance.Period, instance.Task)]
		row.Ref = instanceRef(workflow, instance)
		world.backdates = append(world.backdates, row)
	}

	// The final status, after every instance action: PUT /workflows is a full
	// replacement, so the whole body is resent with the status appended.
	if final := workflow.Lifecycle.FinalStatus; final != "" && final != "active" && final != "draft" {
		updateBody, err := world.workflowBody(workflow)
		if err != nil {
			return err
		}
		updateBody.Status = final
		if err := setup.PUT(ctx, "/workflows/"+created.ID, updateBody, nil); err != nil {
			return fmt.Errorf("PUT /workflows/%s (status %s): %w", created.ID, final, err)
		}
		record.Status = final
	}

	// Read the instances back and check every status is the one the dataset
	// declares: this catches an action that silently did not apply (a 200 on a
	// PUT that a freeze rule turned into a no-op, a mis-keyed period) before the
	// world is handed to `verify`.
	settled, err := listTaskInstances(ctx, setup, created.ID)
	if err != nil {
		return err
	}
	if err := checkInstanceStatuses(workflow, settled, templateKeyByID, world.out.Counts.InstancesByStatus); err != nil {
		return err
	}

	world.out.Workflows[workflow.Key] = record
	fmt.Fprintf(d.Out, "  workflow %-12s %-9s %3d instances, %d templates, status %s\n",
		workflow.Key, workflow.Category, len(instances), len(record.Templates), record.Status)
	return nil
}

// workflowBody renders a workflow's create/update body from the dataset.
func (w *tenantWorld) workflowBody(workflow spec.Workflow) (workflowBody, error) {
	body := workflowBody{
		Name:             workflow.Name,
		WorkflowCategory: workflow.Category,
		ProjectType:      workflow.ProjectType,
		SelectedPeriods:  workflow.SelectedPeriods,
		Periodicity:      workflow.Periodicity,
		DueDateRule:      workflow.DueDateRule,
		TasksSequential:  workflow.TasksSequential,
	}
	if workflow.Description != "" {
		body.Description = &workflow.Description
	}
	if workflow.FinancialYear != "" {
		financialYear := workflow.FinancialYear
		body.FinancialYear = &financialYear
	}
	if !workflow.StartDate.IsZero() {
		startDate := workflow.StartDate.String()
		body.StartDate = &startDate
	}
	if !workflow.EndDate.IsZero() {
		endDate := workflow.EndDate.String()
		body.EndDate = &endDate
	}
	if workflow.Entity != nil {
		entityID, ok := w.entityIDs[*workflow.Entity]
		if !ok {
			return workflowBody{}, fmt.Errorf("unknown entity %s", *workflow.Entity)
		}
		body.EntityID = &entityID
	}
	if workflow.ObligationType != nil {
		typeID, ok := w.obligationTypeIDs[*workflow.ObligationType]
		if !ok {
			return workflowBody{}, fmt.Errorf("unknown obligation type %s", *workflow.ObligationType)
		}
		body.ObligationTypeID = &typeID
	}
	return body, nil
}

func (w *tenantWorld) recordUser(user spec.User, id string) {
	record := userOutput{
		ID:       id,
		Email:    user.Email,
		Name:     user.Name,
		Role:     user.Role,
		Status:   user.Status,
		Password: user.Password,
	}
	if user.ScopeEntity != nil {
		record.ScopeEntity = *user.ScopeEntity
	}
	w.out.Users[user.Key] = record
}

// disableMembers applies status=disabled last: a disabled member cannot be a
// task assignee, so every instance that names one must already exist.
func disableMembers(ctx context.Context, world *tenantWorld) error {
	admin := world.admin()
	for _, user := range world.spec.Users {
		if user.Status != spec.StatusDisabled {
			continue
		}
		memberID, ok := world.userIDs[user.Key]
		if !ok {
			return fmt.Errorf("user %s: no member id to disable", user.Key)
		}
		body := updateMemberBody{Name: user.Name, Status: spec.StatusDisabled}
		if err := admin.PUT(ctx, "/members/"+memberID, body, nil); err != nil {
			return fmt.Errorf("user %s: PUT /members/%s (disable): %w", user.Key, memberID, err)
		}
	}
	return nil
}

// --- start + verification -------------------------------------------------

type previewTask struct {
	TemplateID      string  `json:"templateId"`
	PeriodCode      string  `json:"periodCode"`
	Name            string  `json:"name"`
	DueDate         string  `json:"dueDate"`
	PeriodEndDate   string  `json:"periodEndDate"`
	FilingDeadline  string  `json:"filingDeadline"`
	PaymentDeadline *string `json:"paymentDeadline"`
	OrderIndex      int     `json:"orderIndex"`
}

type workflowPreview struct {
	TotalPeriods  int           `json:"totalPeriods"`
	TaskTemplates int           `json:"taskTemplates"`
	TotalTasks    int           `json:"totalTasks"`
	Tasks         []previewTask `json:"tasks"`
}

type startResponse struct {
	InstancesCreated int `json:"instancesCreated"`
}

type taskInstanceRow struct {
	ID              string  `json:"id"`
	WorkflowTaskID  string  `json:"workflowTaskId"`
	PeriodCode      string  `json:"periodCode"`
	Status          string  `json:"status"`
	DueDate         string  `json:"dueDate"`
	PeriodEndDate   string  `json:"periodEndDate"`
	FilingDeadline  string  `json:"filingDeadline"`
	PaymentDeadline *string `json:"paymentDeadline"`
}

// startWorkflow previews, starts, and asserts that the generated instances are
// exactly the planned ones — same count, same dates, row for row.
func startWorkflow(
	ctx context.Context, session *apiclient.Session, workflow spec.Workflow, workflowID string,
	templateIDs map[string]string,
) ([]taskInstanceRow, error) {
	var preview workflowPreview
	if err := session.GET(ctx, "/workflows/"+workflowID+"/preview", nil, &preview); err != nil {
		return nil, fmt.Errorf("GET /workflows/%s/preview: %w", workflowID, err)
	}
	expected := len(workflow.PeriodCodes()) * len(workflow.Templates)
	if len(preview.Tasks) != expected {
		return nil, fmt.Errorf("the preview plans %d task instances but the dataset describes %d "+
			"(%d periods × %d templates) — the entity calendar or the periodicity does not match the dataset",
			len(preview.Tasks), expected, len(workflow.PeriodCodes()), len(workflow.Templates))
	}

	var started startResponse
	if err := session.POST(ctx, "/workflows/"+workflowID+"/start", nil, &started); err != nil {
		return nil, fmt.Errorf("POST /workflows/%s/start: %w", workflowID, err)
	}
	if started.InstancesCreated != len(preview.Tasks) {
		return nil, fmt.Errorf("start created %d task instances but the preview planned %d",
			started.InstancesCreated, len(preview.Tasks))
	}

	instances, err := listTaskInstances(ctx, session, workflowID)
	if err != nil {
		return nil, err
	}
	if err := checkAgainstPreview(preview, instances, templateIDs); err != nil {
		return nil, err
	}
	return instances, nil
}

// checkAgainstPreview compares the created instances with the plan, date by
// date. Preview and start share one planner, so a difference means something
// mutated between the two calls — worth failing loudly rather than seeding a
// demo whose deadlines nobody predicted.
func checkAgainstPreview(preview workflowPreview, instances []taskInstanceRow, templateIDs map[string]string) error {
	if len(instances) != len(preview.Tasks) {
		return fmt.Errorf("the workflow has %d task instances but the preview planned %d",
			len(instances), len(preview.Tasks))
	}
	known := map[string]bool{}
	for _, id := range templateIDs {
		known[id] = true
	}
	byKey := make(map[string]taskInstanceRow, len(instances))
	for _, instance := range instances {
		byKey[instance.WorkflowTaskID+"|"+instance.PeriodCode] = instance
	}
	for _, task := range preview.Tasks {
		if !known[task.TemplateID] {
			return fmt.Errorf("the preview plans a row for workflow task %s, which this workflow did not create",
				task.TemplateID)
		}
		instance, ok := byKey[task.TemplateID+"|"+task.PeriodCode]
		if !ok {
			return fmt.Errorf("no task instance was created for %s of workflow task %s (%s)",
				task.PeriodCode, task.TemplateID, task.Name)
		}
		if instance.DueDate != task.DueDate ||
			instance.PeriodEndDate != task.PeriodEndDate ||
			instance.FilingDeadline != task.FilingDeadline ||
			derefDate(instance.PaymentDeadline) != derefDate(task.PaymentDeadline) {
			return fmt.Errorf("%s / %s: the created instance has due=%s periodEnd=%s filing=%s payment=%s "+
				"but the preview planned due=%s periodEnd=%s filing=%s payment=%s",
				task.PeriodCode, task.Name,
				instance.DueDate, instance.PeriodEndDate, instance.FilingDeadline, derefDate(instance.PaymentDeadline),
				task.DueDate, task.PeriodEndDate, task.FilingDeadline, derefDate(task.PaymentDeadline))
		}
	}
	return nil
}

// checkInstanceStatuses asserts the world matches the dataset: every listed
// instance carries its declared status, every unlisted one is still
// not_started. It also fills the per-status tally used in the summary.
func checkInstanceStatuses(
	workflow spec.Workflow, instances []taskInstanceRow, templateKeyByID map[string]string, tally map[string]int,
) error {
	expected := map[string]string{}
	for _, instance := range workflow.Instances {
		expected[instanceKey(instance.Period, instance.Task)] = instance.Status
	}
	for _, instance := range instances {
		templateKey, ok := templateKeyByID[instance.WorkflowTaskID]
		if !ok {
			return fmt.Errorf("task instance %s references an unknown workflow task", instance.ID)
		}
		key := instanceKey(instance.PeriodCode, templateKey)
		want, listed := expected[key]
		if !listed {
			want = "not_started"
		}
		if instance.Status != want {
			return fmt.Errorf("%s %s: status is %q but the dataset says %q",
				workflow.Key, key, instance.Status, want)
		}
		tally[instance.Status]++
	}
	return nil
}

// listTaskInstances reads every instance of a workflow, paging through the
// list route (limit is capped at 100).
func listTaskInstances(ctx context.Context, session *apiclient.Session, workflowID string) ([]taskInstanceRow, error) {
	const pageSize = 100
	var all []taskInstanceRow
	for offset := 0; ; {
		path := fmt.Sprintf("/task-instances?workflowId=%s&limit=%d&offset=%d",
			url.QueryEscape(workflowID), pageSize, offset)
		var page []taskInstanceRow
		pagination, err := session.GetPaginated(ctx, path, &page)
		if err != nil {
			return nil, fmt.Errorf("GET %s: %w", path, err)
		}
		all = append(all, page...)
		if len(page) == 0 || !pagination.Present || len(all) >= pagination.Total {
			return all, nil
		}
		offset += len(page)
	}
}

// --- preflight, dry run, summary ------------------------------------------

// preflight refuses to start when the connection bypasses RLS, or when any of
// the dataset's tenants or e-mail addresses already exists. Repairing a
// half-seeded world is guesswork; the operator is told to reset instead.
func preflight(ctx context.Context, d *deps) error {
	if err := ensureRLSEnforced(ctx, d.DB); err != nil {
		return err
	}

	var problems []string
	for _, tenant := range d.Tenants {
		var id string
		err := d.DB.QueryRowxContext(ctx, `SELECT id FROM tenants WHERE slug = $1`, tenant.Slug).Scan(&id)
		switch {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return fmt.Errorf("look up tenant %q: %w", tenant.Slug, err)
		default:
			problems = append(problems, fmt.Sprintf("tenant %q (%s) already exists as %s", tenant.Slug, tenant.Key, id))
		}
	}

	var emails []string
	for _, tenant := range d.Tenants {
		for _, user := range tenant.AllUsers() {
			emails = append(emails, strings.ToLower(strings.TrimSpace(user.Email)))
		}
	}
	taken, err := takenEmails(ctx, d, emails)
	if err != nil {
		return err
	}
	for _, email := range taken {
		problems = append(problems, fmt.Sprintf("the e-mail %s is already registered (users.email is unique across tenants)", email))
	}

	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return fmt.Errorf("this database already holds part of the dataset, and seeding on top of it "+
		"would half-build a world nothing describes:\n  - %s\nrun `seed-demo reset --yes` first "+
		"(or `seed-demo seed --reset --yes`)", strings.Join(problems, "\n  - "))
}

// ensureRLSEnforced refuses to seed over a connection that bypasses row-level
// security — a superuser, or any role with BYPASSRLS.
//
// This is not belt-and-braces; without it the seed is SILENTLY WRONG. The
// tenant bootstrap (platform/seed.CreateTenant) ends by seeding the three
// predefined data templates through the same use case POST
// /data-templates/predefined runs, and that use case is idempotent by listing
// what the tenant already has:
//
//	existing, err := uc.repo.List(ctx, domain.ListDataTemplatesArgs{})
//
// That query carries NO tenant predicate. It is scoped by RLS alone, which is
// exactly right for the API (ADR-0004: the app role is NOBYPASSRLS and every
// statement runs under app.tenant_id) — but on a BYPASSRLS connection the list
// returns EVERY tenant's templates. If any other tenant in the database already
// has "VAT Return", the new tenant is judged to have it too, nothing is
// inserted, and the closing List hands back the other tenant's rows. The
// bootstrap then reports "3 predefined data templates" while the new tenant has
// none, and the run dies much later and far away, at the first task template
// that wants the VAT template ("the tenant has no predefined VAT data
// template"), with a tenant already half-built.
//
// Observed exactly that way against the compose stack on 2026-09-06, seeding
// with the postgres superuser DSN while the stack's original `acme` tenant
// still held the three predefined names.
//
// So: seed as the application role. `reset` is the one subcommand that needs
// more (it clears the append-only audit_log) and takes --admin-dsn for it.
func ensureRLSEnforced(ctx context.Context, db platformseed.DB) error {
	var role string
	var superuser, bypassRLS bool
	err := db.QueryRowxContext(ctx,
		`SELECT rolname, rolsuper, rolbypassrls FROM pg_roles WHERE rolname = current_user`).
		Scan(&role, &superuser, &bypassRLS)
	if err != nil {
		// Not being able to read pg_roles is not a reason to seed blind.
		return fmt.Errorf("check whether the database role enforces row-level security: %w", err)
	}
	return rlsGuardError(role, superuser, bypassRLS)
}

// rlsGuardError is ensureRLSEnforced's decision, separated from the query so
// the rule is testable without a database. nil means "this role enforces RLS".
func rlsGuardError(role string, superuser, bypassRLS bool) error {
	if !superuser && !bypassRLS {
		return nil
	}
	why := "is a superuser"
	if !superuser {
		why = "has BYPASSRLS"
	}
	return fmt.Errorf("refusing to seed as %q, which %s: the tenant bootstrap relies on row-level "+
		"security to scope its idempotency checks (ADR-0004), so on this connection it would report "+
		"templates it never created and leave a half-built tenant behind. Connect as the application "+
		"role instead — in the compose stack:\n"+
		"  --database-url postgres://zentax_app:zentax-local-app@localhost:5433/zentax?sslmode=disable\n"+
		"(`reset` is the subcommand that needs a privileged role; give it one with --admin-dsn)", role, why)
}

// takenEmails returns the addresses of emails that already have an account.
func takenEmails(ctx context.Context, d *deps, emails []string) ([]string, error) {
	if len(emails) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(emails))
	args := make([]any, len(emails))
	for i, email := range emails {
		placeholders[i] = "$" + strconv.Itoa(i+1)
		args[i] = email
	}
	query := `SELECT email FROM users WHERE lower(email) IN (` + strings.Join(placeholders, ", ") + `)`
	var taken []string
	if err := d.DB.SelectContext(ctx, &taken, query, args...); err != nil {
		return nil, fmt.Errorf("look up existing e-mail addresses: %w", err)
	}
	sort.Strings(taken)
	return taken, nil
}

// dryRunSeed validates the dataset and prints the plan without touching
// anything — no database connection was even opened.
func dryRunSeed(d *deps) error {
	fmt.Fprintf(d.Out, "dry run: %s is valid; nothing was written.\n", d.SpecPath)
	fmt.Fprintf(d.Out, "target API %s (not contacted), asOf %s\n\n", d.API.BaseURL(), d.Spec.AsOf)

	for _, tenant := range d.Tenants {
		var (
			instances, templates, puts, submits, approves, documents, versions, backdates, started int
		)
		for _, workflow := range tenant.Workflows {
			templates += len(workflow.Templates)
			if workflow.Lifecycle.Start {
				started++
				instances += len(workflow.PeriodCodes()) * len(workflow.Templates)
			}
			puts += len(workflow.Instances)
			for _, instance := range workflow.Instances {
				switch instance.Via {
				case spec.ViaSubmit:
					submits++
				case spec.ViaApprove:
					submits++
					approves++
				}
				if !instance.CompletedOn.IsZero() || !instance.SubmittedOn.IsZero() {
					backdates++
				}
			}
			for _, document := range workflow.Documents {
				documents++
				versions++
				if document.NewVersion {
					versions++
				}
			}
		}
		fmt.Fprintf(d.Out, "%s (%s, %s)\n", tenant.Key, tenant.Slug, tenant.Timezone)
		fmt.Fprintf(d.Out, "  setup writes as %s; %d users, %d entities, %d obligation types, %d entity obligations\n",
			setupActorKey(tenant), len(tenant.AllUsers()), len(tenant.Entities),
			len(tenant.ObligationTypes), len(tenant.EntityObligations))
		fmt.Fprintf(d.Out, "  %d workflows (%d started), %d task templates, %d task instances\n",
			len(tenant.Workflows), started, templates, instances)
		fmt.Fprintf(d.Out, "  instance actions: %d PUT, %d submit, %d approve; %d documents (%d versions)\n",
			puts, submits, approves, documents, versions)
		fmt.Fprintf(d.Out, "  completion instants to back-date: %d\n", backdates)
	}

	fmt.Fprintf(d.Out, "\nrun without --dry-run to apply. Output would be written to %s.\n", d.OutputPath)
	return nil
}

// printSummary prints the tallies and the sign-in table.
func printSummary(d *deps, worlds []*tenantWorld) {
	fmt.Fprintf(d.Out, "\nseeded %d tenant(s); key → id map written to %s\n", len(worlds), d.OutputPath)
	for _, world := range worlds {
		counts := world.out.Counts
		fmt.Fprintf(d.Out, "\n%s (%s) — tenant_id %s\n", world.spec.Key, world.spec.Slug, world.tenantID)
		fmt.Fprintf(d.Out, "  %d users, %d entities, %d obligation types, %d entity obligations\n",
			counts.Users, counts.Entities, counts.ObligationTypes, counts.EntityObligations)
		fmt.Fprintf(d.Out, "  %d workflows (%d started), %d task templates, %d task instances, %d documents (%d versions)\n",
			counts.Workflows, counts.WorkflowsStarted, counts.WorkflowTasks,
			counts.Instances, counts.Documents, counts.DocumentVersions)
		statuses := make([]string, 0, len(counts.InstancesByStatus))
		for _, status := range sortedStatuses(counts.InstancesByStatus) {
			statuses = append(statuses, fmt.Sprintf("%s %d", status, counts.InstancesByStatus[status]))
		}
		fmt.Fprintf(d.Out, "  instances by status: %s\n", strings.Join(statuses, ", "))
	}

	fmt.Fprintln(d.Out, "\nsign in (demo credentials — this dataset must never describe a real tenant):")
	fmt.Fprintf(d.Out, "  %-24s %-34s %-14s %s\n", "TENANT", "EMAIL", "ROLE", "PASSWORD")
	for _, world := range worlds {
		for _, user := range world.spec.AllUsers() {
			role := user.Role
			if user.ScopeEntity != nil {
				role += " @" + *user.ScopeEntity
			}
			if !user.IsActive() {
				role += " (disabled)"
			}
			fmt.Fprintf(d.Out, "  %-24s %-34s %-14s %s\n", world.spec.Slug, user.Email, role, user.Password)
		}
	}

	fmt.Fprintln(d.Out, "\naudit_log was not back-dated: it is append-only and hash-chained (ADR-0007/0008), "+
		"so the trail correctly shows the instants of this seed run, not the dataset's story dates.")
}

// --- small helpers --------------------------------------------------------

// sortedTemplates returns the workflow's task templates in orderIndex order
// without mutating the dataset's slice.
func sortedTemplates(templates []spec.TaskTemplate) []spec.TaskTemplate {
	out := make([]spec.TaskTemplate, len(templates))
	copy(out, templates)
	sort.SliceStable(out, func(i, j int) bool { return out[i].OrderIndex < out[j].OrderIndex })
	return out
}

func invert(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[v] = k
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func derefDate(d *string) string {
	if d == nil {
		return "null"
	}
	return *d
}
