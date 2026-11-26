package acquiring

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Reddetk/SayMoDev/billing-service/internal/port/out"
)

func TestCreatePaymentLink(t *testing.T) {
	adapter := New("https://pay.example/", "store-1", "secret", "https://app/return")
	link, err := adapter.CreatePaymentLink(context.Background(), out.PaymentLinkRequest{
		CustomerID: "acc-1", OrderNum: "pay-1", AmountKopecks: 2990, Currency: "BYN",
		OperationType: "recurring_bind",
	})
	require.NoError(t, err)
	assert.Contains(t, link, "https://pay.example?")
	assert.Contains(t, link, "wsb_total=29.90")
	assert.Contains(t, link, "wsb_operation_type=recurring_bind")
	assert.Contains(t, link, "wsb_signature=")
	assert.Contains(t, link, "wsb_return_url=https%3A%2F%2Fapp%2Freturn")
}

func TestCreatePaymentLink_NotConfigured(t *testing.T) {
	_, err := New("", "", "", "").CreatePaymentLink(context.Background(), out.PaymentLinkRequest{})
	require.Error(t, err)
}

func TestFormatAmount(t *testing.T) {
	assert.Equal(t, "0.00", formatAmount(0))
	assert.Equal(t, "0.01", formatAmount(1))
	assert.Equal(t, "29.90", formatAmount(2990))
	assert.Equal(t, "-1.05", formatAmount(-105))
}

func TestRecurringPay_PostsForm(t *testing.T) {
	var received string
	server := newTestServer(t, &received)
	defer server.Close()

	adapter := New(server.URL, "store-1", "secret", "")
	require.NoError(t, adapter.RecurringPay(context.Background(), out.RecurringPayRequest{
		OrderNum: "pay-2", AmountKopecks: 4990, Currency: "BYN", RecurringToken: "token", CustomerID: "acc",
	}))
	assert.True(t, strings.Contains(received, "wsb_operation_type=recurring_pay"))
	assert.True(t, strings.Contains(received, "wsb_recurring_token=token"))
}
