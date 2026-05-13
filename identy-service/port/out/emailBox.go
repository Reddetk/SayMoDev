// Package out stands for out of domain opperations
package out

import (
	valobj "IAM/core/valObj"
	"context"
)

type EmailBox interface {
	// HTTP call after DB commit, outside transaction
	SendOTP(ctx context.Context, toEmail string, otpPur valobj.OTPPurpose, code string) error
}
