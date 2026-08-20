package bootstrap

import (
	authdomain "github.com/mohamadhallal/zentax-api/modules/auth/domain"
	authpg "github.com/mohamadhallal/zentax-api/modules/auth/repositories/pg"
	authusecases "github.com/mohamadhallal/zentax-api/modules/auth/usecases"
	entitiesdomain "github.com/mohamadhallal/zentax-api/modules/entities/domain"
	entitiespg "github.com/mohamadhallal/zentax-api/modules/entities/repositories/pg"
	entitiesusecases "github.com/mohamadhallal/zentax-api/modules/entities/usecases"
	"github.com/mohamadhallal/zentax-api/platform/database"
)

// Container is the dependency-injection seam: it constructs and holds the
// per-module use cases wired from the database. New ZenTax domain modules
// (obligations, workflows, task instances) plug in here alongside entities.
type Container struct {
	NexusAccountAPIKeyUseCases authdomain.NexusAccountAPIKeyUseCases
	EntityUseCases             entitiesdomain.EntityUseCases
}

func NewContainer(db database.ExecerPg) *Container {
	nexusAccountAPIKeyRepo := authpg.NewNexusAccountAPIKeyRepo(db)
	nexusAccountAPIKeyUC := authusecases.NewExternalAuth(nexusAccountAPIKeyRepo)

	entityRepo := entitiespg.NewEntityRepo(db)

	return &Container{
		NexusAccountAPIKeyUseCases: nexusAccountAPIKeyUC,
		EntityUseCases:             entitiesusecases.NewUseCases(entityRepo),
	}
}
