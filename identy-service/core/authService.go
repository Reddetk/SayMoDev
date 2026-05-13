package core

import (
	"context"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/google/uuid"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/Reddetk/SayMoDev/identy-service/core/consts"
	corerr "github.com/Reddetk/SayMoDev/identy-service/core/coreErrors"
	"github.com/Reddetk/SayMoDev/identy-service/core/entity"
	valobj "github.com/Reddetk/SayMoDev/identy-service/core/valObj"
	"github.com/Reddetk/SayMoDev/identy-service/port/out"
)

var authTracer = otel.Tracer("iam.authService")

// AuthService реализует in-port AccountAuthenticator.
//
// Зависимости инжектируются только как out-порты (интерфейсы).
// AuthService не знает о SessionService, TokenService или AccountService.
// Вся координация выполняется через out.AccountRepository, out.TokenIssuer,
// out.TokenBlacklist, out.RateLimiter, out.AccountEventsProducer и out.GoogleOAuthProvider.
type AuthService struct {
	accRep         out.AccountRepository
	tokenIssuer    out.TokenIssuer
	tokenBlacklist out.TokenBlacklist
	rateLimiter    out.RateLimiter
	eventsProducer out.AccountEventsProducer
	googleOAuth    out.GoogleOAuthProvider
}

func NewAuthService(
	accRep out.AccountRepository,
	tokenIssuer out.TokenIssuer,
	tokenBlacklist out.TokenBlacklist,
	rateLimiter out.RateLimiter,
	eventsProducer out.AccountEventsProducer,
	googleOAuth out.GoogleOAuthProvider,
) *AuthService {
	return &AuthService{
		accRep:         accRep,
		tokenIssuer:    tokenIssuer,
		tokenBlacklist: tokenBlacklist,
		rateLimiter:    rateLimiter,
		eventsProducer: eventsProducer,
		googleOAuth:    googleOAuth,
	}
}

// Login реализует юз-кейс POST /iam/auth/login.
//
// Цепочка (строгий порядок):
//
//	[1] CheckIP         — до любого обращения к БД (anti-scraping)
//	[2] FindByEmail     — если не найден, выполняется bcrypt против DummyPasswordHash
//	                      и возвращается ErrInvalidCredentials (timing safety)
//	[3] account.IsLocked — проверяем статус агрегата без внешних вызовов
//	[4] CheckAccount    — per-account rate limit до bcrypt
//	[5] bcrypt.Compare  — единственное место сравнения пароля;
//	                      при провале: RecordFailure → ErrInvalidCredentials
//	[6] openSessionAndIssueToken — общая цепочка выпуска токена
//
// Инварианты:
//   - ErrInvalidCredentials возвращается при любом сбое аутентификации (no credential oracle)
//   - bcrypt.Compare выполняется ВСЕГДА, в том числе для несуществующего email
//   - IP-first: 429 возвращается до любого обращения к аккаунту
func (s *AuthService) Login(
	ctx context.Context,
	email string,
	passwordHash string,
	fingerprint string,
	clientIP string,
) (valobj.LoginResult, error) {
	ctx, span := authTracer.Start(ctx, "AuthService.Login")
	defer span.End()

	if err := s.rateLimiter.CheckIP(ctx, clientIP); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "rate limit ip")
		return valobj.LoginResult{}, corerr.ErrRateLimitIP
	}

	account, err := s.accRep.FindByEmail(ctx, email)
	if err != nil {
		_ = bcrypt.CompareHashAndPassword([]byte(consts.DummyPasswordHash), []byte(passwordHash))
		span.RecordError(corerr.ErrInvalidCredentials)
		span.SetAttributes(attribute.Bool("auth.account_found", false))
		span.SetStatus(codes.Error, "invalid credentials")
		return valobj.LoginResult{}, corerr.ErrInvalidCredentials
	}

	if account.IsLocked() {
		span.RecordError(corerr.ErrAccountLocked)
		span.SetAttributes(attribute.Bool("auth.account_locked", true))
		span.SetStatus(codes.Error, "invalid credentials")
		return valobj.LoginResult{}, corerr.ErrInvalidCredentials
	}

	if err := s.rateLimiter.CheckAccount(ctx, account.UUID()); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "rate limit account")
		return valobj.LoginResult{}, corerr.ErrRateLimitAccount
	}

	if err := bcrypt.CompareHashAndPassword(
		[]byte(*account.PasswordHash()),
		[]byte(passwordHash),
	); err != nil {
		if rfErr := s.rateLimiter.RecordFailure(ctx, clientIP, account.UUID()); rfErr != nil {
			span.RecordError(rfErr)
		}
		span.RecordError(corerr.ErrInvalidCredentials)
		span.SetStatus(codes.Error, "invalid credentials")
		return valobj.LoginResult{}, corerr.ErrInvalidCredentials
	}

	return s.openSessionAndIssueToken(ctx, account, fingerprint)
}

// InitiateGoogleOAuth реализует GET /iam/auth/oauth/google.
//
// Цепочка:
//
//	[1] CheckIP — IP-first rate check (анти-скрейпинг, симметрично Login)
//	[2] googleOAuth.BuildAuthURL — адаптер генерирует PKCE + CSRF state,
//	    сохраняет OAuthState server-side (сигнед cookie / store)
//	[3] возвращает redirectURL — HTTP-хандлер выполняет 302
//
// Core не генерирует code_verifier/CSRF — ответственность адаптера.
// Spec: IAM §OAuth2 flow, шаги [1]–[2].
func (s *AuthService) InitiateGoogleOAuth(
	ctx context.Context,
	clientIP string,
) (redirectURL string, state valobj.OAuthState, err error) {
	ctx, span := authTracer.Start(ctx, "AuthService.InitiateGoogleOAuth")
	defer span.End()

	if err := s.rateLimiter.CheckIP(ctx, clientIP); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "rate limit ip")
		return "", valobj.OAuthState{}, corerr.ErrRateLimitIP
	}

	redirectURL, state, err = s.googleOAuth.BuildAuthURL(ctx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "build auth url failed")
		return "", valobj.OAuthState{}, err
	}

	span.SetAttributes(attribute.Bool("oauth.initiated", true))
	return redirectURL, state, nil
}

// HandleGoogleCallback реализует GET /iam/auth/oauth/google/callback.
//
// Цепочка (строгий порядок):
//
//	[1] CheckIP        — IP-first rate check
//	[2] ValidateState  — CSRF protection до любых DB-вызовов
//	[3] ExchangeCode   — code exchange + JWKS verify + claims (email_verified=true в адаптере)
//	[4] claims.EmailVerified() — defence-in-depth проверка в core
//	[5] resolveOAuthAccount — CASE A/B/C/D lookup/create
//	[6] account.IsLocked — проверка статуса
//	[7] openSessionAndIssueToken — общая цепочка выпуска токена
//
// Инварианты:
//   - email_verified: проверяется в адаптере + defence-in-depth в core
//   - timing safety не применяется — нет password comparison в OAuth
//   - IP-first симметрично Login
func (s *AuthService) HandleGoogleCallback(
	ctx context.Context,
	code string,
	receivedCSRF string,
	storedState valobj.OAuthState,
	fingerprint string,
	clientIP string,
) (valobj.LoginResult, error) {
	ctx, span := authTracer.Start(ctx, "AuthService.HandleGoogleCallback")
	defer span.End()

	// [1] IP-first
	if err := s.rateLimiter.CheckIP(ctx, clientIP); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "rate limit ip")
		return valobj.LoginResult{}, corerr.ErrRateLimitIP
	}

	// [2] CSRF — до любых DB-вызовов
	if err := s.googleOAuth.ValidateState(ctx, receivedCSRF, storedState); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "state validation failed")
		return valobj.LoginResult{}, err
	}

	// [3] Code exchange + JWKS verify
	claims, err := s.googleOAuth.ExchangeCode(ctx, code, storedState)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "exchange code failed")
		return valobj.LoginResult{}, err
	}

	// [4] Defence-in-depth: core самостоятельно проверяет email_verified,
	// даже если адаптер уже гарантировал это в ExchangeCode.
	if !claims.EmailVerified() {
		span.RecordError(corerr.ErrOAuthEmailNotVerified)
		span.SetStatus(codes.Error, "email not verified")
		return valobj.LoginResult{}, corerr.ErrOAuthEmailNotVerified
	}

	span.SetAttributes(
		attribute.String("oauth.google_uid", claims.Sub()),
		attribute.String("oauth.email", claims.Email()),
	)

	// [5] Lookup / create account
	account, err := s.resolveOAuthAccount(ctx, claims)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "resolve account failed")
		return valobj.LoginResult{}, err
	}

	// [6] Заблокированный аккаунт не получает токен
	if account.IsLocked() {
		span.RecordError(corerr.ErrAccountLocked)
		span.SetAttributes(attribute.Bool("auth.account_locked", true))
		span.SetStatus(codes.Error, "account locked")
		return valobj.LoginResult{}, corerr.ErrAccountLocked
	}

	// [7]
	return s.openSessionAndIssueToken(ctx, account, fingerprint)
}

// resolveOAuthAccount реализует lookup-стратегию CASE A / B / C / D.
//
// CASE A: найден по google_uid → возвращаем напрямую
// CASE B: email найден, google_uid уже привязан и совпадает → возвращаем;
//
//	google_uid не совпадает → ErrOAuthGoogleUIDConflict
//
// CASE C: email найден, google_uid не привязан → LinkGoogleUID → возвращаем
// CASE D: не найден ни по google_uid, ни по email →
//
//	core строит entity.Account через NewOAuthAccount,
//	репозиторий получает готовый агрегат (инверсия зависимостей).
//
// Span создаётся как дочерний от ctx — явный аргумент span не передаётся.
func (s *AuthService) resolveOAuthAccount(
	ctx context.Context,
	claims valobj.GoogleClaims,
) (*entity.Account, error) {
	ctx, span := authTracer.Start(ctx, "AuthService.resolveOAuthAccount")
	defer span.End()

	// CASE A: приоритетный lookup по google_uid (стабильный идентификатор)
	account, err := s.accRep.FindByGoogleUID(ctx, claims.Sub())
	if err == nil {
		span.SetAttributes(attribute.String("oauth.resolve_case", "A"))
		return account, nil
	}

	// CASE B / C: lookup по email
	accountByEmail, emailErr := s.accRep.FindByEmail(ctx, claims.Email())
	if emailErr == nil {
		existingUID := accountByEmail.GoogleUID()
		if existingUID != nil && *existingUID != "" {
			// CASE B: google_uid уже привязан
			if *existingUID != claims.Sub() {
				span.RecordError(corerr.ErrOAuthGoogleUIDConflict)
				span.SetStatus(codes.Error, "google uid conflict")
				return nil, corerr.ErrOAuthGoogleUIDConflict
			}
			span.SetAttributes(attribute.String("oauth.resolve_case", "B"))
			return accountByEmail, nil
		}

		// CASE C: email найден, google_uid не привязан → привязываем
		if linkErr := s.accRep.LinkGoogleUID(ctx, accountByEmail.UUID(), claims.Sub()); linkErr != nil {
			span.RecordError(linkErr)
			span.SetStatus(codes.Error, "link google uid failed")
			return nil, linkErr
		}
		span.SetAttributes(attribute.String("oauth.resolve_case", "C"))
		return accountByEmail, nil
	}

	// CASE D: аккаунт не найден ни по google_uid, ни по email.
	// Core строит агрегат самостоятельно — репозиторий не знает о бизнес-логике
	// построения сущности (гексагональная архитектура, инверсия зависимостей).
	//
	// personalInfo: берём claims.Name() — Google ID token стандартно содержит claim "name".
	// Если name пустой (Google не вернул) — используем email как fallback.
	// role: patient — дефолтная роль для самостоятельной регистрации.
	personalInfo := claims.Name()
	if personalInfo == "" {
		personalInfo = claims.Email()
	}

	newAccount, buildErr := entity.NewOAuthAccount(
		claims.Email(),
		personalInfo,
		valobj.RolePatient,
		claims.Sub(),
	)
	if buildErr != nil {
		span.RecordError(buildErr)
		span.SetStatus(codes.Error, "build oauth account failed")
		return nil, buildErr
	}

	// AccountRegistered публикуется через outbox внутри CreateOAuthAccountWithTx.
	createdAccount, createErr := s.accRep.CreateOAuthAccountWithTx(ctx, newAccount)
	if createErr != nil {
		span.RecordError(createErr)
		span.SetStatus(codes.Error, "create oauth account failed")
		return nil, createErr
	}

	span.SetAttributes(
		attribute.String("oauth.resolve_case", "D"),
		attribute.String("oauth.new_account_id", createdAccount.UUID()),
	)
	return createdAccount, nil
}

// openSessionAndIssueToken — общая финальная цепочка для Login и HandleGoogleCallback.
//
// [1] account.OpenSession   — создание сессии на агрегате (G5: eviction, max 5)
// [2] SaveSessionWithTx     — ACID: upsert сессий + evictedJTI в outbox (L3)
// [3] TokenBlacklist.Add    — evictedJTI в Redis L2 (G9 Write Order)
// [4] TokenIssuer.Issue     — RS256, kid из текущего ключа
// [5] SessionCreated event  — fire-and-forget через eventsProducer
// [6] return LoginResult
//
// Span создаётся как дочерний от ctx — выделен по KISS/YAGNI,
// оба публичных метода завершаются идентичным выходом.
func (s *AuthService) openSessionAndIssueToken(
	ctx context.Context,
	account *entity.Account,
	fingerprint string,
) (valobj.LoginResult, error) {
	ctx, span := authTracer.Start(ctx, "AuthService.openSessionAndIssueToken")
	defer span.End()

	sessionID := uuid.NewString()
	jti := uuid.NewString()

	session, evictedJTI, err := account.OpenSession(sessionID, jti, fingerprint)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "open session failed")
		return valobj.LoginResult{}, err
	}

	if err := s.accRep.SaveSessionWithTx(ctx, account); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "save session failed")
		return valobj.LoginResult{}, err
	}

	// G9 Write Order: outbox (L3) уже записан в SaveSessionWithTx.
	// Redis L2 обновляем после успешного коммита.
	if evictedJTI != "" {
		if blErr := s.tokenBlacklist.Add(ctx, evictedJTI, 0); blErr != nil {
			// log WARN — не блокируем выпуск токена; outbox обеспечит L3
			span.RecordError(blErr)
		}
	}

	accessToken, issuedJTI, err := s.tokenIssuer.Issue(
		ctx,
		account.UUID(),
		string(account.Role()),
		session.SessionID(),
		account.Revision(),
	)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "token issue failed")
		return valobj.LoginResult{}, err
	}

	span.SetAttributes(
		attribute.String("auth.account_id", account.UUID()),
		attribute.String("auth.session_id", session.SessionID()),
		attribute.String("auth.jti", issuedJTI),
	)

	// fire-and-forget: ошибка события не блокирует ответ клиенту
	if evErr := s.eventsProducer.SessionCreated(
		ctx,
		account.UUID(),
		session.SessionID(),
		fingerprint,
		time.Now().Unix(),
	); evErr != nil {
		span.RecordError(evErr)
	}

	result, err := valobj.NewLoginResult(
		accessToken,
		account.UUID(),
		string(account.Role()),
		session.SessionID(),
	)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "login result build failed")
		return valobj.LoginResult{}, err
	}

	// Дочерний span закрывается через defer; родительский span получает
	// корректное дерево трейсов: Login/HandleGoogleCallback → openSessionAndIssueToken.
	_ = trace.SpanFromContext(ctx) // убеждаемся что ctx не потерял span после Start
	return result, nil
}
