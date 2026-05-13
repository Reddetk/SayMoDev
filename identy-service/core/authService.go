package core

// аутентификация - отдельная операция с собственными rate-limit инвариантами, timing-safety требованиями и bcrypt-логикой.
// Покрывает юз-кейсы и эндпоинты :
// POST /iam/auth/login - IP rate check → account lookup → bcrypt.Compare → SessionService
// GET /iam/auth/oauth/google + GET /iam/auth/oauth/google/callback - PKCE flow, Google JWKS verification, lookup/create account
// Инварианты: dummy hash на несуществующий аккаунт (timing safety), IP-first rate check, email_verified: true для OAuth
