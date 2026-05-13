package core

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"time"

	"github.com/Reddetk/SayMoDev/identy-service/port/out"

	"github.com/Reddetk/SayMoDev/identy-service/core/consts"
	corerr "github.com/Reddetk/SayMoDev/identy-service/core/coreErrors"
	valobj "github.com/Reddetk/SayMoDev/identy-service/core/valObj"

	"go.opentelemetry.io/otel"
)

type OTPService struct {
	otpRep   out.OtpRepository
	accRep   out.AccountRepository
	emailBox out.EmailBox
}

func NewOTPService(o out.OtpRepository, aR out.AccountRepository, e out.EmailBox) *OTPService {
	return &OTPService{o, aR, e}
}

var otpTracer = otel.Tracer("identy-service/core/otp")

func (s *OTPService) IssueRegistrationOTP(ctx context.Context, email string) error {
	return s.issueOTP(ctx, email, valobj.OTPPurposeRegistration, consts.OTPTTL)
}

func (s *OTPService) IssuePasswordResetOTP(ctx context.Context, email string) error {
	return s.issueOTP(ctx, email, valobj.OTPPurposePasswordReset, consts.OTPTTL)
}

func (s *OTPService) issueOTP(ctx context.Context, email string, purpose valobj.OTPPurpose, ttl time.Duration) error {
	ctx, span := otpTracer.Start(ctx, "OTPService.issueOTP")
	defer span.End()

	emailExists, err := s.accRep.EmailExist(ctx, email)
	if err != nil {
		return corerr.ErrAccountRepository
	}

	if emailExists {
		if err := s.otpRep.Immulate(ctx); err != nil {
			return corerr.ErrOTPRepository
		}
		return nil
	}

	code, err := codeForOTPGen()
	if err != nil {
		span.RecordError(err)
		return err
	}

	otp, err := valobj.NewOTP(code)
	if err != nil {
		span.RecordError(err)
		return err
	}

	verifyCode, err := valobj.NewVerificationCode(
		email,
		otp,
		purpose,
		time.Now().Add(ttl).UnixMilli(),
	)
	if err != nil {
		span.RecordError(err)
		return err
	}

	if err = s.otpRep.Upsert(ctx, verifyCode); err != nil {
		span.RecordError(err)
		return corerr.ErrOTPRepository
	}

	if err = s.emailBox.SendOTP(ctx, email, purpose, otp.Value()); err != nil {
		span.RecordError(err)
		return corerr.ErrEmailDeliveryFailed
	}

	return nil
}

// inner proceed
func codeForOTPGen() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}
