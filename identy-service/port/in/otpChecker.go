package in

type OTPchecker interface {
	OTPcheck(email string, usrVerifyCode string) (isCorrect bool, err error)
}
