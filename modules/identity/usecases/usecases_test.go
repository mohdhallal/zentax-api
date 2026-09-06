package usecases

import (
	"context"
	"errors"
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
	return NewUseCases(users, sessions, new(domain.TokenRepositoryMock), new(domain.GrantWriterMock), testSettings()), users, sessions
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
	expectConsume(users, true, nil)
	users.On("ResetFailedLogin", mock.Anything, "u1").Return(nil).Once()
	sessions.On("Create", mock.Anything, mock.Anything).Return(&domain.Session{ID: "s1"}, nil).Once()

	// mixed-case email is normalized to lowercase for lookup
	res, err := uc.Login(ctx, domain.LoginInput{Email: "Admin@Acme.com", Password: "s3cret-pass"})
	require.NoError(t, err)
	assert.False(t, res.MFARequired)
	assert.NotEmpty(t, res.SessionToken)
	users.AssertExpectations(t)
	sessions.AssertExpectations(t)
}

// The attempt that reaches the threshold stamps the lock BEFORE anything is
// known about the password, and only the success path clears it. A client that
// hangs up between the charge and the reset must therefore not leave the account
// locked for the whole window despite having presented the correct password —
// so the success transaction runs on a context the caller cannot cancel.
func TestLogin_SuccessSurvivesAClientHangup(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	uc, users, sessions := newUC()
	users.On("GetByEmail", mock.Anything, "admin@acme.com").Return(activeUser(t, "s3cret-pass"), nil).Once()
	expectConsume(users, true, nil)
	users.On("ResetFailedLogin", mock.MatchedBy(func(c context.Context) bool {
		return c.Err() == nil // the request context is already cancelled; this one must not be
	}), "u1").Return(nil).Once()
	sessions.On("Create", mock.MatchedBy(func(c context.Context) bool {
		return c.Err() == nil
	}), mock.Anything).Return(&domain.Session{ID: "s1"}, nil).Once()

	cancel() // the caller goes away after the attempt has been charged

	res, err := uc.Login(ctx, domain.LoginInput{Email: "admin@acme.com", Password: "s3cret-pass"})
	require.NoError(t, err, "a cancelled caller must not cost the account its unlock")
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
	expectConsume(users, true, nil)
	users.On("ResetFailedLogin", mock.Anything, "u1").Return(nil).Once()

	var created domain.CreateSessionInput
	sessions.On("Create", mock.Anything, mock.Anything).
		Run(func(a mock.Arguments) { created = a.Get(1).(domain.CreateSessionInput) }).
		Return(&domain.Session{ID: "s1"}, nil).Once()

	res, err := uc.Login(ctx, domain.LoginInput{Email: "admin@acme.com", Password: "correct"})
	require.NoError(t, err)
	assert.True(t, res.MFARequired)
	assert.True(t, created.MFAPending)
}

// --- the failed-login attempt budget ---
//
// The counter, the lock and the decision are ONE atomic repository call: the use
// case owns the POLICY (threshold, window, "now"), the repository applies it
// under the row lock and says whether this attempt was inside the budget. These
// tests pin the policy the repository is handed, the ORDER (charge, then verify
// — the property that makes the budget hold under concurrency), and that a
// consume which fails refuses rather than admits, and never turns the 401 into a
// 500.

// anyConsumeCall matches ConsumeLoginAttempt's five arguments, for AssertNotCalled.
var anyConsumeCall = []any{mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything}

// expectConsume sets up the attempt charge with the answer it should give.
func expectConsume(users *domain.UserRepositoryMock, allowed bool, err error) {
	users.On("ConsumeLoginAttempt", mock.Anything, "u1", maxFailedAttempts,
		mock.AnythingOfType("time.Time"), mock.AnythingOfType("time.Time")).
		Return(allowed, err).Once()
}

// verifySpy records what the password-verification seam was asked to do.
//
// The enumeration defence makes two claims about WORK, and both are asserted
// here rather than against a wall clock (which on a shared machine yields only
// flaky approximations):
//
//   - calls — exactly ONE argon2 verification on every outcome;
//   - hashes — WHICH hash each call verified. While an account is locked the
//     stored credential must never be verified, and "never verified" is a
//     property of this seam, not of how long the request happened to take.
type verifySpy struct {
	calls  int
	hashes []string
}

// spyVerify wraps the seam, passing each call through to the real argon2 check.
func spyVerify(uc *UseCases) *verifySpy {
	spy := &verifySpy{}
	real := uc.verifyPassword
	uc.verifyPassword = func(password, encoded string) (bool, error) {
		spy.calls++
		spy.hashes = append(spy.hashes, encoded)
		return real(password, encoded)
	}
	return spy
}

// assertVerifiedDummyOnly asserts the one verification that happened ran against
// the timing equalizer and not against the user's stored hash.
func assertVerifiedDummyOnly(t *testing.T, spy *verifySpy, stored string) {
	t.Helper()
	require.Equal(t, 1, spy.calls, "exactly one argon2 verification on every path")
	assert.Equal(t, dummyHash, spy.hashes[0],
		"an attempt past the budget must be verified against the dummy hash")
	assert.NotEqual(t, stored, spy.hashes[0],
		"the stored credential must never be verified once the budget is spent: "+
			"verifying it is what turns the lock into a password oracle")
}

func TestLogin_WrongPassword_ChargesTheAttemptWithTheLockPolicy(t *testing.T) {
	ctx := t.Context()
	uc, users, _ := newUC()
	now := time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)
	uc.now = func() time.Time { return now }
	users.On("GetByEmail", ctx, "admin@acme.com").Return(activeUser(t, "correct"), nil).Once()
	// The repository is handed the whole policy: threshold, the clock that
	// judges an expired lock, and the stamp a crossing attempt would write.
	users.On("ConsumeLoginAttempt", mock.Anything, "u1", maxFailedAttempts, now, now.Add(lockoutDuration)).
		Return(true, nil).Once()

	_, err := uc.Login(ctx, domain.LoginInput{Email: "admin@acme.com", Password: "wrong"})
	assert.IsType(t, &apperrors.UnauthorizedError{}, err)
	assert.Equal(t, domain.MsgInvalidCredentials, err.Error())
	users.AssertExpectations(t)
}

// THE BLOCKER, at the use-case seam: the attempt is charged BEFORE the password
// is verified, so the budget cannot be widened by arriving in parallel.
//
// The defect this pins was the other order — decide from the row read at the top
// of the request, count afterwards. Every request that started before the
// threshold write committed then read an unlocked account and verified the
// STORED hash, so a burst bought one real verification per thread. Ordering is
// the whole fix, and it is a property of the code path, not of the clock: the
// only honest way to assert it is to watch both seams.
func TestLogin_AttemptIsChargedBeforeThePasswordIsVerified(t *testing.T) {
	ctx := t.Context()
	uc, users, _ := newUC()
	var order []string

	users.On("GetByEmail", ctx, "admin@acme.com").Return(activeUser(t, "correct"), nil).Once()
	users.On("ConsumeLoginAttempt", mock.Anything, "u1", maxFailedAttempts,
		mock.AnythingOfType("time.Time"), mock.AnythingOfType("time.Time")).
		Run(func(a mock.Arguments) {
			order = append(order, "charge")
			// The charge must not ride on a transaction the caller is holding
			// open across the verification: it takes its own connection and is
			// committed by the time the answer is decided.
			chargeCtx, _ := a.Get(0).(context.Context)
			assert.NoError(t, chargeCtx.Err())
		}).
		Return(true, nil).Once()
	real := uc.verifyPassword
	uc.verifyPassword = func(password, encoded string) (bool, error) {
		order = append(order, "verify")
		return real(password, encoded)
	}

	_, err := uc.Login(ctx, domain.LoginInput{Email: "admin@acme.com", Password: "wrong"})

	require.Error(t, err)
	assert.Equal(t, []string{"charge", "verify"}, order,
		"the attempt must be spent before the password is looked at, or concurrency widens the budget")
	users.AssertExpectations(t)
}

// An ELAPSED window must start a fresh one, not resume a saturated counter —
// otherwise one wrong password re-locks a known address forever. The use case no
// longer judges the lock at all: it hands the repository "now" and the policy,
// and the repository's statement (which erases the expired stamp before counting)
// both allows this attempt and turns it into a count of 1. So the row the use
// case read is irrelevant here — the stale stamp on it must not stop the stored
// hash being verified.
func TestLogin_ElapsedLock_AllowedByTheRepository_VerifiesTheStoredHash(t *testing.T) {
	ctx := t.Context()
	uc, users, _ := newUC()
	now := time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)
	uc.now = func() time.Time { return now }
	spy := spyVerify(uc)

	user := activeUser(t, "correct")
	expired := now.Add(-time.Minute)
	user.LockedUntil = &expired
	user.FailedLoginAttempts = maxFailedAttempts // saturated by the lockout that has now elapsed
	users.On("GetByEmail", ctx, "admin@acme.com").Return(user, nil).Once()
	users.On("ConsumeLoginAttempt", mock.Anything, "u1", maxFailedAttempts, now, now.Add(lockoutDuration)).
		Return(true, nil).Once()

	_, err := uc.Login(ctx, domain.LoginInput{Email: "admin@acme.com", Password: "wrong"})
	require.IsType(t, &apperrors.UnauthorizedError{}, err)
	assert.Equal(t, domain.MsgInvalidCredentials, err.Error())
	require.Equal(t, 1, spy.calls)
	assert.Equal(t, *user.PasswordHash, spy.hashes[0],
		"an allowed attempt verifies the stored hash, whatever stale stamp the row carried")
	users.AssertExpectations(t)
}

// A charge that fails FAILS CLOSED. Admitting an uncharged attempt is exactly
// the bypass the budget exists to remove, so a database hiccup must refuse, not
// wave the attempt through — and it must refuse in the same words as everything
// else, never a 500. A 500 here would be an oracle from the other side: this
// statement runs only for a registered, login-capable address.
func TestLogin_ChargeFails_RefusesWithoutTouchingTheStoredHash(t *testing.T) {
	ctx := t.Context()
	uc, users, _ := newUC()
	user := activeUser(t, "correct")
	spy := spyVerify(uc)
	users.On("GetByEmail", ctx, "admin@acme.com").Return(user, nil).Once()
	expectConsume(users, false, errors.New("connection reset"))

	_, err := uc.Login(ctx, domain.LoginInput{Email: "admin@acme.com", Password: "correct"})

	require.IsType(t, &apperrors.UnauthorizedError{}, err)
	assert.Equal(t, domain.MsgInvalidCredentials, err.Error(),
		"a broken budget must not leak to the client")
	assertVerifiedDummyOnly(t, spy, *user.PasswordHash)
	users.AssertNotCalled(t, "ResetFailedLogin", mock.Anything, mock.Anything)
	users.AssertExpectations(t)
}

// A client that hangs up mid-login cannot suppress the charge, because the
// charge already happened: it is the first thing the request does with the
// account, and the answer comes after it. If the cancellation lands early enough
// that the charge itself fails, the attempt is refused rather than admitted
// (fail closed), so aborting is not a way to buy free verifications either.
func TestLogin_CancelledRequest_CannotBuyAnUnchargedVerification(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	uc, users, _ := newUC()
	user := activeUser(t, "correct")
	spy := spyVerify(uc)
	users.On("GetByEmail", ctx, "admin@acme.com").Return(user, nil).Once()
	users.On("ConsumeLoginAttempt", mock.Anything, "u1", maxFailedAttempts,
		mock.AnythingOfType("time.Time"), mock.AnythingOfType("time.Time")).
		Return(false, context.Canceled).Once()

	cancel() // the caller has already gone away
	_, err := uc.Login(ctx, domain.LoginInput{Email: "admin@acme.com", Password: "correct"})

	require.IsType(t, &apperrors.UnauthorizedError{}, err)
	assertVerifiedDummyOnly(t, spy, *user.PasswordHash)
	users.AssertExpectations(t)
}

// lockedUser is an active account whose lock is live: the counter is saturated
// and locked_until is in the future. The use case does not read those columns —
// the repository decides — so what makes it "locked" in these tests is the
// charge coming back NOT allowed.
func lockedUser(t *testing.T, password string) *domain.User {
	t.Helper()
	user := activeUser(t, password)
	future := time.Now().Add(10 * time.Minute)
	user.LockedUntil = &future
	user.FailedLoginAttempts = maxFailedAttempts
	return user
}

// THE PASSWORD-ORACLE REGRESSION.
//
// A lock that answers differently to the RIGHT password than to a wrong one is
// a password oracle, and a far worse one than the enumeration leak it was meant
// to close: the earlier design refused every credential generically but told the
// caller "account temporarily locked" as soon as the password happened to be
// correct. An attacker who has just spent five requests LOCKING an address can
// then read the right password straight off the responses — the lock, which
// exists to stop guessing, becomes the signal that scores each guess, and it
// refuses no verification at all, so guessing continues at full speed.
//
// Once the budget is spent, the correct password must therefore be answered
// exactly like a wrong one: the same generic body, and the stored hash never
// verified. The charge itself changes nothing while the lock is live — the
// counter is already at the threshold and a live window must not be extended by
// the attempts it is refusing — which is the repository's job, asserted in the
// acceptance suite against real Postgres.
func TestLogin_Locked_CorrectPassword_IsIndistinguishable(t *testing.T) {
	ctx := t.Context()
	uc, users, _ := newUC()
	user := lockedUser(t, "correct")
	spy := spyVerify(uc)
	users.On("GetByEmail", ctx, "admin@acme.com").Return(user, nil).Once()
	expectConsume(users, false, nil)

	_, err := uc.Login(ctx, domain.LoginInput{Email: "admin@acme.com", Password: "correct"})

	require.IsType(t, &apperrors.UnauthorizedError{}, err)
	assert.Equal(t, domain.MsgInvalidCredentials, err.Error(),
		"a locked account must answer the CORRECT password exactly as it answers a wrong one, "+
			"or the lockout is a password oracle for whoever minted it")
	assertVerifiedDummyOnly(t, spy, *user.PasswordHash)
	users.AssertNotCalled(t, "ResetFailedLogin", mock.Anything, mock.Anything)
}

// The other half of the pair: a guesser without the password learns nothing
// either. Same answer, same single dummy verification.
func TestLogin_Locked_WrongPassword_IsIndistinguishable(t *testing.T) {
	ctx := t.Context()
	uc, users, _ := newUC()
	user := lockedUser(t, "correct")
	spy := spyVerify(uc)
	users.On("GetByEmail", ctx, "admin@acme.com").Return(user, nil).Once()
	expectConsume(users, false, nil)

	_, err := uc.Login(ctx, domain.LoginInput{Email: "admin@acme.com", Password: "wrong"})

	require.IsType(t, &apperrors.UnauthorizedError{}, err)
	assert.Equal(t, domain.MsgInvalidCredentials, err.Error(),
		"a locked account must not announce itself to somebody without the password")
	assertVerifiedDummyOnly(t, spy, *user.PasswordHash)
	users.AssertExpectations(t)
}

// The two locked answers must be indistinguishable from EACH OTHER, which is the
// property the oracle broke — asserted directly, on one account, rather than
// inferred from two tests that each pin their own constant.
func TestLogin_Locked_RightAndWrongPassword_AnswerIdentically(t *testing.T) {
	answer := func(t *testing.T, password string) (string, *verifySpy) {
		t.Helper()
		ctx := t.Context()
		uc, users, _ := newUC()
		spy := spyVerify(uc)
		users.On("GetByEmail", ctx, "admin@acme.com").Return(lockedUser(t, "correct"), nil).Once()
		expectConsume(users, false, nil)
		_, err := uc.Login(ctx, domain.LoginInput{Email: "admin@acme.com", Password: password})
		require.IsType(t, &apperrors.UnauthorizedError{}, err)
		return err.Error(), spy
	}

	right, rightSpy := answer(t, "correct")
	wrong, wrongSpy := answer(t, "wrong")

	assert.Equal(t, wrong, right,
		"the right password on a locked account must be indistinguishable from a wrong one")
	assert.Equal(t, wrongSpy.hashes, rightSpy.hashes,
		"both must verify the same (dummy) hash, so the work cannot separate them either")
}

func TestLogin_Success_ClearsCounter(t *testing.T) {
	ctx := t.Context()
	uc, users, sessions := newUC()
	user := activeUser(t, "correct")
	user.FailedLoginAttempts = 3
	users.On("GetByEmail", ctx, "admin@acme.com").Return(user, nil).Once()
	expectConsume(users, true, nil)
	users.On("ResetFailedLogin", mock.Anything, "u1").Return(nil).Once()
	sessions.On("Create", mock.Anything, mock.Anything).Return(&domain.Session{ID: "s1"}, nil).Once()

	_, err := uc.Login(ctx, domain.LoginInput{Email: "admin@acme.com", Password: "correct"})
	require.NoError(t, err)
	users.AssertExpectations(t)
}

// The lock window has elapsed, so the charge allows this attempt — and the stamp
// that recorded the old lockout must be cleared with the counter, not left
// behind.
func TestLogin_Success_ClearsStaleLock(t *testing.T) {
	ctx := t.Context()
	uc, users, sessions := newUC()
	user := activeUser(t, "correct")
	expired := time.Now().Add(-time.Minute)
	user.LockedUntil = &expired
	user.FailedLoginAttempts = 0 // counter already zeroed elsewhere: the lock alone must still go
	users.On("GetByEmail", ctx, "admin@acme.com").Return(user, nil).Once()
	expectConsume(users, true, nil)
	users.On("ResetFailedLogin", mock.Anything, "u1").Return(nil).Once()
	sessions.On("Create", mock.Anything, mock.Anything).Return(&domain.Session{ID: "s1"}, nil).Once()

	_, err := uc.Login(ctx, domain.LoginInput{Email: "admin@acme.com", Password: "correct"})
	require.NoError(t, err)
	users.AssertExpectations(t)
}

// A successful login ALWAYS clears the counter, even for an account with a
// spotless record — because getting this far spent an attempt, so the counter is
// never already zero. (It used to be skipped when the row read at the top of the
// request showed 0/NULL; charging before verifying makes that condition false by
// construction, and leaving the write out would have left every successful login
// one attempt down.)
func TestLogin_Success_AlwaysClearsTheAttemptItSpent(t *testing.T) {
	ctx := t.Context()
	uc, users, sessions := newUC()
	users.On("GetByEmail", ctx, "admin@acme.com").Return(activeUser(t, "correct"), nil).Once()
	expectConsume(users, true, nil)
	users.On("ResetFailedLogin", mock.Anything, "u1").Return(nil).Once()
	sessions.On("Create", mock.Anything, mock.Anything).Return(&domain.Session{ID: "s1"}, nil).Once()

	_, err := uc.Login(ctx, domain.LoginInput{Email: "admin@acme.com", Password: "correct"})
	require.NoError(t, err)
	users.AssertExpectations(t)
}

// The reset and the session insert share one transaction: a session must never
// exist for a login whose spent attempt was not cleared.
func TestLogin_Success_ResetAndSessionShareOneTransaction(t *testing.T) {
	ctx := t.Context()
	uc, users, sessions := newUC()
	var inTx []string
	uc.tx = &fakeTx{onEnter: func() { inTx = append(inTx, "begin") }, onExit: func() { inTx = append(inTx, "commit") }}

	users.On("GetByEmail", ctx, "admin@acme.com").Return(activeUser(t, "correct"), nil).Once()
	expectConsume(users, true, nil)
	users.On("ResetFailedLogin", mock.Anything, "u1").
		Run(func(mock.Arguments) { inTx = append(inTx, "reset") }).Return(nil).Once()
	sessions.On("Create", mock.Anything, mock.Anything).
		Run(func(mock.Arguments) { inTx = append(inTx, "session") }).
		Return(&domain.Session{ID: "s1"}, nil).Once()

	_, err := uc.Login(ctx, domain.LoginInput{Email: "admin@acme.com", Password: "correct"})
	require.NoError(t, err)
	assert.Equal(t, []string{"begin", "reset", "session", "commit"}, inTx)
	users.AssertExpectations(t)
}

// fakeTx is the transaction seam for tests that care that one was opened.
type fakeTx struct{ onEnter, onExit func() }

func (f *fakeTx) WithinTransaction(ctx context.Context, fn func(context.Context) error) error {
	f.onEnter()
	err := fn(ctx)
	f.onExit()
	return err
}

func TestLogin_UserNotFound(t *testing.T) {
	ctx := t.Context()
	uc, users, _ := newUC()
	users.On("GetByEmail", ctx, "nobody@acme.com").Return(nil, nil).Once()

	_, err := uc.Login(ctx, domain.LoginInput{Email: "nobody@acme.com", Password: "x"})
	assert.IsType(t, &apperrors.UnauthorizedError{}, err)
}

// TestLogin_EveryFailure_SameAnswerAndSameWork is the enumeration + oracle
// regression, over every shape a refused login can take.
//
// Two defects are pinned here, both found by measurement against the real router:
//
//   - ENUMERATION. A locked account once short-circuited BEFORE the hash
//     comparison, so a locked registered address answered in 2.4ms while an
//     unknown one paid the full argon2 cost (69ms) — a ~28x oracle, mintable
//     against any address for five requests.
//   - PASSWORD ORACLE. Moving the lock check after the verification closed that
//     but opened worse: the locked account then answered "locked" for the RIGHT
//     password and "invalid credentials" for a wrong one, handing an attacker
//     who had just locked the address a full-speed scoring function for guesses.
//
// So every failure must be the same answer for the same work, and the work must
// be spent on the same KIND of hash: the stored credential is verified only when
// the account could authenticate right now AND the attempt was inside the
// budget. Both are asserted at the seam — the property is about work done, which
// a wall clock can only approximate flakily; the acceptance suite logs measured
// medians alongside it.
func TestLogin_EveryFailure_SameAnswerAndSameWork(t *testing.T) {
	lockedInFuture := time.Now().Add(10 * time.Minute)
	staleLock := time.Now().Add(-10 * time.Minute)

	cases := []struct {
		name string
		user func(t *testing.T) *domain.User
		// email is what the client submits; it must normalise to the canonical
		// address the repository is asked for. Empty means the canonical form.
		email    string
		password string
		// charged: the attempt reaches the budget at all (login-capable). The
		// account row's own lock columns do NOT decide this — the repository
		// does, which is why they are set here purely as decoration.
		charged bool
		// allowed: what the charge answers. Only an allowed attempt may verify
		// the stored hash.
		allowed bool
		// chargeFails: the charge could not be made at all. Must refuse (fail
		// closed) and be indistinguishable from a spent budget.
		chargeFails bool
		// wantHashes: the encoded hashes handed to the verification seam, in
		// order. One entry on every path but the undecodable one, which pays its
		// argon2 on the fallback.
		wantHashes func(u *domain.User) []string
	}{
		{name: "unknown address",
			user: func(*testing.T) *domain.User { return nil }, password: "x"},
		{name: "wrong password",
			user:     func(t *testing.T) *domain.User { return activeUser(t, "correct") },
			password: "wrong", charged: true, allowed: true,
			wantHashes: func(u *domain.User) []string { return []string{*u.PasswordHash} }},
		// The two out-of-budget cases are the oracle regression: they must be
		// indistinguishable from each other and from an unknown address, and
		// neither may touch the stored hash.
		{name: "budget spent, wrong password",
			user: func(t *testing.T) *domain.User {
				u := activeUser(t, "correct")
				u.LockedUntil = &lockedInFuture
				u.FailedLoginAttempts = maxFailedAttempts
				return u
			}, password: "wrong", charged: true},
		{name: "budget spent, CORRECT password",
			user: func(t *testing.T) *domain.User {
				u := activeUser(t, "correct")
				u.LockedUntil = &lockedInFuture
				u.FailedLoginAttempts = maxFailedAttempts
				return u
			}, password: "correct", charged: true},
		// An elapsed window is the repository's call, not the use case's: the row
		// still carries a saturated counter and a stale stamp, and the charge
		// still comes back allowed, so the stored hash IS verified.
		{name: "expired lock, wrong password",
			user: func(t *testing.T) *domain.User {
				u := activeUser(t, "correct")
				u.LockedUntil = &staleLock
				u.FailedLoginAttempts = maxFailedAttempts
				return u
			}, password: "wrong", charged: true, allowed: true,
			wantHashes: func(u *domain.User) []string { return []string{*u.PasswordHash} }},
		{name: "disabled member, right password",
			user: func(t *testing.T) *domain.User {
				u := activeUser(t, "correct")
				u.Status = "disabled"
				return u
			}, password: "correct"},
		{name: "service account, right password",
			user: func(t *testing.T) *domain.User {
				u := activeUser(t, "correct")
				u.Kind = domain.KindService
				return u
			}, password: "correct"},
		// An invited member exists but has no credential yet: it must not be
		// distinguishable from an address nobody has ever registered.
		{name: "invited member, no password set",
			user: func(t *testing.T) *domain.User {
				u := activeUser(t, "correct")
				u.Status = domain.StatusInvited
				u.PasswordHash = nil
				return u
			}, password: "correct"},
		// Normalisation must not fork the answer either: an account probed with
		// an odd spelling of its address gets the same generic refusal, and still
		// never has its stored hash verified once the budget is gone.
		{name: "budget spent, mixed-case email",
			user: func(t *testing.T) *domain.User {
				u := activeUser(t, "correct")
				u.LockedUntil = &lockedInFuture
				return u
			}, email: "SomeOne@Acme.COM", password: "correct", charged: true},
		{name: "budget spent, padded email",
			user: func(t *testing.T) *domain.User {
				u := activeUser(t, "correct")
				u.LockedUntil = &lockedInFuture
				return u
			}, email: "  someone@acme.com\t", password: "correct", charged: true},
		{name: "unknown address, mixed-case and padded",
			user:  func(*testing.T) *domain.User { return nil },
			email: " SomeOne@Acme.COM \t", password: "x"},
		// A charge that could not be made refuses (fail closed) and pays the
		// dummy, exactly like a spent budget.
		{name: "charge failed",
			user:     func(t *testing.T) *domain.User { return activeUser(t, "correct") },
			password: "correct", charged: true, chargeFails: true},
		// A stored hash this process cannot decode must not answer 500 while
		// every other refusal answers 401 — that is the same oracle from the
		// other side — and must not answer FASTER either: VerifyPassword decodes
		// before it hashes, so the broken hash costs ~2.4ms against a ~70ms
		// floor. The fallback to the dummy is what pays the difference, and it
		// is the only path with two seam calls: the first did no argon2 at all.
		{name: "unreadable stored hash",
			user: func(t *testing.T) *domain.User {
				u := activeUser(t, "correct")
				broken := "not-an-argon2-hash"
				u.PasswordHash = &broken
				return u
			}, password: "correct", charged: true, allowed: true,
			wantHashes: func(u *domain.User) []string { return []string{*u.PasswordHash, dummyHash} }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			uc, users, _ := newUC()
			verifications := spyVerify(uc)
			user := tc.user(t)

			// Whatever the client typed, the repository is asked for exactly one
			// canonical address — lowercased and trimmed. A mock set on that
			// address and nothing else is the assertion.
			const canonical = "someone@acme.com"
			submitted := tc.email
			if submitted == "" {
				submitted = canonical
			}
			users.On("GetByEmail", ctx, canonical).Return(user, nil).Once()
			if tc.charged {
				var chargeErr error
				if tc.chargeFails {
					chargeErr = errors.New("connection reset")
				}
				expectConsume(users, tc.allowed, chargeErr)
			}

			_, err := uc.Login(ctx, domain.LoginInput{Email: submitted, Password: tc.password})

			require.Error(t, err)
			assert.IsType(t, &apperrors.UnauthorizedError{}, err)
			assert.Equal(t, domain.MsgInvalidCredentials, err.Error(),
				"every failure must read identically — there is no longer any failure that does not")

			wantHashes := []string{dummyHash}
			if tc.wantHashes != nil {
				wantHashes = tc.wantHashes(user)
			}
			assert.Equal(t, wantHashes, verifications.hashes,
				"the stored credential must be verified only when the account could authenticate "+
					"right now AND the attempt was inside the budget; the dummy equalizer otherwise "+
					"— and every path must end up having paid exactly one argon2")

			if !tc.charged {
				users.AssertNotCalled(t, "ConsumeLoginAttempt", anyConsumeCall...)
			}
			users.AssertNotCalled(t, "ResetFailedLogin", mock.Anything, mock.Anything)
			users.AssertExpectations(t)
		})
	}
}

// The successful path pays the same single verification, so a correct password
// is not distinguishable by work either (only by its answer, which is the point).
func TestLogin_Success_VerifiesExactlyOnce(t *testing.T) {
	ctx := t.Context()
	uc, users, sessions := newUC()
	verifications := spyVerify(uc)
	users.On("GetByEmail", ctx, "admin@acme.com").Return(activeUser(t, "correct"), nil).Once()
	expectConsume(users, true, nil)
	users.On("ResetFailedLogin", mock.Anything, "u1").Return(nil).Once()
	sessions.On("Create", mock.Anything, mock.Anything).Return(&domain.Session{ID: "s1"}, nil).Once()

	_, err := uc.Login(ctx, domain.LoginInput{Email: "admin@acme.com", Password: "correct"})
	require.NoError(t, err)
	assert.Equal(t, 1, verifications.calls)
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
