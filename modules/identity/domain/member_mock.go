package domain

import (
	"context"
	"time"

	"github.com/stretchr/testify/mock"
)

// MemberRepositoryMock is a testify mock of MemberRepository.
type MemberRepositoryMock struct {
	mock.Mock
}

var _ MemberRepository = (*MemberRepositoryMock)(nil)

func (m *MemberRepositoryMock) List(ctx context.Context, args ListMembersArgs) ([]Member, int, error) {
	a := m.Called(ctx, args)
	if a.Get(0) == nil {
		return nil, a.Int(1), a.Error(2)
	}
	return a.Get(0).([]Member), a.Int(1), a.Error(2)
}

func (m *MemberRepositoryMock) GetByID(ctx context.Context, tenantID, id string) (*Member, error) {
	a := m.Called(ctx, tenantID, id)
	return memberOrNil(a.Get(0)), a.Error(1)
}

func (m *MemberRepositoryMock) UpdateNameStatus(ctx context.Context, tenantID, id, name, status string) (bool, error) {
	a := m.Called(ctx, tenantID, id, name, status)
	return a.Bool(0), a.Error(1)
}

func (m *MemberRepositoryMock) Activate(ctx context.Context, id, passwordHash string, name *string) (bool, error) {
	a := m.Called(ctx, id, passwordHash, name)
	return a.Bool(0), a.Error(1)
}

func (m *MemberRepositoryMock) InsertGrant(ctx context.Context, userID, role string, scopeEntityID *string) (string, error) {
	a := m.Called(ctx, userID, role, scopeEntityID)
	return a.String(0), a.Error(1)
}

func (m *MemberRepositoryMock) DeleteGrant(ctx context.Context, userID, grantID string) (bool, error) {
	a := m.Called(ctx, userID, grantID)
	return a.Bool(0), a.Error(1)
}

func (m *MemberRepositoryMock) DeleteGrantsForUser(ctx context.Context, userID string) error {
	a := m.Called(ctx, userID)
	return a.Error(0)
}

func (m *MemberRepositoryMock) CountActiveTenantAdmins(ctx context.Context, tenantID string) (int, error) {
	a := m.Called(ctx, tenantID)
	return a.Int(0), a.Error(1)
}

func (m *MemberRepositoryMock) LockAdminGuard(ctx context.Context, tenantID string) error {
	a := m.Called(ctx, tenantID)
	return a.Error(0)
}

func memberOrNil(v any) *Member {
	if v == nil {
		return nil
	}
	return v.(*Member)
}

// InviteTokenRepositoryMock is a testify mock of InviteTokenRepository.
type InviteTokenRepositoryMock struct {
	mock.Mock
}

var _ InviteTokenRepository = (*InviteTokenRepositoryMock)(nil)

func (m *InviteTokenRepositoryMock) Create(ctx context.Context, input CreateInviteTokenInput) (*InviteToken, error) {
	a := m.Called(ctx, input)
	return inviteOrNil(a.Get(0)), a.Error(1)
}

func (m *InviteTokenRepositoryMock) GetByTokenHash(ctx context.Context, tokenHash string) (*InviteToken, error) {
	a := m.Called(ctx, tokenHash)
	return inviteOrNil(a.Get(0)), a.Error(1)
}

func (m *InviteTokenRepositoryMock) MarkAccepted(ctx context.Context, id string, at time.Time) (bool, error) {
	a := m.Called(ctx, id, at)
	return a.Bool(0), a.Error(1)
}

func (m *InviteTokenRepositoryMock) RevokeUnusedForUser(ctx context.Context, userID string) error {
	a := m.Called(ctx, userID)
	return a.Error(0)
}

func inviteOrNil(v any) *InviteToken {
	if v == nil {
		return nil
	}
	return v.(*InviteToken)
}
