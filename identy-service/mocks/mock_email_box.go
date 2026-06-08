package mocks

import (
	"context"

	corerr "github.com/Reddetk/SayMoDev/identy-service/core/coreErrors"
	valobj "github.com/Reddetk/SayMoDev/identy-service/core/valObj"
	"github.com/stretchr/testify/mock"
)

// ErrEmailServiceUnavailable -- SMTP/SES infrastructure unreachable before send.
// Distinct from ErrEmailDeliveryFailed (transport accepted but delivery failed).
var ErrEmailServiceUnavailable = corerr.ErrEmailServiceUnavailable

// MockEmailBox is a testify mock for out.EmailBox.
type MockEmailBox struct {
	mock.Mock
}

// SendOTP dispatches an OTP code via email after DB commit (outside transaction).
//
// Happy path: Return(nil)
// Error path:  Return(mocks.ErrEmailServiceUnavailable)
func (m *MockEmailBox) SendOTP(
	ctx context.Context,
	toEmail string,
	otpPur valobj.OTPPurpose,
	code string,
) error {
	args := m.Called(ctx, toEmail, otpPur, code)
	return args.Error(0)
}

// Compile-time interface satisfaction check.
var _ interface {
	SendOTP(context.Context, string, valobj.OTPPurpose, string) error
} = (*MockEmailBox)(nil)
