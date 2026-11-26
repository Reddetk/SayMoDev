package postbox

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Reddetk/SayMoDev/billing-service/internal/platform/metrics"
	"github.com/Reddetk/SayMoDev/billing-service/internal/port/out"
)

func TestSignV4_KnownVector(t *testing.T) {
	body := []byte(`{"x":1}`)
	request, err := http.NewRequest(http.MethodPost,
		"https://postbox.cloud.yandex.net/v2/email/outbound-emails", strings.NewReader(string(body)))
	require.NoError(t, err)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	signV4(request, body, "AKIDEXAMPLE", "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY", "ru-central1", "ses", now)

	auth := request.Header.Get("Authorization")
	assert.Contains(t, auth, "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20260101/ru-central1/ses/aws4_request")
	assert.Contains(t, auth, "SignedHeaders=host;x-amz-content-sha256;x-amz-date")
	assert.Contains(t, auth, "Signature=5ebd2bc75a087c3a47a310a37b12b1a61bd9e72105a7422eab444f3d1079c424")
	assert.Equal(t, "20260101T000000Z", request.Header.Get("X-Amz-Date"))
	assert.Equal(t, "5041bf1f713df204784353e82f6a4a535931cb64f1f4b4a5aeaffcb720918b22",
		request.Header.Get("X-Amz-Content-Sha256"))
}

func TestSend_IAMToken(t *testing.T) {
	var captured sendRequest
	var headers http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers = r.Header.Clone()
		body, _ := io.ReadAll(r.Body)
		require.NoError(t, json.Unmarshal(body, &captured))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	adapter, err := New(Config{Endpoint: server.URL, IAMToken: "iam-token", FromAddress: "noreply@saymo.ru"})
	require.NoError(t, err)
	err = adapter.Send(context.Background(), out.Notification{
		Kind: "pre_renewal", Recipient: "patient@example.com", PeriodEnd: time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)

	assert.Equal(t, "iam-token", headers.Get("X-YaCloud-SubjectToken"))
	assert.Equal(t, "noreply@saymo.ru", captured.FromEmailAddress)
	assert.Equal(t, []string{"patient@example.com"}, captured.Destination.ToAddresses)
	assert.Contains(t, captured.Content.Simple.Subject.Data, "продлится")
	assert.Contains(t, captured.Content.Simple.Body.Text.Data, "01.12.2026")
	assert.Equal(t, "UTF-8", captured.Content.Simple.Subject.Charset)
}

func TestSend_SigV4(t *testing.T) {
	var auth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		assert.NotEmpty(t, r.Header.Get("X-Amz-Date"))
		assert.NotEmpty(t, r.Header.Get("X-Amz-Content-Sha256"))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	adapter, err := New(Config{
		Endpoint: server.URL, AccessKey: "AKIDEXAMPLE",
		SecretKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY", Region: "ru-central1",
		FromAddress: "noreply@saymo.ru",
	})
	require.NoError(t, err)
	require.NoError(t, adapter.Send(context.Background(), out.Notification{
		Kind: "card_expiring", Recipient: "patient@example.com", CardExpDate: time.Now().AddDate(0, 0, 10),
	}))
	assert.True(t, strings.HasPrefix(auth, "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/"))
	assert.Contains(t, auth, "/ru-central1/ses/aws4_request")
}

func TestSend_Errors(t *testing.T) {
	ctx := context.Background()

	adapter, err := New(Config{Endpoint: "http://localhost", IAMToken: "token", FromAddress: "noreply@saymo.ru"})
	require.NoError(t, err)
	require.Error(t, adapter.Send(ctx, out.Notification{Kind: "payment_failed"}), "empty recipient")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()
	bad, err := New(Config{Endpoint: server.URL, IAMToken: "token", FromAddress: "noreply@saymo.ru"})
	require.NoError(t, err)
	require.Error(t, bad.Send(ctx, out.Notification{Kind: "payment_failed", Recipient: "a@b.c"}))
}

func TestSend_RecordsMetrics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	registry := prometheus.NewRegistry()
	adapter, err := New(Config{Endpoint: server.URL, IAMToken: "token", FromAddress: "noreply@saymo.ru"})
	require.NoError(t, err)
	adapter.WithMetrics(metrics.NewPostbox(registry))

	require.NoError(t, adapter.Send(context.Background(), out.Notification{
		Kind: "pre_renewal", Recipient: "patient@example.com", PeriodEnd: time.Now(),
	}))
	families, err := registry.Gather()
	require.NoError(t, err)
	found := false
	for _, family := range families {
		if family.GetName() == "billing_postbox_send_total" {
			found = true
			assert.EqualValues(t, 1, family.GetMetric()[0].GetCounter().GetValue())
		}
	}
	assert.True(t, found)
}

func TestNew_Validation(t *testing.T) {
	_, err := New(Config{IAMToken: "token"})
	require.Error(t, err, "from address is required")

	_, err = New(Config{FromAddress: "noreply@saymo.ru"})
	require.Error(t, err, "auth is required")

	adapter, err := New(Config{AccessKey: "a", SecretKey: "b", FromAddress: "noreply@saymo.ru"})
	require.NoError(t, err)
	assert.Equal(t, prodEndpoint, adapter.endpoint)
	assert.Equal(t, "ru-central1", adapter.region)
}
