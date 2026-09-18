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

	"github.com/Reddetk/SayMoDev/identy-service/internal/core/consts"
	corerr "github.com/Reddetk/SayMoDev/identy-service/internal/core/coreErrors"
	"github.com/Reddetk/SayMoDev/identy-service/internal/core/entity"
	valobj "github.com/Reddetk/SayMoDev/identy-service/internal/core/valObj"
	"github.com/Reddetk/SayMoDev/identy-service/internal/logger"
	"github.com/Reddetk/SayMoDev/identy-service/internal/port/in"
	"github.com/Reddetk/SayMoDev/identy-service/internal/port/out"
)

var authTracer = otel.Tracer("iam.authService")

type AuthService struct {
	accRep         out.AccountRepository
	tokenIssuer    out.TokenIssuer
	tokenBlacklist out.TokenBlacklist
	rateLimiter    out.RateLimiter
	eventsProducer out.AccountEventsProducer
	googleOAuth    out.GoogleOAuthProvider
	logger         logger.Logger
}

func NewAuthService(
	accRep out.AccountRepository,
	tokenIssuer out.TokenIssuer,
	tokenBlacklist out.TokenBlacklist,
	rateLimiter out.RateLimiter,
	eventsProducer out.AccountEventsProducer,
	googleOAuth out.GoogleOAuthProvider,
	log logger.Logger,
) *AuthService {
	return &AuthService{
		accRep:         accRep,
		tokenIssuer:    tokenIssuer,
		tokenBlacklist: tokenBlacklist,
		rateLimiter:    rateLimiter,
		eventsProducer: eventsProducer,
		googleOAuth:    googleOAuth,
		logger:         log,
	}
}

func (s *AuthService) Login(
	ctx context.Context,
	email string,
	password string,
	fingerprint string,
	clientIP string,
) (in.LoginResult, error) {
	ctx, span := authTracer.Start(ctx, "AuthService.Login")
	defer span.End()

	log := s.logger.With(logger.String("op", "login"), logger.String("ip", clientIP))

	if err := s.rateLimiter.CheckIP(ctx, clientIP); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "rate limit ip")
		log.Warn("auth.login: IP rate limit exceeded")
		return in.LoginResult{}, corerr.ErrRateLimitIP
	}

	account, err := s.accRep.FindByEmail(ctx, email)
	if err != nil {
		_ = bcrypt.CompareHashAndPassword([]byte(consts.DummyPasswordHash), []byte(password))
		span.RecordError(corerr.ErrInvalidCredentials)
		span.SetAttributes(attribute.Bool("auth.account_found", false))
		span.SetStatus(codes.Error, "invalid credentials")
		log.Debug("auth.login: account not found (timing-safe dummy bcrypt executed)")
		return in.LoginResult{}, corerr.ErrInvalidCredentials
	}

	if account.IsLocked() {
		span.RecordError(corerr.ErrAccountLocked)
		span.SetAttributes(attribute.Bool("auth.account_locked", true))
		span.SetStatus(codes.Error, "invalid credentials")
		log.Warn("auth.login: account is locked", logger.String("account_id", account.UUID()))
		return in.LoginResult{}, corerr.ErrInvalidCredentials
	}

	if err := s.rateLimiter.CheckAccount(ctx, account.UUID()); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "rate limit account")
		log.Warn("auth.login: account rate limit exceeded", logger.String("account_id", account.UUID()))
		return in.LoginResult{}, corerr.ErrRateLimitAccount
	}

	if err := bcrypt.CompareHashAndPassword(
		[]byte(*account.PasswordHash()),
		[]byte(password),
	); err != nil {
		if rfErr := s.rateLimiter.RecordFailure(ctx, clientIP, account.UUID()); rfErr != nil {
			span.RecordError(rfErr)
			log.Warn("auth.login: RecordFailure failed", logger.Error(rfErr))
		}
		span.RecordError(corerr.ErrInvalidCredentials)
		span.SetStatus(codes.Error, "invalid credentials")
		log.Debug("auth.login: bcrypt mismatch", logger.String("account_id", account.UUID()), logger.String("password", password))
		return in.LoginResult{}, corerr.ErrInvalidCredentials
	}

	return s.openSessionAndIssueToken(ctx, account, fingerprint)
}

func (s *AuthService) InitiateGoogleOAuth(
	ctx context.Context,
	clientIP string,
) (redirectURL string, state in.OAuthState, err error) {
	ctx, span := authTracer.Start(ctx, "AuthService.InitiateGoogleOAuth")
	defer span.End()

	log := s.logger.With(logger.String("op", "oauthInitiate"), logger.String("ip", clientIP))

	if err := s.rateLimiter.CheckIP(ctx, clientIP); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "rate limit ip")
		log.Warn("auth.oauthInitiate: IP rate limit exceeded")
		return "", in.OAuthState{}, corerr.ErrRateLimitIP
	}

	redirectURL, stateA, err := s.googleOAuth.BuildAuthURL(ctx)
	state = stateA.MapToDTO()
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "build auth url failed")
		log.Error("auth.oauthInitiate: BuildAuthURL failed", logger.Error(err))
		return "", in.OAuthState{}, err
	}

	span.SetAttributes(attribute.Bool("oauth.initiated", true))
	log.Debug("auth.oauthInitiate: redirect URL built")
	return redirectURL, state, nil
}

func (s *AuthService) HandleGoogleCallback(
	ctx context.Context,
	code string,
	receivedCSRF string,
	storedState in.OAuthState,
	fingerprint string,
	clientIP string,
) (in.LoginResult, error) {
	ctx, span := authTracer.Start(ctx, "AuthService.HandleGoogleCallback")
	defer span.End()

	log := s.logger.With(logger.String("op", "oauthCallback"), logger.String("ip", clientIP))

	if err := s.rateLimiter.CheckIP(ctx, clientIP); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "rate limit ip")
		log.Warn("auth.oauthCallback: IP rate limit exceeded")
		return in.LoginResult{}, corerr.ErrRateLimitIP
	}

	storedStateVO, err := valobj.DTOtoOAuthState(storedState)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "invalid stored state")
		log.Warn("auth.oauthCallback: DTOtoOAuthState failed", logger.Error(err))
		return in.LoginResult{}, err
	}

	if err := s.googleOAuth.ValidateState(ctx, receivedCSRF, storedStateVO); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "state validation failed")
		log.Warn("auth.oauthCallback: CSRF state validation failed", logger.Error(err))
		return in.LoginResult{}, err
	}

	claims, err := s.googleOAuth.ExchangeCode(ctx, code, storedStateVO)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "exchange code failed")
		log.Error("auth.oauthCallback: ExchangeCode failed", logger.Error(err))
		return in.LoginResult{}, err
	}

	if !claims.EmailVerified() {
		span.RecordError(corerr.ErrOAuthEmailNotVerified)
		span.SetStatus(codes.Error, "email not verified")
		log.Warn("auth.oauthCallback: email not verified by Google")
		return in.LoginResult{}, corerr.ErrOAuthEmailNotVerified
	}

	span.SetAttributes(
		attribute.String("oauth.google_uid", claims.Sub()),
		attribute.String("oauth.email", claims.Email()),
	)

	account, err := s.resolveOAuthAccount(ctx, claims)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "resolve account failed")
		log.Error("auth.oauthCallback: resolveOAuthAccount failed", logger.Error(err))
		return in.LoginResult{}, err
	}

	if account.IsLocked() {
		span.RecordError(corerr.ErrAccountLocked)
		span.SetAttributes(attribute.Bool("auth.account_locked", true))
		span.SetStatus(codes.Error, "account locked")
		log.Warn("auth.oauthCallback: account is locked", logger.String("account_id", account.UUID()))
		return in.LoginResult{}, corerr.ErrAccountLocked
	}

	return s.openSessionAndIssueToken(ctx, account, fingerprint)
}

func (s *AuthService) resolveOAuthAccount(
	ctx context.Context,
	claims valobj.GoogleClaims,
) (*entity.Account, error) {
	ctx, span := authTracer.Start(ctx, "AuthService.resolveOAuthAccount")
	defer span.End()

	log := s.logger.With(logger.String("op", "resolveOAuthAccount"), logger.String("google_uid", claims.Sub()))

	account, err := s.accRep.FindByGoogleUID(ctx, claims.Sub())
	if err == nil {
		span.SetAttributes(attribute.String("oauth.resolve_case", "A"))
		log.Debug("auth.resolveOAuth: case A  found by google_uid")
		return account, nil
	}

	accountByEmail, emailErr := s.accRep.FindByEmail(ctx, claims.Email())
	if emailErr == nil {
		existingUID := accountByEmail.GoogleUID()
		if existingUID != nil && *existingUID != "" {
			if *existingUID != claims.Sub() {
				span.RecordError(corerr.ErrOAuthGoogleUIDConflict)
				span.SetStatus(codes.Error, "google uid conflict")
				log.Warn("auth.resolveOAuth: case B  google_uid conflict",
					logger.String("account_id", accountByEmail.UUID()),
				)
				return nil, corerr.ErrOAuthGoogleUIDConflict
			}
			span.SetAttributes(attribute.String("oauth.resolve_case", "B"))
			log.Debug("auth.resolveOAuth: case B  matched by email+google_uid")
			return accountByEmail, nil
		}

		if linkErr := s.accRep.LinkGoogleUID(ctx, accountByEmail.UUID(), claims.Sub()); linkErr != nil {
			span.RecordError(linkErr)
			span.SetStatus(codes.Error, "link google uid failed")
			log.Error("auth.resolveOAuth: case C  LinkGoogleUID failed",
				logger.String("account_id", accountByEmail.UUID()),
				logger.Error(linkErr),
			)
			return nil, linkErr
		}
		span.SetAttributes(attribute.String("oauth.resolve_case", "C"))
		log.Debug("auth.resolveOAuth: case C  linked google_uid to existing account",
			logger.String("account_id", accountByEmail.UUID()),
		)
		return accountByEmail, nil
	}

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
		log.Error("auth.resolveOAuth: case D  NewOAuthAccount failed", logger.Error(buildErr))
		return nil, buildErr
	}

	createdAccount, createErr := s.accRep.CreateOAuthAccountWithTx(ctx, newAccount)
	if createErr != nil {
		span.RecordError(createErr)
		span.SetStatus(codes.Error, "create oauth account failed")
		log.Error("auth.resolveOAuth: case D  CreateOAuthAccountWithTx failed", logger.Error(createErr))
		return nil, createErr
	}

	span.SetAttributes(
		attribute.String("oauth.resolve_case", "D"),
		attribute.String("oauth.new_account_id", createdAccount.UUID()),
	)
	log.Info("auth.resolveOAuth: case D  new OAuth account created",
		logger.String("account_id", createdAccount.UUID()),
	)
	return createdAccount, nil
}

func (s *AuthService) openSessionAndIssueToken(
	ctx context.Context,
	account *entity.Account,
	fingerprint string,
) (in.LoginResult, error) {
	ctx, span := authTracer.Start(ctx, "AuthService.openSessionAndIssueToken")
	defer span.End()

	log := s.logger.With(
		logger.String("op", "openSession"),
		logger.String("account_id", account.UUID()),
	)

	sessionID := uuid.NewString()
	jti := uuid.NewString()

	session, evictedJTI, err := account.OpenSession(sessionID, jti, fingerprint)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "open session failed")
		log.Error("auth.openSession: OpenSession failed", logger.Error(err))
		return in.LoginResult{}, err
	}

	if err := s.accRep.SaveSessionWithTx(ctx, account, evictedJTI); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "save session failed")
		log.Error("auth.openSession: SaveSessionWithTx failed", logger.Error(err))
		return in.LoginResult{}, err
	}

	if evictedJTI != "" {
		if blErr := s.tokenBlacklist.Add(ctx, evictedJTI, 0); blErr != nil {
			span.RecordError(blErr)
			log.Warn("auth.openSession: blacklist L2 Add failed for evicted JTI (outbox will retry)",
				logger.String("evicted_jti", evictedJTI),
				logger.Error(blErr),
			)
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
		log.Error("auth.openSession: TokenIssuer.Issue failed", logger.Error(err))
		return in.LoginResult{}, err
	}

	span.SetAttributes(
		attribute.String("auth.account_id", account.UUID()),
		attribute.String("auth.session_id", session.SessionID()),
		attribute.String("auth.jti", issuedJTI),
	)

	if evErr := s.eventsProducer.SessionCreated(
		ctx,
		account.UUID(),
		session.SessionID(),
		fingerprint,
		time.Now().UnixMilli(),
	); evErr != nil {
		span.RecordError(evErr)
		log.Warn("auth.openSession: SessionCreated event failed (non-fatal)", logger.Error(evErr))
	}

	log.Info("auth.openSession: session opened and token issued",
		logger.String("session_id", session.SessionID()),
	)

	_ = trace.SpanFromContext(ctx)
	return in.LoginResult{
		AccessToken: accessToken,
		AccountID:   account.UUID(),
		Role:        account.Role().String(),
		SessionID:   session.SessionID(),
	}, nil
}
