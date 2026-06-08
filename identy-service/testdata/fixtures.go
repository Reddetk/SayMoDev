// Package testdata provides reusable domain object fixtures for unit tests.
// Fixtures use Restore* constructors to bypass validation invariants so tests
// can set arbitrary state without coupling to production constructor rules.
package testdata

import (
	"time"

	"github.com/Reddetk/SayMoDev/identy-service/core/entity"
	valobj "github.com/Reddetk/SayMoDev/identy-service/core/valObj"
)

const (
	// FixtureAccountID is the stable UUID used across all test fixtures.
	FixtureAccountID = "00000000-0000-0000-0000-000000000001"

	// FixtureEmail is the stable email used across all test fixtures.
	FixtureEmail = "fixture@example.com"

	// FixturePasswordHash is a valid bcrypt hash (cost 10) of the string "Password1!".
	FixturePasswordHash = "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"

	// FixtureGoogleUID is the stable google_uid used in OAuth fixture.
	FixtureGoogleUID = "google-uid-fixture-001"

	// FixtureSessionID is the stable session UUID used in fixtures with sessions.
	FixtureSessionID = "00000000-0000-0000-0000-000000000002"

	// FixtureJTI is the stable JTI UUID used in fixtures with sessions.
	FixtureJTI = "00000000-0000-0000-0000-000000000003"

	// FixtureFingerprint is the stable device fingerprint string.
	FixtureFingerprint = "Mozilla/5.0 fixture-fingerprint"

	// FixtureOTPHash is a valid SHA256 hex string (64 chars) used in VerificationCode fixtures.
	FixtureOTPHash = "a665a45920422f9d417e4867efdc4fb8a04a1f3fff1fa07e998e86f7f7a27ae3"

	// FixtureCSRFToken is a stable CSRF token for OAuthState fixtures.
	FixtureCSRFToken = "csrf-token-fixture-001"

	// FixtureCodeVerifier is a 50-char PKCE code_verifier (RFC 7636: 43-128 chars).
	FixtureCodeVerifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk99"

	// FixtureCodeChallenge is the BASE64URL(SHA256(FixtureCodeVerifier)) value.
	FixtureCodeChallenge = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"

	// FixtureClientIP is the stable client IP address used in service-level tests.
	FixtureClientIP = "127.0.0.1"
)

// NewActiveAccount returns a restored active patient Account with a password hash
// and one active session. Safe to call in parallel tests -- returns a new pointer each time.
func NewActiveAccount() *entity.Account {
	now := time.Now().UnixMilli()
	meta, _ := valobj.NewMetadata(now, now)

	session, _ := entity.RestoreSession(
		FixtureSessionID,
		FixtureJTI,
		FixtureFingerprint,
		now,
		meta,
	)

	hash := FixturePasswordHash
	account, _ := entity.RestoreAccount(
		FixtureAccountID,
		FixtureEmail,
		"Test User",
		valobj.RolePatient,
		valobj.StatusActive,
		nil,
		&hash,
		int64(1),
		nil,
		meta,
		[]entity.Session{*session},
		[]valobj.PasswordEntry{},
	)
	return account
}

// NewOAuthAccount returns a restored active patient Account linked to Google OAuth.
// password_hash is nil; google_uid is set to FixtureGoogleUID.
func NewOAuthAccount() *entity.Account {
	now := time.Now().UnixMilli()
	meta, _ := valobj.NewMetadata(now, now)

	uid := FixtureGoogleUID
	account, _ := entity.RestoreAccount(
		FixtureAccountID,
		FixtureEmail,
		"Test OAuth User",
		valobj.RolePatient,
		valobj.StatusActive,
		&uid,
		nil,
		int64(1),
		nil,
		meta,
		[]entity.Session{},
		[]valobj.PasswordEntry{},
	)
	return account
}

// NewFreshVerificationCode returns a VerificationCode that is NOT expired.
// expiresAt is set 10 minutes in the future so IsExpired() returns false.
func NewFreshVerificationCode() *valobj.VerificationCode {
	futureMs := time.Now().Add(10 * time.Minute).UnixMilli()
	vc, _ := valobj.RestoreVerificationCode(
		"00000000-0000-0000-0000-000000000010",
		FixtureEmail,
		FixtureOTPHash,
		valobj.OTPPurposeRegistration,
		futureMs,
	)
	return vc
}

// NewExpiredVerificationCode returns a VerificationCode where IsExpired() == true.
// expiresAt is set 1 minute in the past so the domain rejects it.
func NewExpiredVerificationCode() *valobj.VerificationCode {
	pastMs := time.Now().Add(-1 * time.Minute).UnixMilli()
	vc, _ := valobj.RestoreVerificationCode(
		"00000000-0000-0000-0000-000000000011",
		FixtureEmail,
		FixtureOTPHash,
		valobj.OTPPurposeRegistration,
		pastMs,
	)
	return vc
}

// NewOAuthState returns a valid OAuthState fixture for OAuth2 flow tests.
func NewOAuthState() valobj.OAuthState {
	futureUnix := time.Now().Add(10 * time.Minute).Unix()
	state, _ := valobj.NewOAuthState(
		FixtureCSRFToken,
		FixtureCodeVerifier,
		FixtureCodeChallenge,
		futureUnix,
	)
	return state
}
