// Команда devtoken выпускает тестовый RS256 JWT BC#1 для local/test E2E.
package main

// goca:manual — dev-выпуск токенов для E2E, goca init его не создаёт.

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func main() {
	privatePath := flag.String("private", "testdata/keys/private.pem", "path to RSA private key (PKCS#8)")
	subject := flag.String("sub", uuid.NewString(), "account id (JWT sub)")
	role := flag.String("role", "patient", "role: patient|relative|administrator")
	jti := flag.String("jti", uuid.NewString(), "unique token id")
	sessionID := flag.String("session", uuid.NewString(), "session id")
	rev := flag.Int("rev", 1, "account revision")
	issuer := flag.String("issuer", "urn:saymo:identity-service", "JWT issuer")
	audience := flag.String("aud", "api.saymo", "JWT audience")
	ttl := flag.Duration("ttl", 30*24*time.Hour, "token lifetime")
	flag.Parse()

	key, err := loadPrivateKey(*privatePath)
	if err != nil {
		fail(err)
	}
	now := time.Now()
	claims := jwt.MapClaims{
		"iss":        *issuer,
		"aud":        *audience,
		"sub":        *subject,
		"role":       *role,
		"exp":        now.Add(*ttl).Unix(),
		"iat":        now.Unix(),
		"jti":        *jti,
		"session_id": *sessionID,
		"rev":        *rev,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = "key-v1"
	signed, err := token.SignedString(key)
	if err != nil {
		fail(err)
	}
	fmt.Println(signed)
}

func loadPrivateKey(path string) (*rsa.PrivateKey, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(body)
	if block == nil {
		return nil, fmt.Errorf("private key is not PEM")
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

func fail(err error) {
	fmt.Fprintf(os.Stderr, "devtoken: %v\n", err)
	os.Exit(1)
}
