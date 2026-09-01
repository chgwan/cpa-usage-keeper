# Local Ranking for API-Key Logins — Design

Date: 2026-08-27
Status: Draft (pending review)

## Context

The question that started this: *"where can I set the API-key view scope so they
can see the ranking?"* — **there is no such setting today, and no place to put
one.** Understanding why is most of the design.

Keeper has exactly two ways to log in, and the way you logged in *is* your
permission set (`internal/auth/session.go:211-215`):

| Login | Session role | Lands on | Can see |
| --- | --- | --- | --- |
| Password (`LOGIN_PASSWORD`) | `admin` | `/` (UsagePage) | everything, incl. all ranking routes |
| An API key | `api_key_viewer` | `/key-overview` | only that key's own usage |

Three facts make "API-key view scope" a thing that does not exist:

1. **No per-key permissions anywhere.** `entities.CPAAPIKey`
   (`internal/entities/cpa_api_key.go`) has only `KeyAlias` and
   `LocalRankingAvatarID` as editable columns — both cosmetic. Keys are synced
   from CPA as bare strings (`repository.SyncCPAAPIKeys(db, keys []string, …)`),
   so there is no upstream permission to import either.
2. **Ranking is gated at the route group, not per key.** Every ranking route is
   registered on `adminProtected` (`internal/api/router.go:141-146`), and the
   role middlewares are mutually exclusive — `adminMiddleware` admits only
   `admin`, `apiKeyViewerMiddleware` only `api_key_viewer`
   (`internal/api/auth.go:131-137`). Two tests pin the current 403
   (`internal/api/test/ranking_routes_test.go:68`,
   `local_ranking_routes_test.go:47`), i.e. it is intentional, not an oversight.
3. **The word "scope" in the ranking code means something else.**
   `RankingScope` (`web/src/features/ranking/scope.ts`) is the `local |
   community` board selector persisted in `localStorage`, and
   `allowKeyOverviewRequest(token, scopes…)` in `internal/api/auth.go:355` is a
   rate-limiter bucket name. Neither is authorization.

So granting ranking access is a code change. This spec defines the smallest one
that is safe to ship.

## Goals

- An operator can opt in, with one env var, to letting **API-key logins see the
  local leaderboard**, read-only.
- API-key holders reach it where they already are: a second tab on
  `/key-overview`. No new page, no routing changes.
- Default off. Existing deployments behave exactly as today.
- Reuse the existing local-ranking backend, hook, and leaderboard UI — no new
  aggregation code, no duplicated markup, no schema migration.

## Non-goals

- **Community leaderboards for API-key logins.** They stay admin-only. The
  community board is tied to this instance's participation identity (Ed25519
  keypair, join/pause/exit actions in `internal/ranking/client.go`); exposing it
  to key holders mixes a read feature with instance-level identity.
- **Any mutating ranking action for API-key logins** — `join`, `sync`, `pause`,
  `resume`, `DELETE /ranking`, and `PATCH /ranking/local/profiles/:id` all
  remain admin-only.
- **Per-key permissions.** Considered and rejected for this change: it needs a
  new column on `CPAAPIKey`, a migration, sync-preserving logic, and an admin
  editor UI. The instance-wide flag delivers the requested outcome at a
  fraction of the surface. If genuine per-key scopes are wanted later, the
  `KeyAlias` / `LocalRankingAvatarID` columns are the pattern to follow —
  locally-owned fields that `SyncCPAAPIKeys` preserves.
- Showing an API-key holder only their *own* rank (a "your position" widget
  without other rows) — a leaderboard the viewer cannot compare against has no
  content; see Privacy.

## Decisions

| Question | Decision |
| --- | --- |
| What key holders get | Local leaderboard, read-only |
| Community boards | Unchanged, admin-only |
| Gate | `KEY_VIEWER_LOCAL_RANKING_ENABLED` env var, default `false` |
| Granularity | Instance-wide, not per key |
| Where it appears | Second tab on `/key-overview` |
| Route change | Split local routes; widen only `GET /ranking/local/leaderboards` |
| Throttling | Reuse `allowKeyOverviewRequest`, new scope `"local_ranking"` |
| Capability discovery | New `local_ranking_enabled` field on `GET /auth/session` |
| Storage | None — no migration, no new table or column |

## Backend

### The env var

`KEY_VIEWER_LOCAL_RANKING_ENABLED` (default `false`) follows the existing
`CPA_REQUEST_LOG_ACCESS_ENABLED` precedent exactly: parsed with `getBool` in
`internal/config/config.go`, carried into `api.AuthConfig` in
`internal/app/app.go:317-324`, documented in `.env.example`. It lands on
`AuthConfig` rather than `StatusRouteConfig` because both consumers — the route
group and the session payload — already hold an `AuthConfig`.

### Splitting read from write

`internal/ranking/httpapi/local_routes.go` currently mounts the read and write
route together in `RegisterLocalRoutes`. It becomes two functions —
`RegisterLocalLeaderboardRoutes` (the `GET`) and `RegisterLocalProfileRoutes`
(the `PATCH`) — with `RegisterLocalRoutes` kept as a wrapper calling both.
Handler bodies move verbatim.

`internal/api/router.go` then keeps the `PATCH` on `adminProtected` and, only
when the flag is on, mounts the `GET` on a two-role group:

```go
viewerLocalBoard := apiV1.Group("")
viewerLocalBoard.Use(authHandler.roleMiddleware(auth.RoleAdmin, auth.RoleAPIKeyViewer))
viewerLocalBoard.Use(authHandler.keyViewerRateLimitMiddleware("local_ranking"))
```

`versionProtected` (`router.go:122-123`) is the existing precedent for a
two-role group. When the flag is off, nothing moves and today's 403 stands.

### Throttling

The local leaderboard is an aggregation query over usage rows, so an API-key
holder must not be able to hammer it. `/key-overview` already solves this:
`allowKeyOverviewRequest(token, scope)` allows 1 request/second per session
token per scope bucket (`internal/api/auth.go:355-375`). A new middleware,
`keyViewerRateLimitMiddleware(scope)`, applies that same limiter — but only
when the session role is `api_key_viewer`, so admins are unaffected. On
rejection it returns `429 {"error":"ranking_rate_limited"}`; the frontend's
existing `formatError` already maps any 429 to a rate-limit message, so no new
translations are needed.

### Telling the client

An API-key session cannot call the admin-only status route, so `GET
/auth/session` gains `local_ranking_enabled` (`internal/api/auth.go:64-73,
203-228`). That single field is how the viewer UI knows whether to render the
tab.

## Frontend

### Reusing the existing board

`RankingPage.tsx` already contains a `LeaderboardCard` component that renders
the podium, table, period/metric selectors, and loading/error/empty states, and
it already branches on scope — the community profile button is gated on `scope
=== 'community'`, and the empty-state copy on `scope === 'local'`. Two small
changes make it reusable:

- export it (`function LeaderboardCard` → `export function LeaderboardCard`);
- add `allowLocalProfileEdit?: boolean` (default `true`), threaded to
  `LeaderboardEntryAvatar`, so that when `false` the avatar renders as a plain
  decorative image instead of a button. API-key holders must not see an entry
  point to the admin-only profile `PATCH`.

Exporting in place is deliberate: extracting the card would drag along
`LoadingState` / `ErrorState` / `EmptyState` / `formatError` /
`resolveScoreExplanation` and churn `RankingPage.module.scss` plus the existing
styles test, for no functional gain.

### The new panel

A new `web/src/features/ranking/LocalRankingPanel.tsx` owns `period` / `metric`
state (defaults `today` / `overall`, matching `useRankingData.ts:84-85`) and
calls the existing `useLocalRankingData` hook, which already provides caching,
60-second polling that pauses on hidden tabs, stale-board handling, and
401 → `onAuthRequired`. It is constructed with `api={{ leaderboard:
fetchLocalRankingLeaderboard }}` and **no** `updateProfile`, so the panel
structurally cannot issue an admin call.

### The tab

`KeyOverviewPage.tsx` already renders a single-item tab bar (`styles.tabBar`,
`key_overview.tabs_aria_label`) — the scaffold is there. It gains a
`localRankingEnabled` prop (threaded from `App.tsx`, where the session response
already lands in one place) and an `activeTab` state. The second pill renders
only when the flag is on, labelled with the existing `usage_stats.tab_ranking`
key — already translated in all three locales (en, zh, zh-TW). On the ranking
tab the time-range control and refresh button hide (they only drive the
overview query), and the panel is passed `enabled={activeTab === 'ranking'}` so
it does not poll while hidden — the same pattern as `UsagePage.tsx:881-896`.

No new i18n keys: `ranking.*` already covers every string the card renders.

## Privacy

This is the real trade-off, and the reason for the default-off flag.

The local board is a leaderboard **of API keys**: each row is one key, labelled
by its alias or masked key (`helper.CPAAPIKeyDisplayName`), with its metric
value — top 100 (`localRankingTopLimit`), across `overall`, `total_tokens`,
`request_count`, `cache_read_rate`, `ttft_average`, `latency_average`,
`peak_tpm`, `peak_rpm`. Letting a key holder see it means letting them see
*every other key's* alias and usage volume.

That is inherent to the request — a ranking with the other rows removed is not
a ranking — so the design accepts it and makes it an explicit operator choice:
off unless enabled, and `.env.example` states the consequence in the comment.
Nothing about the *key material* is exposed; `DisplayKey` is already masked
wherever it surfaces.

## Error handling & edge cases

- Flag off → unchanged 403 for API-key sessions on every ranking route.
- `AUTH_ENABLED=false` → `roleMiddleware` short-circuits and everything is open
  already; the flag is irrelevant in that mode.
- Second request inside one second from an API-key session → `429` with the
  existing rate-limit UI copy. Admin requests are never throttled.
- `PATCH /ranking/local/profiles/:id` from an API-key session → `403`, with the
  UI affordance removed as well (defense in depth, not either/or).
- Local ranking provider absent (`localRankingProvider == nil`) → nothing is
  mounted, tab hidden, unchanged `503` semantics if reached.
- Session expires while the panel polls → hook's 401 path calls
  `onAuthRequired`, the existing logout flow.

## Testing

Go:

- `internal/api/test/local_ranking_routes_test.go` — keep
  `TestLocalRankingRouteIsAdminOnly` unchanged (flag off, viewer → 403); add a
  flag-on case: viewer `GET` → 200, viewer `PATCH` → 403, immediate second
  viewer `GET` → 429.
- `internal/api/test/ranking_routes_test.go` — assert community routes stay
  admin-only *with the flag on*.
- Config test (pattern of `internal/config/test/config_cleanup_test.go:32-48`)
  — default false, `=true` parses true.
- Auth session test — `local_ranking_enabled` mirrors config.

Frontend (Vitest, injected-`api` style of `useLocalRankingData.test.tsx`):

- Ranking tab absent without the flag, present with it.
- `LocalRankingPanel` issues only the local leaderboard fetch, and renders no
  profile-edit affordance.

Full gate: `make verify` (`go test ./cmd/... ./internal/...` plus web test /
lint / typecheck / build).

Manual:

1. No new env var, API-key login → Overview tab only; `curl` the local
   leaderboard with the viewer cookie → 403.
2. `KEY_VIEWER_LOCAL_RANKING_ENABLED=true`, restart → Ranking tab appears,
   board loads and refreshes, avatars are not clickable, `PATCH` still 403.
3. Admin login unchanged: both boards, avatar edit still works.

## Related

- Implementation plan: `docs/superpowers/plans/2026-08-27-key-viewer-local-ranking.md`
