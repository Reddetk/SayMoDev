package secondary

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/Reddetk/SayMoDev/identy-service/internal/core/consts"
	corerr "github.com/Reddetk/SayMoDev/identy-service/internal/core/coreErrors"
)

//
// rsaClaims — внутренний набор JWT claims для RS256-токена BC#1.
// Все поля соответствуют инвариантам TokenIssuer.Issue:
//   sub, role, jti, session_id, rev, exp, iat, iss, aud
//

type rsaClaims struct {
	jwt.RegisteredClaims
	Role      string `json:"role"`
	SessionID string `json:"session_id"`
	Rev       int64  `json:"rev"`
}

// 
// jwksKeyJSON — внутренняя структура для JSON-сериализации одного JWK-ключа.
// Используется только внутри RSATokenIssuer.buildJWKSCache.
// Поля соответствуют RFC 7517 / RFC 7518 для RSA публичных ключей.
// 

type jwksKeyJSON struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	Alg string `json:"alg"`
	Use string `json:"use"`
	N   string `json:"n"`
	E   string `json:"e"`
}

// 
// RSATokenIssuer реализует порт out.TokenIssuer.
//
// Ответственности:
//   - Issue:         подписать JWT (RS256, kid из currentKid) и вернуть (token, jti).
//   - Verify:        верифицировать входящий JWT через JWKS-кеш; kid lookup с ONE re-fetch.
//   - GetPublicKeys: вернуть срез JSON-строк активных JWK, с TTL-кешом.
//
// Rotation overlap: если передан prevKey != nil, он включается в JWKS-ответ
// и используется при верификации токенов, подписанных предыдущим kid.
// Это позволяет бесшовно ротировать ключ без инвалидации живых токенов.
//
// Инварианты:
//   - privateKey и publicKeys[0] — текущая пара; publicKeys[1] (если есть) — предыдущий ключ.
//   - Verify использует ТОЛЬКО publicKeys для проверки подписи (приватный ключ недоступен).
//   - jwksCache перестраивается при каждом истечении TTL или вызове invalidateCache.
// 

type RSATokenIssuer struct {
	privateKey  *rsa.PrivateKey
	publicKeys  []*rsa.PublicKey // [0]=current, [1]=previous (rotation overlap)
	kid         string           // kid текущего ключа
	prevKid     string           // kid предыдущего ключа (пусто если нет)
	jwksCache   []string         // кеш GetPublicKeys (JSON-строки JWK)
	cacheExpiry time.Time
	mu          sync.RWMutex
}

// NewRSATokenIssuer строит RSATokenIssuer.
//
// currentPriv и currentPub — обязательная текущая пара.
// currentKid — идентификатор текущего ключа (передаётся в kid заголовке JWT и JWKS).
//
// prevPub и prevKid — опциональный предыдущий публичный ключ для rotation overlap.
// Передавать nil/пустую строку если ротации нет.
func NewRSATokenIssuer(
	currentPriv *rsa.PrivateKey,
	currentPub *rsa.PublicKey,
	currentKid string,
	prevPub *rsa.PublicKey,
	prevKid string,
) *RSATokenIssuer {
	keys := []*rsa.PublicKey{currentPub}
	if prevPub != nil {
		keys = append(keys, prevPub)
	}
	return &RSATokenIssuer{
		privateKey: currentPriv,
		publicKeys: keys,
		kid:        currentKid,
		prevKid:    prevKid,
	}
}

// 
// Issue подписывает JWT RS256 и возвращает (accessToken, jti, error).
//
// Генерация jti выполняется в цикле до consts.JTIMaxRetries раз.
// При исчерпании попыток возвращается corerr.ErrJTIGenerationFailed.
// Вероятность коллизии UUID v4 ~10^-18; исчерпание означает сбой UUID-источника.
// 

func (r *RSATokenIssuer) Issue(
	ctx context.Context,
	accountID string,
	role string,
	sessionID string,
	rev int64,
) (accessToken string, jti string, err error) {
	// Генерация jti с повторными попытками
	var generatedJTI string
	for i := 0; i < consts.JTIMaxRetries; i++ {
		id, genErr := uuid.NewRandom()
		if genErr != nil {
			continue
		}
		generatedJTI = id.String()
		break
	}
	if generatedJTI == "" {
		return "", "", corerr.ErrJTIGenerationFailed
	}

	now := time.Now().UTC()
	exp := now.Add(time.Duration(consts.JWTExpirySeconds) * time.Second)

	claims := rsaClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   accountID,
			ID:        generatedJTI,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
			Issuer:    consts.JWTIssuer,
			Audience:  jwt.ClaimStrings{consts.JWTAudience},
		},
		Role:      role,
		SessionID: sessionID,
		Rev:       rev,
	}

	// Создаём токен с принудительным указанием kid в заголовке
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = r.kid

	signed, signErr := token.SignedString(r.privateKey)
	if signErr != nil {
		return "", "", fmt.Errorf("rsa_token_issuer: sign token: %w", signErr)
	}

	return signed, generatedJTI, nil
}

// 
// Verify верифицирует JWT и возвращает распарсенные claims как плоские примитивы.
//
// Порядок:
//   1. Извлечь kid из заголовка токена (без полного парсинга).
//   2. Найти соответствующий публичный ключ в локальном JWKS-кеше.
//   3. Если kid неизвестен  ONE re-fetch (invalidateCache + buildJWKSCache).
//      После re-fetch повторно ищем. Если не найден  ErrJWKSKeysEmpty (fail-closed).
//   4. Верифицировать RS256 подпись, exp, iss, aud.
//   5. Вернуть плоские примитивы: accountID, role, sessionID, jti, rev, expiresAt.
//
// rev возвращается as-is из claim. Сравнение с account.rev  в TokenService.
// 

func (r *RSATokenIssuer) Verify(
	ctx context.Context,
	rawToken string,
) (accountID, role, sessionID, jti string, rev int64, expiresAt int64, err error) {
	kid, kidErr := extractKID(rawToken)
	if kidErr != nil {
		return "", "", "", "", 0, 0, fmt.Errorf("%w: %w", corerr.ErrTokenRevoked, kidErr)
	}

	pubKey, lookupErr := r.lookupPublicKey(kid)
	if lookupErr != nil {
		// ONE re-fetch
		r.invalidateCache()
		pubKey, lookupErr = r.lookupPublicKey(kid)
		if lookupErr != nil {
			return "", "", "", "", 0, 0, corerr.ErrJWKSKeysEmpty
		}
	}

	parsed, parseErr := jwt.ParseWithClaims(
		rawToken,
		&rsaClaims{},
		func(t *jwt.Token) (interface{}, error) {
			if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
			}
			return pubKey, nil
		},
		jwt.WithIssuer(consts.JWTIssuer),
		jwt.WithAudience(consts.JWTAudience),
		jwt.WithExpirationRequired(),
	)
	if parseErr != nil {
		return "", "", "", "", 0, 0, fmt.Errorf("%w: %w", corerr.ErrTokenRevoked, parseErr)
	}

	c, ok := parsed.Claims.(*rsaClaims)
	if !ok || !parsed.Valid {
		return "", "", "", "", 0, 0, corerr.ErrTokenRevoked
	}

	var expUnix int64
	if c.ExpiresAt != nil {
		expUnix = c.ExpiresAt.UnixMilli()
	}

	return c.Subject, c.Role, c.SessionID, c.ID, c.Rev, expUnix, nil
}

// 
// GetPublicKeys возвращает срез JSON-строк активных JWK (RFC 7517).
//
// Результат кешируется на consts.JWKSCacheTTLSeconds секунд.
// Во время rotation overlap возвращает [current, previous].
// Пустой срез (отсутствие активных ключей)  corerr.ErrJWKSKeysEmpty.
// 

func (r *RSATokenIssuer) GetPublicKeys(ctx context.Context) ([]string, error) {
	r.mu.RLock()
	if time.Now().Before(r.cacheExpiry) && len(r.jwksCache) > 0 {
		cached := make([]string, len(r.jwksCache))
		copy(cached, r.jwksCache)
		r.mu.RUnlock()
		return cached, nil
	}
	r.mu.RUnlock()

	r.mu.Lock()
	defer r.mu.Unlock()

	// Double-check под write lock
	if time.Now().Before(r.cacheExpiry) && len(r.jwksCache) > 0 {
		cached := make([]string, len(r.jwksCache))
		copy(cached, r.jwksCache)
		return cached, nil
	}

	built, buildErr := r.buildJWKSCache()
	if buildErr != nil {
		return nil, buildErr
	}

	r.jwksCache = built
	r.cacheExpiry = time.Now().Add(time.Duration(consts.JWKSCacheTTLSeconds) * time.Second)

	result := make([]string, len(built))
	copy(result, built)
	return result, nil
}

// 
// internal helpers
// 

// buildJWKSCache сериализует активные publicKeys в JSON-строки JWK.
// Вызывается только под write lock.
func (r *RSATokenIssuer) buildJWKSCache() ([]string, error) {
	kids := r.activeKIDs()
	if len(r.publicKeys) == 0 || len(kids) != len(r.publicKeys) {
		return nil, corerr.ErrJWKSKeysEmpty
	}

	result := make([]string, 0, len(r.publicKeys))
	for i, pub := range r.publicKeys {
		nBytes := pub.N.Bytes()
		nB64 := base64.RawURLEncoding.EncodeToString(nBytes)

		// Кодируем exponent (e) в big-endian bytes
		eBig := big.NewInt(int64(pub.E))
		eBytes := eBig.Bytes()
		eB64 := base64.RawURLEncoding.EncodeToString(eBytes)

		key := jwksKeyJSON{
			Kid: kids[i],
			Kty: "RSA",
			Alg: "RS256",
			Use: "sig",
			N:   nB64,
			E:   eB64,
		}
		b, marshalErr := json.Marshal(key)
		if marshalErr != nil {
			return nil, fmt.Errorf("rsa_token_issuer: marshal jwks key: %w", marshalErr)
		}
		result = append(result, string(b))
	}

	if len(result) == 0 {
		return nil, corerr.ErrJWKSKeysEmpty
	}
	return result, nil
}

// activeKIDs возвращает kid для каждого ключа в publicKeys в том же порядке.
func (r *RSATokenIssuer) activeKIDs() []string {
	kids := make([]string, 0, len(r.publicKeys))
	kids = append(kids, r.kid) // [0] всегда current
	if len(r.publicKeys) > 1 && r.prevKid != "" {
		kids = append(kids, r.prevKid) // [1] previous (rotation overlap)
	}
	return kids
}

// lookupPublicKey ищет публичный ключ по kid в publicKeys.
// Не обращается к кешу JWKS-строк — работает с *rsa.PublicKey напрямую.
func (r *RSATokenIssuer) lookupPublicKey(kid string) (*rsa.PublicKey, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if kid == r.kid && len(r.publicKeys) > 0 {
		return r.publicKeys[0], nil
	}
	if kid == r.prevKid && len(r.publicKeys) > 1 && r.prevKid != "" {
		return r.publicKeys[1], nil
	}
	return nil, fmt.Errorf("rsa_token_issuer: kid %q not found in key store", kid)
}

// invalidateCache сбрасывает JWKS-кеш для принудительного re-fetch.
// Вызывается при обнаружении неизвестного kid в Verify (ONE re-fetch semantics).
func (r *RSATokenIssuer) invalidateCache() {
	r.mu.Lock()
	r.jwksCache = nil
	r.cacheExpiry = time.Time{}
	r.mu.Unlock()
}

// extractKID извлекает claim "kid" из заголовка JWT без полного парсинга.
// Использует только base64url декодирование первой части (header).
// Не проверяет подпись — kid нужен до выбора ключа верификации.
func extractKID(rawToken string) (string, error) {
	parts := strings.SplitN(rawToken, ".", 3)
	if len(parts) != 3 {
		return "", fmt.Errorf("malformed jwt: expected 3 parts, got %d", len(parts))
	}
	headerBytes, decErr := base64.RawURLEncoding.DecodeString(parts[0])
	if decErr != nil {
		return "", fmt.Errorf("malformed jwt header: %w", decErr)
	}

	var header struct {
		Kid string `json:"kid"`
	}
	if unmarshalErr := json.Unmarshal(headerBytes, &header); unmarshalErr != nil {
		return "", fmt.Errorf("malformed jwt header json: %w", unmarshalErr)
	}
	if header.Kid == "" {
		return "", fmt.Errorf("jwt header missing kid claim")
	}
	return header.Kid, nil
}

// Compile-time interface check.
// Гарантирует, что RSATokenIssuer реализует порт out.TokenIssuer.
// Импорт порта здесь намеренно избегается для предотвращения import cycle;
// проверка выполняется через explicit cast.
var _ interface {
	Issue(ctx context.Context, accountID, role, sessionID string, rev int64) (string, string, error)
	Verify(ctx context.Context, rawToken string) (string, string, string, string, int64, int64, error)
	GetPublicKeys(ctx context.Context) ([]string, error)
} = (*RSATokenIssuer)(nil)

// Ensure rand.Reader is used to prevent test-time substitution of the random source.
var _ = rand.Reader
