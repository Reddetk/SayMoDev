// Command tokenmint prints a signed RS256 token for local HTTP/E2E checks.
package main

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func main() {
	account := flag.String("account", "11111111-1111-1111-1111-111111111111", "token.sub (accountId)")
	role := flag.String("role", "patient", "token.role")
	ttl := flag.Duration("ttl", time.Hour, "token lifetime")
	kid := flag.String("kid", envOr("JWT_KID", "key-v1"), "JWT header kid")
	issuer := flag.String("iss", envOr("JWT_ISSUER", "saymo-iam"), "issuer")
	audience := flag.String("aud", envOr("JWT_AUDIENCE", "saymo"), "audience")
	rev := flag.Int64("rev", 1, "token.rev")
	jti := flag.String("jti", "", "token.jti (random when empty)")
	keyPath := flag.String("private-key", envOr("JWT_PRIVATE_KEY_PATH", "./testdata/keys/private.pem"), "RSA private key PEM")
	flag.Parse()

	key, err := loadPrivateKey(*keyPath)
	if err != nil {
		log.Fatalf("tokenmint: %v", err)
	}
	id := *jti
	if id == "" {
		id = uuid.NewString()
	}
	now := time.Now()
	claims := jwt.MapClaims{
		"sub":        *account,
		"role":       *role,
		"jti":        id,
		"session_id": uuid.NewString(),
		"rev":        *rev,
		"iss":        *issuer,
		"aud":        *audience,
		"iat":        now.Unix(),
		"exp":        now.Add(*ttl).Unix(),
	}
	t := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	t.Header["kid"] = *kid
	signed, err := t.SignedString(key)
	if err != nil {
		log.Fatalf("tokenmint: sign: %v", err)
	}
	fmt.Println(signed)
}

func loadPrivateKey(path string) (*rsa.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("invalid PEM")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("not an RSA key")
	}
	return key, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
