package auth

// goca:manual — декодирование JWK (n/e) для JWKS BC#1.

import (
	"crypto/rsa"
	"encoding/base64"
	"fmt"
	"math/big"
)

func decodeJWK(encodedN, encodedE string) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(encodedN)
	if err != nil {
		return nil, err
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(encodedE)
	if err != nil {
		return nil, err
	}
	if len(eBytes) == 0 {
		return nil, fmt.Errorf("empty exponent")
	}
	e := 0
	for _, b := range eBytes {
		e = e<<8 | int(b)
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: e}, nil
}
