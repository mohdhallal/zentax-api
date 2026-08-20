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
	"github.com/mohamadhallal/zentax-api/platform/database"
)

// Container is the dependency-injection seam: it constructs and holds the
// per-module use cases wired from the database. New ZenTax domain modules
// (workflows, task instances) plug in here.
type Container struct {
	NexusAccountAPIKeyUseCases authdomain.NexusAccountAPIKeyUseCases
	EntityUseCases             entitiesdomain.EntityUseCases
	ObligationTypeUseCases     obligationtypesdomain.ObligationTypeUseCases
	EntityObligationUseCases   entityobligationsdomain.EntityObligationUseCases
}

func NewContainer(db database.ExecerPg) *Container {
	nexusAccountAPIKeyRepo := authpg.NewNexusAccountAPIKeyRepo(db)
	nexusAccountAPIKeyUC := authusecases.NewExternalAuth(nexusAccountAPIKeyRepo)

	entityRepo := entitiespg.NewEntityRepo(db)
	obligationTypeRepo := obligationtypespg.NewObligationTypeRepo(db)
	entityObligationRepo := entityobligationspg.NewEntityObligationRepo(db)

	return &Container{
		NexusAccountAPIKeyUseCases: nexusAccountAPIKeyUC,
		EntityUseCases:             entitiesusecases.NewUseCases(entityRepo),
		ObligationTypeUseCases:     obligationtypesusecases.NewUseCases(obligationTypeRepo),
		EntityObligationUseCases:   entityobligationsusecases.NewUseCases(entityObligationRepo),
	}
}
