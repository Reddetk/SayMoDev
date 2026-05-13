package out

import (
	valobj "IAM/core/valObj"
	"context"
)

type OtpRepository interface {
	Upsert(ctx context.Context, verCode *valobj.VerificationCode) error
	CleanUp(ctx context.Context, verCode *valobj.VerificationCode) error
	Immulate(ctx context.Context) error

	Find(ctx context.Context, email string, purpose valobj.OTPPurpose) (*valobj.VerificationCode, error)
}
