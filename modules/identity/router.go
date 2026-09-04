package identity

import (
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/routing"
	"github.com/mohamadhallal/zentax-api/modules/identity/domain"
	"github.com/mohamadhallal/zentax-api/modules/identity/handlers"
)

func RegisterRoutes(
	router *routing.Router,
	uc domain.AuthUseCases,
	sa domain.ServiceAccountUseCases,
	members domain.MemberUseCases,
	tenants domain.TenantUseCases,
	cookie handlers.CookieConfig,
) {
	router.Group("/auth", func(r *routing.Router) {
		routing.RegisterRoute(r, handlers.NewLoginHandler(uc, cookie))
		routing.RegisterRoute(r, handlers.NewMfaVerifyHandler(uc, cookie))
		routing.RegisterRoute(r, handlers.NewMfaEnrollHandler(uc))
		routing.RegisterRoute(r, handlers.NewMfaEnableHandler(uc))
		routing.RegisterRoute(r, handlers.NewLogoutHandler(uc, cookie))
		routing.RegisterRoute(r, handlers.NewLogoutAllHandler(uc, cookie))
		routing.RegisterRoute(r, handlers.NewMeHandler(uc, tenants))
		// Public: an invited person redeems their token (no session, no tenant).
		routing.RegisterRoute(r, handlers.NewAcceptInviteHandler(members))
	})

	// Account settings (ADR-0003): the caller's own tenant — read by every
	// role (member:read), name + timezone written by tenant admins.
	router.Group("/tenant", func(r *routing.Router) {
		routing.RegisterRoute(r, handlers.NewGetTenantHandler(tenants))
		routing.RegisterRoute(r, handlers.NewUpdateTenantHandler(tenants))
	})

	// Member administration: the tenant directory (member:read, every role)
	// + invites / status / roles (member:manage, tenant_admin only).
	router.Group("/members", func(r *routing.Router) {
		routing.RegisterRoute(r, handlers.NewListMembersHandler(members))
		routing.RegisterRoute(r, handlers.NewGetMemberHandler(members))
		routing.RegisterRoute(r, handlers.NewInviteMemberHandler(members))
		routing.RegisterRoute(r, handlers.NewReissueInviteHandler(members))
		routing.RegisterRoute(r, handlers.NewUpdateMemberHandler(members))
		routing.RegisterRoute(r, handlers.NewSetMemberRoleHandler(members))
		routing.RegisterRoute(r, handlers.NewAddMemberGrantHandler(members))
		routing.RegisterRoute(r, handlers.NewRemoveMemberGrantHandler(members))
	})

	// Machine identity (agentic-AI B1): service accounts + API tokens, gated
	// by member:manage.
	router.Group("/service-accounts", func(r *routing.Router) {
		routing.RegisterRoute(r, handlers.NewCreateServiceAccountHandler(sa))
		routing.RegisterRoute(r, handlers.NewListServiceAccountsHandler(sa))
		routing.RegisterRoute(r, handlers.NewIssueTokenHandler(sa))
	})
	router.Group("/tokens", func(r *routing.Router) {
		routing.RegisterRoute(r, handlers.NewRevokeTokenHandler(sa))
	})
}
