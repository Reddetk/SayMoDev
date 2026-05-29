package http

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	corerr "github.com/Reddetk/SayMoDev/identy-service/core/coreErrors"
	"github.com/Reddetk/SayMoDev/identy-service/adapter/primary/http/middleware"
	inport "github.com/Reddetk/SayMoDev/identy-service/port/in"
)

// ---- request/response local types (never exported beyond this package) ----

type registerVerifyReq struct {
	Email string `json:"email" binding:"required,email"`
}

type registerReq struct {
	Email       string             `json:"email"       binding:"required,email"`
	VerifyCode  string             `json:"verifyCode"  binding:"required"`
	Password    string             `json:"password"    binding:"required,min=8,max=72"`
	Role        string             `json:"role"        binding:"required"`
	Fingerprint string             `json:"fingerprint" binding:"required"`
	Classifier  classifierReqBody  `json:"classifier"  binding:"required"`
}

type classifierReqBody struct {
	Difficulty  string `json:"difficulty"  binding:"required"`
	AphasiaType string `json:"aphasiaType" binding:"required"`
}

type loginReq struct {
	Email       string `json:"email"       binding:"required,email"`
	Password    string `json:"password"    binding:"required"`
	Fingerprint string `json:"fingerprint" binding:"required"`
}

type passwordResetReq struct {
	Email string `json:"email" binding:"required,email"`
}

type passwordResetConfirmReq struct {
	Email           string `json:"email"           binding:"required,email"`
	Code            string `json:"code"            binding:"required"`
	NewPassword     string `json:"newPassword"     binding:"required,min=8,max=72"`
}

type jwksKeyJSON struct {
	KID string `json:"kid"`
	Kty string `json:"kty"`
	Alg string `json:"alg"`
	Use string `json:"use"`
	N   string `json:"n"`
	E   string `json:"e"`
}

type jwksResponseJSON struct {
	Keys []string `json:"keys"`
}

// ---- helpers ----

// hashPassword wraps bcrypt.GenerateFromPassword.
// The handler is responsible for hashing before calling port/in.
func hashPassword(plain string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// oauthStateCookieName is the cookie used to round-trip the PKCE/CSRF state.
const oauthStateCookieName = "oauth_state"

// ---- handlers ----

// handleGetJWKS -- GET /iam/.well-known/jwks.json
// Auth: none (public)
// Cache-Control: max-age=3600, public
func handleGetJWKS(op inport.TokenOperator) gin.HandlerFunc {
	return func(c *gin.Context) {
		keys, err := op.GetJWKS(c.Request.Context())
		if err != nil {
			if err == corerr.ErrJWKSKeysEmpty {
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": "jwks unavailable"})
				return
			}
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "jwks unavailable"})
			return
		}
		c.Header("Cache-Control", "max-age=3600, public")
		c.JSON(http.StatusOK, gin.H{"keys": keys})
	}
}

// handleRegisterVerify -- POST /iam/auth/register/verify
// Body: { email }
// Response: 200 { message }
func handleRegisterVerify(otp inport.OTPIssuer) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req registerVerifyReq
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		// Anti-enumeration: respond 200 regardless of whether email exists.
		// IssueRegistrationOTP handles unknown emails silently inside core.
		_ = otp.IssueRegistrationOTP(c.Request.Context(), req.Email)
		c.JSON(http.StatusOK, gin.H{"message": "Verification code sent to email"})
	}
}

// handleRegister -- POST /iam/auth/register
// Body: { email, verifyCode, password, role, fingerprint, classifier }
// Response: 201 { access_token, account_id, role, session_id }
func handleRegister(reg inport.AccountRegistrator) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req registerReq
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		passwordHash, err := hashPassword(req.Password)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		classifier := inport.ClassifierDTO{
			Difficulty:  req.Classifier.Difficulty,
			AphasiaType: req.Classifier.AphasiaType,
		}

		// personalInfo is not part of the registration endpoint contract in BC#1;
		// passing empty string -- BC#1 does not store PII beyond email.
		if err := reg.Register(
			c.Request.Context(),
			req.Email,
			req.VerifyCode,
			"", // personalInfo placeholder per BC#1 spec
			passwordHash,
			req.Role,
			classifier,
			req.Fingerprint,
		); err != nil {
			switch err {
			case corerr.ErrOTPInvalid, corerr.ErrOTPExpired:
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid or expired verification code"})
			case corerr.ErrEmailAlreadyTaken:
				c.JSON(http.StatusConflict, gin.H{"error": "email already registered"})
			case corerr.ErrRateLimitIP, corerr.ErrRateLimitAccount:
				c.JSON(http.StatusTooManyRequests, gin.H{"error": "rate limit exceeded"})
			default:
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			}
			return
		}
		// Register returns no LoginResult per current port/in contract.
		// 201 with empty body signals success; client must login separately.
		// NOTE: if AccountRegistrator is extended to return LoginResult, update here.
		c.Status(http.StatusCreated)
	}
}

// handleLogin -- POST /iam/auth/login
// Body: { email, password, fingerprint }
// Response: 200 { access_token, session_id }
func handleLogin(auth inport.AccountAuthenticator) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req loginReq
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		passwordHash, err := hashPassword(req.Password)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		result, err := auth.Login(
			c.Request.Context(),
			req.Email,
			passwordHash,
			req.Fingerprint,
			c.ClientIP(),
		)
		if err != nil {
			switch err {
			case corerr.ErrRateLimitIP, corerr.ErrRateLimitAccount:
				c.JSON(http.StatusTooManyRequests, gin.H{"error": "rate limit exceeded"})
			case corerr.ErrAccountLocked:
				// Timing safety: do not distinguish locked vs wrong credentials at HTTP layer.
				c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
			case corerr.ErrInvalidCredentials:
				c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
			default:
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			}
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"access_token": result.AccessToken,
			"session_id":   result.SessionID,
		})
	}
}

// handleLogout -- POST /iam/auth/logout
// Headers: Authorization: Bearer <JWT>
// Response: 204 No Content
func handleLogout(session inport.SessionOperator, token inport.TokenOperator) gin.HandlerFunc {
	return func(c *gin.Context) {
		authCtx, ok := c.Get(middleware.AuthContextKey)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "missing auth context"})
			return
		}

		// AuthContext is set by JWTMiddleware as valobj.AuthContext.
		// We use the interface to avoid importing valobj here;
		// type-assert via the map accessor methods.
		type authContexter interface {
			AccountID() string
			SessionID() string
			JTI() string
			Rev() int64
			ExpiresAt() int64
		}

		ac, ok := authCtx.(authContexter)
		if !ok {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		ctx := c.Request.Context()

		// 1. Destroy the session (removes from session store).
		if err := session.Logout(ctx, ac.AccountID(), ac.SessionID()); err != nil {
			// Session may already be gone -- treat as success per idempotency.
			if err != corerr.ErrSessionNotFound {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
				return
			}
		}

		// 2. Blacklist the current token's jti.
		if err := token.RevokeToken(
			ctx,
			ac.AccountID(),
			ac.JTI(),
			ac.ExpiresAt(),
			ac.Rev(),
			"logout",
		); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		c.Status(http.StatusNoContent)
	}
}

// handlePasswordResetRequest -- POST /iam/auth/password-reset
// Body: { email }
// Response: 200 { message }
// Anti-enumeration: identical response regardless of whether email exists.
func handlePasswordResetRequest(otp inport.OTPIssuer) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req passwordResetReq
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		// Anti-enumeration: fire-and-forget, never expose existence of email.
		_ = otp.IssuePasswordResetOTP(c.Request.Context(), req.Email)
		c.JSON(http.StatusOK, gin.H{"message": "Reset code sent to email"})
	}
}

// handlePasswordResetConfirm -- POST /iam/auth/password-reset/confirm
// Body: { email, code, newPassword }
// Response: 200 { message }
func handlePasswordResetConfirm(pwdOp inport.PasswordOperator) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req passwordResetConfirmReq
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		newPasswordHash, err := hashPassword(req.NewPassword)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		if err := pwdOp.ConfrimPasswordReset(
			c.Request.Context(),
			req.Email,
			req.Code,
			newPasswordHash,
		); err != nil {
			switch err {
			case corerr.ErrOTPInvalid, corerr.ErrOTPExpired, corerr.ErrOTPAlreadyUsed:
				// §7: generic response -- never distinguish wrong / expired / not found.
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid or expired code"})
			case corerr.ErrPasswordPolicyViolation, corerr.ErrPasswordReused:
				c.JSON(http.StatusBadRequest, gin.H{"error": "password policy violation"})
			case corerr.ErrRateLimitIP, corerr.ErrRateLimitAccount:
				c.JSON(http.StatusTooManyRequests, gin.H{"error": "rate limit exceeded"})
			default:
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			}
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": "Password changed, re-authentication required"})
	}
}

// handleOAuthGoogleInitiate -- GET /iam/auth/oauth/google
// Response: 302 redirect to Google Authorization URL
func handleOAuthGoogleInitiate(auth inport.AccountAuthenticator) gin.HandlerFunc {
	return func(c *gin.Context) {
		redirectURL, state, err := auth.InitiateGoogleOAuth(c.Request.Context(), c.ClientIP())
		if err != nil {
			switch err {
			case corerr.ErrRateLimitIP:
				c.JSON(http.StatusTooManyRequests, gin.H{"error": "rate limit exceeded"})
			default:
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			}
			return
		}

		// Persist PKCE state in a short-lived HttpOnly cookie.
		// Value format: "csrfToken:codeVerifier" -- both opaque strings, no PII.
		cookieVal := state.CsrfToken + ":" + state.CodeVerifier
		c.SetCookie(
			oauthStateCookieName,
			cookieVal,
			600,   // 10 min TTL matches OAuthState.ExpiresAt typical window
			"/",
			"",    // domain: same-site
			true,  // secure
			true,  // httpOnly
		)

		c.Redirect(http.StatusFound, redirectURL)
	}
}

// handleOAuthGoogleCallback -- GET /iam/auth/oauth/google/callback
// Response: 200 { access_token, session_id }
func handleOAuthGoogleCallback(auth inport.AccountAuthenticator) gin.HandlerFunc {
	return func(c *gin.Context) {
		code := c.Query("code")
		receivedCSRF := c.Query("state")
		if code == "" || receivedCSRF == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing code or state parameter"})
			return
		}

		cookieVal, err := c.Cookie(oauthStateCookieName)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing oauth state cookie"})
			return
		}

		// Parse cookie: "csrfToken:codeVerifier"
		parts := strings.SplitN(cookieVal, ":", 2)
		if len(parts) != 2 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "malformed oauth state cookie"})
			return
		}

		storedState := inport.OAuthState{
			CsrfToken:    parts[0],
			CodeVerifier: parts[1],
		}

		// Fingerprint from User-Agent header as best-effort for OAuth flow;
		// full fingerprint (client hints) is not available in server-side callback.
		fingerprint := c.GetHeader("User-Agent")

		result, err := auth.HandleGoogleCallback(
			c.Request.Context(),
			code,
			receivedCSRF,
			storedState,
			fingerprint,
			c.ClientIP(),
		)
		if err != nil {
			switch err {
			case corerr.ErrOAuthCSRFMismatch, corerr.ErrOAuthStateExpired:
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid oauth state"})
			case corerr.ErrRateLimitIP, corerr.ErrRateLimitAccount:
				c.JSON(http.StatusTooManyRequests, gin.H{"error": "rate limit exceeded"})
			case corerr.ErrAccountLocked:
				c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
			default:
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			}
			return
		}

		// Clear the oauth state cookie after successful use.
		c.SetCookie(oauthStateCookieName, "", -1, "/", "", true, true)

		c.JSON(http.StatusOK, gin.H{
			"access_token": result.AccessToken,
			"session_id":   result.SessionID,
		})
	}
}
