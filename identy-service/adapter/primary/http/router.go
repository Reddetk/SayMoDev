// Package http -- primary adapter: Gin HTTP router для BC#1.
//
// Маршрутизация соответствует спецификации endpoints.md (BC#1).
//
// Группы маршрутов:
//   - public:    без JWT (JWKS, login, register, password-reset, OAuth)
//   - protected: JWT required (logout, account CRUD, sessions, password change, lock/unlock)
//
// Middleware порядок выполнения на protected-маршрутах:
//   gin.Recovery() -> JWTMiddleware -> [OwnershipOrAdmin | RequireRole] -> handler
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
type RouterDeps struct {
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
	router.Use(gin.Recovery())

	jwtMW := middleware.NewJWTMiddleware(deps.TokenValidator)

	// --- Public endpoints (no JWT) ---
	public := router.Group("/")
	{
		// GET /iam/.well-known/jwks.json
		// Cache-Control: max-age=3600, public
		public.GET("iam/.well-known/jwks.json", handleGetJWKS(deps.TokenOperator))

		auth := public.Group("/iam/auth")
		{
			// POST /iam/auth/register/verify -- отправка OTP на email
			auth.POST("/register/verify", handleRegisterVerify(deps.OTPIssuer))
			// POST /iam/auth/register
			auth.POST("/register", handleRegister(deps.Registrator))
			// POST /iam/auth/login
			auth.POST("/login", handleLogin(deps.Authenticator))
			// POST /iam/auth/password-reset
			auth.POST("/password-reset", handlePasswordResetRequest(deps.OTPIssuer))
			// POST /iam/auth/password-reset/confirm
			auth.POST("/password-reset/confirm", handlePasswordResetConfirm(deps.PasswordOperator))
			// GET /iam/auth/oauth/google
			auth.GET("/oauth/google", handleOAuthGoogleInitiate(deps.Authenticator))
			// GET /iam/auth/oauth/google/callback
			auth.GET("/oauth/google/callback", handleOAuthGoogleCallback(deps.Authenticator))
		}
	}

	// --- Protected endpoints (JWT required) ---
	protected := router.Group("/", jwtMW.Handle())
	{
		// POST /iam/auth/logout
		protected.POST("/iam/auth/logout", handleLogout(deps.SessionOperator, deps.TokenOperator))

		// Группа /iam/accounts/:accountId
		// OwnershipOrAdmin: пациент видит только свои данные (404 на чужой accountId);
		// administrator -- любой. Spec §Token Validation Flow Step 4 (IDOR prevention).
		accounts := protected.Group("/iam/accounts/:accountId", middleware.OwnershipOrAdmin())
		{
			// GET  /iam/accounts/:accountId
			accounts.GET("", handleGetAccount(deps.AccountOpertator))
			// PATCH /iam/accounts/:accountId
			accounts.PATCH("", handlePatchAccount(deps.AccountOpertator))
			// DELETE /iam/accounts/:accountId
			// RequireRole: пациент не может удалить даже свой аккаунт.
			accounts.DELETE("", middleware.RequireRole(inport.RoleAdministrator), handleDeleteAccount(deps.AccountOpertator))

			// GET    /iam/accounts/:accountId/sessions
			accounts.GET("/sessions", handleListSessions(deps.SessionOperator))
			// DELETE /iam/accounts/:accountId/sessions/:sessionId
			accounts.DELETE("/sessions/:sessionId", handleTerminateSession(deps.SessionOperator))

			// POST /iam/accounts/:accountId/password
			accounts.POST("/password", handleChangePassword(deps.PasswordOperator))

			// POST /iam/accounts/:accountId/lock
			// Spec §Lock Semantics §6 + §RBAC: requires role=administrator
			accounts.POST("/lock", middleware.RequireRole(inport.RoleAdministrator), handleLockAccount(deps.AccountOpertator))

			// POST /iam/accounts/:accountId/unlock
			// Spec §Account Lock / Unlock (admin): requires role=administrator
			accounts.POST("/unlock", middleware.RequireRole(inport.RoleAdministrator), handleUnlockAccount(deps.AccountOpertator))
		}
	}

	return router
}
