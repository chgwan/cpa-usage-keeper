# Fork improvements vs upstream — 2026-09-21 (updated 2026-10-08)

Baseline: local `dev` @ `4ce068f` compared against `origin/main` @ `2c09d65`
(`Willxup/cpa-usage-keeper`). Deployed to TxNj as `v1.15.10-15-g4ce068fd`.

- **10 commits ahead, 0 behind** — fully synced with upstream as of 2026-10-08.
  The fork history was reorganised into one commit per feature on 2026-10-08.
- 133 files changed, ~10,700 insertions, ~900 deletions.
- Every feature below ships with tests; the full `go test ./...` and web suites
  pass (1444 web tests).

---

## 1. Admin two-factor authentication (TOTP)

Adds a second factor to admin login, stored in `app_settings` rather than a new table.

- `internal/auth/totp.go` — TOTP manager: pending enrollment with a 10-minute TTL,
  confirm, verify with ±1 step clock tolerance, and replay rejection via `LastStep`.
- Routes: `GET /auth/totp`, `POST /auth/totp/setup`, `/auth/totp/confirm`, `/auth/totp/disable`.
- Login requires `totp_code` once enrolled; failed codes consume the per-source
  login budget so code guessing is throttled like password guessing.
- `AUTH_TOTP_RESET=true` clears enrollment at startup for an admin who lost their
  authenticator; setup is refused while the flag is still set.
- Web: two-factor settings card with QR enrollment, and a login field that appears
  only when the server asks for a code.

## 2. API key lifecycle management

Full create/regenerate/delete/enable/disable for CPA API keys from the Keeper UI.

- `internal/service/cpa_api_keys_management.go` — lifecycle service with write
  serialization; `internal/cpa/` gains the API-key write methods against CPA.
- Routes: `POST /usage/api-keys`, `DELETE|PATCH /usage/api-keys/:id`,
  `/regenerate`, `/disable`, `/restore`, `/policy`, `/enforcement-logs`.
- Metadata sync keeps policy-disabled keys from being resurrected, and a
  last-key guard prevents locking yourself out of CPA.
- Web: single-column key list with inline actions and an edit modal.
- **Key list sorting.** Status and A–Z toggles in the card header; each click
  cycles off → ascending → descending. Status order is active → manually
  disabled → disabled by quota. With both on, status groups the keys and the
  alias (or masked key) orders them within each group. Both choices persist in
  `localStorage`.
- **Expanded view.** A ⤢ button in the card's top-right corner opens the card as
  a near-full-screen overlay through a portal on `document.body`. Esc, ⤡ or a
  backdrop click closes it, unsaved alias drafts survive the switch, and the
  create / reveal / confirm dialogs still stack above it.

## 3. Per-key cost quota enforcement

A quota engine that disables a key automatically when it burns through its budget.

- `internal/keypolicy/` — limit types and windows (`policy.go`), window boundaries
  in project-local time (`window.go`), breach evaluation (`evaluate.go`),
  per-window usage with pricing applied (`store.go`), and an enforcement runner
  that auto-disables on breach and restores at window rollover (`runner.go`).
- Tables: `cpa_api_key_policies` + `api_key_enforcement_logs`, with dated
  migrations `20260831_create_api_key_policies` and `20260920_cost_only_api_key_limits`.
- **Cost-only limits over daily / weekly / monthly windows.** Token and request
  limits were removed; the migration drops legacy dimensions from stored policies.
  Weekly windows start Monday 00:00 local and use ISO week keys so daily, weekly,
  and monthly keys can never collide.
- Key states are `active` / `disabled_by_quota` / `disabled_manual`, so a manual
  disable is never undone by an automatic restore.
- Web: quota policy modal with progress bars and an audit log of policy changes.

## 4. API-key viewer scope

- New `GET /key-overview/quota` and a **Cost Quota panel** on the viewer's Overview
  page showing daily, weekly, and monthly spend against limits, with a
  "Disabled (quota)" badge when enforcement has fired.
- Local ranking access removed for the api-key viewer role (`KeyRankingPage`
  deleted, viewer navigation narrowed) — viewers no longer see cross-key data.

## 5. Local leaderboards

- Cost dimension added to local leaderboards (`internal/ranking/local_cost.go`),
  alongside the existing token and request dimensions.
- **This Week and Last Week periods.** Weeks start Monday 00:00 in the ranking
  timezone (Asia/Shanghai) and use ISO week keys (`2026-W41`). Week boards are
  aggregated from `usage_events` on demand instead of from the day/month
  snapshot table, so no migration was needed; unlike the month boards they also
  count usage from before local ranking was enabled. The periods are local-only:
  the Community protocol and its period validation are unchanged.
- **Ranking tab is local-only.** The Local/Community switch is gone and the
  Community leaderboard (which ranks this Keeper against other instances through
  an external service) is no longer requested. The change is confined to
  `UsagePage.tsx` so the shared ranking feature still matches upstream.
- **Sortable leaderboard table.** Score, Token, Requests, Cache rate, TTFT,
  Latency, TPM, RPM and Cost headers sort on click: descending, ascending, then
  back to leaderboard order. Rank badges keep each key's leaderboard position and
  the podium is untouched, so a sort never reads as a new ranking; rows missing
  the sorted value stay last; headers expose `aria-sort`.

## 6. Claude Fable weekly quota row

Anthropic reports the Fable weekly limit in the usage payload's structured
`limits` array, and historically in a flat field under the internal codename
`iguana_necktie`. Keeper used neither: `limits` was ignored and the codename
rendered as "Iguana Necktie".

- `limits` entries are now parsed (`kind`, `scope.model.display_name`, `percent`,
  `resets_at`, `is_active`) and a weekly-scoped Fable entry becomes a
  `seven_day_fable` row, preferring the active entry.
- Falls back to relabelling `iguana_necktie` when the array has no usable entry.
- The label follows the upstream model name so a future version number carries
  through on its own.

## 7. Security hardening

From the 2026-09-20 security review. Risks 1–4 are closed:

| Risk | Fix |
|---|---|
| Go 1.26.0 TLS 1.3 KeyUpdate DoS advisories (GO-2026-4870, GO-2026-6090) | Rebuilt on Go 1.27.1; `x/net` 0.25.0→0.59.0, `x/text` 0.20.0→0.42.0, `x/crypto` 0.23.0→0.57.0, `x/sys` 0.20.0→0.48.0. `govulncheck` reports zero vulnerabilities. |
| Anonymous request could stall the server while it drained an incomplete body | 30s read timeout and 180s write deadline on the listener, plus a 1 MiB body cap on every route including anonymous and rejection paths. Verified live: the connection is released at 30s instead of hanging indefinitely. |
| Invented session cookies caused unauthenticated database DELETEs | Unknown tokens are no longer deleted; malformed tokens are rejected without a store query; unknown-token lookups draw on a per-source budget (30/min) separate from the login limiter, returning 429 with `Retry-After`. Live sessions resolve from cache and never spend budget. |
| TOTP enrollment failed open on storage errors | Storage errors are now distinct from "not enrolled"; login refuses to proceed when the second factor cannot be evaluated. |

Still open from that review: items 5–16 (TOTP replacement requires only an admin
cookie, non-atomic replay prevention, no throttle on `/auth/totp/disable`, no
session revocation on credential recovery, shared global auth budget, systemd
containment, backup secrets, cert renewal reload, browser headers, dev-dependency
advisories).

## 8. Operations

- Project timezone set to `Asia/Singapore` via local `.env` (not committed).
- Deployment: build with embedded `web/dist`, scp to `wan@TxNj:services/cpa-usage-keeper/`,
  `systemctl --user restart cpa-usage-keeper`, health check on
  `https://127.0.0.1:58809/healthz`. A `.bak` binary is always kept for rollback.
- Release binaries are scanned before deploy: no production `LOGIN_PASSWORD` or
  `CPA_MANAGEMENT_KEY`, local `.env` values, or fake test credentials appear in
  the executable.
- Local fake-data harness in `.fake-run/ranking/` (untracked): a stub CPA, a seed
  script with six fake API keys and ~72K usage events, and a throwaway database,
  for testing the leaderboard UI on port 58808 without touching production.

---

## Divergence note

Upstream was merged on 2026-09-21 (`827c9b6`), bringing in Meta/Devin executor
token normalization and branding, CPA stream/status-code persistence, the model
usage treemap, and a quota-reset timezone fix. The two conflicts were both in the
migration registry and were resolved in date order; both upstream migrations
(`20260918_usage_event_response_model`, `20260919_usage_event_stream_status_code`)
applied cleanly in production.

Upstream was merged again on 2026-09-23 (`7f3fb3a`, 224 commits: UI control
alignment, quota error styling, empty parent-session normalization, and a large
test reorganisation into `test/` packages with shared fixtures).

- Fork-only tests (admin TOTP, API key quota, local ranking cost) were ported into
  upstream's new test layout rather than reviving the files upstream deleted.
- Upstream re-added viewer local ranking behind `API_KEY_VIEWER_LOCAL_RANKING_ENABLED`
  (default off) and tested a two-column API key list. This fork keeps viewer ranking
  removed and the single-column list; the conflicting upstream tests were dropped or
  rewritten to guard the fork's behaviour.
- Migrations stay in date order: `20260920_cost_only_api_key_limits` then upstream's
  `20260922_normalize_usage_event_parent_session_null`, which applied cleanly in
  production.
