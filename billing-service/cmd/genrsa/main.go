// Команда genrsa генерирует RSA-2048 ключи для локальной проверки JWT.
// Прод-ключи хранятся в Yandex Cloud KMS; этот бинарник только для local/test.
package main

// goca:manual — генератор dev-ключей, goca init его не создаёт.

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	dir := "testdata/keys"
	flag.StringVar(&dir, "dir", dir, "output directory for keys")
	flag.Parse()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fatal("mkdir", err)
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		fatal("generate", err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		fatal("marshal private", err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		fatal("marshal public", err)
	}
	if err := writePEM(filepath.Join(dir, "private.pem"), "PRIVATE KEY", privateDER, 0o600); err != nil {
		fatal("write private", err)
	}
	if err := writePEM(filepath.Join(dir, "public.pem"), "PUBLIC KEY", publicDER, 0o644); err != nil {
		fatal("write public", err)
	}
	fmt.Printf("generated %s/{private,public}.pem\n", dir)
}

func writePEM(path, blockType string, der []byte, perm os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return pem.Encode(f, &pem.Block{Type: blockType, Bytes: der})
}

func fatal(what string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", what, err)
	os.Exit(1)
}
