package in

import context "context"

type SessionOperator interface {
	AdminGetSessions(ctx context.Context, AccID string) ([]SessionDTO, error)

	Logout(
		ctx context.Context,
		accountID string,
		sessionID string,
	) error

	AdminTerminateSession(
		ctx context.Context,
		targetAccountID string,
		sessionID string,
		adminID string,
	) error
}

type SessionDTO struct {
	SessionID    string
	Fingerprint  string
	LastActivity string
	Metadata     string
}
