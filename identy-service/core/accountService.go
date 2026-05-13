// Package core implement core service logic for IAM
package core

// POST /iam/auth/register - проверка OTP -> создание account + password_history + outbox (ACID) + OTP cleanup
//										|->отправка 200 (constant-time comparison, generic error on mismatch
// POST /iam/auth/password-reset, POST /iam/auth/password-change - rev++ + mass-revoke
// POST /iam/admin/accounts/:id/lock, POST /iam/admin/accounts/:id/unlock - rev++ + jti batch blacklist
// Публикует события: AccountRegistered, AccountPasswordChanged, AccountLockedByAdmin, AccountLockedByailedAttempts, AccountUnlocked, AccountEmailChangeRequested, AccountEmailVerifiedForChange

import (
	"IAM/core/entity"
	"IAM/port/out"
	"context"
	"crypto/subtle"
	"time"

	corerr "IAM/core/coreErrors"

	valobj "IAM/core/valObj"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type AccountService struct {
	otpRep         out.OtpRepository
	accRep         out.AccountRepository
	eventsProducer out.AccountEventsProducer
}

var accTracer = otel.Tracer("iam/core/account")

func NewAccountService(otpR out.OtpRepository, accR out.AccountRepository, eventsP out.AccountEventsProducer) *AccountService {
	return &AccountService{otpR, accR, eventsP}
}

// Register — Step 2: POST /iam/auth/register
// Верифицирует OTP, затем создаёт account + password_history + outbox (ACID) + OTP cleanup
func (a *AccountService) Register(
	ctx context.Context,
	email, usrVerifyCode, personalInfo, passwordHash string,
	role valobj.Role,
	classifier valobj.Classifier,
	fingerprint string,
) error {
	ctx, span := accTracer.Start(ctx, "AccountService.Register")
	defer span.End()

	emailExists, err := a.accRep.EmailExist(ctx, email)
	if err != nil {
		return corerr.ErrAccountRepository
	}

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

	a.eventsProducer.AccountRegistered(ctx, email, role, classifier, "email")

	return a.createAccount(ctx, email, personalInfo, role, passwordHash)
}

// checkOTP — загружает запись из репозитория и верифицирует в домене.
// Использует IsExpired() и constant-time hash comparison (OTP Security Invariant §7).
func (a *AccountService) checkOTP(ctx context.Context, email string, otp valobj.OTP, purpose valobj.OTPPurpose) error {
	ctx, span := accTracer.Start(ctx, "AccountService.checkOTP")
	defer span.End()

	stored, err := a.otpRep.Find(ctx, email, purpose)
	if err != nil {
		span.RecordError(err)
		return corerr.ErrOTPRepository
	}

	if stored == nil || stored.IsExpired() {
		return corerr.ErrUserOTPisNotCorrect
	}

	if subtle.ConstantTimeCompare([]byte(stored.Hash()), []byte(otp.Hash())) != 1 {
		return corerr.ErrUserOTPisNotCorrect
	}
	a.eventsProducer.AccountEmailVerified(ctx, email, time.Now().UnixMilli())
	return nil
}

func (a *AccountService) createAccount(
	ctx context.Context,
	email, personalInfo string,
	role valobj.Role,
	passwordHash string,
) error {
	ctx, span := accTracer.Start(ctx, "AccountService.createAccount")
	defer span.End()

	// Step 1: Create account aggregate
	acc, err := entity.NewAccount(email, personalInfo, role, passwordHash)
	if err != nil {
		span.RecordError(err)
		return err
	}

	// Step 2: Create password history entry and add to account
	passwordEntry, err := valobj.NewPasswordEntry(passwordHash, acc.Metadata())
	if err != nil {
		span.RecordError(err)
		return err
	}
	acc.AddPasswordHistory(passwordEntry)

	// Step 3: ACID transaction (inside repository):
	//   - Save account aggregate (with password history)
	//   - Create outbox events (AccountRegistered, AccountEmailVerified)
	//   - Clean up OTP
	//   - All or nothing guarantee
	accountID, err := a.accRep.CreateAccountWithTx(ctx, acc)
	if err != nil {
		span.RecordError(err)
		return err
	}

	span.AddEvent("account.created",
		trace.WithAttributes(attribute.String("accountID", accountID)),
	)
	return nil
}

func (a *AccountService) PasswordReset(ctx context.Context, account *entity.Account, otp valobj.OTP) error {
	ctx, span := accTracer.Start(ctx, "AccountService.PasswordReset")
	defer span.End()
	if err := a.checkOTP(ctx, account.Email(), otp, valobj.OTPPurposePasswordReset); err != nil {
		span.RecordError(err)
		return err
	}
	// rev++
	// mass-revoke
	// publish event AccountPasswordChanged
	return nil
}

func (a *AccountService) revoke

func (a *AccountService) PasswordChange(ctx context.Context, email, newPasswordHash string) error {
	return nil
}
