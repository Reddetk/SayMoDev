// Package consts defines compile-time constants and regular expressions
// used across BC#1 Identity and Access Management.
//
// Constants are grouped by domain concept: lengths, statuses, roles,
// regex patterns. No business logic lives here  only named values
// that prevent magic numbers and strings from appearing in domain code.
package consts

import (
	"regexp"
	"time"
)

// Validation constants
const (
	MaxSessionsPerAccount = 5
	MaxEmailLength        = 254
	MaxPersonalInfoLen    = 10000 // 10KB for personal info
	MinPasswordLength     = 8
	MaxPasswordBytes      = 72 // bcrypt hard limit — inputs exceeding this MUST be rejected (2 Password Policy)
	MaxFingerprintLen     = 500
	MaxSessionIDLen       = 36 // UUID v4 string length
	MaxJTILen             = 36 // UUID v4 string length
	MaxPasswordHistory    = 5
	MaxGoogleUIDLen       = 255
	SHA256HexLen          = 64
)

// Role values
const (
	RolePatient       = "patient"
	RoleRelative      = "relative" // placeholder — no current functional use (BC#1 Ubiquitous Language)
	RoleAdministrator = "administrator"
)

// Status values
const (
	StatusActive  = "active"
	StatusBlocked = "blocked"
	StatusDeleted = "deleted"
)

// Difficulty values for Classifier
const (
	DifficultyEasy   = "easy"
	DifficultyMedium = "medium"
	DifficultyHard   = "hard"
)

// AphasiaType values for Classifier
const (
	AphasiaTypeMotor = "motor_aphasia"
)

// Default / timing values
const (
	DefaultRevision   = int64(1)
	SessionDurationMS = 30 * 24 * 60 * 60 * 1000 // 30 days in milliseconds
	OTPTTL            = 3 * time.Hour            // 3h in milliseconds
)

// Rate limiting — Spec 5 Rate Limiting Invariant
const (
	// MaxFailedLoginAttemptsPerDay is the threshold after which an account
	// is automatically locked for 24 hours.
	// Spec: "50 failed login attempts per day per account → account locked 24h"
	MaxFailedLoginAttemptsPerDay = 50

	// MaxLoginAttemptsPerIPPerHour triggers a 429 response.
	// Spec: "100 login attempts per hour per IP → 429"
	MaxLoginAttemptsPerIPPerHour = 100

	// AccountLockDurationSeconds is the automatic lock duration on brute-force.
	// Spec: agrigats.md "now+24h = автоблокировка по failed attempts"
	AccountLockDurationSeconds = int64(24 * 60 * 60)
)

// JWT token — Spec 3 Token Model and Lifecycle Invariant
const (
	// JWTExpirySeconds is the fixed token lifetime: 30 days.
	// exp = iat + JWTExpirySeconds. Immutable after signing.
	JWTExpirySeconds = int64(30 * 24 * 60 * 60) // 2 592 000 s

	// JWTIssuer is the `iss` claim.
	JWTIssuer = "urn:saymo:identity-service"

	// JWTAudience is the `aud` claim.
	JWTAudience = "api.saymo"

	// JTIMaxRetries is the maximum number of UUID v4 generation retries
	// before returning an error. Collisions should never occur (~10⁻¹⁸).
	JTIMaxRetries = 3
)

// PKCE — Spec Registration — OAuth2, Ubiquitous Language
const (
	// PKCECodeVerifierMinLen is the minimum code_verifier length per RFC 7636.
	PKCECodeVerifierMinLen = 43

	// PKCECodeVerifierMaxLen is the maximum code_verifier length per RFC 7636.
	PKCECodeVerifierMaxLen = 128

	// PKCEMethod is the only supported PKCE challenge method.
	PKCEMethod = "S256"
)

// OAuth2 — Google — Spec Registration — OAuth2 (Google)
const (
	// GoogleTokenEndpoint is the URL for server-to-server authorization code exchange.
	// Spec step [5b]: POST https://oauth2.googleapis.com/token
	GoogleTokenEndpoint = "https://oauth2.googleapis.com/token"

	// GoogleJWKSEndpoint is the public key set URL for Google ID token verification.
	// Spec step [6a]: "Загружает Google JWKS: https://www.googleapis.com/oauth2/v3/certs"
	GoogleJWKSEndpoint = "https://www.googleapis.com/oauth2/v3/certs"

	// GoogleAuthEndpoint is the authorization URL to redirect the user to.
	// Spec step [2].
	GoogleAuthEndpoint = "https://accounts.google.com/o/oauth2/v2/auth"

	// GoogleOAuthScope is the scope requested from Google.
	GoogleOAuthScope = "openid email profile"

	// GoogleIssuer1 and GoogleIssuer2 are the two valid values for the `iss` claim
	// in a Google ID token.
	// Spec step [6c]: "iss: accounts.google.com или https://accounts.google.com"
	GoogleIssuer1 = "accounts.google.com"
	GoogleIssuer2 = "https://accounts.google.com"

	// GoogleJWKSFetchTimeoutSeconds is the hard timeout for fetching Google JWKS.
	// Fail-closed: if unreachable → 503.
	GoogleJWKSFetchTimeoutSeconds = 2

	// OAuthStateExpirySeconds is the TTL for an OAuthState VO.
	// Short-lived: covers only the browser round-trip.
	OAuthStateExpirySeconds = int64(10 * 60) // 10 minutes
)

// Timing safety — Spec Login
const (
	// DummyPasswordHash is a valid bcrypt hash of a random value.
	// Used when a login attempt targets a non-existent email so that
	// bcrypt.Compare runs regardless, producing identical timing for
	// "wrong email" and "wrong password" cases.
	// Spec: "non-existent account runs bcrypt.Compare against dummy hash"
	//
	// NOTE: this is a static compile-time constant. It is NEVER stored in DB
	// and NEVER matches any real password. The value was generated once with
	// bcrypt cost 12 and is intentionally meaningless.
	DummyPasswordHash = "$2a$12$dummyhashfortimingequalityXXXXXXXXXXXXXXXXXXXXXXXXXXX"
)

// JWKS cache — Spec Token Validation Flow
const (
	// JWKSCacheTTLSeconds is the local JWKS cache TTL.
	// Spec: "Resolve RS256 public key from local JWKS cache (TTL 1h)"
	JWKSCacheTTLSeconds = int64(60 * 60) // 1 hour

	// JWKSFetchTimeoutSeconds is the timeout for the one-time re-fetch on unknown kid.
	// Spec: "ONE fetch from IAM /jwks.json (timeout 2s, no retry) → fail-closed on failure"
	JWKSFetchTimeoutSeconds = 2
)

// Regex
var (
	EmailRegex = regexp.MustCompile(`^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`)
	// BcryptHashRegex validates bcrypt hash format.
	// Go's bcrypt library generates $2a$ prefix only.
	BcryptHashRegex = regexp.MustCompile(`^\$2[ab]\$\d{2}\$[./A-Za-z0-9]{53}$`)
)
