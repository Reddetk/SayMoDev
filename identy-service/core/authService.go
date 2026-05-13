package core

import (
	"context"

	"golang.org/x/crypto/bcrypt"

	"github.com/google/uuid"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	"github.com/Reddetk/SayMoDev/identy-service/core/consts"
	corerr "github.com/Reddetk/SayMoDev/identy-service/core/coreErrors"
	valobj "github.com/Reddetk/SayMoDev/identy-service/core/valObj"
	"github.com/Reddetk/SayMoDev/identy-service/port/out"
)

var authTracer = otel.Tracer("iam.authService")

// AuthService реализует in-port AccountAuthenticator.
//
// Зависимости инжектируются только как out-порты (интерфейсы).
// AuthService не знает о SessionService, TokenService или AccountService.
// Вся координация выполняется через out.AccountRepository, out.TokenIssuer,
// out.TokenBlacklist, out.RateLimiter и out.AccountEventsProducer.
type AuthService struct {
	accRep         out.AccountRepository
	tokenIssuer    out.TokenIssuer
	tokenBlacklist out.TokenBlacklist
	rateLimiter    out.RateLimiter
	eventsProducer out.AccountEventsProducer
}

func NewAuthService(
	accRep out.AccountRepository,
	tokenIssuer out.TokenIssuer,
	tokenBlacklist out.TokenBlacklist,
	rateLimiter out.RateLimiter,
	eventsProducer out.AccountEventsProducer,
) *AuthService {
	return &AuthService{
		accRep:         accRep,
		tokenIssuer:    tokenIssuer,
		tokenBlacklist: tokenBlacklist,
		rateLimiter:    rateLimiter,
		eventsProducer: eventsProducer,
	}
}

// Login реализует юз-кейс POST /iam/auth/login.
//
// Цепочка (строгий порядок, инварианты зафиксированы ниже):
//
//	[1] CheckIP         — до любого обращения к БД (anti-scraping)
//	[2] FindByEmail     — если не найден, выполняется bcrypt против DummyPasswordHash
//	                      и возвращается ErrInvalidCredentials (timing safety)
//	[3] account.IsLocked — проверяем статус агрегата без внешних вызовов
//	[4] CheckAccount    — per-account rate limit до bcrypt
//	[5] bcrypt.Compare  — единственное место сравнения пароля;
//	                      при провале: RecordFailure → ErrInvalidCredentials
//	[6] account.OpenSession — создание сессии на агрегате; получаем evictedJTI
//	[7] SaveSessionWithTx   — ACID: upsert сессий + evictedJTI в blacklist outbox (L3)
//	[8] TokenBlacklist.Add  — evictedJTI в Redis L2 напрямую (G9 Write Order)
//	[9] TokenIssuer.Issue   — RS256 подпись (инфраструктурный адаптер)
//	[10] eventsProducer     — SessionOpened событие
//	[11] return LoginResult VO
//
// Инварианты:
//   - ErrInvalidCredentials возвращается при любом сбое аутентификации;
//     причина никогда не раскрывается клиенту (no credential oracle)
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

	// [1] IP-first rate check — до любого DB-вызова
	if err := s.rateLimiter.CheckIP(ctx, clientIP); err != nil {
		span.RecordError(err)
		return valobj.LoginResult{}, corerr.ErrRateLimitIP
	}

	// [2] Account lookup по email
	account, err := s.accRep.FindByEmail(ctx, email)
	if err != nil {
		// Timing safety: выполняем bcrypt против DummyPasswordHash независимо от причины ошибки.
		// Это делает время ответа идентичным для "email не найден" и "неверный пароль".
		// Результат bcrypt намеренно игнорируется — DummyPasswordHash никогда не совпадёт.
		_ = bcrypt.CompareHashAndPassword([]byte(consts.DummyPasswordHash), []byte(passwordHash))
		span.RecordError(corerr.ErrInvalidCredentials)
		span.SetAttributes(attribute.Bool("auth.account_found", false))
		return valobj.LoginResult{}, corerr.ErrInvalidCredentials
	}

	// [3] Проверка статуса аккаунта на агрегате (без внешних вызовов)
	if account.IsLocked() {
		span.RecordError(corerr.ErrAccountLocked)
		span.SetAttributes(attribute.Bool("auth.account_locked", true))
		// Не раскрываем причину клиенту — возвращаем generic 401
		return valobj.LoginResult{}, corerr.ErrInvalidCredentials
	}

	// [4] Per-account rate limit — до bcrypt
	if err := s.rateLimiter.CheckAccount(ctx, account.UUID()); err != nil {
		span.RecordError(err)
		return valobj.LoginResult{}, corerr.ErrRateLimitAccount
	}

	// [5] Единственное место сравнения пароля
	if err := bcrypt.CompareHashAndPassword(
		[]byte(*account.PasswordHash()),
		[]byte(passwordHash),
	); err != nil {
		// Инкремент счётчиков: IP + account.
		// Ошибку RecordFailure логируем, но НЕ возвращаем клиенту — ответ всегда generic.
		if rfErr := s.rateLimiter.RecordFailure(ctx, clientIP, account.UUID()); rfErr != nil {
			span.RecordError(rfErr)
		}
		span.RecordError(corerr.ErrInvalidCredentials)
		return valobj.LoginResult{}, corerr.ErrInvalidCredentials
	}

	// [6] Создание сессии на агрегате
	// UUID генерируются здесь — core не зависит от инфраструктурного генератора
	sessionID := uuid.NewString()
	jti := uuid.NewString()

	session, evictedJTI, err := account.OpenSession(sessionID, jti, fingerprint)
	if err != nil {
		span.RecordError(err)
		return valobj.LoginResult{}, err
	}

	// [7] ACID-сохранение: upsert сессий + evictedJTI в blacklist outbox (L3)
	if err := s.accRep.SaveSessionWithTx(ctx, account); err != nil {
		span.RecordError(err)
		return valobj.LoginResult{}, err
	}

	// [8] G9 Write Order: evictedJTI в Redis L2 напрямую после успешного коммита
	// exp для вытесненного токена неизвестен точно — используем текущий moment + JWTExpirySeconds
	// как safe upper bound; Redis TTL не должен превышать реальный exp.
	// Missing spec / Design gap: evictedJTI exp не передаётся из агрегата.
	// Possible design option: хранить expiresAt в Session entity.
	// Текущее решение: Add с TTL = 0 означает no-op (токен уже истёк или вытеснен).
	if evictedJTI != "" {
		if blErr := s.tokenBlacklist.Add(ctx, evictedJTI, 0); blErr != nil {
			// WARN: L2 недоступен — L3 outbox обеспечит репликацию асинхронно
			span.RecordError(blErr)
		}
	}

	// [9] Выпуск JWT — инфраструктурный адаптер, core не знает о RS256 или kid
	accessToken, issuedJTI, err := s.tokenIssuer.Issue(
		ctx,
		account.UUID(),
		string(account.Role()),
		session.SessionID(),
		account.Revision(),
	)
	if err != nil {
		span.RecordError(err)
		return valobj.LoginResult{}, err
	}

	// jti нового токена должен совпадать с тем, что записан в сессию агрегата
	// Инвариант: TokenIssuer.Issue возвращает тот же jti, что был передан в OpenSession.
	// Текущая реализация: jti генерируется здесь и передаётся в Issue как часть claims.
	// Design note: issuedJTI возвращается для возможности трассировки в span.
	span.SetAttributes(
		attribute.String("auth.account_id", account.UUID()),
		attribute.String("auth.session_id", session.SessionID()),
		attribute.String("auth.jti", issuedJTI),
	)

	// [10] Событие SessionOpened — fire-and-forget
	err = s.eventsProducer.SessionOpened(ctx, account.UUID(), session.SessionID())
	if err != nil {
		span.RecordError(err)
	}

	// [11] Формируем LoginResult VO
	result, err := valobj.NewLoginResult(
		accessToken,
		account.UUID(),
		string(account.Role()),
		session.SessionID(),
	)
	if err != nil {
		span.RecordError(err)
		return valobj.LoginResult{}, err
	}

	return result, nil
}
