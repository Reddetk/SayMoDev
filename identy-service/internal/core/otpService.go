package core

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"time"

	"github.com/Reddetk/SayMoDev/identy-service/internal/logger"
	"github.com/Reddetk/SayMoDev/identy-service/internal/port/out"

	"github.com/Reddetk/SayMoDev/identy-service/internal/core/consts"
	corerr "github.com/Reddetk/SayMoDev/identy-service/internal/core/coreErrors"
	valobj "github.com/Reddetk/SayMoDev/identy-service/internal/core/valObj"

	"go.opentelemetry.io/otel"
)

type OTPService struct {
	otpRep   out.OtpRepository
	accRep   out.AccountRepository
	emailBox out.EmailBox
	log      logger.Logger
}

func NewOTPService(o out.OtpRepository, aR out.AccountRepository, e out.EmailBox, log logger.Logger) *OTPService {
	return &OTPService{o, aR, e, log}
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

	log := s.log.With(logger.String("purpose", string(purpose)))

	emailExists, err := s.accRep.EmailExist(ctx, email)
	if err != nil {
		log.Error("otp.issue: EmailExist failed", logger.Error(err))
		return corerr.ErrAccountRepository
	}

	// Anti-enumeration: email not registered  simulate latency, no real OTP
	antienum := func() error {
		log.Debug("otp.issue: email not found, simulating send (anti-enumeration)")
		if err := s.otpRep.Immulate(ctx); err != nil {
			log.Error("otp.issue: Immulate failed", logger.Error(err))
			return corerr.ErrOTPRepository
		}
		return nil
	}

	switch purpose {
	case valobj.OTPPurposePasswordReset:
		if !emailExists {
			return antienum()
		}
	case valobj.OTPPurposeRegistration:
		if emailExists {
			return antienum()
		}
	}

	code, err := codeForOTPGen()
	if err != nil {
		span.RecordError(err)
		log.Error("otp.issue: code generation failed", logger.Error(err))
		return err
	}

	otp, err := valobj.NewOTP(code)
	if err != nil {
		span.RecordError(err)
		log.Error("otp.issue: NewOTP failed", logger.Error(err))
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
		log.Error("otp.issue: NewVerificationCode failed", logger.Error(err))
		return err
	}

	if err = s.otpRep.Upsert(ctx, verifyCode); err != nil {
		span.RecordError(err)
		log.Error("otp.issue: Upsert failed", logger.Error(err))
		return corerr.ErrOTPRepository
	}

	if err = s.emailBox.SendOTP(ctx, email, purpose, otp.Value()); err != nil {
		span.RecordError(err)
		log.Error("otp.issue: SendOTP failed", logger.Error(err))
		return corerr.ErrEmailDeliveryFailed
	}

	log.Info("otp.issue: OTP sent successfully")
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
