package usecase

// goca:manual — consumer BC#1 events (T2) + ADR-005 archival.

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Reddetk/SayMoDev/billing-service/internal/domain"
	"github.com/Reddetk/SayMoDev/billing-service/internal/port/out"
)

// IdentityEvent — событие BC#1 из Kafka.
type IdentityEvent struct {
	EventID   string
	EventType string
	Payload   json.RawMessage
}

type identityPayload struct {
	AccountID  string `json:"accountId"`
	AccountID2 string `json:"account_id"`
	Reason     string `json:"reason"`
}

func (p identityPayload) account() string {
	if p.AccountID != "" {
		return p.AccountID
	}
	return p.AccountID2
}

// HandleIdentityEvent обрабатывает AccountLocked*/AccountUnlocked/AccountDeleted идемпотентно.
func (s *Service) HandleIdentityEvent(ctx context.Context, event IdentityEvent) error {
	var payload identityPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return NewError(CodeInvalidInput, "invalid identity event payload")
	}
	accountID := payload.account()
	if accountID == "" {
		return NewError(CodeInvalidInput, "accountId is required")
	}

	if event.EventType == "AccountDeleted" {
		return s.handleAccountDeleted(ctx, event, accountID)
	}

	return s.store.WithinTx(ctx, func(tx out.Store) error {
		processed, err := tx.ProcessedEventExists(ctx, event.EventID, "billing-service")
		if err != nil {
			return err
		}
		if processed {
			return nil
		}

		rec, err := tx.GetSubscriptionByAccount(ctx, accountID)
		if errors.Is(err, out.ErrNotFound) {
			return tx.MarkProcessedEvent(ctx, event.EventID, "billing-service")
		}
		if err != nil {
			return err
		}
		sub, err := subscriptionFromRecord(rec)
		if err != nil {
			return err
		}
		now := s.now()

		switch event.EventType {
		case "AccountLockedByFailedAttempts", "AccountLockedByAdmin":
			if sub.Status == domain.StatusPending || sub.Status == domain.StatusActive {
				if err := sub.Suspend(now); err != nil {
					return err
				}
				if err := tx.UpdateSubscription(ctx, subscriptionRecord(sub)); err != nil {
					return err
				}
				if err := emit(ctx, tx, "SubscriptionSuspended", map[string]any{
					"subscriptionId": sub.ID.String(), "reason": payload.Reason,
				}); err != nil {
					return err
				}
			}
			if err := tx.UpsertAccountStatus(ctx, accountID, "locked"); err != nil {
				return err
			}
		case "AccountUnlocked":
			if sub.Status == domain.StatusSuspended {
				if sub.Period.Expired(now) {
					if err := sub.Expire(now); err != nil {
						return err
					}
					if err := tx.UpdateSubscription(ctx, subscriptionRecord(sub)); err != nil {
						return err
					}
					if err := emit(ctx, tx, "SubscriptionExpired", map[string]any{
						"subscriptionId": sub.ID.String(), "expired_at": now,
					}); err != nil {
						return err
					}
					if err := emit(ctx, tx, "TariffPlanDeActivated", map[string]any{"accountId": accountID}); err != nil {
						return err
					}
				} else {
					if err := sub.Resume(now); err != nil {
						return err
					}
					if err := tx.UpdateSubscription(ctx, subscriptionRecord(sub)); err != nil {
						return err
					}
					if err := emit(ctx, tx, "SubscriptionResumed", map[string]any{"subscriptionId": sub.ID.String()}); err != nil {
						return err
					}
				}
			}
			if err := tx.UpsertAccountStatus(ctx, accountID, "active"); err != nil {
				return err
			}
		default:
			// Не наши события (SessionCreated, AccessTokenRevoked, ...): ack без обработки,
			// иначе offset застревает на чужом типе.
			return tx.MarkProcessedEvent(ctx, event.EventID, "billing-service")
		}

		return tx.MarkProcessedEvent(ctx, event.EventID, "billing-service")
	})
}

// handleAccountDeleted: (1) recurring_unbind, (2) archive snapshot, (3) delete subscription.
func (s *Service) handleAccountDeleted(ctx context.Context, event IdentityEvent, accountID string) error {
	processed, err := s.store.ProcessedEventExists(ctx, event.EventID, "billing-service")
	if err != nil {
		return err
	}
	if processed {
		return nil
	}

	rec, err := s.store.GetLatestSubscriptionByAccount(ctx, accountID)
	if errors.Is(err, out.ErrNotFound) {
		return s.store.MarkProcessedEvent(ctx, event.EventID, "billing-service")
	}
	if err != nil {
		return err
	}
	method, err := s.store.GetActivePaymentMethod(ctx, rec.ID)
	if err != nil && !errors.Is(err, out.ErrNotFound) {
		return err
	}
	if method != nil && method.RecurringToken != "" {
		if err := s.acquiring.Unbind(ctx, out.UnbindRequest{
			RecurringToken: method.RecurringToken, CustomerID: method.WSBCustomerID,
		}); err != nil {
			return err
		}
	}

	payments, err := s.store.ListPayments(ctx, rec.ID)
	if err != nil {
		return err
	}
	// ADR-005: recurring_token не архивируется.
	var archivedMethod *out.PaymentMethodRecord
	if method != nil {
		copy := *method
		copy.RecurringToken = ""
		archivedMethod = &copy
	}
	snapshot, err := json.Marshal(map[string]any{
		"subscription":  rec,
		"paymentMethod": archivedMethod,
		"payments":      payments,
		"archivedAt":    time.Now().UTC(),
	})
	if err != nil {
		return err
	}

	return s.store.WithinTx(ctx, func(tx out.Store) error {
		processed, err := tx.ProcessedEventExists(ctx, event.EventID, "billing-service")
		if err != nil {
			return err
		}
		if processed {
			return nil
		}
		if err := tx.ArchiveAccount(ctx, accountID, snapshot); err != nil {
			return err
		}
		if err := tx.DeleteSubscription(ctx, rec.ID); err != nil {
			return err
		}
		if err := tx.UpsertAccountStatus(ctx, accountID, "deleted"); err != nil {
			return err
		}
		return tx.MarkProcessedEvent(ctx, event.EventID, "billing-service")
	})
}
