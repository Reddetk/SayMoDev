//go:build integration

package integration

// E2E BC#2 через HTTP API (не знает внутренностей сервиса).

import (
	"bytes"
	"crypto/md5"
	"crypto/rsa"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

const (
	basicPlan    = "11111111-1111-1111-1111-111111111111"
	standardPlan = "22222222-2222-2222-2222-222222222222"
	premiumPlan  = "33333333-3333-3333-3333-333333333333"
)

func TestBillingLifecycle_LiveStand(t *testing.T) {
	base := envOr("BILLING_BASE_URL", "http://localhost:8002")
	privateKey := envOr("BILLING_JWT_PRIVATE_KEY", "../testdata/keys/private.pem")
	secret := envOr("BILLING_ACQUIRING_SECRET", "local-secret")
	accountID := uuid.NewString()
	token := signToken(t, privateKey, accountID, "patient")

	client := &http.Client{Timeout: 10 * time.Second}

	// 401 без токена.
	status, _ := doJSON(t, client, http.MethodGet, base+"/billing/subscriptions/"+uuid.NewString(), nil, "")
	require.Equal(t, http.StatusUnauthorized, status)

	// Subscribe (идемпотентно).
	idempotencyKey := uuid.NewString()
	status, body := doJSON(t, client, http.MethodPost, base+"/billing/subscriptions",
		map[string]any{"tariffPlanId": basicPlan, "paymentMethod": map[string]any{"type": "card"}},
		token, idempotencyKey)
	require.Equal(t, http.StatusCreated, status, body)
	var subscribed struct {
		SubscriptionID string `json:"subscriptionId"`
		Status         string `json:"status"`
		Payment        struct {
			PaymentID     string `json:"paymentId"`
			AmountKopecks int64  `json:"amount_kopecks"`
			RedirectURL   string `json:"acquiringRedirectUrl"`
		} `json:"payment"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &subscribed))
	require.NotEmpty(t, subscribed.SubscriptionID)
	require.Equal(t, "pending", subscribed.Status)
	require.NotEmpty(t, subscribed.Payment.RedirectURL)
	require.EqualValues(t, 2990, subscribed.Payment.AmountKopecks)

	status, replay := doJSON(t, client, http.MethodPost, base+"/billing/subscriptions",
		map[string]any{"tariffPlanId": basicPlan}, token, idempotencyKey)
	require.Equal(t, http.StatusCreated, status)
	require.Equal(t, body, replay, "idempotent replay must return the same body")

	// Тот же ключ на другом endpoint — 409, а не replay чужого ответа.
	status, body = doJSON(t, client, http.MethodPost,
		base+"/billing/subscriptions/"+subscribed.SubscriptionID+"/downgrade",
		map[string]any{"targetTariffPlanId": basicPlan}, token, idempotencyKey)
	require.Equal(t, http.StatusConflict, status, body)

	// Чужой аккаунт не видит подписку (404).
	foreign := signToken(t, privateKey, uuid.NewString(), "patient")
	status, _ = doJSON(t, client, http.MethodGet, base+"/billing/subscriptions/"+subscribed.SubscriptionID, nil, foreign)
	require.Equal(t, http.StatusNotFound, status)

	// Webhook с неверной подписью.
	status = postWebhook(t, client, base, secret, subscribed.Payment.PaymentID, "29.90", "tx-bad", "1", 1, "bad-signature")
	require.Equal(t, http.StatusUnauthorized, status)

	// Webhook success.
	status = postWebhook(t, client, base, secret, subscribed.Payment.PaymentID, "29.90", "tx-"+uuid.NewString(), "1", 0, "")
	require.Equal(t, http.StatusOK, status)

	status, body = doJSON(t, client, http.MethodGet, base+"/billing/subscriptions/"+subscribed.SubscriptionID, nil, token)
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, body, `"status":"active"`)

	// Preview upgrade.
	status, body = doJSON(t, client, http.MethodGet,
		base+"/billing/subscriptions/"+subscribed.SubscriptionID+"/upgrade/preview?targetTariffPlanId="+premiumPlan, nil, token)
	require.Equal(t, http.StatusOK, status, body)
	var preview struct {
		QuoteID string `json:"quoteId"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &preview))
	require.NotEmpty(t, preview.QuoteID)

	// Upgrade с quote.
	status, body = doJSON(t, client, http.MethodPost,
		base+"/billing/subscriptions/"+subscribed.SubscriptionID+"/upgrade",
		map[string]any{"targetTariffPlanId": premiumPlan, "quoteId": preview.QuoteID}, token, uuid.NewString())
	require.Equal(t, http.StatusOK, status, body)
	require.Contains(t, body, `"status":"active"`)

	// История платежей: initial + upgrade_proration.
	status, body = doJSON(t, client, http.MethodGet, base+"/billing/subscriptions/"+subscribed.SubscriptionID+"/payments", nil, token)
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, body, `"initial"`)
	require.Contains(t, body, `"upgrade_proration"`)

	// Cancel и повторный resume → 400.
	status, body = doJSON(t, client, http.MethodPost,
		base+"/billing/subscriptions/"+subscribed.SubscriptionID+"/cancel", nil, token)
	require.Equal(t, http.StatusOK, status, body)
	require.Contains(t, body, `"status":"cancelled"`)

	status, _ = doJSON(t, client, http.MethodPost,
		base+"/billing/subscriptions/"+subscribed.SubscriptionID+"/resume", nil, token)
	require.Equal(t, http.StatusBadRequest, status)

	// 405 на неподдерживаемый метод.
	status, _ = doJSON(t, client, http.MethodDelete, base+"/billing/subscriptions/"+subscribed.SubscriptionID, nil, token)
	require.Equal(t, http.StatusMethodNotAllowed, status)
}

func TestAdminActivateDeactivate_LiveStand(t *testing.T) {
	base := envOr("BILLING_BASE_URL", "http://localhost:8002")
	privateKey := envOr("BILLING_JWT_PRIVATE_KEY", "../testdata/keys/private.pem")
	admin := signToken(t, privateKey, uuid.NewString(), "administrator")
	accountID := uuid.NewString()
	client := &http.Client{Timeout: 10 * time.Second}

	status, body := doJSON(t, client, http.MethodPost,
		base+"/admin/billing/accounts/"+accountID+"/activate-tariff",
		map[string]any{"tariffPlanId": basicPlan}, admin, uuid.NewString())
	require.Equal(t, http.StatusCreated, status, body)
	require.Contains(t, body, `"status":"active"`)

	status, body = doJSON(t, client, http.MethodGet, base+"/billing/subscriptions/"+
		mustField(t, body, "subscriptionId"), nil, admin)
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, body, `"accountId":"`+accountID+`"`)

	status, body = doJSON(t, client, http.MethodPost,
		base+"/admin/billing/accounts/"+accountID+"/deactivate-tariff", nil, admin, uuid.NewString())
	require.Equal(t, http.StatusOK, status, body)
	require.Contains(t, body, `"status":"cancelled"`)
}

func TestDowngrade_LiveStand(t *testing.T) {
	base := envOr("BILLING_BASE_URL", "http://localhost:8002")
	privateKey := envOr("BILLING_JWT_PRIVATE_KEY", "../testdata/keys/private.pem")
	secret := envOr("BILLING_ACQUIRING_SECRET", "local-secret")
	accountID := uuid.NewString()
	token := signToken(t, privateKey, accountID, "patient")
	client := &http.Client{Timeout: 10 * time.Second}

	status, body := doJSON(t, client, http.MethodPost, base+"/billing/subscriptions",
		map[string]any{"tariffPlanId": standardPlan}, token, uuid.NewString())
	require.Equal(t, http.StatusCreated, status, body)
	var created struct {
		SubscriptionID string `json:"subscriptionId"`
		Payment        struct {
			PaymentID string `json:"paymentId"`
		} `json:"payment"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &created))
	subscriptionID := created.SubscriptionID
	paymentID := created.Payment.PaymentID
	require.NotEmpty(t, paymentID)

	status = postWebhook(t, client, base, secret, paymentID, "49.90", "tx-"+uuid.NewString(), "1", 0, "")
	require.Equal(t, http.StatusOK, status)

	status, body = doJSON(t, client, http.MethodPost,
		base+"/billing/subscriptions/"+subscriptionID+"/downgrade",
		map[string]any{"targetTariffPlanId": basicPlan}, token, uuid.NewString())
	require.Equal(t, http.StatusOK, status, body)
	require.Contains(t, body, `"scheduledChange"`)
	require.Contains(t, body, basicPlan)
}

func doJSON(t *testing.T, client *http.Client, method, url string, body any, token string, idempotencyKey ...string) (int, string) {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(encoded)
	} else {
		reader = bytes.NewReader(nil)
	}
	request, err := http.NewRequest(method, url, reader)
	require.NoError(t, err)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if len(idempotencyKey) > 0 && idempotencyKey[0] != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey[0])
	}
	response, err := client.Do(request)
	require.NoError(t, err)
	defer func() { _ = response.Body.Close() }()
	var buffer bytes.Buffer
	_, _ = buffer.ReadFrom(response.Body)
	return response.StatusCode, strings.TrimSpace(buffer.String())
}

func postWebhook(t *testing.T, client *http.Client, base, secret, paymentID, amount, transactionID, paymentType string, action int, forcedSignature string) int {
	t.Helper()
	timestamp := time.Now().Unix()
	form := url.Values{}
	form.Set("batch_timestamp", strconv.FormatInt(timestamp, 10))
	form.Set("currency_id", "BYN")
	form.Set("amount", amount)
	form.Set("payment_method", "cc")
	form.Set("order_id", "webpay-"+transactionID)
	form.Set("site_order_id", paymentID)
	form.Set("transaction_id", transactionID)
	form.Set("payment_type", paymentType)
	form.Set("rrn", "123456789")
	form.Set("card", "5160**9495")
	form.Set("action", strconv.Itoa(action))
	form.Set("rc", "W0001")
	form.Set("customer_id", "customer")
	form.Set("operation_type", "recurring_bind")
	form.Set("recurring_token", "token-"+transactionID)
	form.Set("offer_exp_date", time.Now().AddDate(1, 0, 0).Format("2006-01-02"))

	signature := forcedSignature
	if signature == "" {
		raw := form.Get("batch_timestamp") + form.Get("currency_id") + form.Get("amount") +
			form.Get("payment_method") + form.Get("order_id") + form.Get("site_order_id") +
			form.Get("transaction_id") + form.Get("payment_type") + form.Get("rrn") +
			form.Get("card") + secret
		sum := md5.Sum([]byte(raw))
		signature = hex.EncodeToString(sum[:])
	}
	form.Set("wsb_signature", signature)

	request, err := http.NewRequest(http.MethodPost, base+"/billing/webhooks/acquiring", strings.NewReader(form.Encode()))
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := client.Do(request)
	require.NoError(t, err)
	defer func() { _ = response.Body.Close() }()
	return response.StatusCode
}

func signToken(t *testing.T, path, subject, role string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	block, _ := pem.Decode(body)
	require.NotNil(t, block)
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	require.NoError(t, err)
	key, ok := parsed.(*rsa.PrivateKey)
	require.True(t, ok)

	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss":        "urn:saymo:identity-service",
		"aud":        "api.saymo",
		"sub":        subject,
		"role":       role,
		"exp":        now.Add(time.Hour).Unix(),
		"iat":        now.Unix(),
		"jti":        uuid.NewString(),
		"session_id": uuid.NewString(),
		"rev":        int64(1),
	})
	token.Header["kid"] = "key-v1"
	signed, err := jwt.NewWithClaims(jwt.SigningMethodRS256, token.Claims).SignedString(key)
	require.NoError(t, err)
	return signed
}

func mustField(t *testing.T, body, field string) string {
	t.Helper()
	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &parsed))
	value, _ := parsed[field].(string)
	require.NotEmpty(t, value, fmt.Sprintf("field %s in %s", field, body))
	return value
}
