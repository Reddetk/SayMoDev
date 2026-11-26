package usecase

// goca:manual — маппинг домен ↔ port DTO и общие помощники BC#2.

import (
	"context"
	"crypto/md5"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Reddetk/SayMoDev/billing-service/internal/domain"
	"github.com/Reddetk/SayMoDev/billing-service/internal/port/in"
	"github.com/Reddetk/SayMoDev/billing-service/internal/port/out"
)

func (s *Service) loadOwned(ctx context.Context, auth in.AuthContext, subscriptionID string) (*domain.Subscription, *out.SubscriptionRecord, error) {
	rec, err := s.store.GetSubscription(ctx, subscriptionID)
	if errors.Is(err, out.ErrNotFound) {
		return nil, nil, NewError(CodeNotFound, "subscription not found")
	}
	if err != nil {
		return nil, nil, err
	}
	if auth.Role != "administrator" && rec.AccountID != auth.AccountID {
		return nil, nil, NewError(CodeNotFound, "subscription not found")
	}
	sub, err := subscriptionFromRecord(rec)
	if err != nil {
		return nil, nil, err
	}
	return sub, rec, nil
}

func (s *Service) loadAny(ctx context.Context, subscriptionID string) (*domain.Subscription, *out.SubscriptionRecord, error) {
	rec, err := s.store.GetSubscription(ctx, subscriptionID)
	if errors.Is(err, out.ErrNotFound) {
		return nil, nil, NewError(CodeNotFound, "subscription not found")
	}
	if err != nil {
		return nil, nil, err
	}
	sub, err := subscriptionFromRecord(rec)
	if err != nil {
		return nil, nil, err
	}
	return sub, rec, nil
}

func subscriptionFromRecord(rec *out.SubscriptionRecord) (*domain.Subscription, error) {
	id, err := parseRecordUUID(rec.ID, "id")
	if err != nil {
		return nil, err
	}
	accountID, err := parseRecordUUID(rec.AccountID, "account_id")
	if err != nil {
		return nil, err
	}
	tariffPlanID, err := parseRecordUUID(rec.TariffPlanID, "tariff_plan_id")
	if err != nil {
		return nil, err
	}
	offerID, err := parseRecordUUID(rec.OfferID, "offer_id")
	if err != nil {
		return nil, err
	}
	period, err := domain.NewBillingPeriod(rec.PeriodStart, rec.PeriodEnd)
	if err != nil {
		return nil, err
	}
	snapshot, err := domain.NewTariffSnapshot(domain.WeekBitmap(rec.WeekBitmap), rec.Timezone)
	if err != nil {
		return nil, err
	}
	sub := &domain.Subscription{
		ID:           id,
		AccountID:    accountID,
		TariffPlanID: tariffPlanID,
		OfferID:      offerID,
		Status:       domain.SubscriptionStatus(rec.Status),
		Snapshot:     snapshot,
		Period:       period,
		RevokedAt:    rec.RevokedAt,
		RetryCount:   rec.RetryCount,
		NextRetryAt:  rec.NextRetryAt,
		CreatedAt:    rec.CreatedAt,
		UpdatedAt:    rec.UpdatedAt,
	}
	if rec.ScheduledTarget != nil && rec.ScheduledAt != nil {
		target, err := parseRecordUUID(*rec.ScheduledTarget, "scheduled_target")
		if err != nil {
			return nil, err
		}
		sub.Scheduled = &domain.ScheduledChange{TargetTariffPlanID: target, EffectiveAt: *rec.ScheduledAt}
	}
	return sub, nil
}

func parseRecordUUID(raw, field string) (uuid.UUID, error) {
	parsed, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, fmt.Errorf("invalid %s in persistence: %w", field, err)
	}
	return parsed, nil
}

func subscriptionRecord(sub *domain.Subscription) out.SubscriptionRecord {
	rec := out.SubscriptionRecord{
		ID:           sub.ID.String(),
		AccountID:    sub.AccountID.String(),
		TariffPlanID: sub.TariffPlanID.String(),
		OfferID:      sub.OfferID.String(),
		Status:       string(sub.Status),
		WeekBitmap:   int16(sub.Snapshot.WeekBitmap),
		Timezone:     sub.Snapshot.Timezone,
		PeriodStart:  sub.Period.Start,
		PeriodEnd:    sub.Period.End,
		RevokedAt:    sub.RevokedAt,
		RetryCount:   sub.RetryCount,
		NextRetryAt:  sub.NextRetryAt,
		CreatedAt:    sub.CreatedAt,
		UpdatedAt:    sub.UpdatedAt,
	}
	if sub.Scheduled != nil {
		target := sub.Scheduled.TargetTariffPlanID.String()
		effective := sub.Scheduled.EffectiveAt
		rec.ScheduledTarget = &target
		rec.ScheduledAt = &effective
	}
	return rec
}

func paymentRecord(subscriptionID string, payment domain.Payment) out.PaymentRecord {
	return out.PaymentRecord{
		ID:                  payment.ID.String(),
		SubscriptionID:      subscriptionID,
		AmountKopecks:       payment.Amount.AmountKopecks,
		Currency:            string(payment.Amount.Currency),
		Status:              string(payment.Status),
		Kind:                string(payment.Kind),
		TariffPlanName:      payment.TariffPlanName,
		ExternalReferenceID: payment.ExternalReferenceID,
		CreatedAt:           payment.CreatedAt,
		UpdatedAt:           payment.UpdatedAt,
	}
}

func paymentMethodRecord(subscriptionID string, method domain.PaymentMethod) out.PaymentMethodRecord {
	cardExp := method.CardExpDate
	return out.PaymentMethodRecord{
		ID:             method.ID.String(),
		SubscriptionID: subscriptionID,
		Type:           method.Type,
		WSBCustomerID:  method.WSBCustomerID,
		RecurringToken: method.RecurringToken,
		CardMask:       method.CardMask,
		CardExpDate:    &cardExp,
		IsActive:       method.IsActive,
		BoundAt:        method.BoundAt,
	}
}

func subscriptionView(rec out.SubscriptionRecord) in.SubscriptionView {
	view := in.SubscriptionView{
		SubscriptionID: rec.ID,
		AccountID:      rec.AccountID,
		Status:         rec.Status,
		TariffSnapshot: in.TariffSnapshotView{WeekBitmap: rec.WeekBitmap, Timezone: rec.Timezone},
		BillingPeriod:  in.PeriodView{PeriodStart: rec.PeriodStart, PeriodEnd: rec.PeriodEnd},
		RevokedAt:      rec.RevokedAt,
	}
	if rec.ScheduledTarget != nil && rec.ScheduledAt != nil {
		view.ScheduledChange = &in.ScheduledChangeView{TargetTariffPlanID: *rec.ScheduledTarget, EffectiveAt: *rec.ScheduledAt}
	}
	return view
}

func snapshotView(snapshot domain.TariffSnapshot) in.TariffSnapshotView {
	return in.TariffSnapshotView{WeekBitmap: int16(snapshot.WeekBitmap), Timezone: snapshot.Timezone}
}

func periodView(period domain.BillingPeriod) in.PeriodView {
	return in.PeriodView{PeriodStart: period.Start, PeriodEnd: period.End}
}

func scheduledView(scheduled *domain.ScheduledChange) *in.ScheduledChangeView {
	if scheduled == nil {
		return nil
	}
	return &in.ScheduledChangeView{TargetTariffPlanID: scheduled.TargetTariffPlanID.String(), EffectiveAt: scheduled.EffectiveAt}
}

func (s *Service) buildPreview(ctx context.Context, sub *domain.Subscription, rec *out.SubscriptionRecord, targetTariffPlanID string) (in.UpgradePreview, error) {
	preview, _, _, err := s.previewWithStore(ctx, s.store, sub, rec, targetTariffPlanID, "")
	return preview, err
}

// previewWithStore считает proration по актуальным офферам и (опционально)
// сверяет переданный quoteId со снимком: account/subscription/offer/цена/дни.
func (s *Service) previewWithStore(
	ctx context.Context,
	store out.Store,
	sub *domain.Subscription,
	rec *out.SubscriptionRecord,
	targetTariffPlanID, quoteID string,
) (in.UpgradePreview, *out.QuoteSnapshot, *out.OfferRecord, error) {
	if sub.Status != domain.StatusActive {
		return in.UpgradePreview{}, nil, nil, NewError(CodeInvalidInput, "only active subscriptions can be upgraded")
	}
	now := s.now()
	remaining := sub.Period.RemainingDays(now)
	if remaining <= 0 {
		return in.UpgradePreview{}, nil, nil, NewError(CodePeriodEnded, "billing period has ended")
	}
	current, err := store.GetOffer(ctx, rec.OfferID)
	if err != nil {
		return in.UpgradePreview{}, nil, nil, err
	}
	target, err := store.FindActiveOffer(ctx, targetTariffPlanID, now)
	if errors.Is(err, out.ErrNotFound) {
		return in.UpgradePreview{}, nil, nil, NewError(CodeInvalidInput, "target tariff has no active offer")
	}
	if err != nil {
		return in.UpgradePreview{}, nil, nil, err
	}
	if target.Currency != current.Currency {
		return in.UpgradePreview{}, nil, nil, NewError(CodeInvalidInput, "currency mismatch")
	}

	oldPrice, _ := domain.NewPrice(current.PriceKopecks, domain.Currency(current.Currency))
	newPrice, _ := domain.NewPrice(target.PriceKopecks, domain.Currency(target.Currency))
	charge, err := domain.CalculateProration(oldPrice, newPrice, current.BillingDays, remaining)
	if err != nil {
		return in.UpgradePreview{}, nil, nil, mapDomainError(err)
	}

	quote := &out.QuoteSnapshot{
		QuoteID:             uuid.NewString(),
		AccountID:           rec.AccountID,
		SubscriptionID:      rec.ID,
		CurrentTariffPlanID: rec.TariffPlanID,
		TargetTariffPlanID:  target.TariffPlanID,
		TargetOfferID:       target.OfferID,
		OldPriceKopecks:     current.PriceKopecks,
		NewPriceKopecks:     target.PriceKopecks,
		ChargeKopecks:       charge.AmountKopecks,
		Currency:            target.Currency,
		BillingDays:         current.BillingDays,
		RemainingDays:       remaining,
		ExpiresAt:           now.Add(s.cfg.QuoteTTL),
	}
	payload, err := json.Marshal(quote)
	if err != nil {
		return in.UpgradePreview{}, nil, nil, err
	}
	if err := s.cache.Set(ctx, "quote:"+quote.QuoteID, payload, s.cfg.QuoteTTL); err != nil {
		return in.UpgradePreview{}, nil, nil, fmt.Errorf("cache quote: %w", err)
	}

	fresh := quotePreview(quote)
	if quoteID == "" {
		return fresh, quote, target, nil
	}

	loaded, err := s.loadQuote(ctx, quoteID)
	if err != nil || !sameQuote(loaded, quote) {
		return fresh, quote, target, &in.QuoteExpiredError{Refreshed: fresh}
	}
	return quotePreview(loaded), loaded, target, nil
}

func sameQuote(loaded, fresh *out.QuoteSnapshot) bool {
	return loaded.AccountID == fresh.AccountID &&
		loaded.SubscriptionID == fresh.SubscriptionID &&
		loaded.TargetTariffPlanID == fresh.TargetTariffPlanID &&
		loaded.TargetOfferID == fresh.TargetOfferID &&
		loaded.OldPriceKopecks == fresh.OldPriceKopecks &&
		loaded.NewPriceKopecks == fresh.NewPriceKopecks &&
		loaded.ChargeKopecks == fresh.ChargeKopecks &&
		loaded.Currency == fresh.Currency &&
		loaded.BillingDays == fresh.BillingDays &&
		loaded.RemainingDays == fresh.RemainingDays
}

func quotePreview(quote *out.QuoteSnapshot) in.UpgradePreview {
	return in.UpgradePreview{
		CurrentTariffPlanID: quote.CurrentTariffPlanID,
		TargetTariffPlanID:  quote.TargetTariffPlanID,
		Proration: in.ProrationView{
			AmountKopecks: quote.ChargeKopecks,
			Currency:      quote.Currency,
			RemainingDays: quote.RemainingDays,
			NewDailyRate:  ceilDiv(quote.NewPriceKopecks, int64(quote.BillingDays)),
			OldDailyRate:  ceilDiv(quote.OldPriceKopecks, int64(quote.BillingDays)),
		},
		QuoteID:        quote.QuoteID,
		QuoteExpiresAt: quote.ExpiresAt,
	}
}

func (s *Service) loadQuote(ctx context.Context, quoteID string) (*out.QuoteSnapshot, error) {
	payload, err := s.cache.Get(ctx, "quote:"+quoteID)
	if err != nil {
		return nil, NewError(CodeQuoteExpired, "quote not found or expired")
	}
	var quote out.QuoteSnapshot
	if err := json.Unmarshal(payload, &quote); err != nil {
		return nil, NewError(CodeQuoteExpired, "quote is corrupted")
	}
	if s.now().After(quote.ExpiresAt) {
		return nil, NewError(CodeQuoteExpired, "quote expired")
	}
	return &quote, nil
}

func (s *Service) verifyWebhook(input in.WebhookInput) error {
	if s.cfg.WebhookSecret == "" {
		s.metricSignatureFailure()
		return NewError(CodeForbidden, "webhook secret is not configured")
	}
	expected := webhookSignature(input, s.cfg.WebhookSecret)
	if subtle.ConstantTimeCompare([]byte(strings.ToLower(expected)), []byte(strings.ToLower(input.Signature))) != 1 {
		s.metricSignatureFailure()
		return NewError(CodeForbidden, "invalid webhook signature")
	}
	now := s.now()
	delta := now.Unix() - input.BatchTimestamp
	if delta < 0 {
		delta = -delta
	}
	if delta > 300 {
		s.metricSignatureFailure()
		return NewError(CodeForbidden, "stale webhook")
	}
	return nil
}

// webhookSignature — MD5(batch_timestamp + currency_id + amount + payment_method +
// order_id + site_order_id + transaction_id + payment_type + rrn + card + SecretKey).
func webhookSignature(input in.WebhookInput, secret string) string {
	raw := strconv.FormatInt(input.BatchTimestamp, 10) + input.CurrencyID + input.Amount + input.PaymentMethod +
		input.OrderID + input.SiteOrderID + input.TransactionID + strconv.Itoa(input.PaymentType) +
		input.RRN + input.Card + secret
	sum := md5.Sum([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func decimalToKopecks(raw string) (int64, error) {
	parts := strings.Split(raw, ".")
	if len(parts) > 2 || parts[0] == "" {
		return 0, fmt.Errorf("invalid decimal")
	}
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, err
	}
	if whole < 0 {
		return 0, fmt.Errorf("negative amount")
	}
	var fraction int64
	if len(parts) == 2 {
		if len(parts[1]) > 2 {
			return 0, fmt.Errorf("more than 2 decimals")
		}
		frac := parts[1]
		for len(frac) < 2 {
			frac += "0"
		}
		fraction, err = strconv.ParseInt(frac, 10, 64)
		if err != nil {
			return 0, err
		}
	}
	if whole > domain.MaxAmountKopecks/100 {
		return 0, fmt.Errorf("amount overflow")
	}
	return whole*100 + fraction, nil
}

func parseCardExpDate(raw string, fallback time.Time) time.Time {
	parsed, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return fallback
	}
	return parsed
}

func ceilDiv(value, divisor int64) int64 {
	if divisor == 0 {
		return 0
	}
	return (value + divisor - 1) / divisor
}

func mustUUID(raw string) uuid.UUID {
	parsed, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil
	}
	return parsed
}

func mapDomainError(err error) error {
	switch {
	case errors.Is(err, domain.ErrNotUpgrade):
		return NewError(CodeNotUpgrade, "target tariff is not an upgrade")
	case errors.Is(err, domain.ErrPeriodExpired):
		return NewError(CodePeriodEnded, "billing period has ended")
	case errors.Is(err, domain.ErrPaymentRequired):
		return NewError(CodePaymentFailed, "payment must succeed before activation")
	case errors.Is(err, domain.ErrInvalidTransition):
		return NewError(CodeInvalidInput, err.Error())
	case errors.Is(err, domain.ErrInvalidArgument), errors.Is(err, domain.ErrAmountLimit), errors.Is(err, domain.ErrOverflow):
		return NewError(CodeInvalidInput, err.Error())
	default:
		return err
	}
}

func emit(ctx context.Context, store out.Store, eventType string, payload any) error {
	eventID := uuid.NewString()
	envelope := map[string]any{
		"event_id":       eventID,
		"event_type":     eventType,
		"event_version":  "v1",
		"correlation_id": uuid.NewString(),
		"occurred_at":    time.Now().UTC(),
		"payload":        payload,
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	return store.InsertOutbox(ctx, eventID, eventType, encoded)
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "23505")
}
