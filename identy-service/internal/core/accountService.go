// Package core implement core service logic for
package core

// TODO v1+: optimistic concurrency  адаптер должен проверять rowsAffected и возвращать ErrConflict при rev mismatch

// AccountService covers:
// POST //auth/register/verify           IssueRegistrationOTP (delegated to OTPService)
// POST //auth/register                  Register: OTP verify -> createAccount (ACID) + events via outbox
// POST //auth/password-reset            ConfrimPasswordReset: OTP verify -> reuse check -> T4 mass-revoke -> update
// POST //auth/password-change           PasswordChange: reuse check -> T4 mass-revoke -> update
// POST //admin/accounts/:id/lock        LockAccount: T4 mass-revoke -> lock
// POST //admin/accounts/:id/unlock      UnlockAccount: restore active status
// DELETE //admin/accounts/:id           SoftDelete: T4 mass-revoke -> mark deleted
//
// Invariants enforced here (see spec ):
//   1  Anti-enumeration: Register and ConfrimPasswordReset return identical response shape regardless of email existence
//   2  Password policy: bcrypt hash length/format validated in entity;
//       history reuse checked here via PasswordEntry.MatchesPlaintext (O(5) bcrypt.Compare)
//   3  Token lifecycle: rev++ is performed by entity.ChangePassword/Lock/SoftDelete; revokedJTIs passed to port
//   6  Lock semantics: both LockAccount and ConfrimPasswordReset perform T4 mass-revoke (rev++ + sessions cleared)
//   7  OTP security: constant-time comparison in checkOTP; generic error on mismatch; OTP plaintext never logged
//   G9 Revocation write order: after each *Tx commit, SetAccountRev writes new rev to Redis L2 (non-fatal on error)
//
// Event publishing:
//   AccountRegistered, AccountEmailVerified      via outbox inside CreateAccountWithTx (repository layer)
//   AccountConfrimPasswordResetCompleted                published after successful ResetPassword TX
//   AccountPasswordChanged                       published after successful ChangePassword TX
//   AccountLockedByAdmin                         published after successful LockAccount TX
//   AccountUnlocked                              published after successful UnlockAccount TX
//   AccountDeleted                               published after successful SoftDelete TX
//   AccessTokenRevoked                           published for every T4 mass-revoke operation

import (
	"context"
	"crypto/subtle"
	"time"

	"github.com/Reddetk/SayMoDev/identy-service/internal/core/entity"
	"github.com/Reddetk/SayMoDev/identy-service/internal/logger"

	"github.com/Reddetk/SayMoDev/identy-service/internal/port/in"
	"github.com/Reddetk/SayMoDev/identy-service/internal/port/out"

	corerr "github.com/Reddetk/SayMoDev/identy-service/internal/core/coreErrors"
	valobj "github.com/Reddetk/SayMoDev/identy-service/internal/core/valObj"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type AccountService struct {
	otpRep         out.OtpRepository
	accRep         out.AccountRepository
	eventsProducer out.AccountEventsProducer
	tokenBlacklist out.TokenBlacklist
	log            logger.Logger
}

var accTracer = otel.Tracer("iam.AccountService")

func NewAccountService(
	otpR out.OtpRepository,
	accR out.AccountRepository,
	eventsP out.AccountEventsProducer,
	tokenBL out.TokenBlacklist,
	log logger.Logger,
) *AccountService {
	return &AccountService{otpR, accR, eventsP, tokenBL, log}
}

func (a *AccountService) AdminGetAccountData(ctx context.Context, accountID string) (in.AccountDTO, error) {
	ctx, span := accTracer.Start(ctx, "AccountService.AdminGetAccount")
	defer span.End()

	acc, err := a.accRep.FindByAccountID(ctx, accountID)
	if err != nil {
		a.log.Error("account.adminGet: FindByAccountID failed",
			logger.String("account_id", accountID),
			logger.Error(err),
		)
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

// Register  Step 2: POST //auth/register
func (a *AccountService) Register(
	ctx context.Context,
	email, usrVerifyCode, personalInfo, passwordHash string,
	roleDTO string,
	classifier in.ClassifierDTO,
	fingerprint string,
) error {
	ctx, span := accTracer.Start(ctx, "AccountService.Register")
	defer span.End()

	log := a.log.With(logger.String("op", "register"))

	role, err := valobj.ParseRole(roleDTO)
	if err != nil {
		span.RecordError(err)
		log.Warn("account.register: invalid role", logger.String("role", roleDTO), logger.Error(err))
		return err
	}

	emailExists, err := a.accRep.EmailExist(ctx, email)
	if err != nil {
		log.Error("account.register: EmailExist failed", logger.Error(err))
		return corerr.ErrAccountRepository
	}

	// 1 Anti-enumeration
	if emailExists {
		log.Debug("account.register: email already exists, simulating (anti-enumeration)")
		if err := a.otpRep.Immulate(ctx); err != nil {
			log.Error("account.register: Immulate failed", logger.Error(err))
			return corerr.ErrOTPRepository
		}
		return nil
	}

	otp, err := valobj.NewOTP(usrVerifyCode)
	if err != nil {
		span.RecordError(err)
		log.Warn("account.register: invalid OTP format", logger.Error(err))
		return err
	}

	if err := a.checkOTP(ctx, email, otp, valobj.OTPPurposeRegistration); err != nil {
		span.RecordError(err)
		log.Warn("account.register: OTP check failed", logger.Error(err))
		return err
	}

	cls, err := valobj.MapClassifier(classifier)
	if err != nil {
		log.Warn("account.register: invalid classifier", logger.Error(err))
		return err
	}
	return a.createAccount(ctx, email, personalInfo, role, passwordHash, valobj.RegistrationMethodEmail, cls)
}

func (a *AccountService) checkOTP(ctx context.Context, email string, otp valobj.OTP, purpose valobj.OTPPurpose) error {
	ctx, span := accTracer.Start(ctx, "AccountService.checkOTP")
	defer span.End()

	stored, err := a.otpRep.Find(ctx, email, purpose)
	if err != nil {
		span.RecordError(err)
		a.log.Error("account.checkOTP: Find failed", logger.Error(err))
		return corerr.ErrOTPRepository
	}

	if stored == nil || stored.IsExpired() {
		a.log.Debug("account.checkOTP: OTP not found or expired")
		return corerr.ErrUserOTPisNotCorrect
	}

	if subtle.ConstantTimeCompare([]byte(stored.Hash()), []byte(otp.Hash())) != 1 {
		a.log.Debug("account.checkOTP: hash mismatch")
		return corerr.ErrUserOTPisNotCorrect
	}

	return nil
}

func (a *AccountService) checkPasswordReuse(history []valobj.PasswordEntry, plainNewPassword string) error {
	for _, entry := range history {
		if entry.MatchesPlaintext(plainNewPassword) {
			return corerr.ErrPasswordReused
		}
	}
	return nil
}

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

	log := a.log.With(logger.String("op", "createAccount"))

	acc, err := entity.NewAccount(email, personalInfo, role, passwordHash)
	if err != nil {
		span.RecordError(err)
		log.Error("account.create: NewAccount failed", logger.Error(err))
		return err
	}

	passwordEntry, err := valobj.NewPasswordEntry(passwordHash, acc.Metadata())
	if err != nil {
		span.RecordError(err)
		log.Error("account.create: NewPasswordEntry failed", logger.Error(err))
		return err
	}
	acc.AddPasswordHistory(passwordEntry)

	accountID, err := a.accRep.CreateAccountWithTx(ctx, acc)
	if err != nil {
		span.RecordError(err)
		log.Error("account.create: CreateAccountWithTx failed", logger.Error(err))
		return err
	}

	err = a.eventsProducer.AccountRegistered(ctx, accountID, role, clasifier, registrationMethod.String())
	if err != nil {
		log.Error("account.create: AccountRegistered event failed",
			logger.String("account_id", accountID),
			logger.Error(err),
		)
		return err
	}

	log.Info("account.create: account created", logger.String("account_id", accountID))
	span.AddEvent("account.created",
		trace.WithAttributes(attribute.String("accountID", accountID)),
	)
	return nil
}

// ConfrimPasswordReset  POST //auth/password-reset
func (a *AccountService) ConfrimPasswordReset(
	ctx context.Context,
	email string,
	otp string,
	plainNewPassword string,
	newPasswordHash string,
) error {
	ctx, span := accTracer.Start(ctx, "AccountService.ConfrimPasswordReset")
	defer span.End()

	log := a.log.With(logger.String("op", "passwordReset"))

	account, err := a.accRep.FindByEmail(ctx, email)
	if err != nil {
		span.RecordError(err)
		log.Error("account.passwordReset: FindByEmail failed", logger.Error(err))
		return err
	}

	otpVO, err := valobj.NewOTP(otp)
	if err != nil {
		span.RecordError(err)
		log.Warn("account.passwordReset: invalid OTP format", logger.Error(err))
		return err
	}

	if err := a.checkOTP(ctx, email, otpVO, valobj.OTPPurposePasswordReset); err != nil {
		span.RecordError(err)
		log.Warn("account.passwordReset: OTP check failed", logger.Error(err))
		return err
	}

	if err := a.checkPasswordReuse(account.PasswordHistory(), plainNewPassword); err != nil {
		span.RecordError(err)
		log.Warn("account.passwordReset: password reuse detected",
			logger.String("account_id", account.UUID()),
		)
		return err
	}

	revokedJTIs, err := account.ChangePassword(newPasswordHash)
	if err != nil {
		span.RecordError(err)
		log.Error("account.passwordReset: ChangePassword failed", logger.Error(err))
		return err
	}

	if err := a.accRep.ResetPassword(ctx, account, newPasswordHash); err != nil {
		span.RecordError(err)
		log.Error("account.passwordReset: ResetPassword TX failed",
			logger.String("account_id", account.UUID()),
			logger.Error(err),
		)
		return err
	}

	a.setAccountRevOrWarn(ctx, span, account.UUID(), account.Revision())

	now := time.Now().UnixMilli()
	a.publishRevokedJTIs(ctx, account.UUID(), account.Revision(), now, "password_reset", revokedJTIs)

	if err := a.eventsProducer.AccountPasswordResetCompleted(ctx, account.UUID(), now); err != nil {
		span.RecordError(err)
		log.Warn("account.passwordReset: AccountPasswordResetCompleted event failed",
			logger.String("account_id", account.UUID()),
			logger.Error(err),
		)
	}

	log.Info("account.passwordReset: completed",
		logger.String("account_id", account.UUID()),
		logger.Int("revoked_sessions", len(revokedJTIs)),
	)
	span.AddEvent("password.reset", trace.WithAttributes(
		attribute.String("accountID", account.UUID()),
		attribute.Int("revokedSessions", len(revokedJTIs)),
	))
	return nil
}

// PasswordChange  POST //auth/password-change (authenticated)
func (a *AccountService) PasswordChange(
	ctx context.Context,
	accountID string,
	plainNewPassword string,
	newPasswordHash string,
) error {
	ctx, span := accTracer.Start(ctx, "AccountService.PasswordChange")
	defer span.End()

	log := a.log.With(logger.String("account_id", accountID), logger.String("op", "passwordChange"))

	account, err := a.accRep.FindByAccountID(ctx, accountID)
	if err != nil {
		span.RecordError(err)
		log.Error("account.passwordChange: FindByAccountID failed", logger.Error(err))
		return err
	}

	if err := a.checkPasswordReuse(account.PasswordHistory(), plainNewPassword); err != nil {
		span.RecordError(err)
		log.Warn("account.passwordChange: password reuse detected")
		return err
	}

	revokedJTIs, err := account.ChangePassword(newPasswordHash)
	if err != nil {
		span.RecordError(err)
		log.Error("account.passwordChange: ChangePassword failed", logger.Error(err))
		return err
	}

	if err := a.accRep.ResetPassword(ctx, account, newPasswordHash); err != nil {
		span.RecordError(err)
		log.Error("account.passwordChange: ResetPassword TX failed", logger.Error(err))
		return err
	}

	a.setAccountRevOrWarn(ctx, span, account.UUID(), account.Revision())

	now := time.Now().UnixMilli()
	a.publishRevokedJTIs(ctx, account.UUID(), account.Revision(), now, "password_changed", revokedJTIs)

	if err := a.eventsProducer.AccountPasswordChanged(
		ctx,
		account.UUID(),
		now,
		len(account.PasswordHistory()),
	); err != nil {
		span.RecordError(err)
		log.Warn("account.passwordChange: AccountPasswordChanged event failed", logger.Error(err))
	}

	log.Info("account.passwordChange: completed",
		logger.Int("revoked_sessions", len(revokedJTIs)),
	)
	span.AddEvent("password.changed", trace.WithAttributes(
		attribute.String("accountID", account.UUID()),
		attribute.Int("revokedSessions", len(revokedJTIs)),
	))
	return nil
}

// LockAccount  POST //admin/accounts/:id/lock
func (a *AccountService) LockAccount(
	ctx context.Context,
	accountID string,
	until *int64,
	actorID string,
) error {
	ctx, span := accTracer.Start(ctx, "AccountService.LockAccount")
	defer span.End()

	log := a.log.With(
		logger.String("account_id", accountID),
		logger.String("actor_id", actorID),
		logger.String("op", "lockAccount"),
	)

	account, err := a.accRep.FindByAccountID(ctx, accountID)
	if err != nil {
		span.RecordError(err)
		log.Error("account.lock: FindByAccountID failed", logger.Error(err))
		return err
	}

	revokedJTIs, err := account.Lock(until)
	if err != nil {
		span.RecordError(err)
		log.Warn("account.lock: Lock entity method failed", logger.Error(err))
		return err
	}

	if err := a.accRep.UpdateAccountStatusTx(ctx, account, revokedJTIs, actorID); err != nil {
		span.RecordError(err)
		log.Error("account.lock: UpdateAccountStatusTx failed", logger.Error(err))
		return err
	}

	a.setAccountRevOrWarn(ctx, span, account.UUID(), account.Revision())

	now := time.Now().UnixMilli()
	a.publishRevokedJTIs(ctx, account.UUID(), account.Revision(), now, "account_locked", revokedJTIs)

	if err := a.eventsProducer.AccountLockedByAdmin(ctx, account.UUID(), until, actorID); err != nil {
		span.RecordError(err)
		log.Warn("account.lock: AccountLockedByAdmin event failed", logger.Error(err))
	}

	log.Info("account.lock: account locked",
		logger.Int("revoked_sessions", len(revokedJTIs)),
	)
	span.AddEvent("account.locked", trace.WithAttributes(
		attribute.String("accountID", account.UUID()),
		attribute.String("actorID", actorID),
		attribute.Int("revokedSessions", len(revokedJTIs)),
	))
	return nil
}

// UnlockAccount  POST //admin/accounts/:id/unlock
func (a *AccountService) UnlockAccount(
	ctx context.Context,
	accountID string,
	actorID string,
) error {
	ctx, span := accTracer.Start(ctx, "AccountService.UnlockAccount")
	defer span.End()

	log := a.log.With(
		logger.String("account_id", accountID),
		logger.String("actor_id", actorID),
		logger.String("op", "unlockAccount"),
	)

	account, err := a.accRep.FindByAccountID(ctx, accountID)
	if err != nil {
		span.RecordError(err)
		log.Error("account.unlock: FindByAccountID failed", logger.Error(err))
		return err
	}

	if err := account.Unlock(); err != nil {
		span.RecordError(err)
		log.Warn("account.unlock: Unlock entity method failed", logger.Error(err))
		return err
	}

	if err := a.accRep.UpdateAccountStatusTx(ctx, account, nil, actorID); err != nil {
		span.RecordError(err)
		log.Error("account.unlock: UpdateAccountStatusTx failed", logger.Error(err))
		return err
	}

	now := time.Now().UnixMilli()
	if err := a.eventsProducer.AccountUnlocked(ctx, account.UUID(), now); err != nil {
		span.RecordError(err)
		log.Warn("account.unlock: AccountUnlocked event failed", logger.Error(err))
	}

	log.Info("account.unlock: account unlocked")
	span.AddEvent("account.unlocked", trace.WithAttributes(
		attribute.String("accountID", account.UUID()),
	))
	return nil
}

// SoftDelete  DELETE //admin/accounts/:id
func (a *AccountService) SoftDelete(
	ctx context.Context,
	accountID string,
	actorID string,
) error {
	ctx, span := accTracer.Start(ctx, "AccountService.SoftDelete")
	defer span.End()

	log := a.log.With(
		logger.String("account_id", accountID),
		logger.String("actor_id", actorID),
		logger.String("op", "softDelete"),
	)

	account, err := a.accRep.FindByAccountID(ctx, accountID)
	if err != nil {
		span.RecordError(err)
		log.Error("account.delete: FindByAccountID failed", logger.Error(err))
		return err
	}

	revokedJTIs, err := account.SoftDelete()
	if err != nil {
		span.RecordError(err)
		log.Error("account.delete: SoftDelete entity method failed", logger.Error(err))
		return err
	}

	if err := a.accRep.UpdateAccountStatusTx(ctx, account, revokedJTIs, actorID); err != nil {
		span.RecordError(err)
		log.Error("account.delete: UpdateAccountStatusTx failed", logger.Error(err))
		return err
	}

	a.setAccountRevOrWarn(ctx, span, account.UUID(), account.Revision())

	now := time.Now().UnixMilli()
	a.publishRevokedJTIs(ctx, account.UUID(), account.Revision(), now, "account_deleted", revokedJTIs)

	if err := a.eventsProducer.AccountDeleted(ctx, account.UUID(), now, actorID); err != nil {
		span.RecordError(err)
		log.Warn("account.delete: AccountDeleted event failed", logger.Error(err))
	}

	log.Info("account.delete: account deleted",
		logger.Int("revoked_sessions", len(revokedJTIs)),
	)
	span.AddEvent("account.deleted", trace.WithAttributes(
		attribute.String("accountID", account.UUID()),
		attribute.String("actorID", actorID),
		attribute.Int("revokedSessions", len(revokedJTIs)),
	))
	return nil
}

// publishRevokedJTIs publishes AccessTokenRevoked for each revoked JTI.
func (a *AccountService) publishRevokedJTIs(
	ctx context.Context,
	accountID string,
	rev int64,
	now int64,
	reason string,
	revokedJTIs []string,
) {
	for _, jti := range revokedJTIs {
		if err := a.eventsProducer.AccessTokenRevoked(ctx, accountID, rev, now, reason); err != nil {
			a.log.Warn("account.publishRevokedJTIs: AccessTokenRevoked failed",
				logger.String("account_id", accountID),
				logger.String("jti", jti),
				logger.String("reason", reason),
				logger.Error(err),
			)
			_, span := accTracer.Start(ctx, "AccountService.publishRevokedJTIs.warn")
			span.RecordError(err)
			span.SetAttributes(
				attribute.String("jti", jti),
				attribute.String("accountID", accountID),
				attribute.String("reason", reason),
			)
			span.End()
		}
	}
}

// setAccountRevOrWarn writes the new rev to Redis L2 after a successful T4 mass-revoke transaction.
func (a *AccountService) setAccountRevOrWarn(
	ctx context.Context,
	span trace.Span,
	accountID string,
	rev int64,
) {
	if err := a.tokenBlacklist.SetAccountRev(ctx, accountID, rev); err != nil {
		a.log.Warn("account.setAccountRev: SetAccountRev failed, degraded to L3 lookup",
			logger.String("account_id", accountID),
			logger.Int64("rev", rev),
			logger.Error(err),
		)
		span.RecordError(err)
		span.SetAttributes(
			attribute.String("warn", "SetAccountRev failed; degraded to L3 lookup"),
			attribute.String("accountID", accountID),
			attribute.Int64("rev", rev),
		)
	}
}
