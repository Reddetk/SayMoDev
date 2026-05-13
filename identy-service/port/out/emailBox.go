// Package out stands for out of domain opperations
package out

import (
	"context"

	valobj "github.com/Reddetk/SayMoDev/identy-service/core/valObj"
)

type EmailBox interface {
	// HTTP call after DB commit, outside transaction
	SendOTP(ctx context.Context, toEmail string, otpPur valobj.OTPPurpose, code string) error
}
