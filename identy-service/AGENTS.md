# AGENTS.md — identity-service

This file compiles high-signal guidance for future OpenCode sessions. Not every line below will apply to every PR, but each reflects a real gotcha or convention that would be easy to miss.

## Quickstart commands (Taskfile)

| Command | What it does |
|-|-|
| `task gen-rsa` | Generate RSA-2048 key pair in `testdata/keys/` (no openssl required) |
| `task infra:up` | Start postgres + redis (detached). Healthchecks managed by docker compose. |
| `task migrate:up` | Apply all pending migrations |
| `task run` | Run identity-service locally (requires infra + env) |
| `task dev` | **First time**: `task gen-rsa && task infra:up && task migrate:up && task run` |
| `task test` | Run unit tests with race detection: `go test ./... -count=1 -race` |
| `task lint` | Run `golangci-lint run ./...` |
| `task build` | Build binary `bin/server` |
| `task docker:up` | Build + start full stack via docker compose (includes migrate-init) |
| `task postbox:mock` | Run local Postbox mock on :9025 (dev-only, never prod). OTP codes print to stdout. |

**Dev flow**: `task gen-rsa && task dev` — generate RSA keys first, then start infra+migrate+server.

## Environment

1. **Copy `.env.example` → `.env.local`** and fill all values. `.env.local` is loaded last in the env priority chain (real env > `.env.${ENV}` > `.env.local`).
2. `ENV` var controls which `.env.*` file is loaded; default is `local`.
3. **Never commit `.env.local`** with real secrets — it is gitignored.

## Infrastructure

- `task infra:up` starts: postgres (port 5433 host → 5432 container), redis-bc1 (6379, JWKS/sessions), redis-blacklist (6380, jti blacklist + account rev).
- `migrate-init` runs automatically on docker compose up; it applies pending migrations before the server starts.
- Server depends on postgres, redis-bc1, redis-blacklist, migrate-init all being healthy.
- To stop: `task infra:down` (preserves volumes) or `task infra:destroy` (deletes volumes).

## RSA keys / JWT

- **Must run `task gen-rsa` before running the service.** Keys are generated in `testdata/keys/private.pem` and `testdata/keys/public.pem`.
- `task gen-rsa` is idempotent — if keys already exist, it skips generation.
- `JWT_PRIVATE_KEY_PATH` and `JWT_PUBLIC_KEY_PATH` env vars must point to the PEM files.
- `JWT_KID=key-v1` — key ID used in JWT headers; rotation uses `JWT_PREV_PUBLIC_KEY_PATH` + `JWT_PREV_KID` with overlap window >= 7 days.
- Without valid RSA keys, the service will panic on startup (missing required env vars).

## CORS and JWT middleware order

- **CORS middleware runs first** in `router.go` — preflight OPTIONS must not pass through JWT validation.
- **ObservabilityMiddleware runs second** — extracts trace context, sets up structured logging, registers Prometheus metrics.
- **gin.Recovery() runs third** — panics caught after observability span is open.
- **JWTMiddleware runs fourth** — validates Bearer token, writes AuthContext to `c.Keys[AuthContextKey]`.
- Order in `router.go`: `CORSMiddleware -> ObservabilityMiddleware -> gin.Recovery() -> JWTMiddleware -> [OwnershipMiddleware | RBACMiddleware] -> handler`.
- CORS `AllowCredentials=true` requires non-wildcard `AllowedOrigins` — browser blocks `*` with credentials.

## G9 Write Order — evictedJTI → blacklist

- When `OpenSession` evicts an old session (6th session exceeds max 5), the evictedJTI **must** be added to Redis blacklist **after** `SaveSessionWithTx` commits.
- Test invariant: `TestLogin_EvictedJTI_AddedToBlacklistAfterSaveSession` verifies `TokenBlacklist.Add` is called after `SaveSessionWithTx`.
- The auth service passes evictedJTI explicitly from `OpenSession` result to the blacklist adapter.

## Fail-closed security

- **Redis unavailable**: Errors propagate up — never silently fall through. Token blacklist `Contains` returns `(false, err)` on Redis failure; rate limiter falls back to in-process counters with conservative thresholds (10% of Redis limits).
- **OwnershipMiddleware returns 404** (not 403) for IDOR prevention — "не раскрываем факт существования аккаунта". Non-owner receives `404 not found`.
- **JWT validation errors**: All non-revoked errors map to generic `"invalid or revoked token"` (401). Do not reveal signature/exp details to client.
- **JWKS keys empty** → 503 Service Unavailable (fail-closed: downstream cannot verify tokens).

## Rate limiting

- **Two-tier**: Redis L2 (exact, shared across instances) + in-process L1 fallback (fail-closed when Redis unavailable).
- Fallback thresholds are conservative: 10% of Redis limits (IP: 10/100 per hour; Account: 5/50 per day).
- `CheckIP` stops **before** `FindByEmail`; `CheckAccount` stops **before** bcrypt comparison.
- Rate limit errors: `ErrRateLimitIP` (429) and `ErrRateLimitAccount` (429) — account is auto-locked 24h after exceeding per-account threshold.

## Password policy

- **History reuse check**: O(5) `PasswordEntry.MatchesPlaintext` against stored history (bcrypt.Compare, not byte equality).
- **Password change** (authenticated) and **password reset** (unauthenticated/OTP-gated): both perform T4 mass-revoke — `rev++`, all sessions cleared, `AccessTokenRevoked` published for every revoked JTI.
- A locked/reset account must not leave valid 30-day tokens outstanding — mass-revoke is mandatory (6).
- Password history limited to 5 entries; 6th entry evicts the oldest.

## Session management (G5)

- **Max 5 sessions per account** (5). New session → oldest evicted → evictedJTI added to blacklist (G9).
- `OpenSession` is the **single** point of session creation; it handles eviction and returns the evicted JTI.
- Sessions have 30-day lifetime (JWT expiry constant `JWTExpirySeconds = 30 * 24 * 60 * 60`).

## OAuth2 / Google

- **PKCE flow**: `code_verifier` length 43-128 chars, `code_challenge` method `S256`.
- **Google JWKS fetch timeout**: 2 seconds (hard timeout; no retry) → fail-closed 503 if unreachable.
- **OAuthState TTL**: 10 minutes — covers only the browser round-trip.
- **Must verify `email_verified` claim** is true; fail-closed: account creation/login rejected if unverified.
- `ErrOAuthEmailNotVerified` — map to 401 / reject flow.
- CORS allowed origins for dev: `http://localhost:3000,http://localhost:5173`.

## OTP (one-time password)

- **TTL**: 3 hours (`OTPTTL = 3 * 60 * 60 * 1000` ms).
- Two flows: `IssueRegistrationOTP` and `IssuePasswordResetOTP`.
- OTP purpose validated via `OTPPurpose` VO; expired/incorrect codes return specific errors.
- OTP sent via Yandex Cloud Postbox; local dev: run `task postbox:mock` and set `POSTBOX_ENDPOINT=http://localhost:9025/v2/email/outbound-emails`.

## DotEnv loading order (migrate + server)

1. **Real environment variables** set in the process (Docker `environment:`, K8s Secrets) — highest priority.
2. **`.env.${ENV}`** — loaded if `ENV` var is set (e.g., `.env.prod`, `.env.staging`).
3. **`.env.local`** — fallback, always loaded last. Does **not** overwrite already-set variables.
- `dotenv.Load()` skips empty lines and `#` comments; supports `export ` prefix for bash compatibility.
- `MIGRATE_TASK_ENV` or `ENV` var controls which env file taskfile loads.

## Testing patterns

- `task test` runs `go test ./... -count=1 -race`.
- Tests use **mocks** from `mocks/` directory + **fixtures** from `testdata/fixtures.go`.
- Key invariant: `ErrInvalidCredentials` is always returned generically — even when account not found or password wrong — to prevent account enumeration via timing.
- Rate limit stops **before** `FindByEmail` (CheckIP) and **before** bcrypt (CheckAccount).
- Login rate limit order: `CheckIP` → `FindByEmail` → `CheckAccount` → bcrypt compare → `Issue`.
- `TestLogin_RateLimitIP_StopsBeforeFindByEmail`: verifies repo `FindByEmail` is never called if IP rate limit exceeded.
- `TestLogin_AccountLocked_StopsAfterFindByEmail`: locked account returns `ErrInvalidCredentials`, not `ErrAccountLocked` — status not leaked.
- Events producer errors are **fire-and-forget** — failure does not block the login response.

## Lint / typecheck

- `task lint` runs `golangci-lint run ./...`. Fix any reported issues before committing.
- There is no separate `typecheck` step — `go test ./...` effectively typechecks the package.
- Run `task lint` locally before pushing; CI will also enforce it.

## Docker

- `task docker:up` builds images + starts full stack (postgres + redis-bc1 + redis-blacklist + migrate-init + server).
- Server depends on all infra being healthy via `depends_on`.
- `Dockerfile.server` builds CGO_ENABLED=0 into distroless/static-debian12; RSA keys must be mounted as volumes or secrets — `Dockerfile.server` comment: "RSA keys mounted outside".
- To run migrations manually outside docker: `task migrate:up` (loads env same as server).
- Docker compose ports: `${POSTGRES_PORT}:5433`, `${REDIS_PORT}:6379`, `${REDIS_BLACKLIST_PORT}:6380`, `${SERVER_PORT}:8080`.

## Important constraints from existing instruction files

- **No openssl required** for key generation — `task gen-rsa` uses Go's `crypto/rsa` only.
- **Postbox mock** is dev-only; never use in prod. OTP codes print to stdout — intentional for local debugging.
- **`.env.local` is gitignored** — do not commit real secrets.
- **Taskfile dotenv** loads `['.env.{{.ENV}}']` + `'.env.local'` last.
- **`go 1.25.0`** — the module targets this Go version; avoid using features newer than 1.25 if supporting older tooling.
- **Workspace**: single Go module `github.com/Reddetk/SayMoDev/identy-service/internal/` — no multi-package boundaries within this repo.