// Package valobj contains Value Objects for BC#1 Identity and Access Management.
//
// Value Objects are immutable -- they have no identity and are defined entirely
// by their attributes. Once created via constructor, they cannot be mutated.
// All invariants are enforced at construction time.
//
// Cross-BC Value Objects (used across multiple bounded contexts):
//   - AuthContext  -- result of JWT validation passed to BC#2-4
//   - Classifier   -- immutable tuple (difficulty, aphasiaType) identifying a program
//   - Role         -- typed enumeration of account roles
//   - AccountStatus -- typed enumeration of account states
//
// BC#1-internal Value Objects:
//   - PasswordEntry -- bcrypt hash record within PasswordHistory
//   - Metadata      -- creation and update timestamps
//
// Constructors:
//   - NewXxx(args)   -- validates args, returns (VO, error)
//   - ParseXxx(str)  -- parses string into typed enum, returns (Type, error)
//   - NewXxxNow()    -- creates VO with current time, cannot fail
package valobj

import corerr "github.com/Reddetk/SayMoDev/identy-service/core/coreErrors"

type AccountStatus string

const (
	StatusActive  AccountStatus = "active"
	StatusBlocked AccountStatus = "blocked"
	StatusDeleted AccountStatus = "deleted"
)

func ParseAccountStatus(value string) (AccountStatus, error) {
	s := AccountStatus(value)
	switch s {
	case StatusActive, StatusBlocked, StatusDeleted:
		return s, nil
	default:
		return "", corerr.ErrInvalidStatus
	}
}

func (s AccountStatus) String() string  { return string(s) }
func (s AccountStatus) IsActive() bool  { return s == StatusActive }
func (s AccountStatus) IsBlocked() bool { return s == StatusBlocked }
func (s AccountStatus) IsDeleted() bool { return s == StatusDeleted }
