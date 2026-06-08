package mocks

import (
	"context"
	"errors"

	valobj "github.com/Reddetk/SayMoDev/identy-service/core/valObj"
	"github.com/stretchr/testify/mock"
)

// ErrEmailServiceUnavailable is a placeholder sentinel for the email delivery error path.
//
// DESIGN GAP: not declared in core/coreErrors/businesErrors.go.
// Note: ErrEmailDeliveryFailed exists but covers delivery failure after send;
// ErrEmailServiceUnavailable covers infrastructure unavailability before send.
// Decide whether to reuse ErrEmailDeliveryFailed or add a new sentinel.
// If reusing:
//
//	ErrEmailServiceUnavailable = corerr.ErrEmailDeliveryFailed
var ErrEmailServiceUnavailable = errors.New("email service is unavailable")

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
