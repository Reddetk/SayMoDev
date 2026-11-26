// Package acquiring — ACL-адаптер WebPay.
package acquiring

// goca:manual — WebPay ACL не генерируется goca; поля и подпись из endpoints.md.

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Reddetk/SayMoDev/billing-service/internal/platform/metrics"
	"github.com/Reddetk/SayMoDev/billing-service/internal/port/out"
)

// WebPay реализует out.Acquiring.
type WebPay struct {
	baseURL    string
	storeID    string
	secretKey  string
	returnURL  string
	httpClient *http.Client
	metrics    *metrics.Acquiring
}

// WithMetrics подключает гистограмму acquiring_latency_seconds.
func (w *WebPay) WithMetrics(acquiringMetrics *metrics.Acquiring) *WebPay {
	w.metrics = acquiringMetrics
	return w
}

// New создаёт адаптер. baseURL пуст → операции возвращают ошибку конфигурации.
func New(baseURL, storeID, secretKey, returnURL string) *WebPay {
	return &WebPay{
		baseURL:    strings.TrimRight(baseURL, "/"),
		storeID:    storeID,
		secretKey:  secretKey,
		returnURL:  returnURL,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// CreatePaymentLink формирует redirect URL на платёжную страницу WebPay.
func (w *WebPay) CreatePaymentLink(_ context.Context, req out.PaymentLinkRequest) (string, error) {
	if w.baseURL == "" {
		return "", fmt.Errorf("acquiring: ACQUIRING_API_URL is not configured")
	}
	total := formatAmount(req.AmountKopecks)
	signature := md5Hex(w.storeID + req.OrderNum + total + req.Currency + w.secretKey)

	values := url.Values{}
	values.Set("wsb_storeid", w.storeID)
	values.Set("wsb_customer_id", req.CustomerID)
	values.Set("wsb_order_num", req.OrderNum)
	values.Set("wsb_total", total)
	values.Set("wsb_currency_id", req.Currency)
	values.Set("wsb_notify_url", req.NotifyURL)
	returnURL := req.ReturnURL
	if returnURL == "" {
		returnURL = w.returnURL
	}
	values.Set("wsb_return_url", returnURL)
	values.Set("wsb_operation_type", req.OperationType)
	values.Set("wsb_signature", signature)
	return w.baseURL + "?" + values.Encode(), nil
}

// RecurringPay инициирует store-initiated списание (renewal).
func (w *WebPay) RecurringPay(ctx context.Context, req out.RecurringPayRequest) error {
	if w.baseURL == "" {
		return fmt.Errorf("acquiring: ACQUIRING_API_URL is not configured")
	}
	form := url.Values{}
	form.Set("wsb_storeid", w.storeID)
	form.Set("wsb_customer_id", req.CustomerID)
	form.Set("wsb_order_num", req.OrderNum)
	form.Set("wsb_total", formatAmount(req.AmountKopecks))
	form.Set("wsb_currency_id", req.Currency)
	form.Set("wsb_operation_type", "recurring_pay")
	form.Set("wsb_recurring_token", req.RecurringToken)
	start := time.Now()
	err := w.post(ctx, form)
	w.observe("charge", err, time.Since(start))
	return err
}

// Unbind отвязывает карту (recurring_unbind).
func (w *WebPay) Unbind(ctx context.Context, req out.UnbindRequest) error {
	if w.baseURL == "" {
		return fmt.Errorf("acquiring: ACQUIRING_API_URL is not configured")
	}
	form := url.Values{}
	form.Set("wsb_storeid", w.storeID)
	form.Set("wsb_customer_id", req.CustomerID)
	form.Set("wsb_operation_type", "recurring_unbind")
	form.Set("wsb_recurring_token", req.RecurringToken)
	start := time.Now()
	err := w.post(ctx, form)
	w.observe("unbind", err, time.Since(start))
	return err
}

func (w *WebPay) observe(operation string, err error, duration time.Duration) {
	status := "success"
	if err != nil {
		status = "failed"
	}
	w.metrics.Observe(operation, status, duration)
}

func (w *WebPay) post(ctx context.Context, form url.Values) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, w.baseURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := w.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("acquiring request: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode >= 300 {
		return fmt.Errorf("acquiring status %d", response.StatusCode)
	}
	return nil
}

func formatAmount(kopecks int64) string {
	sign := ""
	if kopecks < 0 {
		sign = "-"
		kopecks = -kopecks
	}
	return sign + strconv.FormatInt(kopecks/100, 10) + "." + fmt.Sprintf("%02d", kopecks%100)
}

func md5Hex(raw string) string {
	sum := md5.Sum([]byte(raw))
	return hex.EncodeToString(sum[:])
}
