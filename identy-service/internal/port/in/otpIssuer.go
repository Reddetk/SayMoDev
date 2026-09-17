// Package in is
package in

import "context"

type OTPIssuer interface {
	IssueRegistrationOTP(ctx context.Context, email string) error
	IssuePasswordResetOTP(ctx context.Context, email string) error
}
