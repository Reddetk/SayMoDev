package postbox

// goca:manual — криптопримитивы SigV4 (SHA-256/HMAC), goca их не генерирует.

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key []byte, data string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(data))
	return mac.Sum(nil)
}

func hexEncode(data []byte) string { return hex.EncodeToString(data) }
