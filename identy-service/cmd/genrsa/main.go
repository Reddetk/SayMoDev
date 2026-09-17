// cmd/genrsa/main.go  генерация RSA-2048 ключей без зависимости от openssl.
// Используется только для локальной разработки. Прод-ключи хранятся в Yandex Cloud KMS.
package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	dir := filepath.Join("testdata", "keys")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir: %v\n", err)
		os.Exit(1)
	}

	privatePath := filepath.Join(dir, "private.pem")
	publicPath := filepath.Join(dir, "public.pem")

	// if _, err := os.Stat(privatePath); err == nil {
	// 	fmt.Println("keys already exist, skipping generation")
	// 	os.Exit(0)
	// }

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		fmt.Fprintf(os.Stderr, "generate key: %v\n", err)
		os.Exit(1)
	}

	// private key  PKCS#8 PEM
	privDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal private: %v\n", err)
		os.Exit(1)
	}
	if err := writePEM(privatePath, "PRIVATE KEY", privDER, 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "write private: %v\n", err)
		os.Exit(1)
	}

	// public key  PKIX PEM
	pubDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal public: %v\n", err)
		os.Exit(1)
	}
	if err := writePEM(publicPath, "PUBLIC KEY", pubDER, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write public: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("generated:\n  %s\n  %s\n", privatePath, publicPath)
}

func writePEM(path, blockType string, der []byte, perm os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	defer f.Close()
	return pem.Encode(f, &pem.Block{Type: blockType, Bytes: der})
}
