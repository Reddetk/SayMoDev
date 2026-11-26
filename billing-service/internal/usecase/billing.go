package usecase

// goca:manual — application services BC#2; goca usecase не покрывает саги/идемпотентность.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/Reddetk/SayMoDev/billing-service/internal/domain"
	"github.com/Reddetk/SayMoDev/billing-service/internal/port/in"
	"github.com/Reddetk/SayMoDev/billing-service/internal/port/out"
)

// Config — параметры application-слоя.
type Config struct {
	StoreID       string
	NotifyURL     string
	ReturnURL     string
	WebhookSecret string
	QuoteTTL      time.Duration
	// NotificationRecipient — временный адрес получателя BC#2: email в событиях BC#1
	// не передаётся, поэтому адрес задаётся конфигурацией до появления проекции.
	NotificationRecipient string
}

// Service реализует in.BillingService.
type Service struct {
	store     out.Store
	cache     out.Cache
	acquiring out.Acquiring
	notifier  out.Notifier
	cfg       Config
	now       func() time.Time
	metrics   Metrics
}

// New создаёт application service.
func New(store out.Store, cache out.Cache, acquiring out.Acquiring, notifier out.Notifier, cfg Config) *Service {
	if cfg.QuoteTTL <= 0 {
		cfg.QuoteTTL = 5 * time.Minute
	}
	return &Service{
		store:     store,
		cache:     cache,
		acquiring: acquiring,
		notifier:  notifier,
		cfg:       cfg,
		now:       func() time.Time { return time.Now().UTC() },
	}
}

// Subscribe создаёт pending-подписку и pending-платёж (§5, §6).
func (s *Service) Subscribe(ctx context.Context, auth in.AuthContext, input in.SubscribeInput) (in.SubscribeResult, error) {
	if input.TariffPlanID == "" {
		return in.SubscribeResult{}, NewError(CodeInvalidInput, "tariffPlanId is required")
	}
	now := s.now()
	offer, err := s.store.FindActiveOffer(ctx, input.TariffPlanID, now)
	if errors.Is(err, out.ErrNotFound) {
		return in.SubscribeResult{}, NewError(CodeInvalidInput, "tariff plan has no active offer")
	}
	if err != nil {
		return in.SubscribeResult{}, err
	}
	if _, err := s.store.GetSubscriptionByAccount(ctx, auth.AccountID); err == nil {
		return in.SubscribeResult{}, NewError(CodeActiveSubscription, "account already has an active subscription")
	} else if !errors.Is(err, out.ErrNotFound) {
		return in.SubscribeResult{}, err
	}

	price, err := domain.NewPrice(offer.PriceKopecks, domain.Currency(offer.Currency))
	if err != nil {
		return in.SubscribeResult{}, err
	}
	snapshot, err := domain.NewTariffSnapshot(domain.WeekBitmap(offer.WeekBitmap), offer.Timezone)
	if err != nil {
		return in.SubscribeResult{}, err
	}
	period, err := domain.NewBillingPeriod(now, now.AddDate(0, 0, offer.BillingDays))
	if err != nil {
		return in.SubscribeResult{}, err
	}
	sub, err := domain.NewSubscription(uuid.New(), mustUUID(auth.AccountID), mustUUID(offer.TariffPlanID), mustUUID(offer.OfferID), snapshot, period, now)
	if err != nil {
		return in.SubscribeResult{}, err
	}
	payment, err := domain.NewPayment(uuid.New(), price, domain.PaymentInitial, offer.PlanName, now)
	if err != nil {
		return in.SubscribeResult{}, err
	}
	if err := sub.AddPayment(payment); err != nil {
		return in.SubscribeResult{}, err
	}

	// CreatePaymentLink — чистое построение URL (без сетевого вызова), поэтому
	// его можно выполнить до INSERT. Платёж создаётся в БД до фактического
	// редиректа пользователя и до сетевого вызова acquiring (§6).
	redirectURL, err := s.acquiring.CreatePaymentLink(ctx, out.PaymentLinkRequest{
		StoreID:       s.cfg.StoreID,
		CustomerID:    auth.AccountID,
		OrderNum:      payment.ID.String(),
		AmountKopecks: price.AmountKopecks,
		Currency:      string(price.Currency),
		NotifyURL:     s.cfg.NotifyURL,
		ReturnURL:     s.cfg.ReturnURL,
		OperationType: "recurring_bind",
		Description:   "SayMo " + offer.PlanName,
	})
	if err != nil {
		return in.SubscribeResult{}, fmt.Errorf("acquiring: %w", err)
	}

	err = s.store.WithinTx(ctx, func(tx out.Store) error {
		if err := tx.CreateSubscription(ctx, subscriptionRecord(sub)); err != nil {
			return err
		}
		if err := tx.CreatePayment(ctx, paymentRecord(sub.ID.String(), payment)); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		if isUniqueViolation(err) {
			return in.SubscribeResult{}, NewError(CodeActiveSubscription, "account already has an active subscription")
		}
		return in.SubscribeResult{}, err
	}

	return in.SubscribeResult{
		SubscriptionID: sub.ID.String(),
		Status:         string(sub.Status),
		TariffSnapshot: snapshotView(sub.Snapshot),
		BillingPeriod:  periodView(sub.Period),
		Payment: in.InitiatedPayment{
			PaymentID:            payment.ID.String(),
			AmountKopecks:        price.AmountKopecks,
			Status:               string(payment.Status),
			AcquiringRedirectURL: redirectURL,
		},
	}, nil
}

// GetSubscription возвращает подписку с ownership-проверкой (IDOR → 404).
func (s *Service) GetSubscription(ctx context.Context, auth in.AuthContext, subscriptionID string) (in.SubscriptionView, error) {
	rec, err := s.store.GetSubscription(ctx, subscriptionID)
	if errors.Is(err, out.ErrNotFound) {
		return in.SubscriptionView{}, NewError(CodeNotFound, "subscription not found")
	}
	if err != nil {
		return in.SubscriptionView{}, err
	}
	if auth.Role != "administrator" && rec.AccountID != auth.AccountID {
		return in.SubscriptionView{}, NewError(CodeNotFound, "subscription not found")
	}
	return subscriptionView(*rec), nil
}

// ListPayments возвращает историю платежей (DESC).
func (s *Service) ListPayments(ctx context.Context, auth in.AuthContext, subscriptionID string) ([]in.PaymentView, error) {
	rec, err := s.store.GetSubscription(ctx, subscriptionID)
	if errors.Is(err, out.ErrNotFound) {
		return nil, NewError(CodeNotFound, "subscription not found")
	}
	if err != nil {
		return nil, err
	}
	if auth.Role != "administrator" && rec.AccountID != auth.AccountID {
		return nil, NewError(CodeNotFound, "subscription not found")
	}
	payments, err := s.store.ListPayments(ctx, subscriptionID)
	if err != nil {
		return nil, err
	}
	result := make([]in.PaymentView, 0, len(payments))
	for _, payment := range payments {
		result = append(result, in.PaymentView{
			PaymentID:           payment.ID,
			AmountKopecks:       payment.AmountKopecks,
			Currency:            payment.Currency,
			Status:              payment.Status,
			TariffPlanName:      payment.TariffPlanName,
			PaymentKind:         payment.Kind,
			ExternalReferenceID: payment.ExternalReferenceID,
			CreatedAt:           payment.CreatedAt,
		})
	}
	return result, nil
}

// PreviewUpgrade считает proration и фиксирует quote в Redis (TTL 5 мин).
func (s *Service) PreviewUpgrade(ctx context.Context, auth in.AuthContext, subscriptionID, targetTariffPlanID string) (in.UpgradePreview, error) {
	sub, rec, err := s.loadOwned(ctx, auth, subscriptionID)
	if err != nil {
		return in.UpgradePreview{}, err
	}
	preview, err := s.buildPreview(ctx, sub, rec, targetTariffPlanID)
	if err != nil {
		return in.UpgradePreview{}, err
	}
	return preview, nil
}

// Upgrade применяет тариф немедленно и создаёт платёж на доплату (§3).
// Подписка читается FOR UPDATE: конкурентные upgrade сериализуются, второй
// видит новый тариф и получает 400/409, а не двойное списание.
func (s *Service) Upgrade(ctx context.Context, auth in.AuthContext, input in.UpgradeInput) (in.UpgradeResult, error) {
	var (
		result         in.UpgradeResult
		payment        domain.Payment
		oldPlanID      domain.UUID
		oldOfferID     domain.UUID
		oldSnapshot    domain.TariffSnapshot
		appliedOfferID domain.UUID
	)
	err := s.store.WithinTx(ctx, func(tx out.Store) error {
		now := s.now()
		rec, err := tx.GetSubscriptionForUpdate(ctx, input.SubscriptionID)
		if errors.Is(err, out.ErrNotFound) {
			return NewError(CodeNotFound, "subscription not found")
		}
		if err != nil {
			return err
		}
		if auth.Role != "administrator" && rec.AccountID != auth.AccountID {
			return NewError(CodeNotFound, "subscription not found")
		}
		sub, err := subscriptionFromRecord(rec)
		if err != nil {
			return err
		}
		if rec.TariffPlanID == input.TargetTariff {
			// Конкурентный/повторный upgrade того же тарифа: lock уже применён.
			return NewError(CodeConflict, "target tariff is already active")
		}

		preview, _, offer, err := s.previewWithStore(ctx, tx, sub, rec, input.TargetTariff, input.QuoteID)
		if err != nil {
			return err
		}

		// §12: квота расходуется последним шагом, после всех проверок.
		allowed, err := s.cache.AllowUpgradeChange(ctx, auth.AccountID, now)
		if err != nil {
			return fmt.Errorf("rate limit: %w", err)
		}
		if !allowed {
			return NewError(CodeRateLimit, "max 3 plan changes per 24h, min 1h apart")
		}

		snapshot, err := domain.NewTariffSnapshot(domain.WeekBitmap(offer.WeekBitmap), offer.Timezone)
		if err != nil {
			return err
		}
		charge, err := domain.NewPaymentAmount(preview.Proration.AmountKopecks, domain.Currency(offer.Currency))
		if err != nil {
			return err
		}
		oldSnapshot = sub.Snapshot
		oldPlanID = sub.TariffPlanID
		oldOfferID = sub.OfferID
		appliedOfferID = mustUUID(offer.OfferID)
		if err := sub.ApplyUpgrade(mustUUID(offer.TariffPlanID), mustUUID(offer.OfferID), snapshot, now); err != nil {
			return mapDomainError(err)
		}
		payment, err = domain.NewPayment(uuid.New(), charge, domain.PaymentProration, offer.PlanName, now)
		if err != nil {
			return err
		}
		if err := sub.AddPayment(payment); err != nil {
			return err
		}

		if err := tx.UpdateSubscription(ctx, subscriptionRecord(sub)); err != nil {
			return err
		}
		record := paymentRecord(sub.ID.String(), payment)
		applied := offer.OfferID
		revertPlan := oldPlanID.String()
		revertOffer := oldOfferID.String()
		bitmap := int16(oldSnapshot.WeekBitmap)
		tz := oldSnapshot.Timezone
		record.AppliedOfferID = &applied
		record.RevertTariffPlanID = &revertPlan
		record.RevertOfferID = &revertOffer
		record.RevertWeekBitmap = &bitmap
		record.RevertTimezone = &tz
		if err := tx.CreatePayment(ctx, record); err != nil {
			return err
		}
		if err := emit(ctx, tx, "SubscriptionChanged", map[string]any{
			"subscriptionId": sub.ID.String(),
			"tariffSnapshot": snapshotView(snapshot),
			"effectiveAt":    now,
			"change_type":    "upgrade",
		}); err != nil {
			return err
		}
		if err := emit(ctx, tx, "TariffPlanChanged", map[string]any{
			"accountId":   sub.AccountID.String(),
			"oldSnapshot": snapshotView(oldSnapshot),
			"newSnapshot": snapshotView(snapshot),
			"changed_at":  now,
		}); err != nil {
			return err
		}

		result = in.UpgradeResult{
			SubscriptionID:  sub.ID.String(),
			Status:          string(sub.Status),
			TariffSnapshot:  snapshotView(sub.Snapshot),
			ProrationCharge: in.ProrationCharge{AmountKopecks: charge.AmountKopecks, Currency: string(charge.Currency)},
		}
		return nil
	})
	if err != nil {
		return in.UpgradeResult{}, err
	}

	s.metricChange("upgrade")
	// Post-commit: списание доплаты. Если карты нет — возвращаем ссылку на оплату;
	// если инициировать списание не удалось — откатываем тариф.
	redirectURL, chargeErr := s.chargeProration(ctx, auth.AccountID, result.SubscriptionID, payment)
	if chargeErr != nil {
		reverted, revertErr := s.revertUpgrade(ctx, result.SubscriptionID, oldPlanID, oldOfferID, oldSnapshot, appliedOfferID, payment.ID)
		s.metricPayment("failed")
		s.metricPaymentDuration("failed", payment.CreatedAt)
		if revertErr != nil {
			return in.UpgradeResult{}, revertErr
		}
		if !reverted {
			// webhook успел раньше (или тариф уже сменён другим upgrade)
			return result, nil
		}
		return in.UpgradeResult{}, NewError(CodePaymentFailed, "proration charge failed")
	}
	result.AcquiringRedirectURL = redirectURL
	return result, nil
}

// revertUpgrade компенсирует upgrade, если списание не удалось инициировать.
// Возвращает true, если откат выполнен; false — если webhook успел или тариф уже другой.
func (s *Service) revertUpgrade(ctx context.Context, subscriptionID string, planID, offerID domain.UUID,
	snapshot domain.TariffSnapshot, appliedOfferID, paymentID domain.UUID,
) (bool, error) {
	reverted := false
	err := s.store.WithinTx(ctx, func(tx out.Store) error {
		// Порядок блокировок как у webhook/worker: payment → subscription.
		payment, err := tx.GetPaymentForUpdate(ctx, paymentID.String())
		if err != nil {
			return err
		}
		if domain.PaymentStatus(payment.Status) == domain.PaymentSuccess {
			return nil // webhook успел раньше — откат не нужен
		}
		rec, err := tx.GetSubscriptionForUpdate(ctx, subscriptionID)
		if err != nil {
			return err
		}
		sub, err := subscriptionFromRecord(rec)
		if err != nil {
			return err
		}
		if appliedOfferID != (domain.UUID{}) && sub.OfferID != appliedOfferID {
			return nil // тариф уже сменился другим upgrade
		}
		payment.Status = string(domain.PaymentFailed)
		payment.UpdatedAt = s.now()
		if err := tx.UpdatePayment(ctx, *payment); err != nil {
			return err
		}
		appliedSnapshot := sub.Snapshot
		sub.TariffPlanID = planID
		sub.OfferID = offerID
		sub.Snapshot = snapshot
		sub.UpdatedAt = s.now()
		if err := tx.UpdateSubscription(ctx, subscriptionRecord(sub)); err != nil {
			return err
		}
		if err := emit(ctx, tx, "SubscriptionChanged", map[string]any{
			"subscriptionId": sub.ID.String(),
			"tariffSnapshot": snapshotView(sub.Snapshot),
			"effectiveAt":    s.now(),
			"change_type":    "upgrade_reverted",
		}); err != nil {
			return err
		}
		if err := emit(ctx, tx, "TariffPlanChanged", map[string]any{
			"accountId":   sub.AccountID.String(),
			"oldSnapshot": snapshotView(appliedSnapshot),
			"newSnapshot": snapshotView(sub.Snapshot),
			"changed_at":  s.now(),
		}); err != nil {
			return err
		}
		if err := emit(ctx, tx, "PaymentFailed", map[string]any{
			"paymentId": payment.ID, "subscriptionId": payment.SubscriptionID,
			"reason": "proration_charge_failed", "retry_possible": false,
		}); err != nil {
			return err
		}
		reverted = true
		return nil
	})
	return reverted, err
}

// chargeProration инициирует списание доплаты. Без привязанной карты возвращает
// redirect URL (клиент оплачивает вручную); с картой — recurring_pay.
func (s *Service) chargeProration(ctx context.Context, accountID, subscriptionID string, payment domain.Payment) (string, error) {
	request := out.RecurringPayRequest{
		OrderNum:      payment.ID.String(),
		AmountKopecks: payment.Amount.AmountKopecks,
		Currency:      string(payment.Amount.Currency),
		CustomerID:    accountID,
	}
	if method, err := s.store.GetActivePaymentMethod(ctx, subscriptionID); err == nil {
		request.RecurringToken = method.RecurringToken
	}
	if request.RecurringToken == "" {
		return s.acquiring.CreatePaymentLink(ctx, out.PaymentLinkRequest{
			StoreID: s.cfg.StoreID, CustomerID: accountID, OrderNum: payment.ID.String(),
			AmountKopecks: payment.Amount.AmountKopecks, Currency: string(payment.Amount.Currency),
			NotifyURL: s.cfg.NotifyURL, ReturnURL: s.cfg.ReturnURL,
			OperationType: "recurring_bind", Description: "SayMo upgrade proration",
		})
	}
	if err := s.acquiring.RecurringPay(ctx, request); err != nil {
		return "", err
	}
	return "", nil
}

// Downgrade планирует смену тарифа на следующий период (§3).
func (s *Service) Downgrade(ctx context.Context, auth in.AuthContext, input in.DowngradeInput) (in.DowngradeResult, error) {
	var result in.DowngradeResult
	err := s.store.WithinTx(ctx, func(tx out.Store) error {
		now := s.now()
		rec, err := tx.GetSubscriptionForUpdate(ctx, input.SubscriptionID)
		if errors.Is(err, out.ErrNotFound) {
			return NewError(CodeNotFound, "subscription not found")
		}
		if err != nil {
			return err
		}
		if auth.Role != "administrator" && rec.AccountID != auth.AccountID {
			return NewError(CodeNotFound, "subscription not found")
		}
		sub, err := subscriptionFromRecord(rec)
		if err != nil {
			return err
		}

		current, err := tx.GetOffer(ctx, rec.OfferID)
		if err != nil {
			return err
		}
		target, err := tx.FindActiveOffer(ctx, input.TargetTariff, now)
		if errors.Is(err, out.ErrNotFound) {
			return NewError(CodeInvalidInput, "target tariff has no active offer")
		}
		if err != nil {
			return err
		}
		if target.PriceKopecks >= current.PriceKopecks {
			return NewError(CodeInvalidInput, "target tariff is not a downgrade")
		}

		allowed, err := s.cache.AllowUpgradeChange(ctx, auth.AccountID, now)
		if err != nil {
			return fmt.Errorf("rate limit: %w", err)
		}
		if !allowed {
			return NewError(CodeRateLimit, "max 3 plan changes per 24h, min 1h apart")
		}

		if err := sub.ScheduleDowngrade(mustUUID(target.TariffPlanID), sub.Period.End, now); err != nil {
			return mapDomainError(err)
		}
		if err := tx.UpdateSubscription(ctx, subscriptionRecord(sub)); err != nil {
			return err
		}
		if err := emit(ctx, tx, "SubscriptionChanged", map[string]any{
			"subscriptionId": sub.ID.String(),
			"tariffSnapshot": snapshotView(sub.Snapshot),
			"effectiveAt":    sub.Period.End,
			"change_type":    "downgrade",
		}); err != nil {
			return err
		}
		result = in.DowngradeResult{
			SubscriptionID:  sub.ID.String(),
			Status:          string(sub.Status),
			ScheduledChange: scheduledView(sub.Scheduled),
			Message:         "downgrade will take effect at next billing period",
		}
		return nil
	})
	if err != nil {
		return in.DowngradeResult{}, err
	}
	s.metricChange("downgrade")
	return result, nil
}

// Cancel отменяет подписку; доступ живёт до конца периода (§8).
func (s *Service) Cancel(ctx context.Context, auth in.AuthContext, subscriptionID string) (in.CancelResult, error) {
	sub, _, err := s.loadOwned(ctx, auth, subscriptionID)
	if err != nil {
		return in.CancelResult{}, err
	}
	now := s.now()
	if err := sub.Cancel(now); err != nil {
		return in.CancelResult{}, mapDomainError(err)
	}
	err = s.store.WithinTx(ctx, func(tx out.Store) error {
		if err := tx.UpdateSubscription(ctx, subscriptionRecord(sub)); err != nil {
			return err
		}
		return emit(ctx, tx, "SubscriptionDeActivated", map[string]any{
			"subscriptionId":     sub.ID.String(),
			"revoked_at":         now,
			"access_valid_until": sub.Period.End,
		})
	})
	if err != nil {
		return in.CancelResult{}, err
	}
	return in.CancelResult{
		SubscriptionID: sub.ID.String(),
		Status:         string(sub.Status),
		RevokedAt:      sub.RevokedAt,
		Message:        "access continues until " + sub.Period.End.Format(time.RFC3339),
	}, nil
}

// Resume возобновляет suspended-подписку (§7).
func (s *Service) Resume(ctx context.Context, auth in.AuthContext, subscriptionID string) (in.ResumeResult, error) {
	sub, _, err := s.loadOwned(ctx, auth, subscriptionID)
	if err != nil {
		return in.ResumeResult{}, err
	}
	now := s.now()
	if sub.Status != domain.StatusSuspended {
		return in.ResumeResult{}, NewError(CodeNotSuspended, "subscription is not suspended")
	}
	if sub.Period.Expired(now) {
		return in.ResumeResult{}, NewError(CodePeriodExpired, "billing period has expired")
	}
	if err := sub.Resume(now); err != nil {
		return in.ResumeResult{}, mapDomainError(err)
	}
	err = s.store.WithinTx(ctx, func(tx out.Store) error {
		if err := tx.UpdateSubscription(ctx, subscriptionRecord(sub)); err != nil {
			return err
		}
		return emit(ctx, tx, "SubscriptionResumed", map[string]any{"subscriptionId": sub.ID.String()})
	})
	if err != nil {
		return in.ResumeResult{}, err
	}
	return in.ResumeResult{
		SubscriptionID: sub.ID.String(),
		Status:         string(sub.Status),
		RevokedAt:      nil,
		Message:        "subscription resumed; access restored",
	}, nil
}

// HandleWebhook обрабатывает нотификатор WebPay (HMAC → timestamp → idempotency).
func (s *Service) HandleWebhook(ctx context.Context, input in.WebhookInput) error {
	s.metricWebhook()
	if err := s.verifyWebhook(input); err != nil {
		return err
	}
	if existing, err := s.store.GetPaymentByExternalRef(ctx, input.TransactionID); err == nil && existing.Status != "pending" {
		return nil // идемпотентный повтор
	}

	paymentRec, err := s.store.GetPayment(ctx, input.SiteOrderID)
	if errors.Is(err, out.ErrNotFound) {
		return NewError(CodeNotFound, "payment not found")
	}
	if err != nil {
		return err
	}
	amount, err := decimalToKopecks(input.Amount)
	if err != nil {
		return NewError(CodeInvalidInput, "invalid amount")
	}
	if amount != paymentRec.AmountKopecks {
		return NewError(CodeInvalidInput, "amount mismatch")
	}
	now := s.now()
	switch input.PaymentType {
	case 5, 11: // Refunded: отдельная запись с отрицательной суммой (§6)
		return s.handleWebhookRefund(ctx, paymentRec, input, now)
	}
	success := input.Action == 0 && (input.PaymentType == 1 || input.PaymentType == 10)

	if success {
		return s.handleWebhookSuccess(ctx, paymentRec, input, now)
	}
	return s.handleWebhookFailure(ctx, paymentRec, input, now)
}

func (s *Service) handleWebhookRefund(ctx context.Context, original *out.PaymentRecord, input in.WebhookInput, now time.Time) error {
	if _, err := s.store.GetPaymentByExternalRef(ctx, input.TransactionID); err == nil {
		return nil // повтор нотификатора
	}
	return s.store.WithinTx(ctx, func(tx out.Store) error {
		current, err := tx.GetPaymentForUpdate(ctx, original.ID)
		if err != nil {
			return err
		}
		if current.Status == string(domain.PaymentRefunded) {
			return nil
		}
		refund := out.PaymentRecord{
			ID:                  uuid.NewString(),
			SubscriptionID:      current.SubscriptionID,
			AmountKopecks:       -current.AmountKopecks,
			Currency:            current.Currency,
			Status:              string(domain.PaymentSuccess),
			Kind:                current.Kind,
			TariffPlanName:      current.TariffPlanName + " (refund)",
			ExternalReferenceID: input.TransactionID,
			CreatedAt:           now,
			UpdatedAt:           now,
		}
		if err := tx.CreatePayment(ctx, refund); err != nil {
			return err
		}
		// Оригинальный success не модифицируется (§6): refund — отдельная запись.
		return nil
	})
}

func (s *Service) handleWebhookSuccess(ctx context.Context, paymentRec *out.PaymentRecord, input in.WebhookInput, now time.Time) error {
	lateSuccess := false
	err := s.store.WithinTx(ctx, func(tx out.Store) error {
		current, err := tx.GetPaymentForUpdate(ctx, paymentRec.ID)
		if err != nil {
			return err
		}
		// §4/§6: success/failed финальны — повтор или поздний webhook не переворачивает статус.
		switch domain.PaymentStatus(current.Status) {
		case domain.PaymentSuccess:
			return nil
		case domain.PaymentFailed:
			// Поздний success после таймаута: сохраняем transaction_id для ручной
			// сверки, но статус остаётся failed (деньги мог списать провайдер).
			lateSuccess = true
			current.ExternalReferenceID = input.TransactionID
			current.UpdatedAt = now
			if err := tx.UpdatePayment(ctx, *current); err != nil {
				return err
			}
			return emit(ctx, tx, "PaymentFailed", map[string]any{
				"paymentId": current.ID, "subscriptionId": current.SubscriptionID,
				"reason": "late_success_after_timeout", "retry_possible": false,
				"transaction_id": input.TransactionID,
			})
		case domain.PaymentRefunded:
			return nil
		}
		payment := domain.Payment{Status: domain.PaymentStatus(current.Status)}
		if err := payment.MarkSuccess(input.TransactionID, now); err != nil {
			return err
		}
		current.Status = string(payment.Status)
		current.ExternalReferenceID = input.TransactionID
		current.UpdatedAt = now
		paymentRec = current
		if err := tx.UpdatePayment(ctx, *current); err != nil {
			return err
		}
		subRec, err := tx.GetSubscriptionForUpdate(ctx, current.SubscriptionID)
		if err != nil {
			return err
		}
		sub, err := subscriptionFromRecord(subRec)
		if err != nil {
			return err
		}

		switch domain.PaymentKind(current.Kind) {
		case domain.PaymentInitial:
			if sub.Status == domain.StatusPending {
				if err := sub.Activate(true, now); err != nil {
					return err
				}
				s.metricActivation("success")
				if err := tx.UpdateSubscription(ctx, subscriptionRecord(sub)); err != nil {
					return err
				}
				if input.OperationType == "recurring_bind" && input.RecurringToken != "" {
					method, methodErr := domain.NewPaymentMethod(
						uuid.New(), input.CustomerID, input.RecurringToken, input.Card,
						parseCardExpDate(input.OfferExpDate, now), now,
					)
					if methodErr == nil {
						if err := tx.UpsertPaymentMethod(ctx, paymentMethodRecord(sub.ID.String(), method)); err != nil {
							return err
						}
					}
				}
				if err := emit(ctx, tx, "SubscriptionActivated", map[string]any{
					"subscriptionId": sub.ID.String(),
					"accountId":      sub.AccountID.String(),
					"billingPeriod":  periodView(sub.Period),
					"tariffSnapshot": snapshotView(sub.Snapshot),
				}); err != nil {
					return err
				}
				if err := emit(ctx, tx, "TariffPlanActivated", map[string]any{
					"accountId":      sub.AccountID.String(),
					"tariffSnapshot": snapshotView(sub.Snapshot),
				}); err != nil {
					return err
				}
			}
		case domain.PaymentRenewal:
			if sub.Status == domain.StatusActive {
				offer, offerErr := tx.FindActiveOffer(ctx, sub.TariffPlanID.String(), now)
				if offerErr != nil {
					return fmt.Errorf("renewal offer: %w", offerErr)
				}
				start := now
				if sub.Period.End.After(now) {
					start = sub.Period.End
				}
				period, periodErr := domain.NewBillingPeriod(start, start.AddDate(0, 0, offer.BillingDays))
				if periodErr != nil {
					return periodErr
				}
				if err := sub.Renew(period, now); err != nil {
					return err
				}
				if err := tx.UpdateSubscription(ctx, subscriptionRecord(sub)); err != nil {
					return err
				}
				if err := emit(ctx, tx, "SubscriptionRenewed", map[string]any{
					"subscriptionId":   sub.ID.String(),
					"accountId":        sub.AccountID.String(),
					"newBillingPeriod": periodView(sub.Period),
					"tariffSnapshot":   snapshotView(sub.Snapshot),
					"paymentId":        current.ID,
					"occurred_at":      now,
				}); err != nil {
					return err
				}
			}
		case domain.PaymentProration:
			// Тариф уже применён в Upgrade; фиксируем факт оплаты.
			// Если карта привязывалась этим платежом (fallback без токена) — сохраняем её.
			if input.OperationType == "recurring_bind" && input.RecurringToken != "" {
				method, methodErr := domain.NewPaymentMethod(
					uuid.New(), input.CustomerID, input.RecurringToken, input.Card,
					parseCardExpDate(input.OfferExpDate, now), now,
				)
				if methodErr == nil {
					// legacy-метод с пустым токеном не должен ломать unique partial index
					if err := tx.DeactivatePaymentMethods(ctx, sub.ID.String()); err != nil {
						return err
					}
					if err := tx.UpsertPaymentMethod(ctx, paymentMethodRecord(sub.ID.String(), method)); err != nil {
						return err
					}
				}
			}
		}

		return emit(ctx, tx, "PaymentSucceeded", map[string]any{
			"paymentId":      current.ID,
			"subscriptionId": current.SubscriptionID,
			"accountId":      sub.AccountID.String(),
			"amount_kopecks": current.AmountKopecks,
			"currency":       current.Currency,
			"paymentKind":    current.Kind,
			"transaction_id": input.TransactionID,
			"occurred_at":    now,
		})
	})
	if err == nil {
		if lateSuccess {
			s.metricPayment("failed")
			s.metricPaymentDuration("failed", paymentRec.CreatedAt)
			s.metricLateSuccess()
		} else {
			s.metricPayment("success")
			s.metricPaymentDuration("success", paymentRec.CreatedAt)
		}
	}
	return err
}

func (s *Service) handleWebhookFailure(ctx context.Context, paymentRec *out.PaymentRecord, input in.WebhookInput, now time.Time) error {
	var retryPossible bool
	err := s.store.WithinTx(ctx, func(tx out.Store) error {
		current, err := tx.GetPaymentForUpdate(ctx, paymentRec.ID)
		if err != nil {
			return err
		}
		switch domain.PaymentStatus(current.Status) {
		case domain.PaymentFailed, domain.PaymentSuccess, domain.PaymentRefunded:
			return nil // финальный статус не переворачивается
		}
		payment := domain.Payment{Status: domain.PaymentStatus(current.Status)}
		if err := payment.MarkFailed(now); err != nil {
			return err
		}
		current.Status = string(payment.Status)
		current.ExternalReferenceID = input.TransactionID
		current.UpdatedAt = now
		paymentRec = current
		if err := tx.UpdatePayment(ctx, *current); err != nil {
			return err
		}
		subRec, err := tx.GetSubscriptionForUpdate(ctx, current.SubscriptionID)
		if err != nil {
			return err
		}
		sub, err := subscriptionFromRecord(subRec)
		if err != nil {
			return err
		}

		if domain.PaymentKind(current.Kind) == domain.PaymentRenewal && sub.Status == domain.StatusActive {
			sub.RegisterPaymentFailure(nil, now)
			if sub.RetryCount < 3 {
				next := now.Add(time.Duration(sub.RetryCount) * 24 * time.Hour)
				sub.NextRetryAt = &next
				retryPossible = true
			} else if err := sub.Expire(now); err != nil {
				return err
			}
			if err := tx.UpdateSubscription(ctx, subscriptionRecord(sub)); err != nil {
				return err
			}
			if sub.Status == domain.StatusExpired {
				if err := emit(ctx, tx, "SubscriptionExpired", map[string]any{
					"subscriptionId": sub.ID.String(), "expired_at": now,
				}); err != nil {
					return err
				}
				if err := emit(ctx, tx, "TariffPlanDeActivated", map[string]any{"accountId": sub.AccountID.String()}); err != nil {
					return err
				}
			}
		}

		if err := emit(ctx, tx, "PaymentFailed", map[string]any{
			"paymentId":      current.ID,
			"subscriptionId": current.SubscriptionID,
			"reason":         input.RC,
			"retry_possible": retryPossible,
		}); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.metricPayment("failed")
	s.metricPaymentDuration("failed", paymentRec.CreatedAt)
	accountID := ""
	if rec, err := s.store.GetSubscription(context.WithoutCancel(ctx), paymentRec.SubscriptionID); err == nil {
		accountID = rec.AccountID
	}
	_ = s.notifier.Send(context.WithoutCancel(ctx), out.Notification{
		Kind:           "payment_failed",
		Recipient:      s.cfg.NotificationRecipient,
		AccountID:      accountID,
		SubscriptionID: paymentRec.SubscriptionID,
		RetryPossible:  retryPossible,
	})
	return nil
}

// PatchSubscription — admin: только scheduledChange.
func (s *Service) PatchSubscription(ctx context.Context, auth in.AuthContext, input in.PatchInput) (in.SubscriptionView, error) {
	if auth.Role != "administrator" {
		return in.SubscriptionView{}, NewError(CodeForbidden, "administrator role required")
	}
	sub, rec, err := s.loadAny(ctx, input.SubscriptionID)
	if err != nil {
		return in.SubscriptionView{}, err
	}
	if input.TargetTariffPlanID == "" {
		return subscriptionView(*rec), nil
	}
	effectiveAt := sub.Period.End
	if input.EffectiveAt != nil {
		effectiveAt = *input.EffectiveAt
	}
	if err := sub.ScheduleDowngrade(mustUUID(input.TargetTariffPlanID), effectiveAt, s.now()); err != nil {
		return in.SubscriptionView{}, mapDomainError(err)
	}
	err = s.store.WithinTx(ctx, func(tx out.Store) error {
		if err := tx.UpdateSubscription(ctx, subscriptionRecord(sub)); err != nil {
			return err
		}
		return emit(ctx, tx, "SubscriptionChanged", map[string]any{
			"subscriptionId": sub.ID.String(),
			"tariffSnapshot": snapshotView(sub.Snapshot),
			"effectiveAt":    effectiveAt,
			"change_type":    "downgrade",
		})
	})
	if err != nil {
		return in.SubscriptionView{}, err
	}
	return subscriptionView(subscriptionRecord(sub)), nil
}

// AdminActivateTariff создаёт активную подписку без платежа.
func (s *Service) AdminActivateTariff(ctx context.Context, auth in.AuthContext, input in.AdminActivateInput) (in.AdminActivateResult, error) {
	if auth.Role != "administrator" {
		return in.AdminActivateResult{}, NewError(CodeForbidden, "administrator role required")
	}
	now := s.now()
	offer, err := s.store.FindActiveOffer(ctx, input.TariffPlanID, now)
	if errors.Is(err, out.ErrNotFound) {
		return in.AdminActivateResult{}, NewError(CodeInvalidInput, "tariff plan has no active offer")
	}
	if err != nil {
		return in.AdminActivateResult{}, err
	}
	start := now
	if input.PeriodStart != nil {
		start = *input.PeriodStart
	}
	end := start.AddDate(0, 0, offer.BillingDays)
	if input.PeriodEnd != nil {
		end = *input.PeriodEnd
	}
	period, err := domain.NewBillingPeriod(start, end)
	if err != nil {
		return in.AdminActivateResult{}, NewError(CodeInvalidInput, err.Error())
	}
	snapshot, err := domain.NewTariffSnapshot(domain.WeekBitmap(offer.WeekBitmap), offer.Timezone)
	if err != nil {
		return in.AdminActivateResult{}, err
	}
	sub, err := domain.NewSubscription(uuid.New(), mustUUID(input.AccountID), mustUUID(offer.TariffPlanID), mustUUID(offer.OfferID), snapshot, period, now)
	if err != nil {
		return in.AdminActivateResult{}, err
	}
	if err := sub.Activate(true, now); err != nil {
		return in.AdminActivateResult{}, err
	}
	err = s.store.WithinTx(ctx, func(tx out.Store) error {
		if err := tx.CreateSubscription(ctx, subscriptionRecord(sub)); err != nil {
			return err
		}
		if err := emit(ctx, tx, "SubscriptionActivated", map[string]any{
			"subscriptionId": sub.ID.String(), "accountId": sub.AccountID.String(),
			"billingPeriod": periodView(sub.Period), "tariffSnapshot": snapshotView(sub.Snapshot),
		}); err != nil {
			return err
		}
		return emit(ctx, tx, "TariffPlanActivated", map[string]any{
			"accountId": sub.AccountID.String(), "tariffSnapshot": snapshotView(sub.Snapshot),
		})
	})
	if err != nil {
		if isUniqueViolation(err) {
			return in.AdminActivateResult{}, NewError(CodeActiveSubscription, "account already has an active subscription")
		}
		return in.AdminActivateResult{}, err
	}
	return in.AdminActivateResult{
		SubscriptionID: sub.ID.String(), Status: string(sub.Status), TariffSnapshot: snapshotView(sub.Snapshot),
	}, nil
}

// AdminDeactivateTariff отменяет подписку аккаунта.
func (s *Service) AdminDeactivateTariff(ctx context.Context, auth in.AuthContext, accountID string) (in.AdminDeactivateResult, error) {
	if auth.Role != "administrator" {
		return in.AdminDeactivateResult{}, NewError(CodeForbidden, "administrator role required")
	}
	rec, err := s.store.GetSubscriptionByAccount(ctx, accountID)
	if errors.Is(err, out.ErrNotFound) {
		return in.AdminDeactivateResult{}, NewError(CodeNotFound, "active subscription not found")
	}
	if err != nil {
		return in.AdminDeactivateResult{}, err
	}
	sub, err := subscriptionFromRecord(rec)
	if err != nil {
		return in.AdminDeactivateResult{}, err
	}
	now := s.now()
	if err := sub.Cancel(now); err != nil {
		return in.AdminDeactivateResult{}, mapDomainError(err)
	}
	err = s.store.WithinTx(ctx, func(tx out.Store) error {
		if err := tx.UpdateSubscription(ctx, subscriptionRecord(sub)); err != nil {
			return err
		}
		return emit(ctx, tx, "SubscriptionDeActivated", map[string]any{
			"subscriptionId": sub.ID.String(), "revoked_at": now, "access_valid_until": sub.Period.End,
		})
	})
	if err != nil {
		return in.AdminDeactivateResult{}, err
	}
	return in.AdminDeactivateResult{SubscriptionID: sub.ID.String(), Status: string(sub.Status), RevokedAt: sub.RevokedAt}, nil
}
