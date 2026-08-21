package usecases

import (
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/identity/domain"
	"github.com/mohamadhallal/zentax-api/platform/crypto"
)

func testSettings() Settings {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	return Settings{EncryptionKey: key, SessionIdleTTL: 8 * time.Hour, SessionAbsoluteTTL: 720 * time.Hour, TOTPIssuer: "ZenTax"}
}

func newUC() (*UseCases, *domain.UserRepositoryMock, *domain.SessionRepositoryMock) {
	users := new(domain.UserRepositoryMock)
	sessions := new(domain.SessionRepositoryMock)
	return NewUseCases(users, sessions, testSettings()), users, sessions
}

func activeUser(t *testing.T, password string) *domain.User {
	hash, err := crypto.HashPassword(password)
	require.NoError(t, err)
	return &domain.User{ID: "u1", TenantID: "t1", Email: "admin@acme.com", Name: "Admin", PasswordHash: &hash, Status: "active"}
}

func TestLogin_Success(t *testing.T) {
	ctx := t.Context()
	uc, users, sessions := newUC()
	users.On("GetByEmail", ctx, "admin@acme.com").Return(activeUser(t, "s3cret-pass"), nil).Once()
	sessions.On("Create", ctx, mock.Anything).Return(&domain.Session{ID: "s1"}, nil).Once()

	// mixed-case email is normalized to lowercase for lookup
	res, err := uc.Login(ctx, domain.LoginInput{Email: "Admin@Acme.com", Password: "s3cret-pass"})
	require.NoError(t, err)
	assert.False(t, res.MFARequired)
	assert.NotEmpty(t, res.SessionToken)
	users.AssertExpectations(t)
	sessions.AssertExpectations(t)
}

func TestLogin_MFARequired(t *testing.T) {
	ctx := t.Context()
	uc, users, sessions := newUC()
	user := activeUser(t, "correct")
	user.TOTPEnabled = true
	users.On("GetByEmail", ctx, "admin@acme.com").Return(user, nil).Once()

	var created domain.CreateSessionInput
	sessions.On("Create", ctx, mock.Anything).
		Run(func(a mock.Arguments) { created = a.Get(1).(domain.CreateSessionInput) }).
		Return(&domain.Session{ID: "s1"}, nil).Once()

	res, err := uc.Login(ctx, domain.LoginInput{Email: "admin@acme.com", Password: "correct"})
	require.NoError(t, err)
	assert.True(t, res.MFARequired)
	assert.True(t, created.MFAPending)
}

func TestLogin_WrongPassword_RecordsFailure(t *testing.T) {
	ctx := t.Context()
	uc, users, _ := newUC()
	users.On("GetByEmail", ctx, "admin@acme.com").Return(activeUser(t, "correct"), nil).Once()
	users.On("RecordFailedLogin", ctx, "u1", 1, (*time.Time)(nil)).Return(nil).Once()

	_, err := uc.Login(ctx, domain.LoginInput{Email: "admin@acme.com", Password: "wrong"})
	assert.IsType(t, &apperrors.UnauthorizedError{}, err)
	users.AssertExpectations(t)
}

func TestLogin_LocksAfterMaxAttempts(t *testing.T) {
	ctx := t.Context()
	uc, users, _ := newUC()
	user := activeUser(t, "correct")
	user.FailedLoginAttempts = maxFailedAttempts - 1
	users.On("GetByEmail", ctx, "admin@acme.com").Return(user, nil).Once()
	users.On("RecordFailedLogin", ctx, "u1", maxFailedAttempts, mock.AnythingOfType("*time.Time")).Return(nil).Once()

	_, err := uc.Login(ctx, domain.LoginInput{Email: "admin@acme.com", Password: "wrong"})
	assert.IsType(t, &apperrors.UnauthorizedError{}, err)
	users.AssertExpectations(t)
}

func TestLogin_UserNotFound(t *testing.T) {
	ctx := t.Context()
	uc, users, _ := newUC()
	users.On("GetByEmail", ctx, "nobody@acme.com").Return(nil, nil).Once()

	_, err := uc.Login(ctx, domain.LoginInput{Email: "nobody@acme.com", Password: "x"})
	assert.IsType(t, &apperrors.UnauthorizedError{}, err)
}

func TestLogin_LockedAccount(t *testing.T) {
	ctx := t.Context()
	uc, users, _ := newUC()
	user := activeUser(t, "correct")
	future := time.Now().Add(10 * time.Minute)
	user.LockedUntil = &future
	users.On("GetByEmail", ctx, "admin@acme.com").Return(user, nil).Once()

	_, err := uc.Login(ctx, domain.LoginInput{Email: "admin@acme.com", Password: "correct"})
	assert.IsType(t, &apperrors.UnauthorizedError{}, err)
	assert.Contains(t, err.Error(), "locked")
}

func TestAuthenticate_Valid(t *testing.T) {
	ctx := t.Context()
	uc, users, sessions := newUC()
	now := time.Now()
	sess := &domain.Session{ID: "s1", UserID: "u1", TenantID: "t1",
		IdleExpiresAt: now.Add(time.Hour), AbsoluteExpiresAt: now.Add(time.Hour)}
	sessions.On("GetByTokenHash", ctx, crypto.HashToken("tok")).Return(sess, nil).Once()
	users.On("GetByID", ctx, "u1").Return(&domain.User{ID: "u1", Status: "active"}, nil).Once()
	sessions.On("Touch", ctx, "s1", mock.Anything).Return(nil).Once()

	s, u, err := uc.Authenticate(ctx, "tok")
	require.NoError(t, err)
	assert.Equal(t, "s1", s.ID)
	assert.Equal(t, "u1", u.ID)
}

func TestAuthenticate_Expired(t *testing.T) {
	ctx := t.Context()
	uc, _, sessions := newUC()
	past := time.Now().Add(-time.Hour)
	sessions.On("GetByTokenHash", ctx, mock.Anything).
		Return(&domain.Session{ID: "s1", IdleExpiresAt: past, AbsoluteExpiresAt: past}, nil).Once()

	_, _, err := uc.Authenticate(ctx, "tok")
	assert.IsType(t, &apperrors.UnauthorizedError{}, err)
}

func TestAuthenticate_Revoked(t *testing.T) {
	ctx := t.Context()
	uc, _, sessions := newUC()
	now := time.Now()
	revoked := now
	sessions.On("GetByTokenHash", ctx, mock.Anything).
		Return(&domain.Session{ID: "s1", RevokedAt: &revoked, IdleExpiresAt: now.Add(time.Hour), AbsoluteExpiresAt: now.Add(time.Hour)}, nil).Once()

	_, _, err := uc.Authenticate(ctx, "tok")
	assert.IsType(t, &apperrors.UnauthorizedError{}, err)
}

func TestMfaVerify_Success_RotatesToken(t *testing.T) {
	ctx := t.Context()
	uc, users, sessions := newUC()

	key, err := totp.Generate(totp.GenerateOpts{Issuer: "ZenTax", AccountName: "a@b.com"})
	require.NoError(t, err)
	enc, err := crypto.Encrypt(testSettings().EncryptionKey, []byte(key.Secret()))
	require.NoError(t, err)

	now := time.Now()
	sess := &domain.Session{ID: "s1", UserID: "u1", MFAPending: true,
		IdleExpiresAt: now.Add(time.Hour), AbsoluteExpiresAt: now.Add(time.Hour)}
	sessions.On("GetByTokenHash", ctx, mock.Anything).Return(sess, nil).Once()
	users.On("GetByID", ctx, "u1").Return(&domain.User{ID: "u1", TOTPSecretEnc: &enc}, nil).Once()
	sessions.On("CompleteMFA", ctx, "s1", mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

	code, err := totp.GenerateCode(key.Secret(), now)
	require.NoError(t, err)

	res, err := uc.MfaVerify(ctx, "old-token", code)
	require.NoError(t, err)
	assert.NotEmpty(t, res.SessionToken)
	assert.False(t, res.MFARequired)
	sessions.AssertExpectations(t)
}

func TestMfaVerify_WrongCode(t *testing.T) {
	ctx := t.Context()
	uc, users, sessions := newUC()

	key, _ := totp.Generate(totp.GenerateOpts{Issuer: "ZenTax", AccountName: "a@b.com"})
	enc, _ := crypto.Encrypt(testSettings().EncryptionKey, []byte(key.Secret()))
	now := time.Now()
	sess := &domain.Session{ID: "s1", UserID: "u1", MFAPending: true,
		IdleExpiresAt: now.Add(time.Hour), AbsoluteExpiresAt: now.Add(time.Hour)}
	sessions.On("GetByTokenHash", ctx, mock.Anything).Return(sess, nil).Once()
	users.On("GetByID", ctx, "u1").Return(&domain.User{ID: "u1", TOTPSecretEnc: &enc}, nil).Once()

	_, err := uc.MfaVerify(ctx, "old-token", "000000")
	assert.IsType(t, &apperrors.UnauthorizedError{}, err)
}

func TestLogout(t *testing.T) {
	ctx := t.Context()
	uc, _, sessions := newUC()
	sessions.On("GetByTokenHash", ctx, mock.Anything).Return(&domain.Session{ID: "s1"}, nil).Once()
	sessions.On("Revoke", ctx, "s1").Return(nil).Once()

	require.NoError(t, uc.Logout(ctx, "tok"))
	sessions.AssertExpectations(t)
}

func TestLogoutAll(t *testing.T) {
	ctx := t.Context()
	uc, _, sessions := newUC()
	sessions.On("RevokeAllForUser", ctx, "u1").Return(nil).Once()

	require.NoError(t, uc.LogoutAll(ctx, "u1"))
	sessions.AssertExpectations(t)
}
