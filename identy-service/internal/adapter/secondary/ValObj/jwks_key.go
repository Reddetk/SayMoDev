package valobj

import (
	corerr "github.com/Reddetk/SayMoDev/identy-service/internal/core/coreErrors"
)

// JWKSKey — Value Object, представляющий одну запись в JWKS-ответе.
//
// Соответствует RFC 7517 (JSON Web Key) для RSA публичного ключа.
// Поля намеренно имеют те же имена, что и JSON-теги RFC — адаптер
// сериализует их напрямую без преобразований.
//
// Инварианты:
//   - KID, N, E не могут быть пустыми (ключ без идентификатора
//     и без компонентов модуля/экспоненты не является валидным JWK)
//   - Kty всегда "RSA", Alg всегда "RS256", Use всегда "sig" —
//     домен BC#1 не поддерживает иных алгоритмов и назначений
//
// Значения N и E — base64url-encoded компоненты RSA публичного ключа
// (PKIX/SPKI, без padding «=»). Формирует адаптер KeyStore.
//
// Доменный слой не занимается кодированием ключей — это
// инфраструктурный артефакт адаптера. VO лишь переносит данные.
type JWKSKey struct {
	kid string // Key ID — совпадает с kid в заголовке JWT
	kty string // Key Type — всегда "RSA"
	alg string // Algorithm — всегда "RS256"
	use string // Public Key Use — всегда "sig"
	n   string // RSA Modulus, base64url-encoded
	e   string // RSA Public Exponent, base64url-encoded
}

// NewJWKSKey строит валидированный JWKSKey.
// kid, n, e обязательны — без них ключ невалиден по RFC 7517.
// kty, alg, use фиксированы константами домена BC#1.
func NewJWKSKey(kid, n, e string) (JWKSKey, error) {
	if kid == "" {
		return JWKSKey{}, corerr.ErrJWKSKeyIDEmpty
	}
	if n == "" {
		return JWKSKey{}, corerr.ErrJWKSKeyModulusEmpty
	}
	if e == "" {
		return JWKSKey{}, corerr.ErrJWKSKeyExponentEmpty
	}
	return JWKSKey{
		kid: kid,
		kty: "RSA",
		alg: "RS256",
		use: "sig",
		n:   n,
		e:   e,
	}, nil
}

// RestoreJWKSKey восстанавливает JWKSKey из персистентного хранилища.
// Используется только адаптером KeyStore — без повторной валидации
// (данные уже прошли валидацию при создании и хранении).
func RestoreJWKSKey(kid, kty, alg, use, n, e string) JWKSKey {
	return JWKSKey{kid: kid, kty: kty, alg: alg, use: use, n: n, e: e}
}

func (k JWKSKey) KID() string { return k.kid }
func (k JWKSKey) Kty() string { return k.kty }
func (k JWKSKey) Alg() string { return k.alg }
func (k JWKSKey) Use() string { return k.use }
func (k JWKSKey) N() string   { return k.n }
func (k JWKSKey) E() string   { return k.e }
