package in

import (
	context "context"

	valobj "github.com/Reddetk/SayMoDev/identy-service/core/valObj"
)

type AccountRegistrator interface {
	Register(
		ctx context.Context,
		email, usrVerifyCode, personalInfo, passwordHash string,
		role valobj.Role,
		classifier valobj.Classifier,
		fingerprint string,
	) error
}
