// Package consts defines compile-time constants and regular expressions
// used across BC#1 Identity and Access Management.
//
// Constants are grouped by domain concept: lengths, statuses, roles,
// regex patterns. No business logic lives here -- only named values
// that prevent magic numbers and strings from appearing in domain code.
package consts

import "regexp"

// Validation constants
const (
	MaxSessionsPerAccount = 5
	MaxEmailLength        = 254
	MaxPersonalInfoLen    = 10000 // 10KB for personal info
	MinPasswordLength     = 8
	MaxPasswordBytes      = 72 // bcrypt limit
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
	RoleRelative      = "relative"
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

// Default values
const (
	DefaultRevision   = int64(1)
	SessionDurationMS = 30 * 24 * 60 * 60 * 1000 // 30 days in milliseconds
	OTPTTL            = 3 * 60 * 60 * 1000       // 3h  in milliseconds
)

// EmailRegex validates email format (simplified but effective)
var (
	EmailRegex = regexp.MustCompile(`^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`)
	// BcryptHashRegex validates bcrypt hash format
	// Go's bcrypt library generates $2a$ prefix only
	BcryptHashRegex = regexp.MustCompile(`^\$2[ab]\$\d{2}\$[./A-Za-z0-9]{53}$`)
)
