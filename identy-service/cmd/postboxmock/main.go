// cmd/postboxmock/main.go -- локальный mock Yandex Cloud Postbox для разработки.
//
// Запуск: task postbox:mock  (port :9025)
//
// WARNING: dev-only. Никогда не включать в prod-образ.
// OTP-коды печатаются в stdout -- только для локальной отладки.
package main

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	mockPath    = "/v2/email/outbound-emails"
	maxInbox    = 50
	defaultPort = ":9025"
)

// email хранит парсинг одного письма.
type email struct {
	ReceivedAt time.Time
	To         string
	Subject    string
	Body       string
}

var (
	mu    sync.Mutex
	inbox []email
)

// sesv2 структуры -- минимальный subset для парсинга.
type sesv2Body struct {
	FromEmailAddress string `json:"FromEmailAddress"`
	Destination      struct {
		ToAddresses []string `json:"ToAddresses"`
	} `json:"Destination"`
	Content struct {
		Simple struct {
			Subject struct{ Data string } `json:"Subject"`
			Body    struct {
				Text struct{ Data string } `json:"Text"`
			} `json:"Body"`
		} `json:"Simple"`
	} `json:"Content"`
}

func main() {
	port := defaultPort
	if p := os.Getenv("POSTBOX_MOCK_PORT"); p != "" {
		port = ":" + p
	}

	mux := http.NewServeMux()
	mux.HandleFunc(mockPath, handleSend)
	mux.HandleFunc("/", handleInbox)

	log.Printf("[postbox-mock] listening on %s", port)
	log.Printf("[postbox-mock] inbox UI: http://localhost%s/", port)
	log.Printf("[postbox-mock] endpoint: http://localhost%s%s", port, mockPath)

	if err := http.ListenAndServe(port, mux); err != nil {
		log.Fatalf("[postbox-mock] fatal: %v", err)
	}
}

func handleSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<16))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}

	var req sesv2Body
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	to := ""
	if len(req.Destination.ToAddresses) > 0 {
		to = req.Destination.ToAddresses[0]
	}
	subject := req.Content.Simple.Subject.Data
	text := req.Content.Simple.Body.Text.Data

	e := email{
		ReceivedAt: time.Now(),
		To:         to,
		Subject:    subject,
		Body:       text,
	}

	mu.Lock()
	inbox = append([]email{e}, inbox...)
	if len(inbox) > maxInbox {
		inbox = inbox[:maxInbox]
	}
	mu.Unlock()

	// stdout -- dev visibility. OTP виден здесь намеренно (dev-only).
	fmt.Printf("\n[postbox-mock] ---\n")
	fmt.Printf("  TO:      %s\n", to)
	fmt.Printf("  SUBJECT: %s\n", subject)
	fmt.Printf("  BODY:    %s\n", text)
	fmt.Printf("[postbox-mock] ---\n")

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{}`))
}

func handleInbox(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	copy := append([]email(nil), inbox...)
	mu.Unlock()

	var sb strings.Builder
	sb.WriteString(`<!DOCTYPE html><html><head><meta charset="utf-8">`)
	sb.WriteString(`<title>Postbox Mock</title>`)
	sb.WriteString(`<style>body{font-family:monospace;padding:24px;background:#111;color:#eee}`)
	sb.WriteString(`.card{border:1px solid #333;border-radius:6px;padding:16px;margin-bottom:16px;background:#1a1a1a}`)
	sb.WriteString(`.label{color:#888;font-size:12px;text-transform:uppercase;margin-bottom:2px}`)
	sb.WriteString(`.val{color:#fff;margin-bottom:10px;white-space:pre-wrap}`)
	sb.WriteString(`.ts{color:#555;font-size:11px;float:right}</style>`)
	sb.WriteString("</head><body>")
	sb.WriteString(fmt.Sprintf("<h2>Postbox Mock <span style='font-size:14px;color:#555'>(%d emails)</span></h2>", len(copy)))

	if len(copy) == 0 {
		sb.WriteString(`<p style="color:#555">No emails yet. Send an OTP to see it here.</p>`)
	}

	for _, e := range copy {
		sb.WriteString(`<div class="card">`)
		sb.WriteString(fmt.Sprintf(`<span class="ts">%s</span>`, e.ReceivedAt.Format("15:04:05")))
		sb.WriteString(fmt.Sprintf(`<div class="label">TO</div><div class="val">%s</div>`, html.EscapeString(e.To)))
		sb.WriteString(fmt.Sprintf(`<div class="label">SUBJECT</div><div class="val">%s</div>`, html.EscapeString(e.Subject)))
		sb.WriteString(fmt.Sprintf(`<div class="label">BODY</div><div class="val">%s</div>`, html.EscapeString(e.Body)))
		sb.WriteString(`</div>`)
	}

	sb.WriteString(`<script>setTimeout(()=>location.reload(),5000)</script>`)
	sb.WriteString(`</body></html>`)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(sb.String()))
}
