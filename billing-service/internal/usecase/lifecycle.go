package usecase

// goca:manual — Subscription Lifecycle Worker (EventDataFlow BC#2), goca не генерирует воркеры.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/Reddetk/SayMoDev/billing-service/internal/domain"
	"github.com/Reddetk/SayMoDev/billing-service/internal/port/out"
)

// RunLifecycle запускает воркер каждые interval до отмены ctx.
func (s *Service) RunLifecycle(ctx context.Context, interval time.Duration, logger *slog.Logger) {
	if interval <= 0 {
		interval = 15 * time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.LifecycleTick(ctx); err != nil {
				logger.Error("lifecycle tick failed", slog.String("error", err.Error()))
			}
		}
	}
}

// LifecycleTick — одна итерация: renewal, expiry suspended, pre-renewal notice.
func (s *Service) LifecycleTick(ctx context.Context) error {
	var errs []error
	errs = append(errs, s.renewDueSubscriptions(ctx))
	errs = append(errs, s.reconcileStaleRenewals(ctx))
	errs = append(errs, s.reconcileStaleProrations(ctx))
	errs = append(errs, s.expireSuspendedSubscriptions(ctx))
	errs = append(errs, s.expireCancelledSubscriptions(ctx))
	errs = append(errs, s.expireStalePending(ctx))
	errs = append(errs, s.notifyPreRenewal(ctx))
	errs = append(errs, s.notifyCardExpiry(ctx))
	return errors.Join(errs...)
}

func (s *Service) expireCancelledSubscriptions(ctx context.Context) error {
	now := s.now()
	records, err := s.store.ListSubscriptionsDue(ctx, string(domain.StatusCancelled), now, 100)
	if err != nil {
		return err
	}
	var errs []error
	for _, rec := range records {
		expired := false
		if err := s.store.WithinTx(ctx, func(tx out.Store) error {
			current, err := tx.GetSubscriptionForUpdate(ctx, rec.ID)
			if err != nil {
				return err
			}
			sub, err := subscriptionFromRecord(current)
			if err != nil {
				return err
			}
			if sub.Status != domain.StatusCancelled || !sub.Period.Expired(now) {
				return nil
			}
			if err := sub.Expire(now); err != nil {
				return err
			}
			if err := tx.UpdateSubscription(ctx, subscriptionRecord(sub)); err != nil {
				return err
			}
			expired = true
			return emit(ctx, tx, "TariffPlanDeActivated", map[string]any{"accountId": sub.AccountID.String()})
		}); err != nil {
			errs = append(errs, err)
			continue
		}
		if expired {
			s.unbind(ctx, rec.ID)
		}
	}
	return errors.Join(errs...)
}

func (s *Service) expireStalePending(ctx context.Context) error {
	records, err := s.store.ListStalePendingSubscriptions(ctx, s.now().Add(-24*time.Hour), 100)
	if err != nil {
		return err
	}
	var errs []error
	for _, rec := range records {
		var subscriptionID string
		if err := s.store.WithinTx(ctx, func(tx out.Store) error {
			current, err := tx.GetSubscriptionForUpdate(ctx, rec.ID)
			if err != nil {
				return err
			}
			sub, err := subscriptionFromRecord(current)
			if err != nil {
				return err
			}
			if sub.Status != domain.StatusPending {
				return nil
			}
			if err := sub.Abandon(s.now()); err != nil {
				return err
			}
			if err := tx.UpdateSubscription(ctx, subscriptionRecord(sub)); err != nil {
				return err
			}
			subscriptionID = sub.ID.String()
			return emit(ctx, tx, "SubscriptionExpired", map[string]any{
				"subscriptionId": sub.ID.String(), "expired_at": s.now(),
			})
		}); err != nil {
			errs = append(errs, err)
			continue
		}
		_ = subscriptionID
	}
	return errors.Join(errs...)
}

// unbind отвязывает карту (идемпотентно) после истечения подписки (ADR-005).
// Локальный токен стирается только после успешного recurring_unbind.
func (s *Service) unbind(ctx context.Context, subscriptionID string) {
	method, err := s.store.GetActivePaymentMethod(ctx, subscriptionID)
	if err != nil {
		return
	}
	if method.RecurringToken != "" {
		if err := s.acquiring.Unbind(ctx, out.UnbindRequest{
			RecurringToken: method.RecurringToken, CustomerID: method.WSBCustomerID,
		}); err != nil {
			return // токен сохраняем для повтора (AccountDeleted/ручная реконсиляция)
		}
	}
	_ = s.store.DeactivatePaymentMethods(ctx, subscriptionID)
}

func (s *Service) renewDueSubscriptions(ctx context.Context) error {
	now := s.now()
	records, err := s.store.ListSubscriptionsDue(ctx, string(domain.StatusActive), now, 100)
	if err != nil {
		return err
	}
	var errs []error
	for i := range records {
		if err := s.renewOne(ctx, records[i].ID, now); err != nil {
			errs = append(errs, fmt.Errorf("renew %s: %w", records[i].ID, err))
		}
	}
	return errors.Join(errs...)
}

func (s *Service) renewOne(ctx context.Context, subscriptionID string, now time.Time) error {
	var (
		payment domain.Payment
		method  *out.PaymentMethodRecord
		worked  bool
	)
	err := s.store.WithinTx(ctx, func(tx out.Store) error {
		rec, err := tx.GetSubscriptionForUpdate(ctx, subscriptionID)
		if err != nil {
			return err
		}
		sub, err := subscriptionFromRecord(rec)
		if err != nil {
			return err
		}
		if sub.Status != domain.StatusActive {
			return nil
		}

		// Downgrade вступает в силу на границе периода — до создания renewal-платежа.
		if sub.Period.Expired(now) && sub.Scheduled != nil && !now.Before(sub.Scheduled.EffectiveAt) {
			offer, offerErr := tx.FindActiveOffer(ctx, sub.Scheduled.TargetTariffPlanID.String(), now)
			if offerErr != nil {
				return fmt.Errorf("scheduled offer: %w", offerErr)
			}
			snapshot, snapErr := domain.NewTariffSnapshot(domain.WeekBitmap(offer.WeekBitmap), offer.Timezone)
			if snapErr != nil {
				return snapErr
			}
			if err := sub.ApplyScheduledChange(mustUUID(offer.OfferID), mustUUID(offer.TariffPlanID), snapshot, now); err != nil {
				return err
			}
			if err := tx.UpdateSubscription(ctx, subscriptionRecord(sub)); err != nil {
				return err
			}
		}

		offer, err := tx.FindActiveOffer(ctx, sub.TariffPlanID.String(), now)
		if err != nil {
			return fmt.Errorf("renewal offer: %w", err)
		}
		price, err := domain.NewPrice(offer.PriceKopecks, domain.Currency(offer.Currency))
		if err != nil {
			return err
		}
		payment, err = domain.NewPayment(uuid.New(), price, domain.PaymentRenewal, offer.PlanName, now)
		if err != nil {
			return err
		}
		// Период НЕ продлевается до успешного webhook (§ Renewal Transaction Boundary):
		// иначе провал списания даёт бесплатные дни.
		if err := tx.CreatePayment(ctx, paymentRecord(sub.ID.String(), payment)); err != nil {
			return err
		}
		method, _ = tx.GetActivePaymentMethod(ctx, sub.ID.String())
		worked = true
		return nil
	})
	if err != nil {
		return err
	}
	if !worked {
		return nil
	}
	if method == nil || method.RecurringToken == "" {
		return s.failRenewal(ctx, payment, "no recurring token", now)
	}
	if err := s.acquiring.RecurringPay(ctx, out.RecurringPayRequest{
		OrderNum:       payment.ID.String(),
		AmountKopecks:  payment.Amount.AmountKopecks,
		Currency:       string(payment.Amount.Currency),
		RecurringToken: method.RecurringToken,
		CustomerID:     method.WSBCustomerID,
	}); err != nil {
		return s.failRenewal(ctx, payment, err.Error(), now)
	}
	return nil
}

func (s *Service) failRenewal(ctx context.Context, payment domain.Payment, reason string, now time.Time) error {
	var (
		accountID      string
		subscriptionID string
		expired        bool
		retry          bool
	)
	err := s.store.WithinTx(ctx, func(tx out.Store) error {
		rec, err := tx.GetPaymentForUpdate(ctx, payment.ID.String())
		if err != nil {
			return err
		}
		if domain.PaymentStatus(rec.Status) != domain.PaymentPending {
			return nil
		}
		rec.Status = string(domain.PaymentFailed)
		rec.UpdatedAt = now
		if err := tx.UpdatePayment(ctx, *rec); err != nil {
			return err
		}
		subRec, err := tx.GetSubscriptionForUpdate(ctx, rec.SubscriptionID)
		if err != nil {
			return err
		}
		sub, err := subscriptionFromRecord(subRec)
		if err != nil {
			return err
		}
		accountID = sub.AccountID.String()
		subscriptionID = sub.ID.String()
		if sub.Status == domain.StatusActive {
			sub.RegisterPaymentFailure(nil, now)
			if sub.RetryCount < 3 {
				next := now.Add(time.Duration(sub.RetryCount) * 24 * time.Hour)
				sub.NextRetryAt = &next
				retry = true
			} else if err := sub.Expire(now); err != nil {
				return err
			}
			if err := tx.UpdateSubscription(ctx, subscriptionRecord(sub)); err != nil {
				return err
			}
			expired = sub.Status == domain.StatusExpired
		}
		if err := emit(ctx, tx, "PaymentFailed", map[string]any{
			"paymentId": rec.ID, "subscriptionId": rec.SubscriptionID,
			"reason": reason, "retry_possible": retry,
		}); err != nil {
			return err
		}
		if expired {
			if err := emit(ctx, tx, "SubscriptionExpired", map[string]any{
				"subscriptionId": sub.ID.String(), "expired_at": now,
			}); err != nil {
				return err
			}
			return emit(ctx, tx, "TariffPlanDeActivated", map[string]any{"accountId": accountID})
		}
		return nil
	})
	if err != nil {
		return err
	}
	_ = s.notifier.Send(context.WithoutCancel(ctx), out.Notification{
		Kind: "payment_failed", Recipient: s.cfg.NotificationRecipient,
		AccountID: accountID, SubscriptionID: subscriptionID, RetryPossible: retry,
	})
	if expired {
		s.unbind(ctx, subscriptionID)
	}
	return nil
}

func (s *Service) reconcileStaleProrations(ctx context.Context) error {
	records, err := s.store.ListStaleProrationPayments(ctx, s.now().Add(-30*time.Minute), 100)
	if err != nil {
		return err
	}
	var errs []error
	for _, rec := range records {
		if err := s.store.WithinTx(ctx, func(tx out.Store) error {
			payment, err := tx.GetPaymentForUpdate(ctx, rec.ID)
			if err != nil {
				return err
			}
			if domain.PaymentStatus(payment.Status) != domain.PaymentPending {
				return nil
			}
			payment.Status = string(domain.PaymentFailed)
			payment.UpdatedAt = s.now()
			if err := tx.UpdatePayment(ctx, *payment); err != nil {
				return err
			}

			if payment.AppliedOfferID != nil && payment.RevertTariffPlanID != nil && payment.RevertOfferID != nil {
				subRec, err := tx.GetSubscriptionForUpdate(ctx, payment.SubscriptionID)
				if err != nil {
					return err
				}
				sub, err := subscriptionFromRecord(subRec)
				if err != nil {
					return err
				}
				// Откатываем только если подписка всё ещё на тарифе этого upgrade.
				if sub.OfferID.String() == *payment.AppliedOfferID {
					appliedSnapshot := sub.Snapshot
					sub.TariffPlanID = mustUUID(*payment.RevertTariffPlanID)
					sub.OfferID = mustUUID(*payment.RevertOfferID)
					if payment.RevertWeekBitmap != nil && payment.RevertTimezone != nil {
						snapshot, snapErr := domain.NewTariffSnapshot(domain.WeekBitmap(*payment.RevertWeekBitmap), *payment.RevertTimezone)
						if snapErr != nil {
							return snapErr
						}
						sub.Snapshot = snapshot
					}
					sub.UpdatedAt = s.now()
					if err := tx.UpdateSubscription(ctx, subscriptionRecord(sub)); err != nil {
						return err
					}
					// BC#4 должен получить откат тарифа, иначе доступ останется новым.
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
				}
			}

			return emit(ctx, tx, "PaymentFailed", map[string]any{
				"paymentId": payment.ID, "subscriptionId": payment.SubscriptionID,
				"reason": "proration_charge_timeout", "retry_possible": false,
			})
		}); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *Service) reconcileStaleRenewals(ctx context.Context) error {
	records, err := s.store.ListStaleRenewalPayments(ctx, s.now().Add(-30*time.Minute), 100)
	if err != nil {
		return err
	}
	var errs []error
	for _, rec := range records {
		payment, err := domain.NewPayment(mustUUID(rec.ID), domain.Money{
			AmountKopecks: rec.AmountKopecks, Currency: domain.Currency(rec.Currency),
		}, domain.PaymentKind(rec.Kind), rec.TariffPlanName, rec.CreatedAt)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if err := s.failRenewal(ctx, payment, "webhook timeout", s.now()); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *Service) expireSuspendedSubscriptions(ctx context.Context) error {
	now := s.now()
	records, err := s.store.ListSubscriptionsDue(ctx, string(domain.StatusSuspended), now, 100)
	if err != nil {
		return err
	}
	var errs []error
	for _, rec := range records {
		expired := false
		if err := s.store.WithinTx(ctx, func(tx out.Store) error {
			current, err := tx.GetSubscriptionForUpdate(ctx, rec.ID)
			if err != nil {
				return err
			}
			sub, err := subscriptionFromRecord(current)
			if err != nil {
				return err
			}
			if sub.Status != domain.StatusSuspended || !sub.Period.Expired(now) {
				return nil
			}
			if err := sub.Expire(now); err != nil {
				return err
			}
			if err := tx.UpdateSubscription(ctx, subscriptionRecord(sub)); err != nil {
				return err
			}
			expired = true
			if err := emit(ctx, tx, "SubscriptionExpired", map[string]any{
				"subscriptionId": sub.ID.String(), "expired_at": now,
			}); err != nil {
				return err
			}
			return emit(ctx, tx, "TariffPlanDeActivated", map[string]any{"accountId": sub.AccountID.String()})
		}); err != nil {
			errs = append(errs, err)
			continue
		}
		if expired {
			s.unbind(ctx, rec.ID)
		}
	}
	return errors.Join(errs...)
}

func (s *Service) notifyCardExpiry(ctx context.Context) error {
	methods, err := s.store.ListExpiringPaymentMethods(ctx, s.now().AddDate(0, 0, 30))
	if err != nil {
		return err
	}
	for _, method := range methods {
		key := "notified:card:" + method.SubscriptionID + ":" + method.CardExpDate.Format("2006-01")
		if _, err := s.cache.Get(ctx, key); err == nil {
			continue
		}
		if err := s.notifier.Send(ctx, out.Notification{
			Kind: "card_expiring", Recipient: s.cfg.NotificationRecipient,
			AccountID: method.AccountID, SubscriptionID: method.SubscriptionID, CardExpDate: method.CardExpDate,
		}); err != nil {
			continue
		}
		_ = s.cache.Set(ctx, key, []byte("1"), 31*24*time.Hour)
	}
	return nil
}

func (s *Service) notifyPreRenewal(ctx context.Context) error {
	now := s.now()
	records, err := s.store.ListSubscriptionsEndingBefore(ctx, string(domain.StatusActive), now.AddDate(0, 0, 3), 100)
	if err != nil {
		return err
	}
	for _, rec := range records {
		key := "notified:pre:" + rec.ID + ":" + rec.PeriodEnd.Format(time.RFC3339)
		if _, err := s.cache.Get(ctx, key); err == nil {
			continue
		}
		if err := s.notifier.Send(ctx, out.Notification{
			Kind: "pre_renewal", Recipient: s.cfg.NotificationRecipient,
			AccountID: rec.AccountID, SubscriptionID: rec.ID, PeriodEnd: rec.PeriodEnd,
		}); err != nil {
			continue // Postbox недоступен: лог, бизнес-операция не прерывается
		}
		_ = s.cache.Set(ctx, key, []byte("1"), 25*time.Hour)
	}
	return nil
}
