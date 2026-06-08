package core_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

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
	result, err := svc.Login(
		context.Background(),
		testdata.FixtureEmail,
		"Password1!",
		testdata.FixtureFingerprint,
		testdata.FixtureClientIP,
	)

	assert.NoError(t, err)
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
	repo := &mocks.MockAccountRepository{}
	issuer := &mocks.MockTokenIssuer{}
	blacklist := &mocks.MockTokenBlacklist{}
	rl := &mocks.MockRateLimiter{}
	events := &mocks.MockAccountEventsProducer{}
	oauth := &mocks.MockGoogleOAuthProvider{}

	rl.On("CheckIP", mock.Anything, testdata.FixtureClientIP).Return(corerr.ErrRateLimitIP)

	svc := buildAuthService(repo, issuer, blacklist, rl, events, oauth)
	_, err := svc.Login(
		context.Background(),
		testdata.FixtureEmail,
		"Password1!",
		testdata.FixtureFingerprint,
		testdata.FixtureClientIP,
	)

	assert.ErrorIs(t, err, corerr.ErrRateLimitIP)
	repo.AssertNotCalled(t, "FindByEmail", mock.Anything, mock.Anything)
}

func TestLogin_AccountNotFound_ReturnsErrInvalidCredentials(t *testing.T) {
	repo := &mocks.MockAccountRepository{}
	issuer := &mocks.MockTokenIssuer{}
	blacklist := &mocks.MockTokenBlacklist{}
	rl := &mocks.MockRateLimiter{}
	events := &mocks.MockAccountEventsProducer{}
	oauth := &mocks.MockGoogleOAuthProvider{}

	rl.On("CheckIP", mock.Anything, testdata.FixtureClientIP).Return(nil)
	repo.On("FindByEmail", mock.Anything, "unknown@saymo.ru").
		Return(nil, errors.New("not found"))

	svc := buildAuthService(repo, issuer, blacklist, rl, events, oauth)
	_, err := svc.Login(
		context.Background(),
		"unknown@saymo.ru",
		"Password1!",
		testdata.FixtureFingerprint,
		testdata.FixtureClientIP,
	)

	// Инвариант: ErrInvalidCredentials даже если аккаунт не найден (no credential oracle).
	// bcrypt.CompareHashAndPassword выполняется против DummyPasswordHash для timing safety.
	assert.ErrorIs(t, err, corerr.ErrInvalidCredentials)
	issuer.AssertNotCalled(t, "Issue", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestLogin_WrongPassword_RecordsFailureAndReturnsErrInvalidCredentials(t *testing.T) {
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
	_, err := svc.Login(
		context.Background(),
		testdata.FixtureEmail,
		"WrongPassword!",
		testdata.FixtureFingerprint,
		testdata.FixtureClientIP,
	)

	assert.ErrorIs(t, err, corerr.ErrInvalidCredentials)
	rl.AssertCalled(t, "RecordFailure", mock.Anything, testdata.FixtureClientIP, acc.UUID())
	issuer.AssertNotCalled(t, "Issue", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestLogin_AccountLocked_StopsAfterFindByEmail(t *testing.T) {
	repo := &mocks.MockAccountRepository{}
	issuer := &mocks.MockTokenIssuer{}
	blacklist := &mocks.MockTokenBlacklist{}
	rl := &mocks.MockRateLimiter{}
	events := &mocks.MockAccountEventsProducer{}
	oauth := &mocks.MockGoogleOAuthProvider{}

	// Используем NewActiveAccount и вызываем Lock(nil) -- бессрочная блокировка
	acc := testdata.NewActiveAccount()
	_, lockErr := acc.Lock(nil)
	if lockErr != nil {
		t.Fatalf("unexpected Lock error: %v", lockErr)
	}

	rl.On("CheckIP", mock.Anything, testdata.FixtureClientIP).Return(nil)
	repo.On("FindByEmail", mock.Anything, testdata.FixtureEmail).Return(acc, nil)

	svc := buildAuthService(repo, issuer, blacklist, rl, events, oauth)
	_, err := svc.Login(
		context.Background(),
		testdata.FixtureEmail,
		"Password1!",
		testdata.FixtureFingerprint,
		testdata.FixtureClientIP,
	)

	// Инвариант: заблокированный аккаунт возвращает ErrInvalidCredentials, не ErrAccountLocked
	assert.ErrorIs(t, err, corerr.ErrInvalidCredentials)
	// CheckAccount не вызывается -- остановились до него
	rl.AssertNotCalled(t, "CheckAccount", mock.Anything, mock.Anything)
}

func TestLogin_RateLimitAccount_StopsBeforeBcrypt(t *testing.T) {
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
	_, err := svc.Login(
		context.Background(),
		testdata.FixtureEmail,
		"Password1!",
		testdata.FixtureFingerprint,
		testdata.FixtureClientIP,
	)

	assert.ErrorIs(t, err, corerr.ErrRateLimitAccount)
	// TokenIssuer не вызывается -- rate limit останавливает до bcrypt
	issuer.AssertNotCalled(t, "Issue", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestLogin_TokenIssuerFails_ReturnsError(t *testing.T) {
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
	_, err := svc.Login(
		context.Background(),
		testdata.FixtureEmail,
		"Password1!",
		testdata.FixtureFingerprint,
		testdata.FixtureClientIP,
	)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "signing key unavailable")
}

func TestLogin_EventsProducerFails_StillReturnsToken(t *testing.T) {
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
	result, err := svc.Login(
		context.Background(),
		testdata.FixtureEmail,
		"Password1!",
		testdata.FixtureFingerprint,
		testdata.FixtureClientIP,
	)

	assert.NoError(t, err)
	assert.Equal(t, "eyJ.test.token", result.AccessToken)
}

// --- AuthService.InitiateGoogleOAuth ----------------------------------------

func TestInitiateGoogleOAuth_HappyPath(t *testing.T) {
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
	url, state, err := svc.InitiateGoogleOAuth(context.Background(), testdata.FixtureClientIP)

	assert.NoError(t, err)
	assert.Equal(t, expectedURL, url)
	assert.Equal(t, expectedState, state)
	rl.AssertExpectations(t)
	oauth.AssertExpectations(t)
}

func TestInitiateGoogleOAuth_RateLimitIP_StopsBeforeBuildAuthURL(t *testing.T) {
	repo := &mocks.MockAccountRepository{}
	issuer := &mocks.MockTokenIssuer{}
	blacklist := &mocks.MockTokenBlacklist{}
	rl := &mocks.MockRateLimiter{}
	events := &mocks.MockAccountEventsProducer{}
	oauth := &mocks.MockGoogleOAuthProvider{}

	rl.On("CheckIP", mock.Anything, testdata.FixtureClientIP).Return(corerr.ErrRateLimitIP)

	svc := buildAuthService(repo, issuer, blacklist, rl, events, oauth)
	_, _, err := svc.InitiateGoogleOAuth(context.Background(), testdata.FixtureClientIP)

	assert.ErrorIs(t, err, corerr.ErrRateLimitIP)
	oauth.AssertNotCalled(t, "BuildAuthURL", mock.Anything)
}

// --- G9 Write Order: evictedJTI path ----------------------------------------

func TestLogin_EvictedJTI_AddedToBlacklistAfterSaveSession(t *testing.T) {
	// G9 Write Order: если OpenSession возвращает evictedJTI (6-я сессия вытесняет первую),
	// TokenBlacklist.Add должен быть вызван после SaveSessionWithTx.
	repo := &mocks.MockAccountRepository{}
	issuer := &mocks.MockTokenIssuer{}
	blacklist := &mocks.MockTokenBlacklist{}
	rl := &mocks.MockRateLimiter{}
	events := &mocks.MockAccountEventsProducer{}
	oauth := &mocks.MockGoogleOAuthProvider{}

	// NewActiveAccount уже содержит 1 сессию (FixtureSessionID).
	// Добавляем ещё 4 через OpenSession -- итого 5 (лимит).
	// Следующий Login вызовет OpenSession шестой раз и вытеснит первую.
	acc := testdata.NewActiveAccount()
	for i := 0; i < 4; i++ {
		_, _, _ = acc.OpenSession(
			"extra-session-"+string(rune('A'+i)),
			"extra-jti-"+string(rune('A'+i)),
			"extra-fp-"+string(rune('A'+i)),
		)
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
	// evictedJTI должен попасть в blacklist
	blacklist.On("Add", mock.Anything, mock.AnythingOfType("string"), mock.Anything).Return(nil)

	svc := buildAuthService(repo, issuer, blacklist, rl, events, oauth)
	result, err := svc.Login(
		context.Background(),
		testdata.FixtureEmail,
		"Password1!",
		testdata.FixtureFingerprint,
		testdata.FixtureClientIP,
	)

	assert.NoError(t, err)
	assert.Equal(t, "eyJ.test.token", result.AccessToken)
	blacklist.AssertCalled(t, "Add", mock.Anything, mock.AnythingOfType("string"), mock.Anything)
}
