package out

import (
	"context"

	"github.com/Reddetk/SayMoDev.git/identy-service/core/entity"
)

type AccountRepository interface {
	// CreateAccountWithTx performs ACID transaction:
	// - saves account aggregate
	// - saves password history entry
	// - creates outbox events (AccountRegistered, AccountEmailVerified)
	// - on success, returns account ID and nil error
	CreateAccountWithTx(ctx context.Context, account *entity.Account) (string, error)
	ResetPassword(ctx context.Context, account *entity.Account, newPasswordHash string) error

	EmailExist(ctx context.Context, email string) (bool, error)
}
