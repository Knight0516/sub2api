# CODEBUDDY.md This file provides guidance to CodeBuddy when working with code in this repository.

Sub2API — an AI API gateway that distributes/sells upstream AI subscription quota to end users.
Go backend (Gin + Ent + PostgreSQL + Redis) + Vue 3 frontend (Vite + Tailwind + Pinia).
Existing project docs worth reading first: `DEV_GUIDE.md` (env pitfalls, CI rules, PR checklist), `README.md` (deployment + feature surface), `命令手册.md`, `docs/`.

## 1. Commands

### Backend (`backend/`)
- **Run server**: `cd backend && go run ./cmd/server` — first boot serves the Setup Wizard on `:8080`; it writes `config.yaml`. If a `config.yaml` already exists the wizard is skipped and login fails with an empty `users` table (see README "Creating the Admin Account").
- **Build**: `cd backend && make build` (or `go build -tags embed -o sub2api ./cmd/server`; without `-tags embed` the binary does not serve the frontend).
- **Unit tests**: `cd backend && go test -tags=unit ./...`
- **Integration tests** (needs PostgreSQL + Redis): `cd backend && go test -tags=integration ./...`
- **Single test**: `cd backend && go test -tags=unit ./internal/service/ -run TestFoo -v`
- **Lint**: `cd backend && golangci-lint run ./...` — CI pins v2.13; install the same version to avoid noise.
- **Regenerate Ent + Wire** (required after editing `ent/schema/*.go` or adding a service): `cd backend && go generate ./ent && go generate ./cmd/server`. Commit generated files.
- **Root aggregator**: `make test` = backend `go test ./...` + `golangci-lint` + frontend lint/typecheck/critical vitest suite. Windows has no `make`; run the raw commands above.

### Frontend (`frontend/`, pnpm only — never npm)
- **Install / dev / build**: `pnpm install` · `pnpm dev` · `pnpm run build` (build runs the i18n key check first, then `vue-tsc`, then emits to `../backend/internal/web/dist/`).
- **Tests**: `pnpm test:run` (all) · `pnpm exec vitest run <path>` (single) · `pnpm run test:coverage`.
- **Lint / types**: `pnpm run lint` · `pnpm run lint:check` · `pnpm run typecheck`.
- **i18n key check alone**: `pnpm run check:i18n`.

### Cross-cutting musts
- Changing `package.json` requires committing an updated `pnpm-lock.yaml` (CI uses `--frozen-lockfile`).
- Go version is hard-asserted in CI; bumping Go requires editing `backend/go.mod`, all three workflows, and all three Dockerfiles (details in `DEV_GUIDE.md` §三).
- Adding a method to an interface implemented by test stubs means fixing every stub/mock in `internal/`.

## 2. Architecture

### Layering
`handler → service → repository/Ent → PostgreSQL/Redis`. Handlers parse HTTP and own no business logic; services own all business rules and are what tests target.

- `backend/cmd/server` — `main.go` entry. **Google Wire** is the DI container: `cmd/server/wire.go` lists providers in `wire.Build(...)` and `wire_gen.go` is generated (`go generate ./cmd/server`). Never hand-edit `wire_gen.go`.
- `backend/internal/service/wire.go` — the single `ProviderSet` (~300 services). **A new service must be registered here** (plus its dependencies) or Wire fails at generate time.
- `backend/internal/handler` — Gin handlers split into `handler/` (user + gateway) and `handler/admin/` (admin). Both are bundled into a `Handlers` struct consumed by the router.
- `backend/internal/server` — `http.go` (server lifecycle), `router.go` (`SetupRouter` / `registerRoutes`). Global middleware order: RequestLogger → SessionBindingContext → Logger → CORS → SecurityHeaders → ServerTiming, then either `web.ServeEmbeddedFrontend()` (embed build) or `frontendServer.Middleware()` (dev proxy). `/api/v1` is the only public API prefix; gateway paths (`/v1/messages`, etc.) are registered separately and bypass JWT in favour of `APIKeyAuth`.
- `backend/internal/repository` — Ent-backed implementations. Services declare the *interface* they need inside their own package (`...Repository interface` in service files or `*_port.go`); `internal/repository` provides concrete structs. Some services instead take `*dbent.Client` directly and query Ent inline. Both patterns coexist — match the surrounding file.
- Other notable packages: `internal/platform` (upstream adapter layer), `internal/payment` (provider abstractions), `internal/domain` (constants/enums shared across layers), `internal/pkg` (errors, response, logging, cache, etc.), `internal/setup` (bootstrap wizard), `internal/web` (embedded frontend), `internal/migrations` (see below).

### Gateway request path (the hot path — treat with care)
`gateway_handler.go` → `GatewayService`. Key files, all in `internal/service`:
- `gateway_service.go` — service struct; session hash (`GenerateSessionHash`), sticky-session binding (`BindStickySession`), token retrieval (`GetAccessToken`), model listing/caches.
- `gateway_scheduling.go` — the largest file (~2600 lines) and the heart of the system: `SelectAccount` / `SelectAccountForModel` / `SelectAccountWithLoadAwareness` pick an upstream account using sticky sessions, per-account concurrency slots (`tryAcquireAccountSlot`), quota/RPM/window-cost filters, scheduling thresholds, and exclusion sets for retries.
- `gateway_request.go` + `gateway_upstream_request.go` / `gateway_upstream_response.go` — request parsing/conversion and upstream forwarding per protocol (Anthropic Messages, OpenAI Chat Completions/Responses, Gemini, Bedrock, Vertex, System One).
- `gateway_forward.go` and `gateway_forward_as_*.go` — protocol entry points and streaming/non-streaming dispatch.
- `gateway_usage_billing.go` — records usage and debits balance/subscription quotas after the response completes.
- Supporting: `concurrency_service.go` (distributed slots), `billing_service.go` (pricing/cost math), `usage_log.go` / `usage_billing.go` / `usage_record_worker_pool.go` (write path), `scheduler_*.go` (scheduling caches), `error_passthrough_service.go` (provider error → client policy), `temp_unsched.go`, `proxy.go`/`proxy_fallback.go` (egress proxying), `antigravity_gateway_*.go` and `openai_gateway_*.go` (platform-specific gateways).

### Caching
Redis-backed caches are per-service (`*_cache_service.go`, `*_cache.go`): billing cache (`billing_cache_service.go`), API-key auth cache (`api_key_auth_cache*.go`), scheduler cache, token cache, session/RPM caches. **Cache invalidation is publish/subscribe based** — e.g. `auth_cache_invalidation_outbox.go` writes an outbox row that a background subscriber broadcasts, and `BillingCacheService.SubscribeSubscriptionCacheInvalidation` refreshes local state. Changing cached data means finding and reusing the existing invalidation hook rather than only writing to the DB.

### Errors and responses
Never return raw `errors.New` from services. Use `internal/pkg/errors` (imported as `infraerrors`), which maps constructors to HTTP statuses: `BadRequest(400)`, `Unauthorized(401)`, `Forbidden(403)`, `NotFound(404)`, `Conflict(409)`, `TooManyRequests(429)`, `InternalServer(500)`. Signature is `Constructor(reason, message)` where `reason` is a stable machine code consumed by the frontend (e.g. `infraerrors.Conflict("SUBSCRIPTION_ALREADY_EXISTS", "...")`). `IsXxx(err)` helpers support wrapped errors. Handlers return via `internal/pkg/response`: `response.Success(c, data)`, `response.ErrorFrom(c, err)`, `response.BadRequest(c, msg)`. Domain errors are declared as package-level vars next to the owning service.

### Persistence: Ent + SQL migrations
- Schema lives in `backend/ent/schema/*.go`. Mixed conventions: `mixins.TimeMixin` for timestamps, `mixins.SoftDeleteMixin` for soft delete. Postgres-specific column types are set with `SchemaType(map[string]string{dialect.Postgres: "decimal(20,2)"})`. Each schema file documents its delete strategy (hard vs soft) in a comment — read it before assuming.
- Migrations are **hand-written SQL** in `backend/migrations/` (`NNN_snake_case.sql`, latest is around `248_*`). They are embedded via `backend/migrations/migrations.go` and applied automatically at startup by `internal/repository/migrations_runner.go` (`ApplyMigrations`).
- Runner semantics you must respect: (1) files run in filename sort order; (2) each file's SHA-256 is recorded in `schema_migrations` — **editing an already-applied migration breaks checksum validation**, always add a new numbered file; (3) `CREATE/DROP INDEX CONCURRENTLY` must live in a `*_notx.sql` file (the runner rejects `CONCURRENTLY` in transactional migrations and rejects `BEGIN/COMMIT` in `*_notx.sql`); (4) a Postgres advisory lock serializes multi-instance startup; (5) migrations must be idempotent (`IF NOT EXISTS`).
- Adding a column therefore requires **both** a new numbered migration **and** the matching `ent/schema` change + `go generate ./ent`.

### Frontend organisation
`api/` wraps axios per domain (`adminRequest` helper in `api/adminUIRequest.ts` for panel endpoints); components read errors through `utils/apiError.extractApiErrorMessage`. `stores/` are Pinia modules (auth, app, payment, subscriptions, adminSettings, announcements, adminCompliance, onboarding). `views/` splits by `admin/` vs `user/` vs `auth/`; shared UI lives in `components/` (common primitives like `BaseDialog`, `Select`, and domain folders). Route definitions are in `router/`.

Internationalisation: `src/i18n/locales/{en,zh}` (Japanese ships only as a README translation today). `src/i18n/__tests__/localeKeyCompleteness.spec.ts` **fails the build** if English and Chinese key trees diverge, if any leaf is empty, or if any statically referenced production key is missing from either locale. Any new UI string therefore means adding it to **both** locales; `pnpm run build` and `make test` will catch omissions.
