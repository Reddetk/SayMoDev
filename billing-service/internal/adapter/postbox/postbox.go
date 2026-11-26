// Package postbox — ACL-адаптер Yandex Cloud Postbox (SESv2 REST API).
package postbox

// goca:manual — Postbox ACL BC#2 (SESv2 + IAM/SigV4), goca init его не создаёт.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/Reddetk/SayMoDev/billing-service/internal/platform/metrics"
	"github.com/Reddetk/SayMoDev/billing-service/internal/port/out"
)

const prodEndpoint = "https://postbox.cloud.yandex.net/v2/email/outbound-emails"

// Config — параметры ACL-адаптера.
type Config struct {
	Endpoint    string
	IAMToken    string
	AccessKey   string
	SecretKey   string
	Region      string
	FromAddress string
	HTTPClient  *http.Client
}

// Adapter реализует out.Notifier через Yandex Cloud Postbox.
type Adapter struct {
	endpoint    string
	iamToken    string
	accessKey   string
	secretKey   string
	region      string
	fromAddress string
	client      *http.Client
	now         func() time.Time
	metrics     *metrics.Postbox
}

// WithMetrics подключает счётчики billing_postbox_*.
func (a *Adapter) WithMetrics(postboxMetrics *metrics.Postbox) *Adapter {
	a.metrics = postboxMetrics
	return a
}

// New создаёт адаптер.
func New(cfg Config) (*Adapter, error) {
	if cfg.FromAddress == "" {
		return nil, fmt.Errorf("postbox: FromAddress is required")
	}
	if _, err := mail.ParseAddress(cfg.FromAddress); err != nil {
		return nil, fmt.Errorf("postbox: invalid FromAddress: %w", err)
	}
	if cfg.IAMToken == "" && (cfg.AccessKey == "" || cfg.SecretKey == "") {
		return nil, fmt.Errorf("postbox: IAM token or access/secret key pair is required")
	}
	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = prodEndpoint
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	region := cfg.Region
	if region == "" {
		region = "ru-central1"
	}
	return &Adapter{
		endpoint: endpoint, iamToken: cfg.IAMToken,
		accessKey: cfg.AccessKey, secretKey: cfg.SecretKey, region: region,
		fromAddress: cfg.FromAddress, client: client,
		now: func() time.Time { return time.Now().UTC() },
	}, nil
}

type content struct {
	Data    string `json:"Data"`
	Charset string `json:"Charset"`
}

type sendRequest struct {
	FromEmailAddress string `json:"FromEmailAddress"`
	Destination      struct {
		ToAddresses []string `json:"ToAddresses"`
	} `json:"Destination"`
	Content struct {
		Simple struct {
			Subject content `json:"Subject"`
			Body    struct {
				Text content `json:"Text"`
			} `json:"Body"`
		} `json:"Simple"`
	} `json:"Content"`
}

// Send отправляет уведомление; ошибка доставки не прерывает бизнес-операцию у вызывающего.
func (a *Adapter) Send(ctx context.Context, notification out.Notification) (err error) {
	defer func() {
		if a.metrics == nil {
			return
		}
		if err != nil {
			a.metrics.Error(notification.Kind)
		} else {
			a.metrics.Sent(notification.Kind)
		}
	}()
	if notification.Recipient == "" {
		return fmt.Errorf("postbox: recipient is empty")
	}
	if _, parseErr := mail.ParseAddress(notification.Recipient); parseErr != nil {
		return fmt.Errorf("postbox: invalid recipient: %w", parseErr)
	}
	subject, body := messageFor(notification)

	payload := sendRequest{FromEmailAddress: a.fromAddress}
	payload.Destination.ToAddresses = []string{notification.Recipient}
	payload.Content.Simple.Subject = content{Data: subject, Charset: "UTF-8"}
	payload.Content.Simple.Body.Text = content{Data: body, Charset: "UTF-8"}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("postbox: marshal: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint, bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("postbox: request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")

	if a.iamToken != "" {
		request.Header.Set("X-YaCloud-SubjectToken", a.iamToken)
	} else {
		signV4(request, encoded, a.accessKey, a.secretKey, a.region, "ses", a.now())
	}

	response, err := a.client.Do(request)
	if err != nil {
		return fmt.Errorf("postbox: transport: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("postbox: status %d", response.StatusCode)
	}
	return nil
}

func messageFor(notification out.Notification) (string, string) {
	switch notification.Kind {
	case "pre_renewal":
		return "SayMo: подписка продлится автоматически",
			fmt.Sprintf("Ваша подписка SayMo продлится автоматически %s.", notification.PeriodEnd.Format("02.01.2006"))
	case "card_expiring":
		return "SayMo: срок действия карты истекает",
			fmt.Sprintf("Срок действия карты истекает %s. Обновите карту, чтобы не потерять доступ.", notification.CardExpDate.Format("02.01.2006"))
	case "payment_failed":
		return "SayMo: не удалось списать оплату",
			"Не удалось списать оплату подписки SayMo. Проверьте карту и повторите попытку."
	default:
		return "SayMo: уведомление", "Уведомление сервиса SayMo."
	}
}

// signV4 подписывает запрос AWS Signature Version 4 (сервис SES Yandex Postbox).
func signV4(request *http.Request, payload []byte, accessKey, secretKey, region, service string, now time.Time) {
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")
	payloadHash := sha256Hex(payload)
	host := request.URL.Host

	request.Header.Set("Host", host)
	request.Header.Set("X-Amz-Date", amzDate)
	request.Header.Set("X-Amz-Content-Sha256", payloadHash)

	signedHeaders := "host;x-amz-content-sha256;x-amz-date"
	canonicalHeaders := "host:" + host + "\n" +
		"x-amz-content-sha256:" + payloadHash + "\n" +
		"x-amz-date:" + amzDate + "\n"
	canonicalRequest := strings.Join([]string{
		request.Method, canonicalURI(request.URL.Path), request.URL.RawQuery,
		canonicalHeaders, signedHeaders, payloadHash,
	}, "\n")

	scope := strings.Join([]string{dateStamp, region, service, "aws4_request"}, "/")
	stringToSign := strings.Join([]string{"AWS4-HMAC-SHA256", amzDate, scope, sha256Hex([]byte(canonicalRequest))}, "\n")

	kDate := hmacSHA256([]byte("AWS4"+secretKey), dateStamp)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, service)
	kSigning := hmacSHA256(kService, "aws4_request")
	signature := hexEncode(hmacSHA256(kSigning, stringToSign))

	request.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		accessKey, scope, signedHeaders, signature,
	))
}

func canonicalURI(path string) string {
	if path == "" {
		return "/"
	}
	return path
}
