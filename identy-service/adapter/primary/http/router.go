// Package http -- primary adapter: Gin HTTP router для BC#1.
//
// Маршрутизация соответствует спецификации endpoints.md (BC#1).
//
// Группы маршрутов:
//   - public:    без JWT (JWKS, login, register, password-reset, OAuth)
//   - protected: JWT required (logout, account CRUD, sessions, password change, lock/unlock)
//
// Middleware порядок выполнения на каждом маршруте:
//   CORSMiddleware -> gin.Recovery() -> JWTMiddleware -> [OwnershipOrAdmin | RequireRole] -> handler
//
// CORSMiddleware регистрируется первым: preflight OPTIONS должен получить
// ответ до того как JWTMiddleware потребует Authorization-заголовок.
//
// Gate-контроль (кто может достучаться до handler) -- только в middleware.
// Бизнес-правила (что именно разрешено делать) -- только в handler.
package http

import (
	"github.com/gin-gonic/gin"

	"github.com/Reddetk/SayMoDev/identy-service/adapter/primary/http/middleware"
	inport "github.com/Reddetk/SayMoDev/identy-service/port/in"
)

// RouterDeps -- зависимости роутера.
// Все поля -- in-порты; router не знает о реализациях.
// CORS инжектируется из cmd при запуске сервиса.
type RouterDeps struct {
	CORS             middleware.CORSConfig
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

	// CORSMiddleware -- первым: preflight OPTIONS не должен доходить до JWT-валидации.
	router.Use(middleware.NewCORSMiddleware(deps.CORS))
	router.Use(gin.Recovery())

	jwtMW := middleware.NewJWTMiddleware(deps.TokenValidator)

	// --- Public endpoints (no JWT) ---
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

	// --- Protected endpoints (JWT required) ---
	protected := router.Group("/", jwtMW.Handle())
	{
		protected.POST("/iam/auth/logout", handleLogout(deps.SessionOperator, deps.TokenOperator))

		// Группа /iam/accounts/:accountId
		// OwnershipOrAdmin: пациент видит только свои данные (404 на чужой accountId);
		// administrator -- любой. Spec §Token Validation Flow Step 4 (IDOR prevention).
		accounts := protected.Group("/iam/accounts/:accountId", middleware.OwnershipOrAdmin())
		{
			accounts.GET("", handleGetAccount(deps.AccountOpertator))
			accounts.PATCH("", handlePatchAccount(deps.AccountOpertator))
			// DELETE: пациент не может удалить даже свой аккаунт -- только administrator.
			accounts.DELETE("", middleware.RequireRole(middleware.RoleAdministrator), handleDeleteAccount(deps.AccountOpertator))

			accounts.GET("/sessions", handleListSessions(deps.SessionOperator))
			accounts.DELETE("/sessions/:sessionId", handleTerminateSession(deps.SessionOperator))

			// handleChangePassword requires both AccountOperator (currentPassword verification)
			// and PasswordOperator (use case execution).
			accounts.POST("/password", handleChangePassword(deps.AccountOpertator, deps.PasswordOperator))

			// lock/unlock: только administrator.
			accounts.POST("/lock", middleware.RequireRole(middleware.RoleAdministrator), handleLockAccount(deps.AccountOpertator))
			accounts.POST("/unlock", middleware.RequireRole(middleware.RoleAdministrator), handleUnlockAccount(deps.AccountOpertator))
		}
	}

	return router
}
