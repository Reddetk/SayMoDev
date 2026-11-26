package usecase

// goca:manual — application errors BC#2; коды маппятся в HTTP на границе.

// Error — прикладная ошибка с машиночитаемым кодом.
type Error struct {
	Code    string
	Message string
	Details any
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// NewError создаёт ошибку.
func NewError(code, message string) *Error { return &Error{Code: code, Message: message} }

// Коды ошибок BC#2 (совпадают с `error` в endpoints.md).
const (
	CodeInvalidInput          = "invalid_input"
	CodeNotFound              = "not_found"
	CodeForbidden             = "forbidden"
	CodeAccountInactive       = "account_inactive"
	CodeActiveSubscription    = "active_subscription_exists"
	CodeNotSuspended          = "subscription_not_suspended"
	CodePeriodExpired         = "billing_period_expired"
	CodePeriodEnded           = "billing_period_ended"
	CodeNotUpgrade            = "not_an_upgrade"
	CodeQuoteExpired          = "quote_expired"
	CodeRateLimit             = "rate_limit_exceeded"
	CodeIdempotencyInProgress = "idempotency_in_progress"
	CodePaymentFailed         = "payment_failed"
	CodeConflict              = "conflict"
	CodeInternal              = "internal_error"
)
