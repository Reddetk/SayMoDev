package valobj

import (
	corerr "github.com/Reddetk/SayMoDev/identy-service/core/coreErrors"
)

// RegistrationMethod - Value Object that encodes how an account was created.
// Used in the AccountRegistered event payload.
//
// Spec: IAM Event Contracts:
//   "AccountRegistered — registrationMethod: email|oauth2"
type RegistrationMethod string

const (
	RegistrationMethodEmail  RegistrationMethod = "email"
	RegistrationMethodOAuth2 RegistrationMethod = "oauth2"
)

// NewRegistrationMethod parses and validates a raw string.
// Returns ErrInvalidRegistrationMethod for unknown values.
func NewRegistrationMethod(raw string) (RegistrationMethod, error) {
	switch RegistrationMethod(raw) {
	case RegistrationMethodEmail, RegistrationMethodOAuth2:
		return RegistrationMethod(raw), nil
	default:
		return "", corerr.ErrInvalidRegistrationMethod
	}
}

func (m RegistrationMethod) String() string { return string(m) }
