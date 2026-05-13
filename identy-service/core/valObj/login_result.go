package valobj

import (
	corerr "github.com/Reddetk/SayMoDev/identy-service/core/coreErrors"
)

// LoginResult - Value Object returned by AuthService after a successful
// authentication (email/password or OAuth2).
//
// Carries the minimum data required by the HTTP handler to build
// the response body and/or redirect URL.
//
// Spec: IAM §Login — Returns: { access_token }
//       IAM §Registration Step 2 — Returns: { access_token, account_id, role, session_id }
//       IAM §Registration — OAuth2 — Returns: { access_token, account_id, role }
//
// All fields are immutable after construction.
type LoginResult struct {
	accessToken string
	accountID   string
	role        string
	sessionID   string
}

// NewLoginResult constructs a validated LoginResult VO.
// Returns ErrLoginResultAccessTokenEmpty if accessToken is blank.
// Returns ErrLoginResultAccountIDEmpty if accountID is blank.
// Returns ErrLoginResultRoleEmpty if role is blank.
// Returns ErrLoginResultSessionIDEmpty if sessionID is blank.
func NewLoginResult(accessToken, accountID, role, sessionID string) (LoginResult, error) {
	if accessToken == "" {
		return LoginResult{}, corerr.ErrLoginResultAccessTokenEmpty
	}
	if accountID == "" {
		return LoginResult{}, corerr.ErrLoginResultAccountIDEmpty
	}
	if role == "" {
		return LoginResult{}, corerr.ErrLoginResultRoleEmpty
	}
	if sessionID == "" {
		return LoginResult{}, corerr.ErrLoginResultSessionIDEmpty
	}
	return LoginResult{
		accessToken: accessToken,
		accountID:   accountID,
		role:        role,
		sessionID:   sessionID,
	}, nil
}

func (r LoginResult) AccessToken() string { return r.accessToken }
func (r LoginResult) AccountID() string   { return r.accountID }
func (r LoginResult) Role() string        { return r.role }
func (r LoginResult) SessionID() string   { return r.sessionID }
