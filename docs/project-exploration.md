# CPA Usage Keeper — Project Exploration Summary

> Generated 2026-08-31 from a full-codebase exploration. Snapshot of the repo at commit `2998549` (merge of `feat/totp-2fa`).

## What It Is

A standalone persistence + analytics dashboard for [CLIProxyAPI (CPA)](https://github.com/router-for-me/CLIProxyAPI). Keeper ingests CPA usage events into SQLite, syncs CPA credentials and metadata, and serves a single self-contained binary with an embedded React UI covering usage, cost, request health, quotas, and rankings.

- **License:** MIT · **Module:** `cpa-usage-keeper` · **Go 1.26** + CGO SQLite
- **Scale:** ~54k lines of non-test Go (646 files, 311 test files) + ~32k lines of TS/TSX (254 files, 143 test files)
- **Docs/comments:** bilingual (English README + 简体中文 README; code comments largely in Chinese)

## Architecture

```text
CPA (Redis queue @ :8317 / management HTTP API)
        │  SUBSCRIBE → LPOP → HTTP fallback (3-tier degradation)
        ▼
internal/poller  ──►  redis_usage_inboxes (raw staging)
        │  decode + per-provider token canonicalization
        ▼
usage_events (hot, 90-day retention)
        │  async rollups: overview / activity / latency / identity
        │  (single serial runner, 5s debounce, fair turns, checkpoint cursors)
        ▼
internal/api (gin)  ──►  embedded React SPA (go:embed web/dist → one binary)
```

Two defining backend decisions in `internal/repository/db.go`:

1. **Single SQLite writer + hard read-only reader pool** — one writer connection (`MaxOpenConns=1`) plus a `mode=ro&_query_only=on` reader routed through GORM `dbresolver`; all business code holds one `*gorm.DB`. WAL is mandatory for file DBs.
2. **Aggregates fully decoupled from the write path** — `usage_events` inserts commit fast; rollups and identity stats are computed asynchronously. Archiving to `usage_events_archive` refuses to run until every rollup cursor and identity cursor reaches `MAX(id)` (conservative, replay-safe).

## Backend (`internal/`, Go)

| Package | Role |
| --- | --- |
| `app` | Composition root — DB pools, ~12 background runners, router, HTTP server; strict reverse-order cleanup |
| `api` | gin router: public `/healthz`, login routes (rate-limited), `adminProtected`, `keyViewerProtected`, SPA fallback with `__APP_BASE_PATH__` injection |
| `config` | ~30 env vars via godotenv; `TZ` (default `Asia/Shanghai`) force-set into `time.Local` |
| `cpa` | CPA management HTTP client + **hand-written RESP Redis client** (no go-redis dependency, strict size/depth guards) |
| `poller` | Ingestion (3-tier source strategy: Redis SUBSCRIBE → LPOP batch pull → HTTP fallback with backoff) + aggregation runner |
| `repository` | GORM/SQLite persistence, ~22 tables, ~70 ordered dated migrations, update-first upserts (portable, no `ON CONFLICT`) |
| `service` | Usage queries, pricing, identity, sync, request logs; `tokenprocessor` does per-provider token canonicalization |
| `auth` | Sessions (roles: `admin` / `api_key_viewer`), sliding-window login limiter, TOTP 2FA with replay protection and `AUTH_TOTP_RESET` lockout recovery |
| `ranking` | Community leaderboard sync (remote ranking center) + local per-API-key leaderboard (5-min cadence, pinned Asia/Shanghai) |
| `quota` | Per-provider quota refresh (worker-limited), auto-refresh schedule, header snapshots, inspection |
| `pricing` | Atomic snapshot-swapped price catalog; resolver queries only populated token columns |
| `backup` | SQLite online backup wrapper + retention cleanup |
| `overview` / `activity` / `latency` | Pure in-memory aggregation kernels shared by the runtime runner and migrations (incl. DDSketch-style latency sketches) |
| `benchmark` | Offline capacity suite (`benchctl` CLI); verified profile: 4C/2 GiB sustains ≤350 events/s, Core Dashboard p99 < 1.6 s |
| `helper`, `logging`, `timeutil`, `updatecheck`, `version` | Redaction/cost math; logrus setup + retention; project-TZ time serializers (fixed UTC strings); GitHub release check; version var |

### Key tables

`usage_events` (hot) · `usage_events_archive` (cold) · `redis_usage_inboxes` (staging) · `usage_identities` (with per-row aggregation cursors) · `error_events` · `usage_overview_hourly/daily_stats` (10-dimension rollups) · `usage_activity_stats` · `usage_latency_stats` · `usage_aggregation_checkpoints` · `cpa_api_keys` · `auth_sessions` · `app_settings` (KV: TOTP enrollment, ranking state) · `quota_cycles` / `quota_percent_segments` · `local_ranking_period_stats` · `model_price_settings` / `model_price_rules` · `schema_migrations`

### Notable ingest semantics

- **No dedup by design** — the Redis queue is a consumptive source; repeated `request_id`s are stored as independent consumption records.
- Ingest (remote) and process (local SQLite) are separate runners so neither blocks the other.
- Recent-event cache failure is **non-fatal** — realtime endpoints silently degrade to DB queries.
- Daily maintenance does conditional `VACUUM` driven by free-page ratio and available disk.

## Frontend (`web/`, React 19 + TypeScript + Vite 8)

- **No router library** — hand-rolled `history.replaceState` + a static path whitelist (`src/lib/usageNavigation.ts`); a dedicated test asserts the auth/path authorization matrix. API-key viewers are hard-pinned to `/key-overview`.
- **Admin UI:** `UsagePage` with 7 tabs — overview, analysis, ranking, request-events, auth-files, ai-provider, settings. Read-only `KeyOverviewPage` for API-key logins. Dual-tab `LoginPage` (password / CPA API key).
- **State:** Zustand (4 stores). `useUsageStatsStore` implements query-key staleness + in-flight promise dedupe/abort for the shared overview cache. `useConfigStore` and `useNotificationStore` are vestigial stubs from an earlier project.
- **API layer:** hand-written fetch client (`src/lib/api.ts`, ~70 endpoints), cookie-first sessions with a sessionStorage fallback header for CPAMC iframe embedding (`src/embed/cpamcEmbed.ts`).
- **Charts/styles:** Chart.js (tree-shaken registration); Sass CSS Modules with a `--keeper-*` design-token theme system (`light | white | dark | auto`).
- **i18n:** i18next with `en` / `zh` / `zh-TW` inline in one 2,633-line file, with key-parity tests.
- **Embedding into the binary:** Vite `base: './'` → `web/dist` → `//go:embed all:dist` in `web/static.go`; the server injects the real `APP_BASE_PATH` into `index.html` at request time.
- TOTP login flow: server returns `totp_code_required` → login page reveals a one-time-code field → `login(password, totpCode)`.

## Build, CI, Deploy

- **CI** (`.github/workflows/ci.yml`): path-filtered jobs (`backend` / `frontend` / `container`) + an always-run `ci-gate` aggregator. Go tests run on a 3-OS matrix (ubuntu / macos / windows — Windows uses MSYS2 mingw for CGO SQLite). Frontend runs test/lint/typecheck/build on Node 24. Tag (`v*`) workflows publish multi-arch Docker images (GHCR) and static linux binaries; dev-publish workflows are manual-dispatch only and refuse to run from main.
- **Dockerfile:** 3-stage (node:24-alpine → golang:1.26-alpine+CGO → alpine:3.20), non-root `app` user, `/data` volume, wget healthcheck against `/healthz`. Entrypoint creates/chowns work dirs and drops privileges via `su-exec`.
- **Local verification:** `make verify` = `go test ./cmd/... ./internal/...` + frontend `ci`/test/lint/typecheck/build — mirrors CI exactly.
- **Deployment paths:** Docker Compose (CPA+Keeper full stack or Keeper-only), Homebrew (macOS), systemd unit, Linux/Windows binaries, built-in HTTPS option.
- **Config:** `.env.example` groups ~30 variables into 8 sections (minimum required: `CPA_BASE_URL`, `CPA_MANAGEMENT_KEY`; auth on by default requiring `LOGIN_PASSWORD`).

## Design Docs & Current State

`docs/superpowers/` holds two spec→plan pairs:

| Feature | Status |
| --- | --- |
| TOTP 2FA (`2026-08-27-totp-2fa`) | **Merged** at HEAD via `feat/totp-2fa` — spec, 1,981-line 9-task plan, backend + frontend + docs all landed |
| Key-viewer local ranking (`2026-08-27-key-viewer-local-ranking`) | **Draft / not implemented** — spec is untracked, plan is committed; proposes `KEY_VIEWER_LOCAL_RANKING_ENABLED` (default off) to let API-key logins view the local leaderboard read-only by splitting GET/PATCH local-ranking routes. Nothing in `internal/`, `web/src`, or `.env.example` references it yet |

Working tree (as of exploration): clean except the untracked draft spec above and a 17 MB untracked built binary `cpa-usage-keeper` at the repo root.

## Test Layout

- **Go (311 test files):** external tests live in sibling `test/` packages (e.g. `internal/repository/test/`, `internal/api/test/`); migrations are the heaviest tested area (~33 files incl. seeded rebuild tests).
- **Frontend (143 Vitest files):** co-located `test/` dirs + `.logic.test.ts` siblings for pure helpers; 44 files opt into happy-dom. Three meta-tests read source as text to enforce route/auth invariants, embed wiring, and branding consistency.

## Quirks Worth Knowing

- Two independent hand-written RESP protocol implementations (queue client + subscriber), intentionally not shared.
- Update-first-then-insert upserts everywhere instead of SQLite `ON CONFLICT` (portability; avoids burning autoincrement IDs on the hot path).
- Project-wide fixed timezone baked into `time.Local`; local ranking pins its own `Asia/Shanghai` fixed zone regardless of `TZ`.
- Heavy inline comment discipline — nearly every statement explained (in Chinese).
