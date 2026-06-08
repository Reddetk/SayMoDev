// Package testdata provides reusable domain object fixtures for unit tests.
// Fixtures use RestoreAccount to bypass constructor validation so tests
// can set arbitrary state without coupling to NewAccount invariants.
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
	// Length is exactly 60 characters as required by entity invariants.
	FixturePasswordHash = "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"

	// FixtureGoogleUID is the stable google_uid used in OAuth fixture.
	FixtureGoogleUID = "google-uid-fixture-001"

	// FixtureSessionID is the stable session UUID used in fixtures with sessions.
	FixtureSessionID = "00000000-0000-0000-0000-000000000002"

	// FixtureJTI is the stable JTI UUID used in fixtures with sessions.
	FixtureJTI = "00000000-0000-0000-0000-000000000003"

	// FixtureFingerprint is the stable device fingerprint string.
	FixtureFingerprint = "Mozilla/5.0 fixture-fingerprint"
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
