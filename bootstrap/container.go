package bootstrap

import (
	authdomain "github.com/mohamadhallal/zentax-api/modules/auth/domain"
	authpg "github.com/mohamadhallal/zentax-api/modules/auth/repositories/pg"
	authusecases "github.com/mohamadhallal/zentax-api/modules/auth/usecases"
	entitiesdomain "github.com/mohamadhallal/zentax-api/modules/entities/domain"
	entitiespg "github.com/mohamadhallal/zentax-api/modules/entities/repositories/pg"
	entitiesusecases "github.com/mohamadhallal/zentax-api/modules/entities/usecases"
	entityobligationsdomain "github.com/mohamadhallal/zentax-api/modules/entityobligations/domain"
	entityobligationspg "github.com/mohamadhallal/zentax-api/modules/entityobligations/repositories/pg"
	entityobligationsusecases "github.com/mohamadhallal/zentax-api/modules/entityobligations/usecases"
	obligationtypesdomain "github.com/mohamadhallal/zentax-api/modules/obligationtypes/domain"
	obligationtypespg "github.com/mohamadhallal/zentax-api/modules/obligationtypes/repositories/pg"
	obligationtypesusecases "github.com/mohamadhallal/zentax-api/modules/obligationtypes/usecases"
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
)

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
	// WorkflowStarter generates task instances from a workflow's templates
	// (POST /workflows/{id}/start). Implemented by the task-instances generator.
	WorkflowStarter workflowsdomain.Starter
}

func NewContainer(db database.ExecerPg) *Container {
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

	generator := taskinstancesusecases.NewGenerator(taskInstanceRepo, workflowRepo, workflowTaskRepo, entityRepo, authorizer).WithAudit(auditRec)

	return &Container{
		NexusAccountAPIKeyUseCases: nexusAccountAPIKeyUC,
		EntityUseCases:             entitiesusecases.NewUseCases(entityRepo, authorizer).WithAudit(auditRec),
		ObligationTypeUseCases:     obligationtypesusecases.NewUseCases(obligationTypeRepo, authorizer).WithAudit(auditRec),
		EntityObligationUseCases:   entityobligationsusecases.NewUseCases(entityObligationRepo, authorizer).WithAudit(auditRec),
		WorkflowUseCases:           workflowsusecases.NewUseCases(workflowRepo, authorizer).WithAudit(auditRec),
		WorkflowTaskUseCases:       workflowtasksusecases.NewUseCases(workflowTaskRepo, authorizer).WithAudit(auditRec),
		TaskInstanceUseCases:       taskinstancesusecases.NewUseCases(taskInstanceRepo, authorizer).WithAudit(auditRec),
		WorkflowStarter:            generator,
	}
}
