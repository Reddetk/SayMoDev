package in

import context "context"

type AccountOperator interface {
	AdminGetAccountData(ctx context.Context, accountID string) (AccountDTO, error)

	AdminChangeAccountData(ctx context.Context, accountDTO AccountDTO) error

	// LockAccount — POST //admin/accounts/:id/lock
	//
	// §6 Lock Semantics: rev++ + all jti blacklisted + sessions deleted atomically.
	// Both brute-force auto-lock and admin-lock must follow the same T4 mass-revoke procedure.
	// RBAC (administrator role check) is enforced by the HTTP handler / middleware before reaching this method.
	// actorID is taken from JWT claims (token.sub), never from the request body (§ Audit).
	LockAccount(
		ctx context.Context,
		accountID string,
		until *int64,
		actorID string,
	) error

	// UnlockAccount — POST //admin/accounts/:id/unlock
	//
	// Restores status=active, clears lockedUntil.
	// Does NOT issue a new token — actor must re-authenticate.
	// RBAC and actorID sourcing follow the same rules as LockAccount.
	UnlockAccount(
		ctx context.Context,
		accountID string,
		actorID string,
	) error

	// SoftDelete — DELETE //admin/accounts/:id
	//
	// T4 Mass-Revoke: status=deleted, rev++, all sessions cleared.
	// Downstream cascade (BC#2 billing archive, BC#4 PII anonymisation) is driven by AccountDeleted event.
	// actorID is sourced from JWT claims in the calling layer.
	SoftDelete(
		ctx context.Context,
		accountID string,
		actorID string,
	) error
}

type AccountDTO struct {
	ID           string
	Email        string
	PersonalInfo string
	Role         string
	Status       string
	LockedUntil  *int64
	Metadata     string
	PasswordHash *string
	Rev          int64
}
