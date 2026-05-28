package in

import (
	context "context"

	"github.com/Reddetk/SayMoDev/identy-service/core/entity"
	valobj "github.com/Reddetk/SayMoDev/identy-service/core/valObj"
)

type PasswordOperato interface {
	ConfrimPasswordReset(
		ctx context.Context,
		account *entity.Account,
		otp valobj.OTP,
		newPasswordHash string,
	) error
}
