package core

// выпус и отзыв токенов - изолируемая ответственность. JWT signing с RS256, управление kid, JWKS endpoint, blacklist write path.
// Покрывает :
// GET /iam/.well-known/jwks.json - публикация публичных ключей для всех downstream BC
// Выпуск JWT после логина/регистрации (claims: sub, role, jti, session_id, rev, exp, kid)
// Blacklist write: Redis L2 first → outbox → PostgreSQL L3
// Ключевой инвариант: jti uniqueness с retry up to 3x
// Публикует событие: AccessTokenRevoked
