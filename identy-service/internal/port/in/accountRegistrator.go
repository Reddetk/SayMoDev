package in

import (
	context "context"
)

type AccountRegistrator interface {
	Register(
		ctx context.Context,
		email, usrVerifyCode, personalInfo, passwordHash string,
		role string,
		classifier ClassifierDTO,
		fingerprint string,
	) error
}

type ClassifierDTO struct {
	Difficulty  string
	AphasiaType string
}
