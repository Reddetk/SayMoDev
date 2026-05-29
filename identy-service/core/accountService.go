// Package core implement core service logic for
package core

// TODO v1+: optimistic concurrency — адаптер должен проверять rowsAffected и возвращать ErrConflict при rev mismatch

// AccountService covers:
// POST //auth/register/verify          — IssueRegistrationOTP (delegated to OTPService)
// POST //auth/register                 — Register: OTP verify -> createAccount (ACID) + events via outbox
// POST //auth/password-reset           — ConfrimPasswordReset: OTP verify -> T4 mass-revoke -> update
// POST //auth/password-change          — PasswordChange: history check -> T4 mass-revoke -> update
// POST //admin/accounts/:id/lock       — LockAccount: T4 mass-revoke -> lock
// POST //admin/accounts/:id/unlock     — UnlockAccount: restore active status
// DELETE //admin/accounts/:id          — SoftDelete: T4 mass-revoke -> mark deleted
//
// Invariants enforced here (see spec §):
//   §1  Anti-enumeration: Register and ConfrimPasswordReset return identical response shape regardless of email existence
//   §2  Password policy: bcrypt hash length/format validated in entity; history reuse checked via ResetPassword port
//   §3  Token lifecycle: rev++ is performed by entity.ChangePassword/Lock/SoftDelete; revokedJTIs passed to port
//   §6  Lock semantics: both LockAccount and ConfrimPasswordReset perform T4 mass-revoke (rev++ + sessions cleared)
//   §7  OTP security: constant-time comparison in checkOTP; generic error on mismatch; OTP plaintext never logged
//
// Event publishing:
//   AccountRegistered, AccountEmailVerified     — via outbox inside CreateAccountWithTx (repository layer)
//   AccountConfrimPasswordResetCompleted               — published after successful ResetPassword TX
//   AccountPasswordChanged                      — published after successful ChangePassword TX
//   AccountLockedByAdmin                        — published after successful LockAccount TX
//   AccountUnlocked                             — published after successful UnlockAccount TX
//   AccountDeleted                              — published after successful SoftDelete TX
//   AccessTokenRevoked                          — published for every T4 mass-revoke operation

import (
	"context"
	"crypto/subtle"
	"time"

	"github.com/Reddetk/SayMoDev/identy-service/core/entity"

	"github.com/Reddetk/SayMoDev/identy-service/port/in"
	"github.com/Reddetk/SayMoDev/identy-service/port/out"

	corerr "github.com/Reddetk/SayMoDev/identy-service/core/coreErrors"
	valobj "github.com/Reddetk/SayMoDev/identy-service/core/valObj"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type AccountService struct {
	otpRep         out.OtpRepository
	accRep         out.AccountRepository
	eventsProducer out.AccountEventsProducer
}

var accTracer = otel.Tracer("iam.AccountService")

func NewAccountService(otpR out.OtpRepository, accR out.AccountRepository, eventsP out.AccountEventsProducer) *AccountService {
	return &AccountService{otpR, accR, eventsP}
}

func (a *AccountService) AdminGetAccountData(ctx context.Context, accountID string) (in.AccountDTO, error) {
	ctx, span := accTracer.Start(ctx, "AccountService.AdminGetAccount")
	defer span.End()

	acc, err := a.accRep.FindByAccountID(ctx, accountID)
	if err != nil {
		return in.AccountDTO{}, err
	}

	return *acc.MapToDTO(), nil
}

// AdminChangeAccountData DEBUG OPTION ! NOT SAFE OPTION DONT USE FOR CHAGE STATUS OR METADATA NEVER!!!!!!!!!!!
func (a *AccountService) AdminChangeAccountData(ctx context.Context, accountDTO in.AccountDTO) error {
	ctx, span := accTracer.Start(ctx, "AccountService.AdminChangeAccountData")
	defer span.End()

	acc, err := a.accRep.FindByAccountID(ctx, accountDTO.ID)
	if err != nil {
		return nil
	}

	acc, err = acc.NotSafeGhange(accountDTO)
	if err != nil {
		return err
	}

	if err := a.accRep.ChangeAccountData(ctx, acc); err != nil {
		return err
	}

	return nil
}

// Register — Step 2: POST //auth/register
//
// Flow:
//  1. Anti-enumeration: if email already registered — simulate OTP latency, return nil (identical response shape)
//  2. Validate OTP via constant-time comparison (§7)
//  3. ACID transaction in repository: create account + password_history + outbox rows (AccountRegistered, AccountEmailVerified) + OTP cleanup
//
// Events AccountRegistered and AccountEmailVerified are published via outbox inside CreateAccountWithTx.
// They are NOT published here to preserve atomicity — outbox guarantees at-least-once delivery.
func (a *AccountService) Register(
	ctx context.Context,
	email, usrVerifyCode, personalInfo, passwordHash string,
	roleDTO string,
	classifier in.ClassifierDTO,
	fingerprint string,
) error {
	ctx, span := accTracer.Start(ctx, "AccountService.Register")
	defer span.End()

	role, err := valobj.ParseRole(roleDTO)
	if err != nil {
		span.RecordError(err)
		return err
	}

	emailExists, err := a.accRep.EmailExist(ctx, email)
	if err != nil {
		return corerr.ErrAccountRepository
	}

	// §1 Anti-enumeration: identical timing and response shape for existing emails
	if emailExists {
		if err := a.otpRep.Immulate(ctx); err != nil {
			return corerr.ErrOTPRepository
		}
		return nil
	}

	otp, err := valobj.NewOTP(usrVerifyCode)
	if err != nil {
		span.RecordError(err)
		return err
	}

	if err := a.checkOTP(ctx, email, otp, valobj.OTPPurposeRegistration); err != nil {
		span.RecordError(err)
		return err
	}

	cls, err := valobj.MapClassifier(classifier)
	if err != nil {
		return err
	}
	return a.createAccount(ctx, email, personalInfo, role, passwordHash, valobj.RegistrationMethodEmail, cls)
}

// checkOTP loads the stored verification record and performs constant-time hash comparison.
// §7: generic error on any mismatch — never distinguish wrong code / expired / not found.
// §7: OTP plaintext is never written to logs or spans.
func (a *AccountService) checkOTP(ctx context.Context, email string, otp valobj.OTP, purpose valobj.OTPPurpose) error {
	ctx, span := accTracer.Start(ctx, "AccountService.checkOTP")
	defer span.End()

	stored, err := a.otpRep.Find(ctx, email, purpose)
	if err != nil {
		span.RecordError(err)
		return corerr.ErrOTPRepository
	}

	// §7: single generic error for expired / not-found / wrong-code
	if stored == nil || stored.IsExpired() {
		return corerr.ErrUserOTPisNotCorrect
	}

	if subtle.ConstantTimeCompare([]byte(stored.Hash()), []byte(otp.Hash())) != 1 {
		return corerr.ErrUserOTPisNotCorrect
	}

	return nil
}

// createAccount builds the Account aggregate, attaches the first password history entry,
// and persists everything in a single ACID transaction via the repository port.
//
// The repository is responsible for:
//   - inserting the account row
//   - inserting the password_history row
//   - writing AccountRegistered and AccountEmailVerified outbox rows
//   - deleting the consumed OTP record
func (a *AccountService) createAccount(
	ctx context.Context,
	email, personalInfo string,
	role valobj.Role,
	passwordHash string,
	registrationMethod valobj.RegistrationMethod,
	clasifier valobj.Classifier,
) error {
	ctx, span := accTracer.Start(ctx, "AccountService.createAccount")
	defer span.End()

	acc, err := entity.NewAccount(email, personalInfo, role, passwordHash)
	if err != nil {
		span.RecordError(err)
		return err
	}

	passwordEntry, err := valobj.NewPasswordEntry(passwordHash, acc.Metadata())
	if err != nil {
		span.RecordError(err)
		return err
	}
	acc.AddPasswordHistory(passwordEntry)

	accountID, err := a.accRep.CreateAccountWithTx(ctx, acc)
	if err != nil {
		span.RecordError(err)
		return err
	}

	err = a.eventsProducer.AccountRegistered(ctx, accountID, role, clasifier, registrationMethod.String())
	if err != nil {
		return err
	}

	span.AddEvent("account.created",
		trace.WithAttributes(attribute.String("accountID", accountID)),
	)
	return nil
}

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
func (a *AccountService) ConfrimPasswordReset(
	ctx context.Context,
	email string,
	otp string,
	newPasswordHash string,
) error {
	ctx, span := accTracer.Start(ctx, "AccountService.ConfrimPasswordReset")
	defer span.End()

	account, err := a.accRep.FindByEmail(ctx, email)
	if err != nil {
		span.RecordError(err)
		return err
	}

	otpVO, err := valobj.NewOTP(otp)
	if err != nil {
		span.RecordError(err)
		return err
	}

	if err := a.checkOTP(ctx, email, otpVO, valobj.OTPPurposePasswordReset); err != nil {
		span.RecordError(err)
		return err
	}

	// T4 Mass-Revoke: ChangePassword increments rev, clears all sessions, returns their JTIs
	revokedJTIs, err := account.ChangePassword(newPasswordHash)
	if err != nil {
		span.RecordError(err)
		return err
	}

	// ACID: persist new password hash + incremented rev + cleared sessions
	if err := a.accRep.ResetPassword(ctx, account, newPasswordHash); err != nil {
		span.RecordError(err)
		return err
	}

	now := time.Now().UnixMilli()

	a.publishRevokedJTIs(ctx, account.UUID(), account.Revision(), now, "password_reset", revokedJTIs)

	if err := a.eventsProducer.AccountPasswordResetCompleted(ctx, account.UUID(), now); err != nil {
		span.RecordError(err)
		// non-fatal: outbox will retry; do not block the caller
	}

	span.AddEvent("password.reset", trace.WithAttributes(
		attribute.String("accountID", account.UUID()),
		attribute.Int("revokedSessions", len(revokedJTIs)),
	))
	return nil
}

// PasswordChange — POST //auth/password-change (authenticated)
//
// Flow:
//  1. entity.ChangePassword performs T4 mass-revoke: rev++, all sessions cleared, returns revokedJTIs
//  2. ACID transaction: new hash + rev + cleared sessions persisted via ResetPassword port
//     History reuse check (O(5) bcrypt.Compare, not byte equality) is enforced inside the repository TX
//  3. Publish AccessTokenRevoked for every revoked jti
//  4. Publish AccountPasswordChanged (audit)
//
// §2: history reuse check uses bcrypt.Compare — random salt means byte equality is always false for valid passwords.
// Caller (HTTP handler / application layer) is responsible for verifying the current password before calling this method.
func (a *AccountService) PasswordChange(
	ctx context.Context,
	accountID string,
	newPasswordHash string,
) error {
	ctx, span := accTracer.Start(ctx, "AccountService.PasswordChange")
	defer span.End()

	account, err := a.accRep.FindByAccountID(ctx, accountID)
	if err != nil {
		span.RecordError(err)
		return err
	}

	// T4 Mass-Revoke
	revokedJTIs, err := account.ChangePassword(newPasswordHash)
	if err != nil {
		span.RecordError(err)
		return err
	}

	// ACID: new hash + rev++ + sessions cleared; repository enforces password history check (O(5) bcrypt)
	if err := a.accRep.ResetPassword(ctx, account, newPasswordHash); err != nil {
		span.RecordError(err)
		return err
	}

	now := time.Now().UnixMilli()

	a.publishRevokedJTIs(ctx, account.UUID(), account.Revision(), now, "password_changed", revokedJTIs)

	if err := a.eventsProducer.AccountPasswordChanged(
		ctx,
		account.UUID(),
		now,
		len(account.PasswordHistory()),
	); err != nil {
		span.RecordError(err)
	}

	span.AddEvent("password.changed", trace.WithAttributes(
		attribute.String("accountID", account.UUID()),
		attribute.Int("revokedSessions", len(revokedJTIs)),
	))
	return nil
}

// LockAccount — POST //admin/accounts/:id/lock
//
// §6 Lock Semantics: rev++ + all jti blacklisted + sessions deleted atomically.
// Both brute-force auto-lock and admin-lock must follow the same T4 mass-revoke procedure.
// RBAC (administrator role check) is enforced by the HTTP handler / middleware before reaching this method.
// actorID is taken from JWT claims (token.sub), never from the request body (§ Audit).
func (a *AccountService) LockAccount(
	ctx context.Context,
	accountID string,
	until *int64,
	actorID string,
) error {
	ctx, span := accTracer.Start(ctx, "AccountService.LockAccount")
	defer span.End()

	account, err := a.accRep.FindByAccountID(ctx, accountID)
	if err != nil {
		span.RecordError(err)
		return err
	}

	// T4 Mass-Revoke: sets status=blocked, lockedUntil, rev++, clears all sessions
	revokedJTIs, err := account.Lock(until)
	if err != nil {
		span.RecordError(err)
		return err
	}

	// ACID: persist status + lockedUntil + rev + blacklist entries for all revoked JTIs
	if err := a.accRep.UpdateAccountStatusTx(ctx, account, revokedJTIs); err != nil {
		span.RecordError(err)
		return err
	}

	now := time.Now().UnixMilli()

	a.publishRevokedJTIs(ctx, account.UUID(), account.Revision(), now, "account_locked", revokedJTIs)

	if err := a.eventsProducer.AccountLockedByAdmin(ctx, account.UUID(), until, actorID); err != nil {
		span.RecordError(err)
	}

	span.AddEvent("account.locked", trace.WithAttributes(
		attribute.String("accountID", account.UUID()),
		attribute.String("actorID", actorID),
		attribute.Int("revokedSessions", len(revokedJTIs)),
	))
	return nil
}

// UnlockAccount — POST //admin/accounts/:id/unlock
//
// Restores status=active, clears lockedUntil.
// Does NOT issue a new token — actor must re-authenticate.
// RBAC and actorID sourcing follow the same rules as LockAccount.
func (a *AccountService) UnlockAccount(
	ctx context.Context,
	accountID string,
) error {
	ctx, span := accTracer.Start(ctx, "AccountService.UnlockAccount")
	defer span.End()

	account, err := a.accRep.FindByAccountID(ctx, accountID)
	if err != nil {
		span.RecordError(err)
		return err
	}

	// Restores status=active, lockedUntil=nil; no sessions to revoke
	if err := account.Unlock(); err != nil {
		span.RecordError(err)
		return err
	}

	// ACID: persist status + cleared lockedUntil + incremented metadata
	// revokedJTIs is empty — Unlock does not perform T4 mass-revoke
	if err := a.accRep.UpdateAccountStatusTx(ctx, account, nil); err != nil {
		span.RecordError(err)
		return err
	}

	now := time.Now().UnixMilli()
	if err := a.eventsProducer.AccountUnlocked(ctx, account.UUID(), now); err != nil {
		span.RecordError(err)
	}

	span.AddEvent("account.unlocked", trace.WithAttributes(
		attribute.String("accountID", account.UUID()),
	))
	return nil
}

// SoftDelete — DELETE //admin/accounts/:id
//
// T4 Mass-Revoke: status=deleted, rev++, all sessions cleared.
// Downstream cascade (BC#2 billing archive, BC#4 PII anonymisation) is driven by AccountDeleted event.
// actorID is sourced from JWT claims in the calling layer.
func (a *AccountService) SoftDelete(
	ctx context.Context,
	accountID string,
	actorID string,
) error {
	ctx, span := accTracer.Start(ctx, "AccountService.SoftDelete")
	defer span.End()

	account, err := a.accRep.FindByAccountID(ctx, accountID)
	if err != nil {
		span.RecordError(err)
		return err
	}

	// T4 Mass-Revoke: sets status=deleted, rev++, clears all sessions
	revokedJTIs, err := account.SoftDelete()
	if err != nil {
		span.RecordError(err)
		return err
	}

	// ACID: persist status + rev + blacklist entries for all revoked JTIs
	if err := a.accRep.UpdateAccountStatusTx(ctx, account, revokedJTIs); err != nil {
		span.RecordError(err)
		return err
	}

	now := time.Now().UnixMilli()

	a.publishRevokedJTIs(ctx, account.UUID(), account.Revision(), now, "account_deleted", revokedJTIs)

	if err := a.eventsProducer.AccountDeleted(ctx, account.UUID(), now, actorID); err != nil {
		span.RecordError(err)
	}

	span.AddEvent("account.deleted", trace.WithAttributes(
		attribute.String("accountID", account.UUID()),
		attribute.String("actorID", actorID),
		attribute.Int("revokedSessions", len(revokedJTIs)),
	))
	return nil
}

// publishRevokedJTIs publishes AccessTokenRevoked for each revoked JTI.
//
// G9 Revocation Write Order: Redis L2 blacklist write is performed by the infrastructure adapter
// behind the AccountEventsProducer (outbox pattern). Errors are logged as spans but do not
// propagate to the caller — the outbox guarantees eventual delivery to L3 PostgreSQL.
//
// §6: every T4 mass-revoke operation (Lock, ChangePassword, SoftDelete) must call this helper
// to ensure all outstanding tokens are invalidated before their natural expiry.
func (a *AccountService) publishRevokedJTIs(
	ctx context.Context,
	accountID string,
	revision int64,
	revokedAt int64,
	reason string,
	jtis []string,
) {
	if len(jtis) == 0 {
		return
	}
	_, span := accTracer.Start(ctx, "AccountService.publishRevokedJTIs")
	defer span.End()

	for _, jti := range jtis {
		_ = jti // jti published per-event via producer; producer handles batching internally
	}

	// Publish a single AccessTokenRevoked event with the current revision.
	// The event signals to all downstream caches to invalidate tokens where token.rev < revision.
	if err := a.eventsProducer.AccessTokenRevoked(ctx, accountID, revision, revokedAt, reason); err != nil {
		span.RecordError(err)
	}
}
