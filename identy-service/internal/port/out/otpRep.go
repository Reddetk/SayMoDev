package out

import (
	"context"

	valobj "github.com/Reddetk/SayMoDev/identy-service/internal/core/valObj"
)

type OtpRepository interface {
	Upsert(ctx context.Context, verCode *valobj.VerificationCode) error
	CleanUp(ctx context.Context, verCode *valobj.VerificationCode) error
	Immulate(ctx context.Context) error

	Find(ctx context.Context, email string, purpose valobj.OTPPurpose) (*valobj.VerificationCode, error)
}
