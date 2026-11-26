// Команда postboxmock — локальная заглушка Yandex Cloud Postbox (SESv2).
// Принимает POST /v2/email/outbound-emails, печатает письмо в stdout, отдаёт 200.
package main

// goca:manual — локальная заглушка Postbox для local/test.

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
)

func main() {
	addr := os.Getenv("POSTBOX_MOCK_ADDR")
	if addr == "" {
		addr = ":9025"
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/email/outbound-emails", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		var payload struct {
			FromEmailAddress string `json:"FromEmailAddress"`
			Destination      struct {
				ToAddresses []string `json:"ToAddresses"`
			} `json:"Destination"`
			Content struct {
				Simple struct {
					Subject struct {
						Data string `json:"Data"`
					} `json:"Subject"`
					Body struct {
						Text struct {
							Data string `json:"Data"`
						} `json:"Text"`
					} `json:"Body"`
				} `json:"Simple"`
			} `json:"Content"`
		}
		_ = json.Unmarshal(body, &payload)
		log.Printf("postboxmock: from=%s to=%v subject=%q body=%q",
			payload.FromEmailAddress, payload.Destination.ToAddresses,
			payload.Content.Simple.Subject.Data, payload.Content.Simple.Body.Text.Data)
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("Alive"))
	})
	log.Printf("postboxmock listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		fmt.Fprintf(os.Stderr, "postboxmock: %v\n", err)
		os.Exit(1)
	}
}
