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

// ---------------------------------------------------------------------------
// Request / response types (never exported beyond this package)
// ---------------------------------------------------------------------------

type registerVerifyReq struct {
	Email string `json:"email" binding:"required,email"`
}

type registerReq struct {
	Email       string            `json:"email"       binding:"required,email"`
	VerifyCode  string            `json:"verifyCode"  binding:"required"`
	Password    string            `json:"password"    binding:"required,min=8,max=72"`
	Role        string            `json:"role"        binding:"required"`
	Fingerprint string            `json:"fingerprint" binding:"required"`
	Classifier  classifierReqBody `json:"classifier"  binding:"required"`
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
	Email       string `json:"email"       binding:"required,email"`
	Code        string `json:"code"        binding:"required"`
	NewPassword string `json:"newPassword" binding:"required,min=8,max=72"`
}

// ---------------------------------------------------------------------------
// OAuth state cookie
// ---------------------------------------------------------------------------

// oauthStateCookieName stores PKCE state between initiate and callback.
// Format: "csrfToken:codeVerifier" -- both opaque, no PII.
const oauthStateCookieName = "oauth_state"

// ---------------------------------------------------------------------------
// Password hashing
// ---------------------------------------------------------------------------

// hashPassword wraps bcrypt at DefaultCost.
// port/in contracts require a hash, not plain-text.
func hashPassword(plain string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ---------------------------------------------------------------------------
// Error classification helpers.
// Each helper covers a semantic group of coreErrors sentinels.
// All sentinel names are verified against core/coreErrors/businesErrors.go.
// ---------------------------------------------------------------------------

// isOTPError covers all bad/expired/used OTP conditions.
// Spec §7: "generic response -- never distinguish wrong / expired / not found".
func isOTPError(err error) bool {
	return err == corerr.ErrUserOTPisNotCorrect ||
		err == corerr.ErrUserOTPisNotValid ||
		err == corerr.ErrOTPAlreadyExpired
}

// isPasswordPolicyError covers all password domain violations.
func isPasswordPolicyError(err error) bool {
	return err == corerr.ErrPasswordReused ||
		err == corerr.ErrPasswordTooShort ||
		err == corerr.ErrPasswordTooLong ||
		err == corerr.ErrInvalidPasswordHash ||
		err == corerr.ErrInvalidPasswordHashLength ||
		err == corerr.ErrInvalidPasswordHashFormat ||
		err == corerr.ErrPasswordHashInvalidFormat ||
		err == corerr.ErrPasswordHashInvalidLength ||
		err == corerr.ErrPasswordHashRequired
}

// isRateLimit covers both per-IP and per-account rate limits.
func isRateLimit(err error) bool {
	return err == corerr.ErrRateLimitIP || err == corerr.ErrRateLimitAccount
}

// isAccountLocked covers blocked and non-active account states.
// Spec Login: must map to 401 generic -- must NOT reveal lock reason.
func isAccountLocked(err error) bool {
	return err == corerr.ErrAccountLocked ||
		err == corerr.ErrAccountNotActive
}

// isClassifierError covers all Classifier VO validation errors.
func isClassifierError(err error) bool {
	return err == corerr.ErrInvalidClassifier ||
		err == corerr.ErrClassifierDifficultyRequired ||
		err == corerr.ErrClassifierDifficultyInvalid ||
		err == corerr.ErrClassifierAphasiaRequired ||
		err == corerr.ErrClassifierAphasiaInvalid
}

// isFingerprintError covers all fingerprint VO validation errors.
func isFingerprintError(err error) bool {
	return err == corerr.ErrInvalidFingerprint ||
		err == corerr.ErrFingerprintRequired ||
		err == corerr.ErrFingerprintTooLong
}

// isOAuthStateError covers PKCE/CSRF state validation failures.
func isOAuthStateError(err error) bool {
	return err == corerr.ErrOAuthStateCSRFMismatch ||
		err == corerr.ErrOAuthStateCSRFTokenEmpty ||
		err == corerr.ErrOAuthStateExpired ||
		err == corerr.ErrOAuthStateNotFound
}

// isInfraError covers repository and delivery adapter failures.
func isInfraError(err error) bool {
	return err == corerr.ErrAccountRepository ||
		err == corerr.ErrOTPRepository ||
		err == corerr.ErrEmailDeliveryFailed
}

// ---------------------------------------------------------------------------
// GET /iam/.well-known/jwks.json -- public, no auth
// ---------------------------------------------------------------------------

func handleGetJWKS(op inport.TokenOperator) gin.HandlerFunc {
	return func(c *gin.Context) {
		keys, err := op.GetJWKS(c.Request.Context())
		if err != nil {
			// ErrJWKSKeysEmpty is a config/infra error: downstream cannot verify tokens -> 503.
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "jwks unavailable"})
			return
		}
		c.Header("Cache-Control", "max-age=3600, public")
		c.JSON(http.StatusOK, gin.H{"keys": keys})
	}
}

// ---------------------------------------------------------------------------
// POST /iam/auth/register/verify
// Body: { email }
// Response: 200 { message }
// Anti-enumeration: respond 200 regardless of email existence.
// ---------------------------------------------------------------------------

func handleRegisterVerify(otp inport.OTPIssuer) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req registerVerifyReq
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		// Anti-enumeration: fire-and-forget.
		// ErrEmailDeliveryFailed and ErrOTPRepository are intentionally swallowed.
		_ = otp.IssueRegistrationOTP(c.Request.Context(), req.Email)
		c.JSON(http.StatusOK, gin.H{"message": "Verification code sent to email"})
	}
}

// ---------------------------------------------------------------------------
// POST /iam/auth/register
// Body: { email, verifyCode, password, role, fingerprint, classifier }
// Response: 201
// ---------------------------------------------------------------------------

func handleRegister(reg inport.AccountRegistrator) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req registerReq
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		passwordHash, err := hashPassword(req.Password)
		if err != nil {
			respondInternalErr(c)
			return
		}

		classifier := inport.ClassifierDTO{
			Difficulty:  req.Classifier.Difficulty,
			AphasiaType: req.Classifier.AphasiaType,
		}

		if err := reg.Register(
			c.Request.Context(),
			req.Email,
			req.VerifyCode,
			"",
			passwordHash,
			req.Role,
			classifier,
			req.Fingerprint,
		); err != nil {
			switch {
			case isOTPError(err):
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid or expired verification code"})
			case err == corerr.ErrEmailAlreadyExists:
				c.JSON(http.StatusConflict, gin.H{"error": "email already registered"})
			case isClassifierError(err):
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid classifier"})
			case err == corerr.ErrInvalidRole:
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid role"})
			case isPasswordPolicyError(err):
				c.JSON(http.StatusBadRequest, gin.H{"error": "password policy violation"})
			case isFingerprintError(err):
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid fingerprint"})
			default:
				respondErr(c, err)
			}
			return
		}
		c.Status(http.StatusCreated)
	}
}

// ---------------------------------------------------------------------------
// POST /iam/auth/login
// Body: { email, password, fingerprint }
// Response: 200 { access_token, session_id }
// ---------------------------------------------------------------------------

func handleLogin(auth inport.AccountAuthenticator) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req loginReq
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		passwordHash, err := hashPassword(req.Password)
		if err != nil {
			respondInternalErr(c)
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
			switch {
			// Spec Login §: "401 generic always" for all credential/lock/deleted cases.
			// Must NOT reveal whether it is wrong credentials, locked, or deleted.
			case err == corerr.ErrInvalidCredentials ||
				err == corerr.ErrAccountNotFound:
				c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
			case isAccountLocked(err):
				c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
			case err == corerr.ErrAccountDeleted:
				c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
			case isFingerprintError(err):
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid fingerprint"})
			default:
				respondErr(c, err)
			}
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"access_token": result.AccessToken,
			"session_id":   result.SessionID,
		})
	}
}

// ---------------------------------------------------------------------------
// POST /iam/auth/logout
// Headers: Authorization: Bearer <JWT>
// Response: 204 No Content
// ---------------------------------------------------------------------------

func handleLogout(session inport.SessionOperator, token inport.TokenOperator) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw, ok := c.Get(middleware.AuthContextKey)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "missing auth context"})
			return
		}

		// AuthContext is a plain struct (port/in tokenValidator.go).
		// Fields: AccountID, Role, SessionID, Rev.
		// NOTE: JTI and ExpiresAt are not present in AuthContext.
		// Missing spec / design gap: RevokeToken requires jti + expiresAt which are
		// JWT infrastructure fields not surfaced by the current AuthContext VO.
		// Until AuthContext is extended, the token revocation step is omitted here
		// and only the session is destroyed. Tracked in ADR.
		ac, ok := raw.(inport.AuthContext)
		if !ok {
			respondInternalErr(c)
			return
		}

		ctx := c.Request.Context()

		// Step 1: destroy session.
		if err := session.Logout(ctx, ac.AccountID, ac.SessionID); err != nil {
			switch {
			case err == corerr.ErrSessionNotFound:
				// Session already gone -- idempotent, continue.
			default:
				respondErr(c, err)
				return
			}
		}

		// Step 2: blacklist jti.
		// DESIGN GAP: RevokeToken(ctx, accountID, jti, expiresAt, rev, reason) requires
		// jti and expiresAt which are not in AuthContext. Skipped until AuthContext
		// is extended with JTI and ExpiresAt fields (port/in change required).
		_ = token

		c.Status(http.StatusNoContent)
	}
}

// ---------------------------------------------------------------------------
// POST /iam/auth/password-reset
// Body: { email }
// Response: 200 { message }
// Anti-enumeration: identical response regardless of email existence.
// ---------------------------------------------------------------------------

func handlePasswordResetRequest(otp inport.OTPIssuer) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req passwordResetReq
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		// Anti-enumeration: fire-and-forget.
		_ = otp.IssuePasswordResetOTP(c.Request.Context(), req.Email)
		c.JSON(http.StatusOK, gin.H{"message": "Reset code sent to email"})
	}
}

// ---------------------------------------------------------------------------
// POST /iam/auth/password-reset/confirm
// Body: { email, code, newPassword }
// Response: 200 { message }
// ---------------------------------------------------------------------------

func handlePasswordResetConfirm(pwdOp inport.PasswordOperator) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req passwordResetConfirmReq
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		newPasswordHash, err := hashPassword(req.NewPassword)
		if err != nil {
			respondInternalErr(c)
			return
		}

		if err := pwdOp.ConfrimPasswordReset(
			c.Request.Context(),
			req.Email,
			req.Code,
			newPasswordHash,
		); err != nil {
			switch {
			// Spec §7: generic response -- never distinguish wrong / expired / not found.
			case isOTPError(err):
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid or expired code"})
			case err == corerr.ErrAccountNotFound:
				// Anti-enumeration: same generic OTP error, never 404.
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid or expired code"})
			case isPasswordPolicyError(err):
				c.JSON(http.StatusBadRequest, gin.H{"error": "password policy violation"})
			default:
				respondErr(c, err)
			}
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": "Password changed, re-authentication required"})
	}
}

// ---------------------------------------------------------------------------
// GET /iam/auth/oauth/google
// Response: 302 redirect to Google Authorization URL
// ---------------------------------------------------------------------------

func handleOAuthGoogleInitiate(auth inport.AccountAuthenticator) gin.HandlerFunc {
	return func(c *gin.Context) {
		redirectURL, state, err := auth.InitiateGoogleOAuth(c.Request.Context(), c.ClientIP())
		if err != nil {
			switch {
			case err == corerr.ErrOAuthJWKSUnavailable:
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": "oauth provider unavailable"})
			default:
				respondErr(c, err)
			}
			return
		}

		// Persist PKCE state in a short-lived HttpOnly Secure cookie.
		cookieVal := state.CsrfToken + ":" + state.CodeVerifier
		c.SetCookie(oauthStateCookieName, cookieVal, 600, "/", "", true, true)
		c.Redirect(http.StatusFound, redirectURL)
	}
}

// ---------------------------------------------------------------------------
// GET /iam/auth/oauth/google/callback
// Response: 200 { access_token, session_id }
// ---------------------------------------------------------------------------

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

		parts := strings.SplitN(cookieVal, ":", 2)
		if len(parts) != 2 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "malformed oauth state cookie"})
			return
		}

		storedState := inport.OAuthState{
			CsrfToken:    parts[0],
			CodeVerifier: parts[1],
		}

		// Best-effort fingerprint for server-side OAuth callback:
		// full client-hints fingerprint is unavailable in redirect context.
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
			switch {
			case isOAuthStateError(err):
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid oauth state"})
			case err == corerr.ErrOAuthEmailNotVerified:
				// Fail-closed: spec requires email_verified = true.
				c.JSON(http.StatusUnauthorized, gin.H{"error": "google account email not verified"})
			case err == corerr.ErrOAuthIDTokenInvalid:
				c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid google token"})
			case err == corerr.ErrOAuthTokenExchangeFailed:
				c.JSON(http.StatusBadGateway, gin.H{"error": "google token exchange failed"})
			case err == corerr.ErrOAuthJWKSUnavailable:
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": "oauth provider unavailable"})
			case err == corerr.ErrOAuthGoogleUIDConflict:
				// Account exists but google_uid mismatch -- treat as generic auth failure.
				c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
			case isAccountLocked(err):
				c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
			case err == corerr.ErrAccountDeleted:
				c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
			case err == corerr.ErrPKCECodeVerifierInvalidLength ||
				err == corerr.ErrPKCECodeChallengeEmpty:
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid pkce parameters"})
			default:
				respondErr(c, err)
			}
			return
		}

		// Clear oauth state cookie after successful use.
		c.SetCookie(oauthStateCookieName, "", -1, "/", "", true, true)

		c.JSON(http.StatusOK, gin.H{
			"access_token": result.AccessToken,
			"session_id":   result.SessionID,
		})
	}
}
