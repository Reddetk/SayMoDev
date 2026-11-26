package usecase

// In-memory shim для application-тестов. Реализованы только используемые методы;
// встроенный out.Store закрывает остальные сигнатуры (вызов упал бы явно).

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/Reddetk/SayMoDev/billing-service/internal/port/out"
)

type fakeStore struct {
	out.Store
	mu sync.Mutex

	fail map[string]error

	subscriptions map[string]out.SubscriptionRecord
	payments      map[string]out.PaymentRecord
	methods       map[string]out.PaymentMethodRecord
	offers        map[string]out.OfferRecord
	idempotency   map[string]out.IdempotencyRecord
	outbox        []out.OutboxRecord
	processed     map[string]bool
	accounts      map[string]string
	archives      []string
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		fail:          map[string]error{},
		subscriptions: map[string]out.SubscriptionRecord{},
		payments:      map[string]out.PaymentRecord{},
		methods:       map[string]out.PaymentMethodRecord{},
		offers:        map[string]out.OfferRecord{},
		idempotency:   map[string]out.IdempotencyRecord{},
		processed:     map[string]bool{},
		accounts:      map[string]string{},
	}
}

func (f *fakeStore) WithinTx(ctx context.Context, fn func(out.Store) error) error { return fn(f) }

func (f *fakeStore) failWith(method string) error {
	if err, ok := f.fail[method]; ok {
		return err
	}
	return nil
}

func (f *fakeStore) CreateSubscription(_ context.Context, rec out.SubscriptionRecord) error {
	if err := f.failWith("CreateSubscription"); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, existing := range f.subscriptions {
		if existing.AccountID == rec.AccountID &&
			(existing.Status == "pending" || existing.Status == "active" || existing.Status == "suspended") {
			return errDuplicateKey
		}
	}
	f.subscriptions[rec.ID] = rec
	return nil
}

func (f *fakeStore) GetSubscription(_ context.Context, id string) (*out.SubscriptionRecord, error) {
	if err := f.failWith("GetSubscription"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	rec, ok := f.subscriptions[id]
	if !ok {
		return nil, out.ErrNotFound
	}
	return &rec, nil
}

func (f *fakeStore) GetSubscriptionForUpdate(ctx context.Context, id string) (*out.SubscriptionRecord, error) {
	return f.GetSubscription(ctx, id)
}

func (f *fakeStore) GetSubscriptionByAccount(_ context.Context, accountID string) (*out.SubscriptionRecord, error) {
	return f.latest(accountID, true)
}

func (f *fakeStore) GetLatestSubscriptionByAccount(_ context.Context, accountID string) (*out.SubscriptionRecord, error) {
	return f.latest(accountID, false)
}

func (f *fakeStore) latest(accountID string, aliveOnly bool) (*out.SubscriptionRecord, error) {
	if err := f.failWith("GetSubscriptionByAccount"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var found *out.SubscriptionRecord
	for _, rec := range f.subscriptions {
		if rec.AccountID != accountID {
			continue
		}
		if aliveOnly && rec.Status != "pending" && rec.Status != "active" && rec.Status != "suspended" {
			continue
		}
		if found == nil || rec.CreatedAt.After(found.CreatedAt) {
			copy := rec
			found = &copy
		}
	}
	if found == nil {
		return nil, out.ErrNotFound
	}
	return found, nil
}

func (f *fakeStore) Now(context.Context) (time.Time, error) { return time.Now().UTC(), nil }

func (f *fakeStore) ListStalePendingSubscriptions(_ context.Context, before time.Time, limit int) ([]out.SubscriptionRecord, error) {
	if err := f.failWith("ListStalePendingSubscriptions"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var result []out.SubscriptionRecord
	for _, rec := range f.subscriptions {
		if rec.Status == "pending" && !rec.CreatedAt.After(before) {
			result = append(result, rec)
		}
	}
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (f *fakeStore) UpdateSubscription(_ context.Context, rec out.SubscriptionRecord) error {
	if err := f.failWith("UpdateSubscription"); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.subscriptions[rec.ID]; !ok {
		return out.ErrNotFound
	}
	f.subscriptions[rec.ID] = rec
	return nil
}

func (f *fakeStore) ListSubscriptionsDue(_ context.Context, status string, now time.Time, limit int) ([]out.SubscriptionRecord, error) {
	if err := f.failWith("ListSubscriptionsDue"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var result []out.SubscriptionRecord
	for _, rec := range f.subscriptions {
		if rec.Status != status || rec.PeriodEnd.After(now) {
			continue
		}
		if rec.NextRetryAt != nil && rec.NextRetryAt.After(now) {
			continue
		}
		if f.hasPendingRenewalLocked(rec.ID) {
			continue
		}
		result = append(result, rec)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].PeriodEnd.Before(result[j].PeriodEnd) })
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (f *fakeStore) hasPendingRenewalLocked(subscriptionID string) bool {
	for _, payment := range f.payments {
		if payment.SubscriptionID == subscriptionID && payment.Kind == "renewal" && payment.Status == "pending" {
			return true
		}
	}
	return false
}

func (f *fakeStore) ListStaleRenewalPayments(_ context.Context, before time.Time, limit int) ([]out.PaymentRecord, error) {
	if err := f.failWith("ListStaleRenewalPayments"); err != nil {
		return nil, err
	}
	return f.stalePayments("renewal", before, limit), nil
}

func (f *fakeStore) ListStaleProrationPayments(_ context.Context, before time.Time, limit int) ([]out.PaymentRecord, error) {
	if err := f.failWith("ListStaleProrationPayments"); err != nil {
		return nil, err
	}
	return f.stalePayments("upgrade_proration", before, limit), nil
}

func (f *fakeStore) stalePayments(kind string, before time.Time, limit int) []out.PaymentRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	var result []out.PaymentRecord
	for _, payment := range f.payments {
		if payment.Kind == kind && payment.Status == "pending" && !payment.CreatedAt.After(before) {
			result = append(result, payment)
		}
	}
	if len(result) > limit {
		result = result[:limit]
	}
	return result
}

func (f *fakeStore) ListSubscriptionsEndingBefore(_ context.Context, status string, before time.Time, limit int) ([]out.SubscriptionRecord, error) {
	if err := f.failWith("ListSubscriptionsEndingBefore"); err != nil {
		return nil, err
	}
	return f.list(status, func(rec out.SubscriptionRecord) bool {
		return rec.PeriodEnd.After(time.Now()) && !rec.PeriodEnd.After(before)
	}, limit), nil
}

func (f *fakeStore) list(status string, predicate func(out.SubscriptionRecord) bool, limit int) []out.SubscriptionRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	var result []out.SubscriptionRecord
	for _, rec := range f.subscriptions {
		if rec.Status == status && predicate(rec) {
			result = append(result, rec)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].PeriodEnd.Before(result[j].PeriodEnd) })
	if len(result) > limit {
		result = result[:limit]
	}
	return result
}

func (f *fakeStore) CreatePayment(_ context.Context, rec out.PaymentRecord) error {
	if err := f.failWith("CreatePayment"); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.payments[rec.ID] = rec
	return nil
}

func (f *fakeStore) UpdatePayment(_ context.Context, rec out.PaymentRecord) error {
	if err := f.failWith("UpdatePayment"); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.payments[rec.ID] = rec
	return nil
}

func (f *fakeStore) GetPayment(_ context.Context, id string) (*out.PaymentRecord, error) {
	if err := f.failWith("GetPayment"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	rec, ok := f.payments[id]
	if !ok {
		return nil, out.ErrNotFound
	}
	return &rec, nil
}

func (f *fakeStore) GetPaymentForUpdate(ctx context.Context, id string) (*out.PaymentRecord, error) {
	return f.GetPayment(ctx, id)
}

func (f *fakeStore) GetPaymentByExternalRef(_ context.Context, ref string) (*out.PaymentRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, rec := range f.payments {
		if rec.ExternalReferenceID == ref {
			copy := rec
			return &copy, nil
		}
	}
	return nil, out.ErrNotFound
}

func (f *fakeStore) ListPayments(_ context.Context, subscriptionID string) ([]out.PaymentRecord, error) {
	if err := f.failWith("ListPayments"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var result []out.PaymentRecord
	for _, rec := range f.payments {
		if rec.SubscriptionID == subscriptionID {
			result = append(result, rec)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.After(result[j].CreatedAt) })
	return result, nil
}

func (f *fakeStore) UpsertPaymentMethod(_ context.Context, rec out.PaymentMethodRecord) error {
	if err := f.failWith("UpsertPaymentMethod"); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.methods[rec.SubscriptionID] = rec
	return nil
}

func (f *fakeStore) GetActivePaymentMethod(_ context.Context, subscriptionID string) (*out.PaymentMethodRecord, error) {
	if err := f.failWith("GetActivePaymentMethod"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	rec, ok := f.methods[subscriptionID]
	if !ok || !rec.IsActive {
		return nil, out.ErrNotFound
	}
	return &rec, nil
}

func (f *fakeStore) DeactivatePaymentMethods(_ context.Context, subscriptionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.methods, subscriptionID)
	return nil
}

func (f *fakeStore) ListExpiringPaymentMethods(_ context.Context, before time.Time) ([]out.ExpiringMethod, error) {
	if err := f.failWith("ListExpiringPaymentMethods"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var result []out.ExpiringMethod
	for subscriptionID, method := range f.methods {
		if method.IsActive && method.CardExpDate != nil && !method.CardExpDate.After(before) {
			result = append(result, out.ExpiringMethod{
				SubscriptionID: subscriptionID, AccountID: f.subscriptions[subscriptionID].AccountID, CardExpDate: *method.CardExpDate,
			})
		}
	}
	return result, nil
}

func (f *fakeStore) GetOffer(_ context.Context, offerID string) (*out.OfferRecord, error) {
	if err := f.failWith("GetOffer"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	rec, ok := f.offers[offerID]
	if !ok {
		return nil, out.ErrNotFound
	}
	return &rec, nil
}

func (f *fakeStore) FindActiveOffer(_ context.Context, tariffPlanID string, at time.Time) (*out.OfferRecord, error) {
	if err := f.failWith("FindActiveOffer"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, rec := range f.offers {
		if rec.TariffPlanID == tariffPlanID && rec.IsActive && !at.Before(rec.ValidFrom) {
			copy := rec
			return &copy, nil
		}
	}
	return nil, out.ErrNotFound
}

func (f *fakeStore) InsertOutbox(_ context.Context, eventID, eventType string, payload []byte) error {
	if err := f.failWith("InsertOutbox"); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.outbox = append(f.outbox, out.OutboxRecord{ID: int64(len(f.outbox) + 1), EventID: eventID, EventType: eventType, Payload: payload})
	return nil
}

func (f *fakeStore) ProcessedEventExists(_ context.Context, eventID, consumer string) (bool, error) {
	if err := f.failWith("ProcessedEventExists"); err != nil {
		return false, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.processed[eventID+consumer], nil
}

func (f *fakeStore) MarkProcessedEvent(_ context.Context, eventID, consumer string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.processed[eventID+consumer] = true
	return nil
}

func (f *fakeStore) GetAccountStatus(_ context.Context, accountID string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	status, ok := f.accounts[accountID]
	if !ok {
		return "", out.ErrNotFound
	}
	return status, nil
}

func (f *fakeStore) UpsertAccountStatus(_ context.Context, accountID, status string) error {
	if err := f.failWith("UpsertAccountStatus"); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.accounts[accountID] = status
	return nil
}

func (f *fakeStore) ArchiveAccount(_ context.Context, accountID string, _ []byte) error {
	if err := f.failWith("ArchiveAccount"); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.archives = append(f.archives, accountID)
	return nil
}

func (f *fakeStore) DeleteSubscription(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.subscriptions, id)
	delete(f.methods, id)
	return nil
}

func (f *fakeStore) eventTypes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	result := make([]string, 0, len(f.outbox))
	for _, rec := range f.outbox {
		result = append(result, rec.EventType)
	}
	return result
}

// fakeCache реализует out.Cache.
type fakeCache struct {
	mu       sync.Mutex
	values   map[string][]byte
	allow    bool
	allowErr error
	getErr   error
	revoked  bool
}

func newFakeCache() *fakeCache {
	return &fakeCache{values: map[string][]byte{}, allow: true}
}

func (c *fakeCache) Get(_ context.Context, key string) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.getErr != nil {
		return nil, c.getErr
	}
	value, ok := c.values[key]
	if !ok {
		return nil, out.ErrNotFound
	}
	return value, nil
}

func (c *fakeCache) Set(_ context.Context, key string, value []byte, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.values[key] = value
	return nil
}

func (c *fakeCache) AllowUpgradeChange(context.Context, string, time.Time) (bool, error) {
	if c.allowErr != nil {
		return false, c.allowErr
	}
	return c.allow, nil
}

func (c *fakeCache) IsRevoked(context.Context, string, string, int) (bool, error) {
	return c.revoked, nil
}

// fakeAcquiring реализует out.Acquiring.
type fakeAcquiring struct {
	linkCalls      int
	recurringCalls int
	unbindCalls    int
	failRecurring  bool
	linkErr        error
	unbindErr      error
}

func (a *fakeAcquiring) CreatePaymentLink(context.Context, out.PaymentLinkRequest) (string, error) {
	a.linkCalls++
	if a.linkErr != nil {
		return "", a.linkErr
	}
	return "https://pay.example/?order=1", nil
}

func (a *fakeAcquiring) RecurringPay(context.Context, out.RecurringPayRequest) error {
	a.recurringCalls++
	if a.failRecurring {
		return errAcquiring
	}
	return nil
}

func (a *fakeAcquiring) Unbind(context.Context, out.UnbindRequest) error {
	a.unbindCalls++
	return a.unbindErr
}

type fakeNotifier struct {
	sent []out.Notification
	err  error
}

func (n *fakeNotifier) Send(_ context.Context, notification out.Notification) error {
	if n.err != nil {
		return n.err
	}
	n.sent = append(n.sent, notification)
	return nil
}
