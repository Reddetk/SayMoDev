// Package postgres — pgx-реализация вторичных портов BC#2.
package postgres

// goca:manual — pgx-адаптер вместо GORM (BIGINT/CHECK/FOR UPDATE, ADR-007).

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Reddetk/SayMoDev/billing-service/internal/port/out"
)

// Store реализует out.Store.
type Store struct {
	pool *pgxpool.Pool
	tx   pgx.Tx
}

// New создаёт Store.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconnCommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// pgconnCommandTag — минимальный контракт результата Exec.
type pgconnCommandTag interface {
	RowsAffected() int64
}

func (s *Store) q() interface {
	Exec(context.Context, string, ...any) (pgconnCommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
} {
	if s.tx != nil {
		return pgxTx{s.tx}
	}
	return pgxPool{s.pool}
}

type pgxTx struct{ tx pgx.Tx }

func (p pgxTx) Exec(ctx context.Context, sql string, args ...any) (pgconnCommandTag, error) {
	return p.tx.Exec(ctx, sql, args...)
}
func (p pgxTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return p.tx.Query(ctx, sql, args...)
}
func (p pgxTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return p.tx.QueryRow(ctx, sql, args...)
}

type pgxPool struct{ pool *pgxpool.Pool }

func (p pgxPool) Exec(ctx context.Context, sql string, args ...any) (pgconnCommandTag, error) {
	return p.pool.Exec(ctx, sql, args...)
}
func (p pgxPool) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return p.pool.Query(ctx, sql, args...)
}
func (p pgxPool) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return p.pool.QueryRow(ctx, sql, args...)
}

// WithinTx выполняет fn в транзакции. Вложенный вызов переиспользует текущую tx.
func (s *Store) WithinTx(ctx context.Context, fn func(out.Store) error) error {
	if s.tx != nil {
		return fn(s)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	if err := fn(&Store{pool: s.pool, tx: tx}); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// ---------- subscriptions ----------

const subscriptionColumns = `id, account_id, offer_id, tariff_plan_id, status, week_bitmap, timezone,
	period_start, period_end, scheduled_target_tariff_plan, scheduled_effective_at, revoked_at,
	retry_count, next_retry_at, created_at, updated_at`

func (s *Store) CreateSubscription(ctx context.Context, rec out.SubscriptionRecord) error {
	_, err := s.q().Exec(ctx, `INSERT INTO subscriptions (`+subscriptionColumns+`)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`,
		rec.ID, rec.AccountID, rec.OfferID, rec.TariffPlanID, rec.Status, rec.WeekBitmap, rec.Timezone,
		rec.PeriodStart, rec.PeriodEnd, rec.ScheduledTarget, rec.ScheduledAt, rec.RevokedAt,
		rec.RetryCount, rec.NextRetryAt, rec.CreatedAt, rec.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert subscription: %w", err)
	}
	return nil
}

func (s *Store) GetSubscription(ctx context.Context, id string) (*out.SubscriptionRecord, error) {
	return s.getSubscription(ctx, `SELECT `+subscriptionColumns+` FROM subscriptions WHERE id = $1`, id)
}

func (s *Store) GetSubscriptionForUpdate(ctx context.Context, id string) (*out.SubscriptionRecord, error) {
	return s.getSubscription(ctx, `SELECT `+subscriptionColumns+` FROM subscriptions WHERE id = $1 FOR UPDATE`, id)
}

func (s *Store) GetSubscriptionByAccount(ctx context.Context, accountID string) (*out.SubscriptionRecord, error) {
	return s.getSubscription(ctx, `SELECT `+subscriptionColumns+` FROM subscriptions
		WHERE account_id = $1 AND status IN ('pending','active','suspended') ORDER BY created_at DESC LIMIT 1`, accountID)
}

func (s *Store) GetLatestSubscriptionByAccount(ctx context.Context, accountID string) (*out.SubscriptionRecord, error) {
	return s.getSubscription(ctx, `SELECT `+subscriptionColumns+` FROM subscriptions
		WHERE account_id = $1 ORDER BY created_at DESC LIMIT 1`, accountID)
}

func (s *Store) Now(ctx context.Context) (time.Time, error) {
	var now time.Time
	if err := s.q().QueryRow(ctx, `SELECT now()`).Scan(&now); err != nil {
		return time.Time{}, err
	}
	return now.UTC(), nil
}

func (s *Store) getSubscription(ctx context.Context, query string, arg any) (*out.SubscriptionRecord, error) {
	var rec out.SubscriptionRecord
	err := s.q().QueryRow(ctx, query, arg).Scan(
		&rec.ID, &rec.AccountID, &rec.OfferID, &rec.TariffPlanID, &rec.Status, &rec.WeekBitmap, &rec.Timezone,
		&rec.PeriodStart, &rec.PeriodEnd, &rec.ScheduledTarget, &rec.ScheduledAt, &rec.RevokedAt,
		&rec.RetryCount, &rec.NextRetryAt, &rec.CreatedAt, &rec.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, out.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("select subscription: %w", err)
	}
	return &rec, nil
}

func (s *Store) UpdateSubscription(ctx context.Context, rec out.SubscriptionRecord) error {
	tag, err := s.q().Exec(ctx, `UPDATE subscriptions SET
		offer_id=$2, tariff_plan_id=$3, status=$4, week_bitmap=$5, timezone=$6,
		period_start=$7, period_end=$8, scheduled_target_tariff_plan=$9, scheduled_effective_at=$10,
		revoked_at=$11, retry_count=$12, next_retry_at=$13, updated_at=$14
		WHERE id=$1`,
		rec.ID, rec.OfferID, rec.TariffPlanID, rec.Status, rec.WeekBitmap, rec.Timezone,
		rec.PeriodStart, rec.PeriodEnd, rec.ScheduledTarget, rec.ScheduledAt,
		rec.RevokedAt, rec.RetryCount, rec.NextRetryAt, rec.UpdatedAt)
	if err != nil {
		return fmt.Errorf("update subscription: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return out.ErrNotFound
	}
	return nil
}

func (s *Store) ListSubscriptionsDue(ctx context.Context, status string, now time.Time, limit int) ([]out.SubscriptionRecord, error) {
	rows, err := s.q().Query(ctx, `SELECT `+subscriptionColumns+` FROM subscriptions s
		WHERE s.status = $1
		  AND s.period_end <= $2
		  AND (s.next_retry_at IS NULL OR s.next_retry_at <= $2)
		  AND NOT EXISTS (SELECT 1 FROM payments p WHERE p.subscription_id = s.id
		                  AND p.status='pending' AND p.payment_kind='renewal')
		ORDER BY s.period_end LIMIT $3`, status, now, limit)
	return scanSubscriptions(rows, err)
}

func (s *Store) ListStaleRenewalPayments(ctx context.Context, before time.Time, limit int) ([]out.PaymentRecord, error) {
	return s.listStalePayments(ctx, "renewal", before, limit)
}

func (s *Store) ListStaleProrationPayments(ctx context.Context, before time.Time, limit int) ([]out.PaymentRecord, error) {
	return s.listStalePayments(ctx, "upgrade_proration", before, limit)
}

func (s *Store) listStalePayments(ctx context.Context, kind string, before time.Time, limit int) ([]out.PaymentRecord, error) {
	rows, err := s.q().Query(ctx, `SELECT `+paymentColumns+` FROM payments
		WHERE status='pending' AND payment_kind=$1 AND created_at <= $2
		ORDER BY created_at LIMIT $3`, kind, before, limit)
	if err != nil {
		return nil, fmt.Errorf("select stale payments: %w", err)
	}
	defer rows.Close()

	var result []out.PaymentRecord
	for rows.Next() {
		var rec out.PaymentRecord
		if err := rows.Scan(&rec.ID, &rec.SubscriptionID, &rec.AmountKopecks, &rec.Currency, &rec.Status,
			&rec.Kind, &rec.TariffPlanName, &rec.ExternalReferenceID, &rec.AppliedOfferID,
			&rec.RevertTariffPlanID, &rec.RevertOfferID, &rec.RevertWeekBitmap, &rec.RevertTimezone,
			&rec.CreatedAt, &rec.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, rec)
	}
	return result, rows.Err()
}

func (s *Store) ListStalePendingSubscriptions(ctx context.Context, before time.Time, limit int) ([]out.SubscriptionRecord, error) {
	rows, err := s.q().Query(ctx, `SELECT `+subscriptionColumns+` FROM subscriptions
		WHERE status='pending' AND created_at <= $1 ORDER BY created_at LIMIT $2`, before, limit)
	return scanSubscriptions(rows, err)
}

func (s *Store) ListSubscriptionsEndingBefore(ctx context.Context, status string, before time.Time, limit int) ([]out.SubscriptionRecord, error) {
	rows, err := s.q().Query(ctx, `SELECT `+subscriptionColumns+` FROM subscriptions
		WHERE status = $1 AND period_end > now() AND period_end <= $2
		ORDER BY period_end LIMIT $3`, status, before, limit)
	return scanSubscriptions(rows, err)
}

func scanSubscriptions(rows pgx.Rows, err error) ([]out.SubscriptionRecord, error) {
	if err != nil {
		return nil, fmt.Errorf("select subscriptions: %w", err)
	}
	defer rows.Close()

	var result []out.SubscriptionRecord
	for rows.Next() {
		var rec out.SubscriptionRecord
		if err := rows.Scan(
			&rec.ID, &rec.AccountID, &rec.OfferID, &rec.TariffPlanID, &rec.Status, &rec.WeekBitmap, &rec.Timezone,
			&rec.PeriodStart, &rec.PeriodEnd, &rec.ScheduledTarget, &rec.ScheduledAt, &rec.RevokedAt,
			&rec.RetryCount, &rec.NextRetryAt, &rec.CreatedAt, &rec.UpdatedAt,
		); err != nil {
			return nil, err
		}
		result = append(result, rec)
	}
	return result, rows.Err()
}

// ---------- payments ----------

const paymentColumns = `id, subscription_id, amount_kopecks, currency, status, payment_kind,
	tariff_plan_name, COALESCE(external_reference_id,''), applied_offer_id, revert_tariff_plan_id,
	revert_offer_id, revert_week_bitmap, revert_timezone, created_at, updated_at`

const paymentInsertColumns = `id, subscription_id, amount_kopecks, currency, status, payment_kind,
	tariff_plan_name, external_reference_id, applied_offer_id, revert_tariff_plan_id,
	revert_offer_id, revert_week_bitmap, revert_timezone, created_at, updated_at`

func (s *Store) CreatePayment(ctx context.Context, rec out.PaymentRecord) error {
	_, err := s.q().Exec(ctx, `INSERT INTO payments (`+paymentInsertColumns+`)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		rec.ID, rec.SubscriptionID, rec.AmountKopecks, rec.Currency, rec.Status, rec.Kind,
		rec.TariffPlanName, nullIfEmpty(rec.ExternalReferenceID), rec.AppliedOfferID,
		rec.RevertTariffPlanID, rec.RevertOfferID, rec.RevertWeekBitmap, rec.RevertTimezone,
		rec.CreatedAt, rec.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert payment: %w", err)
	}
	return nil
}

func (s *Store) UpdatePayment(ctx context.Context, rec out.PaymentRecord) error {
	tag, err := s.q().Exec(ctx, `UPDATE payments SET amount_kopecks=$2, currency=$3, status=$4,
		payment_kind=$5, tariff_plan_name=$6, external_reference_id=$7, updated_at=$8 WHERE id=$1`,
		rec.ID, rec.AmountKopecks, rec.Currency, rec.Status, rec.Kind,
		rec.TariffPlanName, nullIfEmpty(rec.ExternalReferenceID), rec.UpdatedAt)
	if err != nil {
		return fmt.Errorf("update payment: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return out.ErrNotFound
	}
	return nil
}

func (s *Store) GetPayment(ctx context.Context, id string) (*out.PaymentRecord, error) {
	return s.getPayment(ctx, `SELECT `+paymentColumns+` FROM payments WHERE id=$1`, id)
}

func (s *Store) GetPaymentForUpdate(ctx context.Context, id string) (*out.PaymentRecord, error) {
	return s.getPayment(ctx, `SELECT `+paymentColumns+` FROM payments WHERE id=$1 FOR UPDATE`, id)
}

func (s *Store) GetPaymentByExternalRef(ctx context.Context, ref string) (*out.PaymentRecord, error) {
	return s.getPayment(ctx, `SELECT `+paymentColumns+` FROM payments WHERE external_reference_id=$1`, ref)
}

func (s *Store) getPayment(ctx context.Context, query string, arg any) (*out.PaymentRecord, error) {
	var rec out.PaymentRecord
	err := s.q().QueryRow(ctx, query, arg).Scan(
		&rec.ID, &rec.SubscriptionID, &rec.AmountKopecks, &rec.Currency, &rec.Status, &rec.Kind,
		&rec.TariffPlanName, &rec.ExternalReferenceID, &rec.AppliedOfferID, &rec.RevertTariffPlanID,
		&rec.RevertOfferID, &rec.RevertWeekBitmap, &rec.RevertTimezone, &rec.CreatedAt, &rec.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, out.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("select payment: %w", err)
	}
	return &rec, nil
}

func (s *Store) ListPayments(ctx context.Context, subscriptionID string) ([]out.PaymentRecord, error) {
	rows, err := s.q().Query(ctx, `SELECT `+paymentColumns+` FROM payments
		WHERE subscription_id=$1 ORDER BY created_at DESC`, subscriptionID)
	if err != nil {
		return nil, fmt.Errorf("select payments: %w", err)
	}
	defer rows.Close()

	var result []out.PaymentRecord
	for rows.Next() {
		var rec out.PaymentRecord
		if err := rows.Scan(&rec.ID, &rec.SubscriptionID, &rec.AmountKopecks, &rec.Currency, &rec.Status,
			&rec.Kind, &rec.TariffPlanName, &rec.ExternalReferenceID, &rec.AppliedOfferID,
			&rec.RevertTariffPlanID, &rec.RevertOfferID, &rec.RevertWeekBitmap, &rec.RevertTimezone,
			&rec.CreatedAt, &rec.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, rec)
	}
	return result, rows.Err()
}

// ---------- payment methods ----------

const methodColumns = `id, subscription_id, type, wsb_customer_id, recurring_token, card_mask,
	card_exp_date, is_active, bound_at`

func (s *Store) UpsertPaymentMethod(ctx context.Context, rec out.PaymentMethodRecord) error {
	_, err := s.q().Exec(ctx, `INSERT INTO payment_methods (`+methodColumns+`)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (id) DO UPDATE SET recurring_token=EXCLUDED.recurring_token,
			card_mask=EXCLUDED.card_mask, card_exp_date=EXCLUDED.card_exp_date, is_active=EXCLUDED.is_active`,
		rec.ID, rec.SubscriptionID, rec.Type, rec.WSBCustomerID, rec.RecurringToken, rec.CardMask,
		rec.CardExpDate, rec.IsActive, rec.BoundAt)
	if err != nil {
		return fmt.Errorf("upsert payment method: %w", err)
	}
	return nil
}

func (s *Store) GetActivePaymentMethod(ctx context.Context, subscriptionID string) (*out.PaymentMethodRecord, error) {
	var rec out.PaymentMethodRecord
	err := s.q().QueryRow(ctx, `SELECT `+methodColumns+` FROM payment_methods
		WHERE subscription_id=$1 AND is_active LIMIT 1`, subscriptionID).Scan(
		&rec.ID, &rec.SubscriptionID, &rec.Type, &rec.WSBCustomerID, &rec.RecurringToken, &rec.CardMask,
		&rec.CardExpDate, &rec.IsActive, &rec.BoundAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, out.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("select payment method: %w", err)
	}
	return &rec, nil
}

func (s *Store) DeactivatePaymentMethods(ctx context.Context, subscriptionID string) error {
	_, err := s.q().Exec(ctx, `UPDATE payment_methods SET is_active=false, recurring_token=''
		WHERE subscription_id=$1`, subscriptionID)
	return err
}

func (s *Store) ListExpiringPaymentMethods(ctx context.Context, before time.Time) ([]out.ExpiringMethod, error) {
	rows, err := s.q().Query(ctx, `SELECT m.subscription_id, s.account_id, m.card_exp_date
		FROM payment_methods m JOIN subscriptions s ON s.id = m.subscription_id
		WHERE m.is_active AND s.status='active' AND m.card_exp_date IS NOT NULL AND m.card_exp_date <= $1`, before)
	if err != nil {
		return nil, fmt.Errorf("select expiring methods: %w", err)
	}
	defer rows.Close()

	var result []out.ExpiringMethod
	for rows.Next() {
		var item out.ExpiringMethod
		if err := rows.Scan(&item.SubscriptionID, &item.AccountID, &item.CardExpDate); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

// ---------- catalog ----------

const offerSelect = `SELECT o.id, o.tariff_plan_id, p.name, p.week_access_bitmap, p.timezone,
	o.price_kopecks, o.currency, o.billing_days, o.valid_from, o.valid_until, o.is_active
	FROM subscription_offers o JOIN tariff_plans p ON p.id = o.tariff_plan_id`

func (s *Store) GetOffer(ctx context.Context, offerID string) (*out.OfferRecord, error) {
	return s.getOffer(ctx, offerSelect+` WHERE o.id=$1`, offerID)
}

func (s *Store) FindActiveOffer(ctx context.Context, tariffPlanID string, at time.Time) (*out.OfferRecord, error) {
	return s.getOffer(ctx, offerSelect+` WHERE o.tariff_plan_id=$1 AND o.is_active
		AND o.valid_from <= $2 AND (o.valid_until IS NULL OR o.valid_until > $2)
		ORDER BY o.valid_from DESC LIMIT 1`, tariffPlanID, at)
}

func (s *Store) getOffer(ctx context.Context, query string, args ...any) (*out.OfferRecord, error) {
	var rec out.OfferRecord
	err := s.q().QueryRow(ctx, query, args...).Scan(
		&rec.OfferID, &rec.TariffPlanID, &rec.PlanName, &rec.WeekBitmap, &rec.Timezone,
		&rec.PriceKopecks, &rec.Currency, &rec.BillingDays, &rec.ValidFrom, &rec.ValidUntil, &rec.IsActive)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, out.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("select offer: %w", err)
	}
	return &rec, nil
}

// ---------- idempotency ----------

func (s *Store) GetIdempotency(ctx context.Context, accountID, key string) (*out.IdempotencyRecord, error) {
	var rec out.IdempotencyRecord
	err := s.q().QueryRow(ctx, `SELECT account_id, key, endpoint, status, COALESCE(response_code,0),
		COALESCE(response_body,'{}'), expires_at FROM idempotency_keys
		WHERE account_id=$1 AND key=$2`, accountID, key).Scan(
		&rec.AccountID, &rec.Key, &rec.Endpoint, &rec.Status, &rec.ResponseCode, &rec.ResponseBody, &rec.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, out.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("select idempotency: %w", err)
	}
	return &rec, nil
}

// CreateIdempotency возвращает false, если ключ уже существует.
func (s *Store) CreateIdempotency(ctx context.Context, rec out.IdempotencyRecord) (bool, error) {
	tag, err := s.q().Exec(ctx, `INSERT INTO idempotency_keys
		(account_id, key, endpoint, status, expires_at) VALUES ($1,$2,$3,'in_progress', now() + INTERVAL '7 days')
		ON CONFLICT (account_id, key) DO NOTHING`, rec.AccountID, rec.Key, rec.Endpoint)
	if err != nil {
		return false, fmt.Errorf("insert idempotency: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (s *Store) CompleteIdempotency(ctx context.Context, rec out.IdempotencyRecord) error {
	_, err := s.q().Exec(ctx, `UPDATE idempotency_keys SET status='completed', response_code=$3,
		response_body=$4 WHERE account_id=$1 AND key=$2`,
		rec.AccountID, rec.Key, rec.ResponseCode, rec.ResponseBody)
	return err
}

func (s *Store) DeleteIdempotency(ctx context.Context, accountID, key string) error {
	_, err := s.q().Exec(ctx, `DELETE FROM idempotency_keys WHERE account_id=$1 AND key=$2`, accountID, key)
	return err
}

// ---------- outbox / consumers ----------

func (s *Store) InsertOutbox(ctx context.Context, eventID, eventType string, payload []byte) error {
	_, err := s.q().Exec(ctx, `INSERT INTO outbox (event_id, event_type, payload) VALUES ($1,$2,$3)`,
		eventID, eventType, payload)
	if err != nil {
		return fmt.Errorf("insert outbox: %w", err)
	}
	return nil
}

func (s *Store) FetchOutboxBatch(ctx context.Context, limit int) ([]out.OutboxRecord, error) {
	rows, err := s.q().Query(ctx, `SELECT id, event_id, event_type, payload, occurred_at
		FROM outbox WHERE sent_at IS NULL ORDER BY id LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("select outbox: %w", err)
	}
	defer rows.Close()

	var result []out.OutboxRecord
	for rows.Next() {
		var rec out.OutboxRecord
		if err := rows.Scan(&rec.ID, &rec.EventID, &rec.EventType, &rec.Payload, &rec.OccurredAt); err != nil {
			return nil, err
		}
		result = append(result, rec)
	}
	return result, rows.Err()
}

func (s *Store) MarkOutboxSent(ctx context.Context, id int64) error {
	_, err := s.q().Exec(ctx, `UPDATE outbox SET sent_at=now() WHERE id=$1`, id)
	return err
}

func (s *Store) MarkOutboxFailed(ctx context.Context, id int64, reason string) error {
	_, err := s.q().Exec(ctx, `UPDATE outbox SET attempts=attempts+1, last_error=$2 WHERE id=$1`, id, reason)
	return err
}

func (s *Store) ProcessedEventExists(ctx context.Context, eventID, consumer string) (bool, error) {
	var exists bool
	err := s.q().QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM processed_events WHERE event_id=$1 AND consumer=$2)`,
		eventID, consumer).Scan(&exists)
	return exists, err
}

func (s *Store) MarkProcessedEvent(ctx context.Context, eventID, consumer string) error {
	_, err := s.q().Exec(ctx, `INSERT INTO processed_events (event_id, consumer) VALUES ($1,$2)
		ON CONFLICT DO NOTHING`, eventID, consumer)
	return err
}

// ---------- account projection / archive ----------

func (s *Store) GetAccountStatus(ctx context.Context, accountID string) (string, error) {
	var status string
	err := s.q().QueryRow(ctx, `SELECT status FROM account_status WHERE account_id=$1`, accountID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", out.ErrNotFound
	}
	return status, err
}

func (s *Store) UpsertAccountStatus(ctx context.Context, accountID, status string) error {
	_, err := s.q().Exec(ctx, `INSERT INTO account_status (account_id, status, updated_at)
		VALUES ($1,$2,now()) ON CONFLICT (account_id) DO UPDATE SET status=EXCLUDED.status, updated_at=now()`,
		accountID, status)
	return err
}

func (s *Store) ArchiveAccount(ctx context.Context, accountID string, snapshot []byte) error {
	_, err := s.q().Exec(ctx, `INSERT INTO billing_archive (account_id, snapshot) VALUES ($1,$2)`, accountID, snapshot)
	return err
}

func (s *Store) DeleteSubscription(ctx context.Context, id string) error {
	_, err := s.q().Exec(ctx, `DELETE FROM subscriptions WHERE id=$1`, id)
	return err
}

func (s *Store) SubscriptionCounts(ctx context.Context) ([]out.SubscriptionCount, error) {
	rows, err := s.q().Query(ctx, `SELECT st.status, p.name, count(sub.id)
		FROM tariff_plans p
		CROSS JOIN (VALUES ('active'), ('suspended')) AS st(status)
		LEFT JOIN subscriptions sub ON sub.tariff_plan_id = p.id AND sub.status = st.status
		GROUP BY st.status, p.name
		ORDER BY p.name, st.status`)
	if err != nil {
		return nil, fmt.Errorf("subscription counts: %w", err)
	}
	defer rows.Close()
	var result []out.SubscriptionCount
	for rows.Next() {
		var item out.SubscriptionCount
		if err := rows.Scan(&item.Status, &item.PlanType, &item.Count); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) PendingOutboxCount(ctx context.Context) (int64, error) {
	var count int64
	err := s.q().QueryRow(ctx, `SELECT count(*) FROM outbox WHERE sent_at IS NULL`).Scan(&count)
	return count, err
}

func nullIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}
