package core_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/Reddetk/SayMoDev/identy-service/core"
	corerr "github.com/Reddetk/SayMoDev/identy-service/core/coreErrors"
	"github.com/Reddetk/SayMoDev/identy-service/core/entity"
	valobj "github.com/Reddetk/SayMoDev/identy-service/core/valObj"
	"github.com/Reddetk/SayMoDev/identy-service/mocks"
)

// --- helpers ----------------------------------------------------------------

const (
	testEmail       = "patient@saymo.ru"
	testFingerprint = "fp-abc123"
	testClientIP    = "1.2.3.4"
	testAccountID   = "00000000-0000-0000-0000-000000000001"
	testSessionID   = "00000000-0000-0000-0000-000000000002"
	testAccessToken = "eyJ.test.token"
	testJTI         = "00000000-0000-0000-0000-000000000003"
)

// buildActiveAccount строит entity.Account с предвычисленным bcrypt-хешем пароля "Password1!".
// Хеш соответствует паролю "Password1!" с cost=10.
func buildActiveAccount(t *testing.T) *entity.Account {
	t.Helper()
	const bcryptHash = "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"
	acc, err := entity.NewAccount(
		testEmail,
		"Test Patient",
		valobj.RolePatient,
		&bcryptHash,
	)
	if err != nil {
		t.Fatalf("buildActiveAccount: %v", err)
	}
	return acc
}

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

	acc := buildActiveAccount(t)

	rl.On("CheckIP", mock.Anything, testClientIP).Return(nil)
	repo.On("FindByEmail", mock.Anything, testEmail).Return(acc, nil)
	rl.On("CheckAccount", mock.Anything, acc.UUID()).Return(nil)
	repo.On("SaveSessionWithTx", mock.Anything, acc).Return(nil)
	issuer.On("Issue",
		mock.Anything,
		acc.UUID(),
		mock.AnythingOfType("string"),
		mock.AnythingOfType("string"),
		acc.Revision(),
	).Return(testAccessToken, testJTI, nil)
	events.On("SessionCreated",
		mock.Anything,
		acc.UUID(),
		mock.AnythingOfType("string"),
		testFingerprint,
		mock.AnythingOfType("int64"),
	).Return(nil)

	svc := buildAuthService(repo, issuer, blacklist, rl, events, oauth)
	result, err := svc.Login(context.Background(), testEmail, "Password1!", testFingerprint, testClientIP)

	assert.NoError(t, err)
	assert.Equal(t, testAccessToken, result.AccessToken)
	assert.Equal(t, acc.UUID(), result.AccountID)
	assert.Equal(t, valobj.RolePatient.String(), result.Role)
	assert.NotEmpty(t, result.SessionID)

	repo.AssertExpectations(t)
	rl.AssertExpectations(t)
	issuer.AssertExpectations(t)
	events.AssertExpectations(t)
}

func TestLogin_RateLimitIP_Returns429Error(t *testing.T) {
	repo := &mocks.MockAccountRepository{}
	issuer := &mocks.MockTokenIssuer{}
	blacklist := &mocks.MockTokenBlacklist{}
	rl := &mocks.MockRateLimiter{}
	events := &mocks.MockAccountEventsProducer{}
	oauth := &mocks.MockGoogleOAuthProvider{}

	rl.On("CheckIP", mock.Anything, testClientIP).Return(corerr.ErrRateLimitIP)

	svc := buildAuthService(repo, issuer, blacklist, rl, events, oauth)
	_, err := svc.Login(context.Background(), testEmail, "Password1!", testFingerprint, testClientIP)

	assert.ErrorIs(t, err, corerr.ErrRateLimitIP)
	// FindByEmail не должен вызываться до IP-чека
	repo.AssertNotCalled(t, "FindByEmail", mock.Anything, mock.Anything)
	rl.AssertExpectations(t)
}

func TestLogin_AccountNotFound_ReturnsErrInvalidCredentials(t *testing.T) {
	repo := &mocks.MockAccountRepository{}
	issuer := &mocks.MockTokenIssuer{}
	blacklist := &mocks.MockTokenBlacklist{}
	rl := &mocks.MockRateLimiter{}
	events := &mocks.MockAccountEventsProducer{}
	oauth := &mocks.MockGoogleOAuthProvider{}

	rl.On("CheckIP", mock.Anything, testClientIP).Return(nil)
	repo.On("FindByEmail", mock.Anything, "unknown@saymo.ru").
		Return((*entity.Account)(nil), errors.New("not found"))

	svc := buildAuthService(repo, issuer, blacklist, rl, events, oauth)
	_, err := svc.Login(context.Background(), "unknown@saymo.ru", "Password1!", testFingerprint, testClientIP)

	// Инвариант: ErrInvalidCredentials даже если аккаунт не найден (no credential oracle)
	assert.ErrorIs(t, err, corerr.ErrInvalidCredentials)
	issuer.AssertNotCalled(t, "Issue", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestLogin_WrongPassword_ReturnsErrInvalidCredentials(t *testing.T) {
	repo := &mocks.MockAccountRepository{}
	issuer := &mocks.MockTokenIssuer{}
	blacklist := &mocks.MockTokenBlacklist{}
	rl := &mocks.MockRateLimiter{}
	events := &mocks.MockAccountEventsProducer{}
	oauth := &mocks.MockGoogleOAuthProvider{}

	acc := buildActiveAccount(t)

	rl.On("CheckIP", mock.Anything, testClientIP).Return(nil)
	repo.On("FindByEmail", mock.Anything, testEmail).Return(acc, nil)
	rl.On("CheckAccount", mock.Anything, acc.UUID()).Return(nil)
	// RecordFailure вызывается при провале bcrypt
	rl.On("RecordFailure", mock.Anything, testClientIP, acc.UUID()).Return(nil)

	svc := buildAuthService(repo, issuer, blacklist, rl, events, oauth)
	_, err := svc.Login(context.Background(), testEmail, "WrongPassword!", testFingerprint, testClientIP)

	assert.ErrorIs(t, err, corerr.ErrInvalidCredentials)
	rl.AssertCalled(t, "RecordFailure", mock.Anything, testClientIP, acc.UUID())
	issuer.AssertNotCalled(t, "Issue", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestLogin_AccountLocked_ReturnsErrInvalidCredentials(t *testing.T) {
	repo := &mocks.MockAccountRepository{}
	issuer := &mocks.MockTokenIssuer{}
	blacklist := &mocks.MockTokenBlacklist{}
	rl := &mocks.MockRateLimiter{}
	events := &mocks.MockAccountEventsProducer{}
	oauth := &mocks.MockGoogleOAuthProvider{}

	acc := buildActiveAccount(t)
	acc.Lock()

	rl.On("CheckIP", mock.Anything, testClientIP).Return(nil)
	repo.On("FindByEmail", mock.Anything, testEmail).Return(acc, nil)

	svc := buildAuthService(repo, issuer, blacklist, rl, events, oauth)
	_, err := svc.Login(context.Background(), testEmail, "Password1!", testFingerprint, testClientIP)

	// Заблокированный аккаунт: инвариант -- ErrInvalidCredentials (не ErrAccountLocked)
	assert.ErrorIs(t, err, corerr.ErrInvalidCredentials)
	// CheckAccount не вызывается для заблокированного аккаунта
	rl.AssertNotCalled(t, "CheckAccount", mock.Anything, mock.Anything)
}

func TestLogin_RateLimitAccount_ReturnsErrRateLimitAccount(t *testing.T) {
	repo := &mocks.MockAccountRepository{}
	issuer := &mocks.MockTokenIssuer{}
	blacklist := &mocks.MockTokenBlacklist{}
	rl := &mocks.MockRateLimiter{}
	events := &mocks.MockAccountEventsProducer{}
	oauth := &mocks.MockGoogleOAuthProvider{}

	acc := buildActiveAccount(t)

	rl.On("CheckIP", mock.Anything, testClientIP).Return(nil)
	repo.On("FindByEmail", mock.Anything, testEmail).Return(acc, nil)
	rl.On("CheckAccount", mock.Anything, acc.UUID()).Return(corerr.ErrRateLimitAccount)

	svc := buildAuthService(repo, issuer, blacklist, rl, events, oauth)
	_, err := svc.Login(context.Background(), testEmail, "Password1!", testFingerprint, testClientIP)

	assert.ErrorIs(t, err, corerr.ErrRateLimitAccount)
	// bcrypt.Compare не вызывается -- rate limit останавливает до него
	issuer.AssertNotCalled(t, "Issue", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestLogin_TokenIssuerFails_ReturnsError(t *testing.T) {
	repo := &mocks.MockAccountRepository{}
	issuer := &mocks.MockTokenIssuer{}
	blacklist := &mocks.MockTokenBlacklist{}
	rl := &mocks.MockRateLimiter{}
	events := &mocks.MockAccountEventsProducer{}
	oauth := &mocks.MockGoogleOAuthProvider{}

	acc := buildActiveAccount(t)
	issueErr := errors.New("signing key unavailable")

	rl.On("CheckIP", mock.Anything, testClientIP).Return(nil)
	repo.On("FindByEmail", mock.Anything, testEmail).Return(acc, nil)
	rl.On("CheckAccount", mock.Anything, acc.UUID()).Return(nil)
	repo.On("SaveSessionWithTx", mock.Anything, acc).Return(nil)
	issuer.On("Issue",
		mock.Anything, acc.UUID(), mock.AnythingOfType("string"),
		mock.AnythingOfType("string"), acc.Revision(),
	).Return("", "", issueErr)

	svc := buildAuthService(repo, issuer, blacklist, rl, events, oauth)
	_, err := svc.Login(context.Background(), testEmail, "Password1!", testFingerprint, testClientIP)

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

	acc := buildActiveAccount(t)

	rl.On("CheckIP", mock.Anything, testClientIP).Return(nil)
	repo.On("FindByEmail", mock.Anything, testEmail).Return(acc, nil)
	rl.On("CheckAccount", mock.Anything, acc.UUID()).Return(nil)
	repo.On("SaveSessionWithTx", mock.Anything, acc).Return(nil)
	issuer.On("Issue",
		mock.Anything, acc.UUID(), mock.AnythingOfType("string"),
		mock.AnythingOfType("string"), acc.Revision(),
	).Return(testAccessToken, testJTI, nil)
	// fire-and-forget: ошибка события не блокирует ответ
	events.On("SessionCreated",
		mock.Anything, acc.UUID(),
		mock.AnythingOfType("string"), testFingerprint,
		mock.AnythingOfType("int64"),
	).Return(errors.New("kafka unavailable"))

	svc := buildAuthService(repo, issuer, blacklist, rl, events, oauth)
	result, err := svc.Login(context.Background(), testEmail, "Password1!", testFingerprint, testClientIP)

	assert.NoError(t, err)
	assert.Equal(t, testAccessToken, result.AccessToken)
}

// --- AuthService.InitiateGoogleOAuth ----------------------------------------

func TestInitiateGoogleOAuth_HappyPath(t *testing.T) {
	repo := &mocks.MockAccountRepository{}
	issuer := &mocks.MockTokenIssuer{}
	blacklist := &mocks.MockTokenBlacklist{}
	rl := &mocks.MockRateLimiter{}
	events := &mocks.MockAccountEventsProducer{}
	oauth := &mocks.MockGoogleOAuthProvider{}

	expectedURL := "https://accounts.google.com/o/oauth2/auth?..."
	expectedState := valobj.OAuthState{State: "csrf-token-123", ExpiresAt: time.Now().Add(5 * time.Minute)}

	rl.On("CheckIP", mock.Anything, testClientIP).Return(nil)
	oauth.On("BuildAuthURL", mock.Anything).Return(expectedURL, expectedState, nil)

	svc := buildAuthService(repo, issuer, blacklist, rl, events, oauth)
	url, state, err := svc.InitiateGoogleOAuth(context.Background(), testClientIP)

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

	rl.On("CheckIP", mock.Anything, testClientIP).Return(corerr.ErrRateLimitIP)

	svc := buildAuthService(repo, issuer, blacklist, rl, events, oauth)
	_, _, err := svc.InitiateGoogleOAuth(context.Background(), testClientIP)

	assert.ErrorIs(t, err, corerr.ErrRateLimitIP)
	oauth.AssertNotCalled(t, "BuildAuthURL", mock.Anything)
}

// --- openSessionAndIssueToken: evictedJTI path ------------------------------

func TestLogin_EvictedJTI_AddedToBlacklist(t *testing.T) {
	// G9 Write Order: если OpenSession возвращает evictedJTI (5-я сессия вытеснила первую),
	// TokenBlacklist.Add вызывается после SaveSessionWithTx.
	repo := &mocks.MockAccountRepository{}
	issuer := &mocks.MockTokenIssuer{}
	blacklist := &mocks.MockTokenBlacklist{}
	rl := &mocks.MockRateLimiter{}
	events := &mocks.MockAccountEventsProducer{}
	oauth := &mocks.MockGoogleOAuthProvider{}

	acc := buildActiveAccount(t)
	// Заполняем 5 сессий -- следующая OpenSession вытеснит первую
	for i := 0; i < 5; i++ {
		_, _, _ = acc.OpenSession(
			"session-"+string(rune('A'+i)),
			"jti-"+string(rune('A'+i)),
			"fp-"+string(rune('A'+i)),
		)
	}

	rl.On("CheckIP", mock.Anything, testClientIP).Return(nil)
	repo.On("FindByEmail", mock.Anything, testEmail).Return(acc, nil)
	rl.On("CheckAccount", mock.Anything, acc.UUID()).Return(nil)
	repo.On("SaveSessionWithTx", mock.Anything, acc).Return(nil)
	issuer.On("Issue",
		mock.Anything, acc.UUID(), mock.AnythingOfType("string"),
		mock.AnythingOfType("string"), acc.Revision(),
	).Return(testAccessToken, testJTI, nil)
	events.On("SessionCreated",
		mock.Anything, acc.UUID(),
		mock.AnythingOfType("string"), testFingerprint,
		mock.AnythingOfType("int64"),
	).Return(nil)
	// evictedJTI должен попасть в blacklist
	blacklist.On("Add", mock.Anything, mock.AnythingOfType("string"), mock.Anything).Return(nil)

	svc := buildAuthService(repo, issuer, blacklist, rl, events, oauth)
	result, err := svc.Login(context.Background(), testEmail, "Password1!", testFingerprint, testClientIP)

	assert.NoError(t, err)
	assert.Equal(t, testAccessToken, result.AccessToken)
	blacklist.AssertCalled(t, "Add", mock.Anything, mock.AnythingOfType("string"), mock.Anything)
}
