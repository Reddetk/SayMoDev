package metrics

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSubscriptionGauge_CollectsFromSource(t *testing.T) {
	registry := NewRegistry("test", "test")
	NewSubscriptionGauge(registry, func() []SubscriptionCount {
		return []SubscriptionCount{{Status: "active", PlanType: "Basic", Count: 3}}
	})

	families, err := registry.Gather()
	require.NoError(t, err)
	found := false
	for _, family := range families {
		if family.GetName() == "active_subscriptions_count" {
			found = true
			require.Len(t, family.GetMetric(), 1)
			assert.EqualValues(t, 3, family.GetMetric()[0].GetGauge().GetValue())
		}
	}
	assert.True(t, found)
}

func TestOutboxPendingGauge(t *testing.T) {
	registry := NewRegistry("test", "test")
	NewOutboxPendingGauge(registry, func() (float64, bool) { return 7, true })

	families, err := registry.Gather()
	require.NoError(t, err)
	found := false
	for _, family := range families {
		if family.GetName() == "outbox_pending_count" {
			found = true
			assert.EqualValues(t, 7, family.GetMetric()[0].GetGauge().GetValue())
		}
	}
	assert.True(t, found)

	// При ошибке БД серия отсутствует, а не 0.
	errorRegistry := NewRegistry("test", "test")
	NewOutboxPendingGauge(errorRegistry, func() (float64, bool) { return 0, false })
	families, err = errorRegistry.Gather()
	require.NoError(t, err)
	for _, family := range families {
		assert.NotEqual(t, "outbox_pending_count", family.GetName())
	}
}

func TestSubscriptionGauge_EmitsZeroSeries(t *testing.T) {
	registry := NewRegistry("test", "test")
	NewSubscriptionGauge(registry, func() []SubscriptionCount {
		return []SubscriptionCount{{Status: "active", PlanType: "Basic", Count: 0}}
	})
	families, err := registry.Gather()
	require.NoError(t, err)
	found := false
	for _, family := range families {
		if family.GetName() == "active_subscriptions_count" {
			found = true
			assert.EqualValues(t, 0, family.GetMetric()[0].GetGauge().GetValue())
		}
	}
	assert.True(t, found)
}

func TestDuplicateRegistrationDoesNotPanic(t *testing.T) {
	registry := NewRegistry("test", "test")
	assert.NotPanics(t, func() {
		NewBusiness(registry)
		NewBusiness(registry)
		NewAcquiring(registry)
		NewAcquiring(registry)
		NewPostbox(registry)
		NewPostbox(registry)
		NewOutbox(registry)
		NewOutbox(registry)
		NewSubscriptionGauge(registry, func() []SubscriptionCount { return nil })
		NewSubscriptionGauge(registry, func() []SubscriptionCount { return nil })
	})
}

func TestAcquiringLatency(t *testing.T) {
	registry := NewRegistry("test", "test")
	acquiring := NewAcquiring(registry)
	acquiring.Observe("charge", "success", 120*time.Millisecond)

	families, err := registry.Gather()
	require.NoError(t, err)
	found := false
	for _, family := range families {
		if family.GetName() == "acquiring_latency_seconds" {
			found = true
			assert.EqualValues(t, 1, family.GetMetric()[0].GetHistogram().GetSampleCount())
		}
	}
	assert.True(t, found)

	// nil-safe
	var nilMetrics *Acquiring
	nilMetrics.Observe("charge", "failed", time.Second)
}

func TestPostboxCounters(t *testing.T) {
	registry := NewRegistry("test", "test")
	postbox := NewPostbox(registry)
	postbox.Sent("pre_renewal")
	postbox.Error("payment_failed")

	families, err := registry.Gather()
	require.NoError(t, err)
	names := map[string]float64{}
	for _, family := range families {
		if family.GetName() == "billing_postbox_send_total" || family.GetName() == "billing_postbox_error_total" {
			names[family.GetName()] = family.GetMetric()[0].GetCounter().GetValue()
		}
	}
	assert.EqualValues(t, 1, names["billing_postbox_send_total"])
	assert.EqualValues(t, 1, names["billing_postbox_error_total"])
}

func TestPaymentDurationHistogram(t *testing.T) {
	registry := prometheus.NewRegistry()
	business := NewBusiness(registry)
	business.PaymentDuration("success", 1.5)

	families, err := registry.Gather()
	require.NoError(t, err)
	found := false
	for _, family := range families {
		if family.GetName() == "payment_duration_seconds" {
			found = true
			assert.EqualValues(t, 1, family.GetMetric()[0].GetHistogram().GetSampleCount())
		}
	}
	assert.True(t, found)
}
