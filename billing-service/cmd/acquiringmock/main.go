// Команда acquiringmock — локальная заглушка WebPay для E2E (local/test).
// Принимает redirect и recurring-запросы, всегда отвечает 200.
package main

// goca:manual — локальная заглушка эквайринга, goca init её не создаёт.

import (
	"fmt"
	"log"
	"net/http"
	"os"
)

func main() {
	addr := os.Getenv("ACQUIRING_MOCK_ADDR")
	if addr == "" {
		addr = ":9031"
	}
	handler := http.NewServeMux()
	handler.HandleFunc("/pay", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		log.Printf("acquiringmock: %s order=%s type=%s", r.Method, r.Form.Get("wsb_order_num"), r.Form.Get("wsb_operation_type"))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})
	handler.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("Alive"))
	})
	log.Printf("acquiringmock listening on %s", addr)
	if err := http.ListenAndServe(addr, handler); err != nil {
		fmt.Fprintf(os.Stderr, "acquiringmock: %v\n", err)
		os.Exit(1)
	}
}
