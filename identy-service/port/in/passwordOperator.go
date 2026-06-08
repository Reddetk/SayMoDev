package in

import (
	context "context"
)

type PasswordOperator interface {
	// ConfrimPasswordReset — POST //auth/password-reset (unauthenticated, OTP-gated)
	//
	// Flow:
	//  1. Constant-time OTP verification (§7)
	//  2. §2 Password history reuse check: O(5) PasswordEntry.MatchesPlaintext against stored history
	//  3. entity.ChangePassword performs T4 mass-revoke: rev++, all sessions cleared, returns revokedJTIs (ADR)
	//  4. ACID transaction: password update + rev + cleared sessions persisted via ResetPassword port
	//  5. Publish AccessTokenRevoked for every revoked jti (outbox -- async durable)
	//  6. Publish AccountPasswordResetCompleted (audit)
	//
	// §6: mass-revoke is mandatory -- a locked/reset account must not leave valid 30-day tokens outstanding.
	// §3: rev++ performed by entity; revokedJTIs returned and passed to events producer.
	//
	// plainNewPassword: raw password from the request -- used only for history reuse check (bcrypt.Compare).
	//   It is never stored, logged, or forwarded beyond this service method.
	// newPasswordHash: bcrypt hash produced by the HTTP handler -- stored in the account.
ConfrimPasswordReset(
		ctx context.Context,
		email string,
		otp string,
		plainNewPassword string,
		newPasswordHash string,
	) error

	// PasswordChange — POST //auth/password-change (authenticated)
	//
	// Flow:
	//  1. §2 Password history reuse check: O(5) PasswordEntry.MatchesPlaintext against stored history
	//  2. entity.ChangePassword performs T4 mass-revoke: rev++, all sessions cleared, returns revokedJTIs (ADR)
	//  3. ACID transaction: new hash + rev + cleared sessions persisted via ResetPassword port
	//  4. Publish AccessTokenRevoked for every revoked jti
	//  5. Publish AccountPasswordChanged (audit)
	//
	// §2: history reuse check uses PasswordEntry.MatchesPlaintext (bcrypt.Compare) -- not byte equality.
	// Caller (HTTP handler) is responsible for verifying the current password before calling this method.
	//
	// plainNewPassword: raw password from the request -- used only for history reuse check.
	// newPasswordHash: bcrypt hash produced by the HTTP handler -- stored in the account.
	PasswordChange(
		ctx context.Context,
		accountID string,
		plainNewPassword string,
		newPasswordHash string,
	) error
}
