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

	// UpdateAccountStatusTx выполняет атомарное обновление статуса аккаунта:
	// - обновляет status, lockedUntil и revision в accounts
	// - удаляет все активные сессии аккаунта (уже очищены entity-методом)
	// - записывает все jti сессий в blacklist (outbox L3)
	// - обновляет metadata (updated_at)
	// - при status=deleted вставляет outbox account.deleted с actorID
	//
	// Используется исключительно операциями изменения статуса: LockAccount, UnlockAccount, SoftDelete.
	// Не затрагивает passwordHash и password_history -- в отличие от ResetPassword.
	//
	// revokedJTIs передаются отдельно, так как к моменту вызова entity уже очистила sessions;
	// адаптер обязан записать их в blacklist в рамках одной транзакции.
	//
	// actorID -- UUID инициатора операции (adminID или accountID самого пользователя).
	// Обязателен для payload account.deleted (compliance). Для lock/unlock допустима пустая строка.
	UpdateAccountStatusTx(ctx context.Context, account *entity.Account, revokedJTIs []string, actorID string) error

	// EmailExist проверяет существование email без загрузки агрегата
	// Используется в Registration flow перед созданием аккаунта
	EmailExist(ctx context.Context, email string) (bool, error)

	// FindByEmail загружает Account aggregate по email
	// Включает все активные сессии и passwordHistory (последние 5)
	// Возвращает ErrAccountNotFound если email не существует
	// FindByEmail -- ТОЛЬКО для unauthenticated flows:
	// Login, ConfrimPasswordReset, Register (anti-enumeration check).
	// Для всех аутентифицированных операций использовать FindByAccountID.
	FindByEmail(ctx context.Context, email string) (*entity.Account, error)

	// FindByAccountID загружает Account aggregate по UUID аккаунта.
	// Используется SessionService: accountID поступает из AuthContext JWT,
	// избавляя от дополнительного lookup по email или sessionID.
	// Включает все активные сессии (нужны для eviction-проверки и RevokeSession).
	// Возвращает ErrAccountNotFound если accountID не существует.
	// ADR-001: симметрично FindByEmail, не нарушает гексагональную архитектуру.
	FindByAccountID(ctx context.Context, accountID string) (*entity.Account, error)

	// SaveSessionWithTx сохраняет состояние Account после account.OpenSession()
	// ACID транзакция:
	// - upsert активных сессий аккаунта
	// - если account.EvictedJTI() != "" -- записывает его в blacklist (outbox L3) и вызывает ClearEvictedJTI()
	// - обновляет metadata аккаунта (updated_at)
	// Полный агрегат передаётся для консистентности; адаптер извлекает нужные поля
	SaveSessionWithTx(ctx context.Context, account *entity.Account) error

	// DeleteSessionWithTx удаляет сессию из агрегата и персистирует результат.
	// ACID транзакция:
	// - DELETE sessions WHERE jti=? AND account_id=?
	// - INSERT blacklist (jti, ttl) через outbox (L3)
	// - обновляет metadata аккаунта (updated_at)
	// Полный агрегат передаётся после account.RevokeSession(); адаптер берёт нужные поля.
	// ADR-001: выделен отдельно от SaveSessionWithTx для явного разделения
	// жизненного цикла создания и завершения сессии.
	DeleteSessionWithTx(ctx context.Context, account *entity.Account, revokedJTI string) error

	// FindByGoogleUID загружает Account по google_uid (claim sub из Google ID token).
	// Возвращает ErrAccountNotFound если google_uid не существует.
	// Приоритетный lookup для OAuth flow -- google_uid стабилен при смене email в Google.
	FindByGoogleUID(ctx context.Context, googleUID string) (*entity.Account, error)

	// LinkGoogleUID привязывает google_uid к существующему аккаунту.
	// CASE C: пользователь ранее регистрировался через email/password.
	// Атомарное UPDATE accounts SET google_uid=? WHERE id=? AND google_uid IS NULL.
	LinkGoogleUID(ctx context.Context, accountID string, googleUID string) error

	// CreateOAuthAccountWithTx создаёт аккаунт через OAuth в ACID-транзакции:
	//   - INSERT account (password_hash=NULL, google_uid, email, role, status=active)
	//   - INSERT outbox: AccountRegistered {registrationMethod: "oauth2", classifier}
	// Если classifier=nil и role=patient -- логика согласно missing spec / design gap выше.
	CreateOAuthAccountWithTx(ctx context.Context, params *entity.Account) (*entity.Account, error)

	// ChangeAccountData stands for UNSAFE admin changing of general account data
	ChangeAccountData(ctx context.Context, params *entity.Account) error
}
