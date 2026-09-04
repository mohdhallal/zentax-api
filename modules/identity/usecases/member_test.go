package usecases

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/mohamadhallal/zentax-api/app"
	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/identity/domain"
	"github.com/mohamadhallal/zentax-api/platform/crypto"
)

type memberMocks struct {
	uc       *UseCases
	users    *domain.UserRepositoryMock
	sessions *domain.SessionRepositoryMock
	tokens   *domain.TokenRepositoryMock
	members  *domain.MemberRepositoryMock
	invites  *domain.InviteTokenRepositoryMock
}

func newMemberUC() memberMocks {
	m := memberMocks{
		users:    new(domain.UserRepositoryMock),
		sessions: new(domain.SessionRepositoryMock),
		tokens:   new(domain.TokenRepositoryMock),
		members:  new(domain.MemberRepositoryMock),
		invites:  new(domain.InviteTokenRepositoryMock),
	}
	m.uc = NewUseCases(m.users, m.sessions, m.tokens, new(domain.GrantWriterMock), testSettings()).
		WithMembers(m.members, m.invites)
	return m
}

func (m memberMocks) assertAll(t *testing.T) {
	t.Helper()
	m.users.AssertExpectations(t)
	m.sessions.AssertExpectations(t)
	m.tokens.AssertExpectations(t)
	m.members.AssertExpectations(t)
	m.invites.AssertExpectations(t)
}

func adminCtx(t *testing.T, tenantID, userID string) context.Context {
	ctx := app.WithTenantID(t.Context(), tenantID)
	return app.WithRequester(ctx, &app.Requester{Kind: app.RequesterUser, ID: userID})
}

func TestInvite_IssuesTokenOnce(t *testing.T) {
	m := newMemberUC()
	ctx := adminCtx(t, "t1", "admin-1")

	m.users.On("Create", ctx, mock.MatchedBy(func(in domain.CreateUserInput) bool {
		return in.TenantID == "t1" && in.Email == "jane@acme.com" && in.Kind == domain.KindHuman &&
			in.Status == domain.StatusInvited && in.PasswordHash == nil
	})).Return(&domain.User{ID: "u2", TenantID: "t1", Email: "jane@acme.com"}, nil).Once()
	m.members.On("InsertGrant", ctx, "u2", "preparer", (*string)(nil)).Return("g1", nil).Once()

	var created domain.CreateInviteTokenInput
	m.invites.On("Create", ctx, mock.Anything).Run(func(args mock.Arguments) {
		created = args.Get(1).(domain.CreateInviteTokenInput)
	}).Return(&domain.InviteToken{ID: "i1"}, nil).Once()
	m.members.On("GetByID", ctx, "t1", "u2").
		Return(&domain.Member{ID: "u2", TenantID: "t1", Status: domain.StatusInvited}, nil).Once()

	res, err := m.uc.Invite(ctx, domain.CreateMemberInput{Email: " Jane@Acme.com ", Name: "Jane", Role: "preparer"})
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(res.RawToken, domain.InviteTokenPrefix))
	assert.Equal(t, crypto.HashToken(res.RawToken), created.TokenHash, "only the hash is stored")
	assert.Equal(t, "t1", created.TenantID)
	assert.Equal(t, "admin-1", *created.CreatedBy)
	assert.WithinDuration(t, time.Now().Add(domain.InviteTTL), res.ExpiresAt, time.Minute)
	m.assertAll(t)
}

func TestInvite_UnknownRoleRejected(t *testing.T) {
	m := newMemberUC()
	ctx := adminCtx(t, "t1", "admin-1")
	_, err := m.uc.Invite(ctx, domain.CreateMemberInput{Email: "x@acme.com", Name: "X", Role: "superuser"})
	assert.IsType(t, &apperrors.ValidationError{}, err)
	m.assertAll(t)
}

func TestReissueInvite_OnlyForInvited(t *testing.T) {
	m := newMemberUC()
	ctx := adminCtx(t, "t1", "admin-1")
	m.members.On("GetByID", ctx, "t1", "u2").
		Return(&domain.Member{ID: "u2", TenantID: "t1", Status: domain.StatusActive}, nil).Once()

	_, err := m.uc.ReissueInvite(ctx, "u2")
	assert.IsType(t, &apperrors.ConflictError{}, err)
	m.assertAll(t)
}

func TestUpdateMember_CannotDisableSelf(t *testing.T) {
	m := newMemberUC()
	ctx := adminCtx(t, "t1", "admin-1")
	m.members.On("GetByID", ctx, "t1", "admin-1").
		Return(&domain.Member{ID: "admin-1", TenantID: "t1", Status: domain.StatusActive}, nil).Once()

	_, err := m.uc.UpdateMember(ctx, "admin-1", domain.UpdateMemberInput{Name: "Me", Status: domain.StatusDisabled})
	assert.IsType(t, &apperrors.ValidationError{}, err)
	m.assertAll(t)
}

func TestUpdateMember_InvitedCannotBeActivatedHere(t *testing.T) {
	m := newMemberUC()
	ctx := adminCtx(t, "t1", "admin-1")
	m.members.On("GetByID", ctx, "t1", "u2").
		Return(&domain.Member{ID: "u2", TenantID: "t1", Status: domain.StatusInvited}, nil).Once()

	_, err := m.uc.UpdateMember(ctx, "u2", domain.UpdateMemberInput{Name: "Jane", Status: domain.StatusActive})
	assert.IsType(t, &apperrors.ValidationError{}, err)
	m.assertAll(t)
}

func TestUpdateMember_DisableRevokesSessionsAndTokens(t *testing.T) {
	m := newMemberUC()
	ctx := adminCtx(t, "t1", "admin-1")
	member := &domain.Member{ID: "u2", TenantID: "t1", Status: domain.StatusActive}
	m.members.On("GetByID", ctx, "t1", "u2").Return(member, nil).Twice()
	m.members.On("LockAdminGuard", ctx, "t1").Return(nil).Once()
	m.members.On("UpdateNameStatus", ctx, "t1", "u2", "Jane", domain.StatusDisabled).Return(true, nil).Once()
	m.sessions.On("RevokeAllForUser", ctx, "u2").Return(nil).Once()
	m.tokens.On("RevokeAllForUser", ctx, "u2").Return(nil).Once()
	m.invites.On("RevokeUnusedForUser", ctx, "u2").Return(nil).Once()
	m.members.On("CountActiveTenantAdmins", ctx, "t1").Return(1, nil).Once()

	_, err := m.uc.UpdateMember(ctx, "u2", domain.UpdateMemberInput{Name: "Jane", Status: domain.StatusDisabled})
	require.NoError(t, err)
	m.assertAll(t)
}

// Re-enabling a disabled member yields active only when it can sign in: a
// human that never accepted its invite (no password) goes back to invited; a
// service account (never has a password) stays active.
func TestUpdateMember_ReenableRestoresInvitedWithoutPassword(t *testing.T) {
	cases := []struct {
		name   string
		member domain.Member
		want   string
	}{
		{"human with password", domain.Member{Kind: domain.KindHuman, HasPassword: true}, domain.StatusActive},
		{"human without password", domain.Member{Kind: domain.KindHuman}, domain.StatusInvited},
		{"service account", domain.Member{Kind: domain.KindService}, domain.StatusActive},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := newMemberUC()
			ctx := adminCtx(t, "t1", "admin-1")
			member := c.member
			member.ID, member.TenantID, member.Status = "u2", "t1", domain.StatusDisabled
			m.members.On("GetByID", ctx, "t1", "u2").Return(&member, nil).Twice()
			m.members.On("UpdateNameStatus", ctx, "t1", "u2", "Jane", c.want).Return(true, nil).Once()

			_, err := m.uc.UpdateMember(ctx, "u2", domain.UpdateMemberInput{Name: "Jane", Status: domain.StatusActive})
			require.NoError(t, err)
			m.assertAll(t)
		})
	}
}

func TestUpdateMember_OtherTenantIs404(t *testing.T) {
	m := newMemberUC()
	ctx := adminCtx(t, "t1", "admin-1")
	m.members.On("GetByID", ctx, "t1", "foreign").Return(nil, nil).Once()

	_, err := m.uc.UpdateMember(ctx, "foreign", domain.UpdateMemberInput{Name: "X", Status: domain.StatusActive})
	assert.IsType(t, &apperrors.NotFoundError{}, err)
	m.assertAll(t)
}

func TestSetRole_LastAdminGuard(t *testing.T) {
	m := newMemberUC()
	ctx := adminCtx(t, "t1", "admin-1")
	m.members.On("GetByID", ctx, "t1", "admin-1").
		Return(&domain.Member{ID: "admin-1", TenantID: "t1", Status: domain.StatusActive, Kind: domain.KindHuman}, nil).Once()
	m.members.On("LockAdminGuard", ctx, "t1").Return(nil).Once()
	m.members.On("DeleteGrantsForUser", ctx, "admin-1").Return(nil).Once()
	m.members.On("InsertGrant", ctx, "admin-1", "viewer", (*string)(nil)).Return("g9", nil).Once()
	m.members.On("CountActiveTenantAdmins", ctx, "t1").Return(0, nil).Once()

	_, err := m.uc.SetRole(ctx, "admin-1", domain.GrantInput{Role: "viewer"})
	assert.IsType(t, &apperrors.ConflictError{}, err)
	m.assertAll(t)
}

// tenant_admin is tenant-wide by construction — a scoped admin grant is
// refused on every path that mints one (invite, replace-role, add-grant).
func TestGrants_ScopedTenantAdminRejected(t *testing.T) {
	scope := "entity-1"
	m := newMemberUC()
	ctx := adminCtx(t, "t1", "admin-1")
	m.members.On("GetByID", ctx, "t1", "u2").
		Return(&domain.Member{ID: "u2", TenantID: "t1", Status: domain.StatusActive, Kind: domain.KindHuman}, nil).Twice()

	_, err := m.uc.Invite(ctx, domain.CreateMemberInput{Email: "x@acme.com", Name: "X", Role: "tenant_admin", ScopeEntityID: &scope})
	assert.IsType(t, &apperrors.ValidationError{}, err)
	_, err = m.uc.SetRole(ctx, "u2", domain.GrantInput{Role: "tenant_admin", ScopeEntityID: &scope})
	assert.IsType(t, &apperrors.ValidationError{}, err)
	_, err = m.uc.AddGrant(ctx, "u2", domain.GrantInput{Role: "tenant_admin", ScopeEntityID: &scope})
	assert.IsType(t, &apperrors.ValidationError{}, err)
	m.assertAll(t) // nothing was created, locked or granted
}

func TestAddGrant_ServiceAccountCannotBeAdmin(t *testing.T) {
	m := newMemberUC()
	ctx := adminCtx(t, "t1", "admin-1")
	m.members.On("GetByID", ctx, "t1", "svc").
		Return(&domain.Member{ID: "svc", TenantID: "t1", Status: domain.StatusActive, Kind: domain.KindService}, nil).Once()

	_, err := m.uc.AddGrant(ctx, "svc", domain.GrantInput{Role: "tenant_admin"})
	assert.IsType(t, &apperrors.ValidationError{}, err)
	m.assertAll(t)
}

func TestRemoveGrant_UnknownGrantIs404(t *testing.T) {
	m := newMemberUC()
	ctx := adminCtx(t, "t1", "admin-1")
	m.members.On("GetByID", ctx, "t1", "u2").
		Return(&domain.Member{ID: "u2", TenantID: "t1", Grants: []domain.MemberGrant{{ID: "g1", Role: "viewer"}}}, nil).Once()

	err := m.uc.RemoveGrant(ctx, "u2", "g-missing")
	assert.IsType(t, &apperrors.NotFoundError{}, err)
	m.assertAll(t)
}

func TestAcceptInvite_ActivatesInTokenTenant(t *testing.T) {
	m := newMemberUC()
	ctx := t.Context()
	raw := domain.InviteTokenPrefix + "secret"
	token := &domain.InviteToken{ID: "i1", UserID: "u2", TenantID: "t1", ExpiresAt: time.Now().Add(time.Hour)}
	m.invites.On("GetByTokenHash", ctx, crypto.HashToken(raw)).Return(token, nil).Once()
	m.users.On("GetByID", ctx, "u2").
		Return(&domain.User{ID: "u2", TenantID: "t1", Email: "jane@acme.com", Status: domain.StatusInvited}, nil).Once()

	// The activation writes run on a context bound to the TOKEN's tenant and
	// attributed to the activating user.
	inTenant := mock.MatchedBy(func(c context.Context) bool {
		req := app.GetRequester(c)
		return app.GetTenantID(c) == "t1" && req != nil && req.ID == "u2"
	})
	m.invites.On("MarkAccepted", inTenant, "i1", mock.Anything).Return(true, nil).Once()
	m.members.On("Activate", inTenant, "u2", mock.MatchedBy(func(hash string) bool {
		ok, _ := crypto.VerifyPassword("correct horse battery", hash)
		return ok
	}), (*string)(nil)).Return(true, nil).Once()
	m.invites.On("RevokeUnusedForUser", inTenant, "u2").Return(nil).Once()

	res, err := m.uc.AcceptInvite(ctx, domain.AcceptInviteInput{Token: raw, Password: "correct horse battery"})
	require.NoError(t, err)
	assert.Equal(t, "jane@acme.com", res.Email)
	m.assertAll(t)
}

func TestAcceptInvite_GenericFailures(t *testing.T) {
	cases := map[string]func(m memberMocks, ctx context.Context){
		"wrong prefix": func(m memberMocks, ctx context.Context) {},
		"unknown token": func(m memberMocks, ctx context.Context) {
			m.invites.On("GetByTokenHash", ctx, mock.Anything).Return(nil, nil).Once()
		},
		"expired": func(m memberMocks, ctx context.Context) {
			m.invites.On("GetByTokenHash", ctx, mock.Anything).
				Return(&domain.InviteToken{ID: "i1", UserID: "u2", TenantID: "t1", ExpiresAt: time.Now().Add(-time.Hour)}, nil).Once()
		},
		"already accepted": func(m memberMocks, ctx context.Context) {
			at := time.Now()
			m.invites.On("GetByTokenHash", ctx, mock.Anything).
				Return(&domain.InviteToken{ID: "i1", UserID: "u2", TenantID: "t1", ExpiresAt: time.Now().Add(time.Hour), AcceptedAt: &at}, nil).Once()
		},
		"user no longer invited": func(m memberMocks, ctx context.Context) {
			m.invites.On("GetByTokenHash", ctx, mock.Anything).
				Return(&domain.InviteToken{ID: "i1", UserID: "u2", TenantID: "t1", ExpiresAt: time.Now().Add(time.Hour)}, nil).Once()
			m.users.On("GetByID", ctx, "u2").Return(&domain.User{ID: "u2", TenantID: "t1", Status: domain.StatusDisabled}, nil).Once()
		},
	}
	for name, arrange := range cases {
		t.Run(name, func(t *testing.T) {
			m := newMemberUC()
			ctx := t.Context()
			arrange(m, ctx)
			raw := domain.InviteTokenPrefix + "secret"
			if name == "wrong prefix" {
				raw = "ztx_not-an-invite"
			}
			_, err := m.uc.AcceptInvite(ctx, domain.AcceptInviteInput{Token: raw, Password: "correct horse battery"})
			var v *apperrors.ValidationError
			require.ErrorAs(t, err, &v)
			assert.Equal(t, domain.MsgInviteInvalid, v.Error())
			m.assertAll(t)
		})
	}
}
