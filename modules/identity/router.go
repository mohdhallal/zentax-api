package identity

import (
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/routing"
	"github.com/mohamadhallal/zentax-api/modules/identity/domain"
	"github.com/mohamadhallal/zentax-api/modules/identity/handlers"
)

func RegisterRoutes(router *routing.Router, uc domain.AuthUseCases, cookie handlers.CookieConfig) {
	router.Group("/auth", func(r *routing.Router) {
		routing.RegisterRoute(r, handlers.NewLoginHandler(uc, cookie))
		routing.RegisterRoute(r, handlers.NewMfaVerifyHandler(uc, cookie))
		routing.RegisterRoute(r, handlers.NewMfaEnrollHandler(uc))
		routing.RegisterRoute(r, handlers.NewMfaEnableHandler(uc))
		routing.RegisterRoute(r, handlers.NewLogoutHandler(uc, cookie))
		routing.RegisterRoute(r, handlers.NewLogoutAllHandler(uc, cookie))
		routing.RegisterRoute(r, handlers.NewMeHandler(uc))
	})
}
