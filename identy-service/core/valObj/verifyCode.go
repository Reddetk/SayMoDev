package valobj

import (
	"time"

	"github.com/Reddetk/SayMoDev/identy-service/core/consts"

	corerr "github.com/Reddetk/SayMoDev/identy-service/core/coreErrors"
)

// VerificationCode represents a one-time verification code record.
// Not part of Account aggregate -- independent lifecycle tied to a use-case.
type VerificationCode struct {
	email     string
	hash      string // SHA256 hex of plaintext OTP, never the plaintext itself
	purpose   OTPPurpose
	expiresAt int64 // Unix milliseconds
}

func validateNewVerificationCodeArgs(email string, purpose OTPPurpose, expiresAt int64) error {
	if email == "" {
		return corerr.ErrEmailRequired
	}
	if len(email) > consts.MaxEmailLength {
		return corerr.ErrEmailTooLong
	}
	if !consts.EmailRegex.MatchString(email) {
		return corerr.ErrInvalidEmail
	}

	if purpose == "" {
		return corerr.ErrInvalidOTPPurpose
	}

	if expiresAt <= time.Now().UnixMilli() {
		return corerr.ErrOTPAlreadyExpired
	}

	return nil
}

func validateRestoreVerificationCodeArgs(id, email, hash string, purpose OTPPurpose) error {
	if id == "" {
		return corerr.ErrVerificationCodeIDRequired
	}
	if email == "" {
		return corerr.ErrEmailRequired
	}
	if len(email) > consts.MaxEmailLength {
		return corerr.ErrEmailTooLong
	}
	if !consts.EmailRegex.MatchString(email) {
		return corerr.ErrInvalidEmail
	}
	if hash == "" {
		return corerr.ErrOTPHashRequired
	}
	if len(hash) != consts.SHA256HexLen {
		return corerr.ErrOTPHashInvalidLength
	}
	if purpose == "" {
		return corerr.ErrInvalidOTPPurpose
	}
	return nil
}

// NewVerificationCode creates new record -- hashes OTP internally
func NewVerificationCode(email string, otp OTP, purpose OTPPurpose, expiresAt int64) (*VerificationCode, error) {
	if err := validateNewVerificationCodeArgs(email, purpose, expiresAt); err != nil {
		return nil, err
	}

	return &VerificationCode{
		email:     email,
		hash:      otp.Hash(),
		purpose:   purpose,
		expiresAt: expiresAt,
	}, nil
}

// RestoreVerificationCode rebuilds record from persistence -- used by repository mapper
func RestoreVerificationCode(id, email, hash string, purpose OTPPurpose, expiresAt int64) (*VerificationCode, error) {
	if err := validateRestoreVerificationCodeArgs(id, email, hash, purpose); err != nil {
		return nil, err
	}
	// expiresAt не валидируется -- запись в БД может быть просроченной,
	// сервис проверит IsExpired() и обработает сам
	return &VerificationCode{
		email:     email,
		hash:      hash,
		purpose:   purpose,
		expiresAt: expiresAt,
	}, nil
}

// IsExpired checks if code has passed its TTL
func (v *VerificationCode) IsExpired() bool {
	return time.Now().UnixMilli() > v.expiresAt
}

func (v *VerificationCode) Email() string       { return v.email }
func (v *VerificationCode) Hash() string        { return v.hash }
func (v *VerificationCode) Purpose() OTPPurpose { return v.purpose }
func (v *VerificationCode) ExpiresAt() int64    { return v.expiresAt }
