package valobj

import (
	corerr "IAM/core/coreErrors"

	"github.com/google/uuid"
)

// AuthContext represents authentication context extracted from JWT
// Cross-BC VO: passed to business logic of BC#2-4
type AuthContext struct {
	accountID string
	role      Role
	sessionID string
	rev       int64
}

func validateAuthContext(accountID, sessionID string, role Role, rev int64) error {
	if accountID == "" {
		return corerr.ErrAccountIDRequired
	}
	if _, err := uuid.Parse(accountID); err != nil {
		return corerr.ErrAccountIDInvalid
	}

	if role == "" {
		return corerr.ErrRoleRequired
	}

	if sessionID == "" {
		return corerr.ErrSessionIDRequired
	}
	if _, err := uuid.Parse(sessionID); err != nil {
		return corerr.ErrSessionIDInvalid
	}

	if rev < 1 {
		return corerr.ErrInvalidRevision
	}

	return nil
}

// NewAuthContext accepts Role type -- caller must ParseRole() first
func NewAuthContext(accountID string, role Role, sessionID string, rev int64) (AuthContext, error) {
	if err := validateAuthContext(accountID, sessionID, role, rev); err != nil {
		return AuthContext{}, err
	}
	return AuthContext{
		accountID: accountID,
		role:      role,
		sessionID: sessionID,
		rev:       rev,
	}, nil
}

func (a AuthContext) AccountID() string { return a.accountID }
func (a AuthContext) Role() Role        { return a.role }
func (a AuthContext) SessionID() string { return a.sessionID }
func (a AuthContext) Rev() int64        { return a.rev }

func (a AuthContext) Equals(other AuthContext) bool {
	return a.accountID == other.accountID &&
		a.role == other.role &&
		a.sessionID == other.sessionID &&
		a.rev == other.rev
}
