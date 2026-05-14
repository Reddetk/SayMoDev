package valobj

import (
	corerr "github.com/Reddetk/SayMoDev/identy-service/core/coreErrors"
)

// JWKSResponse — Value Object, представляющий тело ответа
// GET /iam/.well-known/jwks.json.
//
// Содержит срез валидных JWKSKey. Инвариант: Keys не пуст —
// пустой JWKS означает невозможность верификации токенов
// для всех downstream BC и является ошибкой конфигурации.
//
// HTTP-адаптер сериализует VO в JSON { "keys": [...] } напрямую.
type JWKSResponse struct {
	keys []JWKSKey
}

// NewJWKSResponse строит валидированный JWKSResponse.
// Возвращает ErrJWKSKeysEmpty если keys пуст.
func NewJWKSResponse(keys []JWKSKey) (JWKSResponse, error) {
	if len(keys) == 0 {
		return JWKSResponse{}, corerr.ErrJWKSKeysEmpty
	}
	copy := make([]JWKSKey, len(keys))
	for i, k := range keys {
		copy[i] = k
	}
	return JWKSResponse{keys: copy}, nil
}

func (r JWKSResponse) Keys() []JWKSKey { return r.keys }
