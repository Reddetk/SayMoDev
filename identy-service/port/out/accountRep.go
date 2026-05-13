package out

import (
	"context"

	"github.com/Reddetk/SayMoDev/identy-service/core/entity"
)

type AccountRepository interface {
	// CreateAccountWithTx performs ACID transaction:
	// - saves account aggregate
	// - saves password history entry
	// - creates outbox events (AccountRegistered, AccountEmailVerified)
	// - on success, returns account ID and nil error
	CreateAccountWithTx(ctx context.Context, account *entity.Account) (string, error)

	// ResetPassword выполняет атомарное обновление пароля:
	// - обновляет passwordHash и revision в accounts
	// - удаляет все активные сессии аккаунта
	// - записывает все jti в blacklist (outbox L3)
	// - добавляет запись в password_history
	ResetPassword(ctx context.Context, account *entity.Account, newPasswordHash string) error

	// EmailExist проверяет существование email без загрузки агрегата
	// Используется в Registration flow перед созданием аккаунта
	EmailExist(ctx context.Context, email string) (bool, error)

	// FindByEmail загружает Account aggregate по email
	// Включает все активные сессии и passwordHistory (последние 5)
	// Возвращает ErrAccountNotFound если email не существует
	FindByEmail(ctx context.Context, email string) (*entity.Account, error)

	// SaveSessionWithTx сохраняет состояние Account после account.OpenSession()
	// ACID транзакция:
	// - upsert активных сессий аккаунта
	// - если evictedJTI != "" -- записывает его в blacklist (outbox L3)
	// - обновляет metadata аккаунта (updated_at)
	// Полный агрегат передаётся для консистентности; адаптер извлекает нужные поля
	SaveSessionWithTx(ctx context.Context, account *entity.Account) error
}
