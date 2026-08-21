package domain

import (
	"context"
	"time"

	"github.com/stretchr/testify/mock"
)

type UserRepositoryMock struct {
	mock.Mock
}

var _ UserRepository = (*UserRepositoryMock)(nil)

func (m *UserRepositoryMock) GetByEmail(ctx context.Context, email string) (*User, error) {
	args := m.Called(ctx, email)
	return userOrNil(args.Get(0)), args.Error(1)
}

func (m *UserRepositoryMock) GetByID(ctx context.Context, id string) (*User, error) {
	args := m.Called(ctx, id)
	return userOrNil(args.Get(0)), args.Error(1)
}

func (m *UserRepositoryMock) Create(ctx context.Context, input CreateUserInput) (*User, error) {
	args := m.Called(ctx, input)
	return userOrNil(args.Get(0)), args.Error(1)
}

func (m *UserRepositoryMock) RecordFailedLogin(ctx context.Context, id string, attempts int, lockedUntil *time.Time) error {
	return m.Called(ctx, id, attempts, lockedUntil).Error(0)
}

func (m *UserRepositoryMock) ResetFailedLogin(ctx context.Context, id string) error {
	return m.Called(ctx, id).Error(0)
}

func (m *UserRepositoryMock) SetTOTP(ctx context.Context, id string, secretEnc *string, enabled bool) error {
	return m.Called(ctx, id, secretEnc, enabled).Error(0)
}

func userOrNil(v any) *User {
	if v == nil {
		return nil
	}
	return v.(*User)
}

type SessionRepositoryMock struct {
	mock.Mock
}

var _ SessionRepository = (*SessionRepositoryMock)(nil)

func (m *SessionRepositoryMock) Create(ctx context.Context, input CreateSessionInput) (*Session, error) {
	args := m.Called(ctx, input)
	return sessionOrNil(args.Get(0)), args.Error(1)
}

func (m *SessionRepositoryMock) GetByTokenHash(ctx context.Context, tokenHash string) (*Session, error) {
	args := m.Called(ctx, tokenHash)
	return sessionOrNil(args.Get(0)), args.Error(1)
}

func (m *SessionRepositoryMock) Touch(ctx context.Context, id string, idleExpiresAt time.Time) error {
	return m.Called(ctx, id, idleExpiresAt).Error(0)
}

func (m *SessionRepositoryMock) CompleteMFA(ctx context.Context, id, newTokenHash string, idleExpiresAt, absoluteExpiresAt time.Time) error {
	return m.Called(ctx, id, newTokenHash, idleExpiresAt, absoluteExpiresAt).Error(0)
}

func (m *SessionRepositoryMock) Revoke(ctx context.Context, id string) error {
	return m.Called(ctx, id).Error(0)
}

func (m *SessionRepositoryMock) RevokeAllForUser(ctx context.Context, userID string) error {
	return m.Called(ctx, userID).Error(0)
}

func sessionOrNil(v any) *Session {
	if v == nil {
		return nil
	}
	return v.(*Session)
}

func (m *UserRepositoryMock) ListByKind(ctx context.Context, tenantID, kind string) ([]User, error) {
	args := m.Called(ctx, tenantID, kind)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]User), args.Error(1)
}

// TokenRepositoryMock is a testify mock of TokenRepository.
type TokenRepositoryMock struct {
	mock.Mock
}

var _ TokenRepository = (*TokenRepositoryMock)(nil)

func (m *TokenRepositoryMock) Create(ctx context.Context, input CreateAPITokenInput) (*APIToken, error) {
	args := m.Called(ctx, input)
	return tokenOrNil(args.Get(0)), args.Error(1)
}

func (m *TokenRepositoryMock) GetByTokenHash(ctx context.Context, tokenHash string) (*APIToken, error) {
	args := m.Called(ctx, tokenHash)
	return tokenOrNil(args.Get(0)), args.Error(1)
}

func (m *TokenRepositoryMock) TouchLastUsed(ctx context.Context, id string, at time.Time) error {
	args := m.Called(ctx, id, at)
	return args.Error(0)
}

func (m *TokenRepositoryMock) Revoke(ctx context.Context, id, tenantID string) (bool, error) {
	args := m.Called(ctx, id, tenantID)
	return args.Bool(0), args.Error(1)
}

func tokenOrNil(v any) *APIToken {
	if v == nil {
		return nil
	}
	return v.(*APIToken)
}

// GrantWriterMock is a testify mock of GrantWriter.
type GrantWriterMock struct {
	mock.Mock
}

var _ GrantWriter = (*GrantWriterMock)(nil)

func (m *GrantWriterMock) Insert(ctx context.Context, userID, role string, scopeEntityID *string) error {
	args := m.Called(ctx, userID, role, scopeEntityID)
	return args.Error(0)
}
