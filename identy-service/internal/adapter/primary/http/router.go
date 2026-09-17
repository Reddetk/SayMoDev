// Package http  primary adapter: Gin HTTP router для BC#1.
//
// Маршрутизация соответствует спецификации endpoints.md (BC#1).
//
// Группы маршрутов:
//   - public:    без JWT (JWKS, login, register, password-reset, OAuth)
//   - protected: JWT required (logout, account CRUD, sessions, password change, lock/unlock)
//
// Middleware порядок выполнения на каждом маршруте:
//
//	CORSMiddleware -> ObservabilityMiddleware -> gin.Recovery() -> JWTMiddleware -> [OwnershipMiddleware | RBACMiddleware] -> handler
//
// CORSMiddleware регистрируется первым: preflight OPTIONS не должен доходить до JWT-валидации.
//
// ObservabilityMiddleware регистрируется вторым: извлекает W3C traceparent,
// создаёт root span "gateway.request.total", пишет ZAP request-completion log.
// Запускается ДО gin.Recovery() чтобы span закрылся даже при панике.
//
// Gate-контроль (кто может достучься до handler)  только в middleware.
// Бизнес-правила (что именно разрешено делать)  только в handler.
package http

import (
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/Reddetk/SayMoDev/identy-service/internal/adapter/primary/http/middleware"
	"github.com/Reddetk/SayMoDev/identy-service/internal/logger"
	inport "github.com/Reddetk/SayMoDev/identy-service/internal/port/in"
)

// RouterDeps  зависимости роутера.
// Все поля  in-порты; router не знает о реализациях.
// CORS, Logger и Metrics инжектируются из cmd при запуске сервиса.
type RouterDeps struct {
	CORS             middleware.CORSConfig
	Logger           logger.Logger         // ZAP logger; используется ObservabilityMiddleware, JWTMiddleware, RBACMiddleware, OwnershipMiddleware
	Metrics          prometheus.Registerer // Prometheus registerer; используется ObservabilityMiddleware
	TokenValidator   inport.TokenValidator
	Authenticator    inport.AccountAuthenticator
	Registrator      inport.AccountRegistrator
	SessionOperator  inport.SessionOperator
	TokenOperator    inport.TokenOperator
	AccountOpertator inport.AccountOperator
	PasswordOperator inport.PasswordOperator
	OTPIssuer        inport.OTPIssuer
}

// NewGinRouter строит *gin.Engine с полным набором маршрутов BC#1.
func NewGinRouter(deps RouterDeps) *gin.Engine {
	router := gin.New()

	// 1. CORSMiddleware  первым: preflight OPTIONS не должен доходить до JWT-валидации.
	router.Use(middleware.NewCORSMiddleware(deps.CORS))

	// 2. ObservabilityMiddleware  извлекает/создаёт trace context, логирует запрос,
	//    кладёт per-request logger с trace_id/span_id в gin.Context[ContextKeyLogger].
	//    Регистрируется ДО gin.Recovery() чтобы span.End() вызвался даже при панике handler-а.
	router.Use(middleware.NewObservabilityMiddleware(middleware.ObservabilityDeps{
		Logger:     deps.Logger,
		Registerer: deps.Metrics,
		TracerName: "identity-service",
	}))

	// 3. gin.Recovery()  перехватывает паники после того как observability span открыт.
	router.Use(gin.Recovery())

	// Middleware создаются через конструкторы  логер инжектируется один раз,
	// per-request поля берутся из gin.Context[ContextKeyLogger] внутри Handle().
	jwtMW := middleware.NewJWTMiddleware(deps.TokenValidator, deps.Logger)
	rbacMW := middleware.NewRBACMiddleware(deps.Logger)
	ownershipMW := middleware.NewOwnershipMiddleware(deps.Logger)

	//  Public endpoints (no JWT) -
	public := router.Group("/")
	{
		public.GET("iam/.well-known/jwks.json", handleGetJWKS(deps.TokenOperator))

		auth := public.Group("/iam/auth")
		{
			auth.POST("/register/verify", handleRegisterVerify(deps.OTPIssuer))
			auth.POST("/register", handleRegister(deps.Registrator))
			auth.POST("/login", handleLogin(deps.Authenticator))
			auth.POST("/password-reset", handlePasswordResetRequest(deps.OTPIssuer))
			auth.POST("/password-reset/confirm", handlePasswordResetConfirm(deps.PasswordOperator))
			auth.GET("/oauth/google", handleOAuthGoogleInitiate(deps.Authenticator))
			auth.GET("/oauth/google/callback", handleOAuthGoogleCallback(deps.Authenticator))
		}
	}

	//  Protected endpoints (JWT required) -
	protected := router.Group("/", jwtMW.Handle())
	{
		protected.POST("/iam/auth/logout", handleLogout(deps.SessionOperator, deps.TokenOperator))

		// Группа /iam/accounts/:accountId
		// OwnershipMiddleware: пациент видит только свои данные (404 на чужой accountId);
		// administrator  любой. Spec §Token Validation Flow Step 4 (IDOR prevention).
		accounts := protected.Group("/iam/accounts/:accountId", ownershipMW.Handle())
		{
			accounts.GET("", handleGetAccount(deps.AccountOpertator))
			accounts.PATCH("", handlePatchAccount(deps.AccountOpertator))
			// DELETE: пациент не может удалить даже свой аккаунт  только administrator.
			accounts.DELETE("", rbacMW.RequireRole(middleware.RoleAdministrator), handleDeleteAccount(deps.AccountOpertator))

			accounts.GET("/sessions", handleListSessions(deps.SessionOperator))
			accounts.DELETE("/sessions/:sessionId", handleTerminateSession(deps.SessionOperator))

			accounts.POST("/password", handleChangePassword(deps.AccountOpertator, deps.PasswordOperator))

			// lock/unlock: только administrator.
			accounts.POST("/lock", rbacMW.RequireRole(middleware.RoleAdministrator), handleLockAccount(deps.AccountOpertator))
			accounts.POST("/unlock", rbacMW.RequireRole(middleware.RoleAdministrator), handleUnlockAccount(deps.AccountOpertator))
		}
	}

	return router
}
