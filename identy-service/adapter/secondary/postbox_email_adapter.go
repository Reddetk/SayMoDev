// Package secondary contains outbound adapter implementations.
package secondary

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.uber.org/zap"

	corerr "github.com/Reddetk/SayMoDev/identy-service/core/coreErrors"
	valobj "github.com/Reddetk/SayMoDev/identy-service/core/valObj"
	"github.com/Reddetk/SayMoDev/identy-service/logger"
)

// postboxProdEndpoint -- AWS SESv2-compatible endpoint Yandex Cloud Postbox.
// Переопределяется через PostboxConfig.Endpoint для локальной разработки.
const postboxProdEndpoint = "https://postbox.cloud.yandex.net/v2/email/outbound-emails"

// subjectByPurpose maps OTPPurpose to a human-readable email subject.
// Subject does not contain the code (OTP Security Invariant SS7).
var subjectByPurpose = map[valobj.OTPPurpose]string{
	valobj.OTPPurposeRegistration:  "SayMo: код подтверждения регистрации",
	valobj.OTPPurposePasswordReset: "SayMo: код сброса пароля",
	valobj.OTPPurposeEmailChange:   "SayMo: код подтверждения смены почты",
}

// sesv2SimpleTextContent mirrors the SESv2 SimpleEmailContent / Content schema.
// Only Text is populated -- HTML is intentionally omitted (no links, no markup).
type sesv2SimpleTextContent struct {
	Data    string `json:"Data"`
	Charset string `json:"Charset"`
}

type sesv2Content struct {
	Simple struct {
		Subject sesv2SimpleTextContent `json:"Subject"`
		Body    struct {
			Text sesv2SimpleTextContent `json:"Text"`
		} `json:"Body"`
	} `json:"Simple"`
}

type sesv2SendRequest struct {
	FromEmailAddress string `json:"FromEmailAddress"`
	Destination      struct {
		ToAddresses []string `json:"ToAddresses"`
	} `json:"Destination"`
	Content sesv2Content `json:"Content"`
}

// PostboxEmailAdapter implements port/out.EmailBox via Yandex Cloud Postbox
// (AWS SESv2-compatible REST API).
//
// Auth: X-YaCloud-SubjectToken header (IAM token).
// The adapter is stateless: IAM token rotation is the caller's responsibility
// (inject a fresh token per request or use a token-refreshing wrapper).
type PostboxEmailAdapter struct {
	httpClient  *http.Client
	iamToken    string
	fromAddress string
	endpoint    string // prod URL or local mock
	logger      logger.Logger
}

// PostboxConfig holds constructor parameters.
type PostboxConfig struct {
	// IAMToken is the Yandex Cloud IAM subject token used in X-YaCloud-SubjectToken.
	IAMToken string

	// FromAddress must be a verified sender address registered in Yandex Postbox.
	FromAddress string

	// Endpoint overrides the default prod URL.
	// Used in local development to point at cmd/postboxmock.
	// If empty, defaults to postboxProdEndpoint.
	Endpoint string

	// HTTPClient is optional; defaults to a client with a 10-second timeout.
	HTTPClient *http.Client

	// Logger is optional; defaults to zap.NewNop().
	Logger logger.Logger
}

// NewPostboxEmailAdapter constructs the adapter and validates required config.
func NewPostboxEmailAdapter(cfg PostboxConfig) (*PostboxEmailAdapter, error) {
	if cfg.IAMToken == "" {
		return nil, fmt.Errorf("postbox adapter: IAMToken is required")
	}
	if cfg.FromAddress == "" {
		return nil, fmt.Errorf("postbox adapter: FromAddress is required")
	}

	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = postboxProdEndpoint
	}

	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}

	logger := cfg.Logger

	return &PostboxEmailAdapter{
		httpClient:  client,
		iamToken:    cfg.IAMToken,
		fromAddress: cfg.FromAddress,
		endpoint:    endpoint,
		logger:      logger,
	}, nil
}

// SendOTP implements port/out.EmailBox.
//
// Security invariants:
//   - OTP Security Invariant SS7: `code` is NEVER written to logs.
//   - Body is plaintext-only; no HTML, no links.
//   - Called after DB commit, outside any transaction.
//
// Error contract:
//   - Postbox unreachable (network, DNS, timeout) -> ErrEmailServiceUnavailable.
//   - Postbox reachable but returns non-2xx -> ErrEmailDeliveryFailed.
//   - OTP record in DB is NOT rolled back; the caller may surface a retry prompt.
func (a *PostboxEmailAdapter) SendOTP(
	ctx context.Context,
	toEmail string,
	otpPur valobj.OTPPurpose,
	code string,
) error {
	subject, ok := subjectByPurpose[otpPur]
	if !ok {
		a.logger.Error("postbox: unknown OTPPurpose", zap.String("purpose", otpPur.String()))
		return corerr.ErrEmailDeliveryFailed
	}

	body := fmt.Sprintf("Ваш код подтверждения: %s\n\nКод действителен 10 минут.", code)

	payload := sesv2SendRequest{
		FromEmailAddress: a.fromAddress,
	}
	payload.Destination.ToAddresses = []string{toEmail}
	payload.Content.Simple.Subject = sesv2SimpleTextContent{Data: subject, Charset: "UTF-8"}
	payload.Content.Simple.Body.Text = sesv2SimpleTextContent{Data: body, Charset: "UTF-8"}

	rawJSON, err := json.Marshal(payload)
	if err != nil {
		a.logger.Error("postbox: failed to marshal request",
			zap.String("to", toEmail),
			zap.String("purpose", otpPur.String()),
			zap.Error(err),
		)
		return corerr.ErrEmailDeliveryFailed
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint, bytes.NewReader(rawJSON))
	if err != nil {
		a.logger.Error("postbox: failed to build HTTP request",
			zap.String("to", toEmail),
			zap.Error(err),
		)
		return corerr.ErrEmailServiceUnavailable
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-YaCloud-SubjectToken", a.iamToken)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		a.logger.Error("postbox: HTTP transport error",
			zap.String("to", toEmail),
			zap.String("purpose", otpPur.String()),
			zap.Error(err),
		)
		return corerr.ErrEmailServiceUnavailable
	}
	defer func() { _ = resp.Body.Close() }()

	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		a.logger.Error("postbox: non-2xx response",
			zap.String("to", toEmail),
			zap.String("purpose", otpPur.String()),
			zap.Int("status", resp.StatusCode),
		)
		return corerr.ErrEmailDeliveryFailed
	}

	a.logger.Info("postbox: OTP email sent",
		zap.String("to", toEmail),
		zap.String("purpose", otpPur.String()),
		// code намеренно отсутствует -- OTP Security Invariant SS7
	)
	return nil
}
