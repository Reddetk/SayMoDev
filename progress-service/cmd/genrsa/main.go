// Command genrsa generates an RS256 key pair for the local test bench (not for production).
package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"flag"
	"log"
	"os"
	"path/filepath"
)

func main() {
	out := flag.String("out", "./testdata/keys", "output directory")
	flag.Parse()

	if err := os.MkdirAll(*out, 0o755); err != nil {
		log.Fatal(err)
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		log.Fatal(err)
	}
	priv := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	pubBytes, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		log.Fatal(err)
	}
	pub := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubBytes})
	if err := os.WriteFile(filepath.Join(*out, "private.pem"), priv, 0o600); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(*out, "public.pem"), pub, 0o644); err != nil {
		log.Fatal(err)
	}
	log.Printf("keys written to %s", *out)
}
