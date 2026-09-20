package valobj

import (
	corerr "github.com/Reddetk/SayMoDev/identy-service/internal/core/coreErrors"
)

// GoogleClaims - Value Object, extracted from a verified Google ID token.
// All fields are immutable after construction.
// email_verified MUST be true before this VO is constructed (invariant 6 OAuth).
//
// Spec: IAM Registration — OAuth2 (Google), step [6]:
//
//	"email_verified: true — ОБЯЗАТЕЛЬНАЯ проверка до создания/входа в account"
type GoogleClaims struct {
	// sub is the google_uid: stable Google account identifier.
	// Must NOT be used as the only identifier — user may change their Google email.
	sub string

	// email is the Google account email at the time of login.
	// May change on the Google side; google_uid (sub) is the stable key.
	email string

	// name is the display name from the Google profile (free-form).
	name string

	// picture is the profile photo URL from Google.
	picture string

	// emailVerified mirrors the email_verified claim from the ID token.
	// MUST be true. Construction fails if false.
	emailVerified bool
}

// NewGoogleClaims constructs a validated GoogleClaims VO.
// Returns ErrOAuthEmailNotVerified if emailVerified is false.
// Returns ErrOAuthGoogleUIDEmpty if sub is empty.
// Returns ErrOAuthEmailEmpty if email is empty.
func NewGoogleClaims(sub, email, name, picture string, emailVerified bool) (GoogleClaims, error) {
	if !emailVerified {
		return GoogleClaims{}, corerr.ErrOAuthEmailNotVerified
	}
	if sub == "" {
		return GoogleClaims{}, corerr.ErrOAuthGoogleUIDEmpty
	}
	if email == "" {
		return GoogleClaims{}, corerr.ErrOAuthEmailEmpty
	}
	return GoogleClaims{
		sub:           sub,
		email:         email,
		name:          name,
		picture:       picture,
		emailVerified: emailVerified,
	}, nil
}

func (g GoogleClaims) Sub() string         { return g.sub }
func (g GoogleClaims) Email() string       { return g.email }
func (g GoogleClaims) Name() string        { return g.name }
func (g GoogleClaims) Picture() string     { return g.picture }
func (g GoogleClaims) EmailVerified() bool { return g.emailVerified }
