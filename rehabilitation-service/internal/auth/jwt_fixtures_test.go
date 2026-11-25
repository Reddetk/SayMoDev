package auth

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"
)

func intPtr(v int) *int { return &v }

// TestJWKFixtures — таблица testdata/fixtures/jwt.csv от чистого субагента.
func TestJWKFixtures(t *testing.T) {
	key := newRSAKey(t)

	for _, row := range readAuthCSV(t, "jwt.csv") {
		row := row
		t.Run(row["case_id"], func(t *testing.T) {
			var fc fixtureClaims
			if err := json.Unmarshal([]byte(row["claims_json"]), &fc); err != nil {
				t.Fatalf("claims_json: %v", err)
			}
			offset, err := strconv.Atoi(strings.TrimSpace(row["clock_offset_seconds"]))
			if err != nil {
				t.Fatalf("clock_offset_seconds: %v", err)
			}

			realNow := time.Now().UTC()
			claims := buildClaims(fc, realNow)
			raw := signForFixture(t, key, row["signing"], row["alg_header"], row["kid_header"], claims)

			var jwksURL string
			if row["jwks_available"] == "true" {
				srv := jwksServer(t, &key.PublicKey)
				defer srv.Close()
				jwksURL = srv.URL
			} else {
				srv := jwksServer(t, &key.PublicKey)
				jwksURL = srv.URL
				srv.Close()
			}

			auth := NewAuthenticator(Options{
				JWKSURL: jwksURL,
				Now:     func() time.Time { return realNow.Add(time.Duration(offset) * time.Second) },
			}, &fakeStore{accountRev: intPtr(0)})

			identity, err := auth.Authenticate(context.Background(), raw)
			wantStatus, _ := strconv.Atoi(row["expected_status"])
			token := row["expected_error_contains"]

			if wantStatus == 200 {
				if err != nil {
					t.Fatalf("want valid token, got %v", err)
				}
				if identity.AccountID != fc.Sub || identity.Role != fc.Role {
					t.Fatalf("identity: want %s/%s, got %s/%s", fc.Sub, fc.Role, identity.AccountID, identity.Role)
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
