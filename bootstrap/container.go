package bootstrap

import (
	authdomain "github.com/mohamadhallal/zentax-api/modules/auth/domain"
	authpg "github.com/mohamadhallal/zentax-api/modules/auth/repositories/pg"
	authusecases "github.com/mohamadhallal/zentax-api/modules/auth/usecases"
	"github.com/mohamadhallal/zentax-api/platform/database"
)

// Container is the dependency-injection seam: it constructs and holds the
// per-module use cases wired from the database. The boilerplate's example
// modules (users/orders/payments) have been pruned; the real ZenTax domain
// modules (entities, obligations, workflows, task instances) plug in here.
type Container struct {
	NexusAccountAPIKeyUseCases authdomain.NexusAccountAPIKeyUseCases
}

func NewContainer(db database.ExecerPg) *Container {
	nexusAccountAPIKeyRepo := authpg.NewNexusAccountAPIKeyRepo(db)
	nexusAccountAPIKeyUC := authusecases.NewExternalAuth(nexusAccountAPIKeyRepo)

	return &Container{
		NexusAccountAPIKeyUseCases: nexusAccountAPIKeyUC,
	}
}
