package in

import (
	context "context"

	"github.com/Reddetk/SayMoDev/identy-service/core/entity"
	valobj "github.com/Reddetk/SayMoDev/identy-service/core/valObj"
)

type PasswordOperator interface {
	// ConfrimPasswordReset — POST //auth/password-reset (unauthenticated, OTP-gated)
	//
	// Flow:
	//  1. Constant-time OTP verification (§7)
	//  2. entity.ChangePassword performs T4 mass-revoke: rev++, all sessions cleared, returns revokedJTIs (ADR)
	//  3. ACID transaction: password update + rev + cleared sessions persisted via ResetPassword port
	//  4. Publish AccessTokenRevoked for every revoked jti (outbox — async durable)
	//  5. Publish AccountPasswordResetCompleted (audit)
	//
	// §6: mass-revoke is mandatory — a locked/reset account must not leave valid 30-day tokens outstanding.
	// §3: rev++ performed by entity; revokedJTIs returned and passed to events producer.
	ConfrimPasswordReset(
		ctx context.Context,
		account *entity.Account,
		otp valobj.OTP,
		newPasswordHash string,
	) error

	// PasswordChange — POST //auth/password-change (authenticated)
	//
	// Flow:
	//  1. entity.ChangePassword performs T4 mass-revoke: rev++, all sessions cleared, returns revokedJTIs (ADR)
	//  2. ACID transaction: new hash + rev + cleared sessions persisted via ResetPassword port
	//     History reuse check (O(5) bcrypt.Compare, not byte equality) is enforced inside the repository TX
	//  3. Publish AccessTokenRevoked for every revoked jti
	//  4. Publish AccountPasswordChanged (audit)
	//
	// §2: history reuse check uses bcrypt.Compare — random salt means byte equality is always false for valid passwords.
	// Caller (HTTP handler / application layer) is responsible for verifying the current password before calling this method.
	PasswordChange(
		ctx context.Context,
		accountID string,
		newPasswordHash string,
	) error
}
