package bootstrap

import (
	auditlogdomain "github.com/mohamadhallal/zentax-api/modules/auditlog/domain"
	auditlogpg "github.com/mohamadhallal/zentax-api/modules/auditlog/repositories/pg"
	authdomain "github.com/mohamadhallal/zentax-api/modules/auth/domain"
	authpg "github.com/mohamadhallal/zentax-api/modules/auth/repositories/pg"
	authusecases "github.com/mohamadhallal/zentax-api/modules/auth/usecases"
	datatemplatesdomain "github.com/mohamadhallal/zentax-api/modules/datatemplates/domain"
	datatemplatespg "github.com/mohamadhallal/zentax-api/modules/datatemplates/repositories/pg"
	datatemplatesusecases "github.com/mohamadhallal/zentax-api/modules/datatemplates/usecases"
	documentsdomain "github.com/mohamadhallal/zentax-api/modules/documents/domain"
	documentspg "github.com/mohamadhallal/zentax-api/modules/documents/repositories/pg"
	documentsusecases "github.com/mohamadhallal/zentax-api/modules/documents/usecases"
	entitiesdomain "github.com/mohamadhallal/zentax-api/modules/entities/domain"
	entitiespg "github.com/mohamadhallal/zentax-api/modules/entities/repositories/pg"
	entitiesusecases "github.com/mohamadhallal/zentax-api/modules/entities/usecases"
	entityobligationsdomain "github.com/mohamadhallal/zentax-api/modules/entityobligations/domain"
	entityobligationspg "github.com/mohamadhallal/zentax-api/modules/entityobligations/repositories/pg"
	entityobligationsusecases "github.com/mohamadhallal/zentax-api/modules/entityobligations/usecases"
	identitypg "github.com/mohamadhallal/zentax-api/modules/identity/repositories/pg"
	obligationtypesdomain "github.com/mohamadhallal/zentax-api/modules/obligationtypes/domain"
	obligationtypespg "github.com/mohamadhallal/zentax-api/modules/obligationtypes/repositories/pg"
	obligationtypesusecases "github.com/mohamadhallal/zentax-api/modules/obligationtypes/usecases"
	reportsdomain "github.com/mohamadhallal/zentax-api/modules/reports/domain"
	reportspg "github.com/mohamadhallal/zentax-api/modules/reports/repositories/pg"
	taskinstancesdomain "github.com/mohamadhallal/zentax-api/modules/taskinstances/domain"
	taskinstancespg "github.com/mohamadhallal/zentax-api/modules/taskinstances/repositories/pg"
	taskinstancesusecases "github.com/mohamadhallal/zentax-api/modules/taskinstances/usecases"
	workflowsdomain "github.com/mohamadhallal/zentax-api/modules/workflows/domain"
	workflowspg "github.com/mohamadhallal/zentax-api/modules/workflows/repositories/pg"
	workflowsusecases "github.com/mohamadhallal/zentax-api/modules/workflows/usecases"
	workflowtasksdomain "github.com/mohamadhallal/zentax-api/modules/workflowtasks/domain"
	workflowtaskspg "github.com/mohamadhallal/zentax-api/modules/workflowtasks/repositories/pg"
	workflowtasksusecases "github.com/mohamadhallal/zentax-api/modules/workflowtasks/usecases"
	"github.com/mohamadhallal/zentax-api/platform/audit"
	"github.com/mohamadhallal/zentax-api/platform/authz"
	authzpg "github.com/mohamadhallal/zentax-api/platform/authz/pg"
	"github.com/mohamadhallal/zentax-api/platform/database"
	"github.com/mohamadhallal/zentax-api/platform/storage"
)

// ContainerOption tunes NewContainer beyond the database.
type ContainerOption func(*containerDeps)

type containerDeps struct {
	store          storage.Storage
	maxUploadBytes int64
}

// WithStorage injects the document blob store (ADR-0022) and the per-file
// upload cap. Without it the documents use cases reject uploads/downloads
// with a "storage is not configured" error.
func WithStorage(store storage.Storage, maxUploadBytes int64) ContainerOption {
	return func(d *containerDeps) {
		d.store = store
		d.maxUploadBytes = maxUploadBytes
	}
}

// Container is the dependency-injection seam: it constructs and holds the
// per-module use cases wired from the database.
type Container struct {
	NexusAccountAPIKeyUseCases authdomain.NexusAccountAPIKeyUseCases
	EntityUseCases             entitiesdomain.EntityUseCases
	ObligationTypeUseCases     obligationtypesdomain.ObligationTypeUseCases
	EntityObligationUseCases   entityobligationsdomain.EntityObligationUseCases
	WorkflowUseCases           workflowsdomain.WorkflowUseCases
	WorkflowTaskUseCases       workflowtasksdomain.WorkflowTaskUseCases
	TaskInstanceUseCases       taskinstancesdomain.TaskInstanceUseCases
	DataTemplateUseCases       datatemplatesdomain.DataTemplateUseCases
	DocumentUseCases           documentsdomain.DocumentUseCases
	// WorkflowStarter generates task instances from a workflow's templates
	// (POST /workflows/{id}/start). Implemented by the task-instances generator.
	WorkflowStarter workflowsdomain.Starter
	// ReportsReader serves the read-only /reports views (enriched task
	// instances, per-workflow stats); AuditLogReader the /audit-log trail.
	// Both are hand-written SQL over RLS-scoped tables — no use-case layer.
	ReportsReader  reportsdomain.Reader
	AuditLogReader auditlogdomain.Reader
}

func NewContainer(db database.ExecerPg, opts ...ContainerOption) *Container {
	deps := containerDeps{}
	for _, opt := range opts {
		opt(&deps)
	}

	nexusAccountAPIKeyRepo := authpg.NewNexusAccountAPIKeyRepo(db)
	nexusAccountAPIKeyUC := authusecases.NewExternalAuth(nexusAccountAPIKeyRepo)

	entityRepo := entitiespg.NewEntityRepo(db)
	obligationTypeRepo := obligationtypespg.NewObligationTypeRepo(db)
	entityObligationRepo := entityobligationspg.NewEntityObligationRepo(db)
	workflowRepo := workflowspg.NewWorkflowRepo(db)
	workflowTaskRepo := workflowtaskspg.NewWorkflowTaskRepo(db)
	taskInstanceRepo := taskinstancespg.NewTaskInstanceRepo(db)

	// Scoped-RBAC authorizer (ADR-0012, Increment B-2): resolves entity subtrees
	// so a scoped grant only authorizes its own branch. Injected into every write
	// use case; a tenant-wide grant short-circuits it without a DB lookup.
	authorizer := authz.NewAuthorizer(authzpg.NewResolver(db))

	// Application audit trail (ADR-0008): every domain mutation appends an
	// entry on the same transaction — the write and its evidence commit or
	// roll back together.
	auditRec := audit.NewRecorder(db)

	// The generator derives each instance's payment deadline from the entity
	// obligation linking the workflow's entity and obligation type
	// (ADR-0023 §5) through the ObligationResolver port, implemented by the
	// entity-obligations repository.
	generator := taskinstancesusecases.NewGenerator(taskInstanceRepo, workflowRepo, workflowTaskRepo, entityRepo, authorizer).
		WithAudit(auditRec).
		WithObligations(entityObligationRepo)

	return &Container{
		NexusAccountAPIKeyUseCases: nexusAccountAPIKeyUC,
		EntityUseCases:             entitiesusecases.NewUseCases(entityRepo, authorizer).WithAudit(auditRec),
		ObligationTypeUseCases:     obligationtypesusecases.NewUseCases(obligationTypeRepo, authorizer).WithAudit(auditRec),
		EntityObligationUseCases:   entityobligationsusecases.NewUseCases(entityObligationRepo, authorizer).WithAudit(auditRec),
		WorkflowUseCases:           workflowsusecases.NewUseCases(workflowRepo, authorizer).WithAudit(auditRec),
		WorkflowTaskUseCases:       workflowtasksusecases.NewUseCases(workflowTaskRepo, authorizer).WithAudit(auditRec),
		// Task assignment is validated against the tenant directory: an assignee
		// must be an ACTIVE HUMAN member of the caller's tenant (identity owns
		// the users query; task-instances only sees the AssigneeChecker port).
		// Tax data is validated against the attached data template (ADR-0001
		// server-side authority) through the TemplateResolver port, implemented
		// by the data-templates module.
		TaskInstanceUseCases: taskinstancesusecases.NewUseCases(taskInstanceRepo, authorizer).
			WithAudit(auditRec).
			WithAssigneeChecker(identitypg.NewAssigneeChecker(db)).
			WithTemplateResolver(datatemplatespg.NewTemplateResolver(db)),
		DataTemplateUseCases: datatemplatesusecases.NewUseCases(datatemplatespg.NewDataTemplateRepo(db), authorizer).WithAudit(auditRec),
		// Documents (ADR-0022): metadata on the request tx, blobs behind the
		// storage seam; writes scoped through the owning workflow's entity.
		DocumentUseCases: documentsusecases.NewUseCases(documentspg.NewDocumentRepo(db), deps.store, deps.maxUploadBytes, authorizer).
			WithAudit(auditRec),
		WorkflowStarter: generator,
		ReportsReader:   reportspg.NewReportsRepo(db),
		AuditLogReader:  auditlogpg.NewAuditLogRepo(db),
	}
}
