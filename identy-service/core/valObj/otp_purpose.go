package valobj

import corerr "github.com/Reddetk/SayMoDev/identy-service/core/coreErrors"

type OTPPurpose string

const (
	OTPPurposeRegistration  OTPPurpose = "registration"
	OTPPurposePasswordReset OTPPurpose = "password_reset"
	OTPPurposeEmailChange   OTPPurpose = "email_change"
)

func ParseOTPPurpose(value string) (OTPPurpose, error) {
	p := OTPPurpose(value)
	switch p {
	case OTPPurposeRegistration, OTPPurposePasswordReset, OTPPurposeEmailChange:
		return p, nil
	default:
		return "", corerr.ErrInvalidOTPPurpose
	}
}

func (p OTPPurpose) String() string { return string(p) }
