// Package corerr - conatain errors of core
package corerr

import "errors"

// ---------------------------------------------------------------------------
// Business errors
// ---------------------------------------------------------------------------
var (
	// core/coreErrors/errors.go
	ErrFederatedAccountHasNoPassword = errors.New("federated account has no password set")
	ErrUserOTPisNotCorrect           = errors.New("otp is not correct")
	ErrUserOTPisNotValid             = errors.New("otp is not valid")
	ErrEmailAlreadyExists            = errors.New("email already exists")
)

// ---------------------------------------------------------------------------
// Adapter errors
// ---------------------------------------------------------------------------
var (
	ErrEmailDeliveryFailed = errors.New("email selivery failed")
	ErrOTPRepository       = errors.New("otp rep req is failed")
	ErrAccountRepository   = errors.New("acc rep is failed")
	ErrAccountNotFound     = errors.New("account not found")
)

// ---------------------------------------------------------------------------
// Validation errors
// ---------------------------------------------------------------------------
var (
	ErrSessionNotFound                          = errors.New("session not found")
	ErrInvalidEmail                             = errors.New("invalid email format")
	ErrEmailTooLong                             = errors.New("email too long")
	ErrEmailRequired                            = errors.New("email is required")
	ErrPasswordTooShort                         = errors.New("password too short (minimum 8 characters)")
	ErrPasswordTooLong                          = errors.New("password too long (bcrypt limit: 72 bytes)")
	ErrInvalidRole                              = errors.New("invalid role")
	ErrRoleRequired                             = errors.New("role is required")
	ErrInvalidStatus                            = errors.New("invalid status")
	ErrStatusRequired                           = errors.New("status is required")
	ErrInvalidRevision                          = errors.New("revision cannot be negative")
	ErrInvalidLockedUntil                       = errors.New("locked until cannot be in the past for active accounts")
	ErrEmptyPersonalInfo                        = errors.New("personal info cannot be empty")
	ErrPersonalInfoTooLong                      = errors.New("personal info is too long")
	ErrPersonalInfoRequired                     = errors.New("personal info is required")
	ErrInvalidClassifier                        = errors.New("invalid classifier")
	ErrInvalidSessionID                         = errors.New("invalid session ID")
	ErrInvalidJTI                               = errors.New("invalid JTI format")
	ErrInvalidFingerprint                       = errors.New("invalid fingerprint")
	ErrInvalidLastActivity                      = errors.New("invalid last activity time")
	ErrInvalidPasswordHash                      = errors.New("invalid password hash")
	ErrInvalidPasswordHashLength                = errors.New("invalid password hash length")
	ErrInvalidPasswordHashFormat                = errors.New("invalid bcrypt hash format")
	ErrPasswordHistoryFull                      = errors.New("password history is limited to 5 entries")
	ErrAccountRequiresAuth                      = errors.New("account must have either google uid or password hash")
	ErrAccountCannotHaveBoth                    = errors.New("account cannot have both google uid and password hash")
	ErrGoogleUIDCannotBeEmpty                   = errors.New("google uid cannot be empty")
	ErrGoogleUIDTooLong                         = errors.New("google uid too long")
	ErrPasswordHashCannotBeEmpty                = errors.New("password hash cannot be empty")
	ErrOAuthAccountMustHaveNullPasswordHash     = errors.New("OAuth accounts must have null password hash")
	ErrEmailPasswordAccountMustHavePasswordHash = errors.New("email/password accounts must have password hash")
	ErrActiveAccountCannotBeLockedInFuture      = errors.New("active account cannot be locked in future")
	ErrLockedUntilNegative                      = errors.New("lockedUntil cannot be negative")
	ErrAccountDeleted                           = errors.New("account deleted")
	ErrAccountNotActive                         = errors.New("account not active")
	ErrAccountAlreadyDeleted                    = errors.New("account already deleted")

	// ErrAccountAlreadyLocked is returned by entity.Account.Lock when the account
	// is already in StatusBlocked. Prevents rev increment and silent no-op on
	// empty sessions. Mapped to HTTP 409 Conflict by the admin handler.
	ErrAccountAlreadyLocked = errors.New("account is already locked")

	// ErrAccountNotLocked is returned by entity.Account.Unlock when the account
	// is not in StatusBlocked. Prevents silently overwriting an active account
	// status. Mapped to HTTP 409 Conflict by the admin handler.
	ErrAccountNotLocked = errors.New("account is not locked")

	ErrOTPAlreadyExpired          = errors.New("otp hash already expired")
	ErrOTPHashRequired            = errors.New("otp hash req")
	ErrOTPHashInvalidLength       = errors.New("otp hash invalid length")
	ErrVerificationCodeIDRequired = errors.New("verf code id req")
)

// ---------------------------------------------------------------------------
// OTP purpose errors
// ---------------------------------------------------------------------------
var (
	ErrInvalidOTPPurpose = errors.New("invalid otp purose")
)

// ---------------------------------------------------------------------------
// Classifier errors
// ---------------------------------------------------------------------------
var (
	ErrClassifierDifficultyRequired = errors.New("classifier: difficulty is required")
	ErrClassifierDifficultyInvalid  = errors.New("classifier: invalid difficulty value")
	ErrClassifierAphasiaRequired    = errors.New("classifier: aphasia type is required")
	ErrClassifierAphasiaInvalid     = errors.New("classifier: invalid aphasia type")
)

// ---------------------------------------------------------------------------
// Metadata entity errors
// ---------------------------------------------------------------------------
var (
	ErrMetadataUpdatedBeforeCreated = errors.New("metadata updated before created")
	ErrMetadataUpdatedAtNegative    = errors.New("metadata updated negativly")
	ErrMetadataCreatedAtNegative    = errors.New("metadata created negativly")
)

// ---------------------------------------------------------------------------
// Password entity errors
// ---------------------------------------------------------------------------
var (
	ErrPasswordHashInvalidFormat = errors.New("pasword hash invalid")
	ErrPasswordHashInvalidLength = errors.New("pasword hash invalid length")
	ErrPasswordHashRequired      = errors.New("pasword hash req")
)

// ---------------------------------------------------------------------------
// OTP validation errors
// ---------------------------------------------------------------------------
var (
	ErrOTPValNotCorrectLen = errors.New("otp len not correct")
	ErrOTPValNotNumeric    = errors.New("opt contain only nums")
)

// ---------------------------------------------------------------------------
// Session validation errors
// ---------------------------------------------------------------------------
var (
	ErrSessionIDRequired = errors.New("session ID is required")
	ErrSessionIDTooLong  = errors.New("session ID is too long")
	ErrSessionIDInvalid  = errors.New("invalid session ID")

	ErrJTIRequired = errors.New("JTI is required")
	ErrJTITooLong  = errors.New("JTI is too long")
	ErrJTIInvalid  = errors.New("invalid JTI")

	ErrFingerprintRequired = errors.New("fingerprint is required")
	ErrFingerprintTooLong  = errors.New("fingerprint is too long")

	ErrLastActivityNegative = errors.New("lastActivity cannot be negative")
	ErrLastActivityFuture   = errors.New("lastActivity too far in the future")

	ErrAccountIDRequired = errors.New("account ID is required")
	ErrAccountIDInvalid  = errors.New("invalid account ID")
)

// ---------------------------------------------------------------------------
// Authentication errors — Login flow
// Spec: IAM §Login — "401 generic always; 429 on rate limit"
// All auth errors return a GENERIC message to the client. Internal
// sentinel values exist only for service-layer branching.
// ---------------------------------------------------------------------------
var (
	// ErrInvalidCredentials is returned when email or password do not match.
	// Mapped to HTTP 401 with a generic body.
	ErrInvalidCredentials = errors.New("invalid credentials")

	// ErrAccountLocked is returned when account.status = "blocked".
	// Mapped to HTTP 401 with a generic body (must not reveal lock reason).
	ErrAccountLocked = errors.New("account is locked")

	// ErrRateLimitIP is returned when the per-IP hourly threshold is exceeded.
	// Spec: "100 login attempts per hour per IP -> 429"
	// Mapped to HTTP 429.
	ErrRateLimitIP = errors.New("rate limit exceeded for IP")

	// ErrRateLimitAccount is returned when the per-account daily threshold is exceeded.
	// Spec: "50 failed login attempts per day per account -> account locked 24h"
	// The account is locked automatically before this error is returned.
	// Mapped to HTTP 429.
	ErrRateLimitAccount = errors.New("rate limit exceeded for account")

	// ErrPasswordReused is returned when the new password matches one of the last 5 stored hashes.
	// Spec §2: "Password history: last 5 hashes; reuse rejected via bcrypt.Compare per stored hash"
	ErrPasswordReused = errors.New("password was recently used")
)

// ---------------------------------------------------------------------------
// OAuth2 / Google errors — Spec §Registration — OAuth2 (Google)
// ---------------------------------------------------------------------------
var (
	// ErrOAuthEmailNotVerified is returned when email_verified claim is false.
	// Spec: "email_verified: true — ОБЯЗАТЕЛЬНАЯ проверка до создания/входа в account"
	// Fail-closed: account creation / login rejected.
	ErrOAuthEmailNotVerified = errors.New("google account email is not verified")

	// ErrOAuthGoogleUIDEmpty is returned when the sub claim from Google ID token is empty.
	ErrOAuthGoogleUIDEmpty = errors.New("google uid (sub) is empty")

	// ErrOAuthEmailEmpty is returned when the email claim from Google ID token is empty.
	ErrOAuthEmailEmpty = errors.New("google email claim is empty")

	// ErrOAuthStateCSRFMismatch is returned when the state parameter in the
	// callback does not match the stored CSRF token.
	// Spec step [5a]: "Проверяет state (CSRF protection)"
	ErrOAuthStateCSRFMismatch = errors.New("oauth state CSRF token mismatch")

	// ErrOAuthStateCSRFTokenEmpty is returned when the csrf token is empty during OAuthState construction.
	ErrOAuthStateCSRFTokenEmpty = errors.New("oauth state CSRF token is empty")

	// ErrOAuthStateExpired is returned when the OAuthState has exceeded its TTL.
	ErrOAuthStateExpired = errors.New("oauth state has expired")

	// ErrOAuthStateNotFound is returned when no OAuthState exists for a callback.
	ErrOAuthStateNotFound = errors.New("oauth state not found")

	// ErrOAuthJWKSUnavailable is returned when Google JWKS cannot be fetched.
	// Spec: "Fail-closed на недоступность: если JWKS недоступен -> 503"
	ErrOAuthJWKSUnavailable = errors.New("google JWKS endpoint unavailable")

	// ErrOAuthIDTokenInvalid is returned when the Google ID token fails RS256 verification
	// or claim validation (iss, aud, exp).
	ErrOAuthIDTokenInvalid = errors.New("google ID token is invalid")

	// ErrOAuthTokenExchangeFailed is returned when the server-to-server call
	// to Google Token Endpoint fails.
	// Spec step [5b].
	ErrOAuthTokenExchangeFailed = errors.New("google token exchange failed")

	// ErrOAuthGoogleUIDConflict is returned when a login attempt with Google supplies
	// a google_uid that does not match the one stored on the existing account.
	// Spec CASE C: "Привязать google_uid к существующему account"
	ErrOAuthGoogleUIDConflict = errors.New("google uid does not match stored value for this account")
)

// ---------------------------------------------------------------------------
// PKCE errors — Spec §Ubiquitous Language (code_verifier, code_challenge)
// ---------------------------------------------------------------------------
var (
	// ErrPKCECodeVerifierInvalidLength is returned when the code_verifier length
	// is outside the RFC 7636 range [43, 128].
	ErrPKCECodeVerifierInvalidLength = errors.New("PKCE code_verifier length must be between 43 and 128 characters")

	// ErrPKCECodeChallengeEmpty is returned when code_challenge is empty.
	ErrPKCECodeChallengeEmpty = errors.New("PKCE code_challenge is empty")
)

// ---------------------------------------------------------------------------
// LoginResult VO construction errors
// ---------------------------------------------------------------------------
var (
	ErrLoginResultAccessTokenEmpty = errors.New("login result: access token is empty")
	ErrLoginResultAccountIDEmpty   = errors.New("login result: account ID is empty")
	ErrLoginResultRoleEmpty        = errors.New("login result: role is empty")
	ErrLoginResultSessionIDEmpty   = errors.New("login result: session ID is empty")
)

// ---------------------------------------------------------------------------
// RegistrationMethod VO errors
// ---------------------------------------------------------------------------
var (
	ErrInvalidRegistrationMethod = errors.New("invalid registration method: must be 'email' or 'oauth2'")
)

// ---------------------------------------------------------------------------
// Token domain errors — Spec §Token Model and Lifecycle Invariant
// ---------------------------------------------------------------------------
var (
	// ErrJTIGenerationFailed is returned by TokenIssuer.Issue when all
	// consts.JTIMaxRetries UUID v4 generation attempts produce a colliding jti.
	// Probability per attempt: ~10^-18. Returning this error indicates
	// a catastrophic failure in the UUID source, not a business rule violation.
	// Mapped to HTTP 500.
	ErrJTIGenerationFailed = errors.New("jti generation failed after max retries")

	// ErrTokenRevoked is returned by token validation middleware when
	// TokenBlacklist.Contains returns true for the incoming jti.
	// Spec §Blacklist: "L1 in-memory -> L2 Redis -> L3 PostgreSQL (source of truth)"
	// Mapped to HTTP 401.
	ErrTokenRevoked = errors.New("token has been revoked")

	// ErrJWKSKeysEmpty is returned by TokenIssuer.GetPublicKeys when the
	// adapter finds no active RSA keys in the key store.
	// This is a configuration error, not a transient failure.
	// Mapped to HTTP 503 (downstream cannot verify tokens).
	ErrJWKSKeysEmpty = errors.New("no active JWKS keys found")
)

// ---------------------------------------------------------------------------
// JWKS Key VO construction errors — RFC 7517
// ---------------------------------------------------------------------------
var (
	// ErrJWKSKeyIDEmpty is returned by NewJWKSKey when kid is blank.
	// kid must match the kid header in the signed JWT.
	ErrJWKSKeyIDEmpty = errors.New("jwks key: kid is required")

	// ErrJWKSKeyModulusEmpty is returned by NewJWKSKey when the RSA modulus (n) is blank.
	// n is the base64url-encoded modulus of the RSA public key (RFC 7518 §6.3.1.1).
	ErrJWKSKeyModulusEmpty = errors.New("jwks key: modulus (n) is required")

	// ErrJWKSKeyExponentEmpty is returned by NewJWKSKey when the RSA exponent (e) is blank.
	// e is the base64url-encoded public exponent of the RSA key (RFC 7518 §6.3.1.2).
	ErrJWKSKeyExponentEmpty = errors.New("jwks key: exponent (e) is required")
)
