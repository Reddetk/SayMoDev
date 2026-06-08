package valobj

import (
	"golang.org/x/crypto/bcrypt"

	"github.com/Reddetk/SayMoDev/identy-service/core/consts"
	corerr "github.com/Reddetk/SayMoDev/identy-service/core/coreErrors"
)

// PasswordEntry represents a bcrypt hash in password history.
type PasswordEntry struct {
	hash     string
	metadata Metadata
}

func validatePasswordHash(hash string) error {
	if hash == "" {
		return corerr.ErrPasswordHashRequired
	}
	if len(hash) != 60 {
		return corerr.ErrPasswordHashInvalidLength
	}
	if !consts.BcryptHashRegex.MatchString(hash) {
		return corerr.ErrPasswordHashInvalidFormat
	}
	return nil
}

func NewPasswordEntry(hash string, metadata Metadata) (PasswordEntry, error) {
	if err := validatePasswordHash(hash); err != nil {
		return PasswordEntry{}, err
	}
	return PasswordEntry{
		hash:     hash,
		metadata: metadata,
	}, nil
}

func (p PasswordEntry) Hash() string       { return p.hash }
func (p PasswordEntry) Metadata() Metadata { return p.metadata }

func (p PasswordEntry) Equals(other PasswordEntry) bool {
	return p.hash == other.hash && p.metadata == other.metadata
}

// MatchesPlaintext reports whether plain matches the stored bcrypt hash.
//
// Used by AccountService.checkPasswordReuse to enforce §2 (password history reuse policy).
// bcrypt.CompareHashAndPassword is the only correct comparison -- byte equality is always
// false for valid passwords because each bcrypt hash embeds a unique random salt.
func (p PasswordEntry) MatchesPlaintext(plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(p.hash), []byte(plain)) == nil
}
