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
