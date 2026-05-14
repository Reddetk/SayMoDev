package in

import context "context"

type SessionOperator interface {
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
