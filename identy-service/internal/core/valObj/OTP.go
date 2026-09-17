package valobj

import (
	"crypto/sha256"
	"encoding/hex"

	corerr "github.com/Reddetk/SayMoDev/identy-service/internal/core/coreErrors"
)

type OTP struct {
	value string
}

func NewOTP(value string) (OTP, error) {
	if err := validateOTP(value); err != nil {
		return OTP{}, err
	}
	return OTP{value: value}, nil
}

func validateOTP(value string) error {
	if len(value) != 6 {
		return corerr.ErrOTPValNotCorrectLen
	}
	for _, c := range value {
		if c < '0' || c > '9' {
			return corerr.ErrOTPValNotNumeric
		}
	}
	return nil
}
func (o OTP) Value() string { return o.value }
func (o OTP) Hash() string {
	h := sha256.Sum256([]byte(o.Value()))
	return hex.EncodeToString(h[:]) // h[:] конвертирует [32]byte → []byte
}
