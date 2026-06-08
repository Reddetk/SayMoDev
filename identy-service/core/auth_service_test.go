package core_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/Reddetk/SayMoDev/identy-service/core"
	corerr "github.com/Reddetk/SayMoDev/identy-service/core/coreErrors"
	valobj "github.com/Reddetk/SayMoDev/identy-service/core/valObj"
	"github.com/Reddetk/SayMoDev/identy-service/mocks"
	"github.com/Reddetk/SayMoDev/identy-service/testdata"
)

// --- helpers ----------------------------------------------------------------

func buildAuthService(
	repo *mocks.MockAccountRepository,
	issuer *mocks.MockTokenIssuer,
	blacklist *mocks.MockTokenBlacklist,
	rl *mocks.MockRateLimiter,
	events *mocks.MockAccountEventsProducer,
	oauth *mocks.MockGoogleOAuthProvider,
) *core.AuthService {
	return core.NewAuthService(repo, issuer, blacklist, rl, events, oauth)
}

// --- AuthService.Login ------------------------------------------------------

func TestLogin_HappyPath(t *testing.T) {
	// Arrange
	repo := &mocks.MockAccountRepository{}
	issuer := &mocks.MockTokenIssuer{}
	blacklist := &mocks.MockTokenBlacklist{}
	rl := &mocks.MockRateLimiter{}
	events := &mocks.MockAccountEventsProducer{}
	oauth := &mocks.MockGoogleOAuthProvider{}

	acc := testdata.NewActiveAccount()

	rl.On("CheckIP", mock.Anything, testdata.FixtureClientIP).Return(nil)
	repo.On("FindByEmail", mock.Anything, testdata.FixtureEmail).Return(acc, nil)
	rl.On("CheckAccount", mock.Anything, acc.UUID()).Return(nil)
	repo.On("SaveSessionWithTx", mock.Anything, acc).Return(nil)
	issuer.On("Issue",
		mock.Anything,
		acc.UUID(),
		mock.AnythingOfType("string"),
		mock.AnythingOfType("string"),
		acc.Revision(),
	).Return("eyJ.test.token", testdata.FixtureJTI, nil)
	events.On("SessionCreated",
		mock.Anything,
		acc.UUID(),
		mock.AnythingOfType("string"),
		testdata.FixtureFingerprint,
		mock.AnythingOfType("int64"),
	).Return(nil)

	svc := buildAuthService(repo, issuer, blacklist, rl, events, oauth)

	// Act
	result, err := svc.Login(
		context.Background(),
		testdata.FixtureEmail,
		testdata.FixturePassword1,
		testdata.FixtureFingerprint,
		testdata.FixtureClientIP,
	)

	// Assert
	require.NoError(t, err)
	assert.Equal(t, "eyJ.test.token", result.AccessToken)
	assert.Equal(t, acc.UUID(), result.AccountID)
	assert.Equal(t, valobj.RolePatient.String(), result.Role)
	assert.NotEmpty(t, result.SessionID)

	repo.AssertExpectations(t)
	rl.AssertExpectations(t)
	issuer.AssertExpectations(t)
	events.AssertExpectations(t)
}

func TestLogin_RateLimitIP_StopsBeforeFindByEmail(t *testing.T) {
	// Arrange
	repo := &mocks.MockAccountRepository{}
	issuer := &mocks.MockTokenIssuer{}
	blacklist := &mocks.MockTokenBlacklist{}
	rl := &mocks.MockRateLimiter{}
	events := &mocks.MockAccountEventsProducer{}
	oauth := &mocks.MockGoogleOAuthProvider{}

	rl.On("CheckIP", mock.Anything, testdata.FixtureClientIP).Return(corerr.ErrRateLimitIP)

	svc := buildAuthService(repo, issuer, blacklist, rl, events, oauth)

	// Act
	_, err := svc.Login(
		context.Background(),
		testdata.FixtureEmail,
		testdata.FixturePassword1,
		testdata.FixtureFingerprint,
		testdata.FixtureClientIP,
	)

	// Assert
	assert.ErrorIs(t, err, corerr.ErrRateLimitIP)
	repo.AssertNotCalled(t, "FindByEmail", mock.Anything, mock.Anything)
}

func TestLogin_AccountNotFound_ReturnsErrInvalidCredentials(t *testing.T) {
	// Arrange
	repo := &mocks.MockAccountRepository{}
	issuer := &mocks.MockTokenIssuer{}
	blacklist := &mocks.MockTokenBlacklist{}
	rl := &mocks.MockRateLimiter{}
	events := &mocks.MockAccountEventsProducer{}
	oauth := &mocks.MockGoogleOAuthProvider{}

	unknownEmail := "unknown@saymo.ru"

	rl.On("CheckIP", mock.Anything, testdata.FixtureClientIP).Return(nil)
	repo.On("FindByEmail", mock.Anything, unknownEmail).Return(nil, errors.New("not found"))

	svc := buildAuthService(repo, issuer, blacklist, rl, events, oauth)

	// Act
	// Инвариант: ErrInvalidCredentials даже если аккаунт не найден (no credential oracle).
	// bcrypt.CompareHashAndPassword выполняется против DummyPasswordHash для timing safety.
	_, err := svc.Login(
		context.Background(),
		unknownEmail,
		testdata.FixturePassword1,
		testdata.FixtureFingerprint,
		testdata.FixtureClientIP,
	)

	// Assert
	assert.ErrorIs(t, err, corerr.ErrInvalidCredentials,
		"account not found must return generic credentials error to prevent enumeration")
	issuer.AssertNotCalled(t, "Issue", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestLogin_WrongPassword_RecordsFailureAndReturnsErrInvalidCredentials(t *testing.T) {
	// Arrange
	repo := &mocks.MockAccountRepository{}
	issuer := &mocks.MockTokenIssuer{}
	blacklist := &mocks.MockTokenBlacklist{}
	rl := &mocks.MockRateLimiter{}
	events := &mocks.MockAccountEventsProducer{}
	oauth := &mocks.MockGoogleOAuthProvider{}

	acc := testdata.NewActiveAccount()

	rl.On("CheckIP", mock.Anything, testdata.FixtureClientIP).Return(nil)
	repo.On("FindByEmail", mock.Anything, testdata.FixtureEmail).Return(acc, nil)
	rl.On("CheckAccount", mock.Anything, acc.UUID()).Return(nil)
	// RecordFailure вызывается при провале bcrypt.Compare
	rl.On("RecordFailure", mock.Anything, testdata.FixtureClientIP, acc.UUID()).Return(nil)

	svc := buildAuthService(repo, issuer, blacklist, rl, events, oauth)

	// Act
	_, err := svc.Login(
		context.Background(),
		testdata.FixtureEmail,
		"WrongPassword!",
		testdata.FixtureFingerprint,
		testdata.FixtureClientIP,
	)

	// Assert
	assert.ErrorIs(t, err, corerr.ErrInvalidCredentials)
	rl.AssertCalled(t, "RecordFailure", mock.Anything, testdata.FixtureClientIP, acc.UUID())
	issuer.AssertNotCalled(t, "Issue", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestLogin_AccountLocked_StopsAfterFindByEmail(t *testing.T) {
	// Arrange
	repo := &mocks.MockAccountRepository{}
	issuer := &mocks.MockTokenIssuer{}
	blacklist := &mocks.MockTokenBlacklist{}
	rl := &mocks.MockRateLimiter{}
	events := &mocks.MockAccountEventsProducer{}
	oauth := &mocks.MockGoogleOAuthProvider{}

	acc := testdata.NewActiveAccount()
	_, lockErr := acc.Lock(nil)
	require.NoError(t, lockErr, "fixture setup: Lock must not fail")

	rl.On("CheckIP", mock.Anything, testdata.FixtureClientIP).Return(nil)
	repo.On("FindByEmail", mock.Anything, testdata.FixtureEmail).Return(acc, nil)

	svc := buildAuthService(repo, issuer, blacklist, rl, events, oauth)

	// Act
	// Инвариант: заблокированный аккаунт возвращает ErrInvalidCredentials, не ErrAccountLocked.
	_, err := svc.Login(
		context.Background(),
		testdata.FixtureEmail,
		testdata.FixturePassword1,
		testdata.FixtureFingerprint,
		testdata.FixtureClientIP,
	)

	// Assert
	assert.ErrorIs(t, err, corerr.ErrInvalidCredentials,
		"locked account must not leak status via error type")
	// CheckAccount не вызывается: остановились до него
	rl.AssertNotCalled(t, "CheckAccount", mock.Anything, mock.Anything)
}

func TestLogin_RateLimitAccount_StopsBeforeBcrypt(t *testing.T) {
	// Arrange
	repo := &mocks.MockAccountRepository{}
	issuer := &mocks.MockTokenIssuer{}
	blacklist := &mocks.MockTokenBlacklist{}
	rl := &mocks.MockRateLimiter{}
	events := &mocks.MockAccountEventsProducer{}
	oauth := &mocks.MockGoogleOAuthProvider{}

	acc := testdata.NewActiveAccount()

	rl.On("CheckIP", mock.Anything, testdata.FixtureClientIP).Return(nil)
	repo.On("FindByEmail", mock.Anything, testdata.FixtureEmail).Return(acc, nil)
	rl.On("CheckAccount", mock.Anything, acc.UUID()).Return(corerr.ErrRateLimitAccount)

	svc := buildAuthService(repo, issuer, blacklist, rl, events, oauth)

	// Act
	_, err := svc.Login(
		context.Background(),
		testdata.FixtureEmail,
		testdata.FixturePassword1,
		testdata.FixtureFingerprint,
		testdata.FixtureClientIP,
	)

	// Assert
	assert.ErrorIs(t, err, corerr.ErrRateLimitAccount)
	// TokenIssuer не вызывается: rate limit останавливает до bcrypt
	issuer.AssertNotCalled(t, "Issue", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestLogin_TokenIssuerFails_ReturnsError(t *testing.T) {
	// Arrange
	repo := &mocks.MockAccountRepository{}
	issuer := &mocks.MockTokenIssuer{}
	blacklist := &mocks.MockTokenBlacklist{}
	rl := &mocks.MockRateLimiter{}
	events := &mocks.MockAccountEventsProducer{}
	oauth := &mocks.MockGoogleOAuthProvider{}

	acc := testdata.NewActiveAccount()

	rl.On("CheckIP", mock.Anything, testdata.FixtureClientIP).Return(nil)
	repo.On("FindByEmail", mock.Anything, testdata.FixtureEmail).Return(acc, nil)
	rl.On("CheckAccount", mock.Anything, acc.UUID()).Return(nil)
	repo.On("SaveSessionWithTx", mock.Anything, acc).Return(nil)
	issuer.On("Issue",
		mock.Anything, acc.UUID(), mock.AnythingOfType("string"),
		mock.AnythingOfType("string"), acc.Revision(),
	).Return("", "", errors.New("signing key unavailable"))

	svc := buildAuthService(repo, issuer, blacklist, rl, events, oauth)

	// Act
	_, err := svc.Login(
		context.Background(),
		testdata.FixtureEmail,
		testdata.FixturePassword1,
		testdata.FixtureFingerprint,
		testdata.FixtureClientIP,
	)

	// Assert
	require.Error(t, err)
	assert.Contains(t, err.Error(), "signing key unavailable")
}

func TestLogin_EventsProducerFails_StillReturnsToken(t *testing.T) {
	// Arrange
	repo := &mocks.MockAccountRepository{}
	issuer := &mocks.MockTokenIssuer{}
	blacklist := &mocks.MockTokenBlacklist{}
	rl := &mocks.MockRateLimiter{}
	events := &mocks.MockAccountEventsProducer{}
	oauth := &mocks.MockGoogleOAuthProvider{}

	acc := testdata.NewActiveAccount()

	rl.On("CheckIP", mock.Anything, testdata.FixtureClientIP).Return(nil)
	repo.On("FindByEmail", mock.Anything, testdata.FixtureEmail).Return(acc, nil)
	rl.On("CheckAccount", mock.Anything, acc.UUID()).Return(nil)
	repo.On("SaveSessionWithTx", mock.Anything, acc).Return(nil)
	issuer.On("Issue",
		mock.Anything, acc.UUID(), mock.AnythingOfType("string"),
		mock.AnythingOfType("string"), acc.Revision(),
	).Return("eyJ.test.token", testdata.FixtureJTI, nil)
	// fire-and-forget: ошибка события не блокирует ответ клиенту
	events.On("SessionCreated",
		mock.Anything, acc.UUID(),
		mock.AnythingOfType("string"), testdata.FixtureFingerprint,
		mock.AnythingOfType("int64"),
	).Return(errors.New("kafka unavailable"))

	svc := buildAuthService(repo, issuer, blacklist, rl, events, oauth)

	// Act
	result, err := svc.Login(
		context.Background(),
		testdata.FixtureEmail,
		testdata.FixturePassword1,
		testdata.FixtureFingerprint,
		testdata.FixtureClientIP,
	)

	// Assert
	require.NoError(t, err)
	assert.Equal(t, "eyJ.test.token", result.AccessToken)
}

// --- AuthService.InitiateGoogleOAuth ----------------------------------------

func TestInitiateGoogleOAuth_HappyPath(t *testing.T) {
	// Arrange
	repo := &mocks.MockAccountRepository{}
	issuer := &mocks.MockTokenIssuer{}
	blacklist := &mocks.MockTokenBlacklist{}
	rl := &mocks.MockRateLimiter{}
	events := &mocks.MockAccountEventsProducer{}
	oauth := &mocks.MockGoogleOAuthProvider{}

	expectedURL := "https://accounts.google.com/o/oauth2/auth?state=csrf-token-fixture-001"
	expectedState := testdata.NewOAuthState()

	rl.On("CheckIP", mock.Anything, testdata.FixtureClientIP).Return(nil)
	oauth.On("BuildAuthURL", mock.Anything).Return(expectedURL, expectedState, nil)

	svc := buildAuthService(repo, issuer, blacklist, rl, events, oauth)

	// Act
	url, state, err := svc.InitiateGoogleOAuth(context.Background(), testdata.FixtureClientIP)

	// Assert
	require.NoError(t, err)
	assert.Equal(t, expectedURL, url)
	assert.Equal(t, expectedState, state)
	rl.AssertExpectations(t)
	oauth.AssertExpectations(t)
}

func TestInitiateGoogleOAuth_RateLimitIP_StopsBeforeBuildAuthURL(t *testing.T) {
	// Arrange
	repo := &mocks.MockAccountRepository{}
	issuer := &mocks.MockTokenIssuer{}
	blacklist := &mocks.MockTokenBlacklist{}
	rl := &mocks.MockRateLimiter{}
	events := &mocks.MockAccountEventsProducer{}
	oauth := &mocks.MockGoogleOAuthProvider{}

	rl.On("CheckIP", mock.Anything, testdata.FixtureClientIP).Return(corerr.ErrRateLimitIP)

	svc := buildAuthService(repo, issuer, blacklist, rl, events, oauth)

	// Act
	_, _, err := svc.InitiateGoogleOAuth(context.Background(), testdata.FixtureClientIP)

	// Assert
	assert.ErrorIs(t, err, corerr.ErrRateLimitIP)
	oauth.AssertNotCalled(t, "BuildAuthURL", mock.Anything)
}

// --- G9 Write Order: evictedJTI path ----------------------------------------

func TestLogin_EvictedJTI_AddedToBlacklistAfterSaveSession(t *testing.T) {
	// G9 Write Order: если OpenSession возвращает evictedJTI (6-я сессия вытесняет первую),
	// TokenBlacklist.Add должен быть вызван после SaveSessionWithTx.

	// Arrange
	repo := &mocks.MockAccountRepository{}
	issuer := &mocks.MockTokenIssuer{}
	blacklist := &mocks.MockTokenBlacklist{}
	rl := &mocks.MockRateLimiter{}
	events := &mocks.MockAccountEventsProducer{}
	oauth := &mocks.MockGoogleOAuthProvider{}

	// NewActiveAccount содержит 1 сессию с FixtureJTI (FixtureSessionID).
	// Добавляем ещё 4 валидных сессии через OpenSession -- итого 5 (лимит по G5).
	// Следующий Login откроет шестую сессию и вытеснит самую старую (FixtureJTI).
	acc := testdata.NewActiveAccount()

	extraSessionIDs := []string{
		"00000000-0000-0000-0000-000000000101",
		"00000000-0000-0000-0000-000000000102",
		"00000000-0000-0000-0000-000000000103",
		"00000000-0000-0000-0000-000000000104",
	}
	extraJTIs := []string{
		"00000000-0000-0000-0000-000000000201",
		"00000000-0000-0000-0000-000000000202",
		"00000000-0000-0000-0000-000000000203",
		"00000000-0000-0000-0000-000000000204",
	}

	for i := 0; i < 4; i++ {
		_, _, err := acc.OpenSession(
			extraSessionIDs[i],
			extraJTIs[i],
			"extra-fp-"+string(rune('A'+i)),
		)
		require.NoError(t, err, "fixture setup: OpenSession %d must not fail", i)
	}

	rl.On("CheckIP", mock.Anything, testdata.FixtureClientIP).Return(nil)
	repo.On("FindByEmail", mock.Anything, testdata.FixtureEmail).Return(acc, nil)
	rl.On("CheckAccount", mock.Anything, acc.UUID()).Return(nil)
	repo.On("SaveSessionWithTx", mock.Anything, acc).Return(nil)
	issuer.On("Issue",
		mock.Anything, acc.UUID(), mock.AnythingOfType("string"),
		mock.AnythingOfType("string"), acc.Revision(),
	).Return("eyJ.test.token", testdata.FixtureJTI, nil)
	events.On("SessionCreated",
		mock.Anything, acc.UUID(),
		mock.AnythingOfType("string"), testdata.FixtureFingerprint,
		mock.AnythingOfType("int64"),
	).Return(nil)
	// FixtureJTI -- JTI самой старой сессии, должен попасть в blacklist (G9)
	blacklist.On("Add", mock.Anything, testdata.FixtureJTI, mock.Anything).Return(nil)

	svc := buildAuthService(repo, issuer, blacklist, rl, events, oauth)

	// Act
	result, err := svc.Login(
		context.Background(),
		testdata.FixtureEmail,
		testdata.FixturePassword1,
		testdata.FixtureFingerprint,
		testdata.FixtureClientIP,
	)

	// Assert
	require.NoError(t, err)
	assert.Equal(t, "eyJ.test.token", result.AccessToken)
	blacklist.AssertCalled(t, "Add", mock.Anything, testdata.FixtureJTI, mock.Anything)
}
