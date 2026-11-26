// Command jwksmock serves a local JWKS document for the test bench (no network, no IAM needed).
package main

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"flag"
	"log"
	"math/big"
	"net/http"
	"os"
)

type key struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
}

func main() {
	addr := flag.String("addr", envOr("MOCK_JWKS_ADDR", ":8099"), "listen address")
	path := flag.String("public-key", envOr("MOCK_JWKS_PUBLIC_KEY", "./testdata/keys/public.pem"), "RSA public key PEM")
	kid := flag.String("kid", envOr("JWT_KID", "key-v1"), "key id")
	flag.Parse()

	pub, err := loadPublicKey(*path)
	if err != nil {
		log.Fatalf("jwksmock: %v", err)
	}
	doc := map[string]any{"keys": []key{{
		Kty: "RSA", Kid: *kid, Use: "sig", Alg: "RS256",
		N: base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		E: base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}}}

	http.HandleFunc("/jwks.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(doc)
	})
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })

	log.Printf("jwksmock listening on %s (kid=%s)", *addr, *kid)
	log.Fatal(http.ListenAndServe(*addr, nil))
}

func loadPublicKey(path string) (*rsa.PublicKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, os.ErrInvalid
	}
	if key, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		if rsaKey, ok := key.(*rsa.PublicKey); ok {
			return rsaKey, nil
		}
	}
	if key, err := x509.ParsePKCS1PublicKey(block.Bytes); err == nil {
		return key, nil
	}
	return nil, os.ErrInvalid
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
