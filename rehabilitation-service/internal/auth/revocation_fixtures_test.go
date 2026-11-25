package auth

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestRevocationFixtures — таблица testdata/fixtures/revocation.csv.
func TestRevocationFixtures(t *testing.T) {
	key := newRSAKey(t)
	srv := jwksServer(t, &key.PublicKey)
	defer srv.Close()

	now := time.Now().UTC()
	const (
		sub = "3f2b9c4e-71a5-4d2e-9b8f-0c6a1d5e7f21"
		jti = "b7c1d2e3-4f5a-4b7c-8d9e-0f1a2b3c4d5e"
	)

	for _, row := range readAuthCSV(t, "revocation.csv") {
		row := row
		t.Run(row["case_id"], func(t *testing.T) {
			tokenRev, err := strconv.Atoi(strings.TrimSpace(row["token_rev"]))
			if err != nil {
				t.Fatalf("token_rev: %v", err)
			}

			var accountRev *int
			if raw := strings.TrimSpace(row["account_rev"]); raw != "" {
				v, err := strconv.Atoi(raw)
				if err != nil {
					t.Fatalf("account_rev: %v", err)
				}
				accountRev = &v
			}

			store := &fakeStore{
				blacklisted: row["jti_blacklisted"] == "true",
				accountRev:  accountRev,
			}
			if row["redis_available"] == "false" {
				store.err = errors.New("redis: connection refused")
			}

			auth := NewAuthenticator(Options{
				JWKSURL: srv.URL,
				Now:     func() time.Time { return now },
			}, store)

			raw := signForFixture(t, key, "valid", "RS256", testKID, Claims{
				Role:             "patient",
				SessionID:        "5c8e1f37-2a4b-4d96-8e03-7f1a6b2c9d85",
				Rev:              tokenRev,
				RegisteredClaims: registered(sub, jti, now),
			})

			// L1-кэш: токен был проверен до отзыва, Redis уже недоступен.
			if row["case_id"] == "rev-007-redis-down-l1-hit" {
				store.blacklisted = false
				store.err = nil
				if _, err := auth.Authenticate(context.Background(), raw); err != nil {
					t.Fatalf("prime L1: %v", err)
				}
				store.blacklisted = true
				store.err = errors.New("redis: connection refused")
			}

			_, err = auth.Authenticate(context.Background(), raw)
			wantStatus, _ := strconv.Atoi(row["expected_status"])
			token := row["expected_error_contains"]

			if wantStatus == 200 {
				if err != nil {
					t.Fatalf("want valid token, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("want error, got valid identity")
			}
			if !strings.Contains(err.Error(), token) {
				t.Fatalf("want error containing %q, got %q", token, err.Error())
			}
		})
	}
}
