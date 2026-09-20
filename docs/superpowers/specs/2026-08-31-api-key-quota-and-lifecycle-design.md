# API-Key Lifecycle Management and Usage Quota Enforcement — Design

Date: 2026-08-31
Status: Approved (brainstorming session)

## Context

Keeper currently treats CPA API keys as read-only metadata. The metadata
sync runner pulls `GET /v0/management/api-keys` and upserts the local
`cpa_api_keys` table (`internal/service/metadata_management_api_keys.go`
→ `repository.SyncCPAAPIKeys`); the only local mutation is the key alias
(`PATCH /usage/api-keys/:id` in `internal/api/cpa_api_keys.go`).

The user's requirements (`docs/add-api-quota.md`):

1. Configure usage limits per API key (and per-key model control).
2. Regenerate and modify API keys, using CPA's native management API.

Investigation of upstream CPA (v6 docs) established the ground rules:

- CPA's `api-keys` config is a flat list of strings — **no per-key
  metadata exists upstream**, so limits cannot be delegated to CPA.
- CPA's native key management endpoints are sufficient for requirement 2
  (exact shapes verified against the docs):
  - `PUT /v0/management/api-keys` — replace all: body `["k1","k2"]` or
    `{"items":[…]}`
  - `PATCH /v0/management/api-keys` — replace one: `{"old":"…","new":"…"}`
    (or `{"index":n,"value":"…"}`)
  - `DELETE /v0/management/api-keys?value=…` (or `?index=n`)
- Keeper is **not in the request path** — usage events arrive from CPA's
  Redis queue seconds after a request completes. Enforcement can therefore
  only be reactive: detect a breach, then remove the key from CPA so all
  subsequent requests fail.

**Dropped during brainstorming (user decision):** per-key model control.
A whitelist can only be checked after the request already succeeded, which
does not meet the requirement ("无法阻断"). True request-time blocking
would require a CPA v6 access-provider SDK module (compile-time Go plugin
inside CPA) — recorded as future work, not part of this spec.

## Goals

- Create, regenerate (alias-preserving), and delete CPA API keys from the
  Keeper UI, using only CPA's native management endpoints.
- Per-key quota policy: limits on **requests / tokens / cost** over
  **today / this-month** windows (project-TZ local calendar, same
  convention as local ranking).
- Automatic enforcement: when any configured limit is breached, remove the
  key from CPA within seconds (event-driven evaluation plus a 1-minute
  fallback tick); when the window resets, automatically re-add the key.
- Full audit trail of every enforcement action, visible in the UI.
- Quota progress (used vs limit) visible per key in the settings list.

## Non-goals

- Model whitelist / model control — dropped (cannot block at request
  time).
- Request-time blocking of any kind (CPA access-provider module) —
  possible future upgrade; the policy schema stays simple on purpose.
- Rolling or hourly windows (only today / this-month).
- Notifications (webhook/email) on breach — the enforcement log and UI
  badge are the v1 surface.
- Merging historical usage of an old key string into its regenerated
  successor (user-confirmed: history stays under the old key string).
- Changes to the api-key viewer role or `POST /auth/api-key-login`. A key
  removed from CPA is marked deleted by sync, so viewer login with it
  stops working — accepted consequence.

## Decisions

| Question | Decision |
| --- | --- |
| Enforcement semantics | Reactive auto-disable: CPA `DELETE ?value=<key>` on breach |
| Limit dimensions | requests / tokens / cost, any subset per key; tokens = `SUM(total_tokens)` over the window |
| Windows | today / this-month, project TZ local calendar |
| Restore | Automatic on window flip; manual button also available |
| Key lifecycle ops | CPA native: PUT (create/restore), PATCH old→new (regenerate), DELETE (disable/delete) |
| New key format | `sk-` + 43 chars base64url from crypto/rand (or admin-supplied custom value), uniqueness-checked against current list |
| Policy storage | New 1:1 table `cpa_api_key_policies` |
| Audit | New table `api_key_enforcement_logs` |
| Last-key guard | Never auto-disable the only key present in CPA |
| Old-key history | Stays under the old key string; no merge |

## Backend

### CPA client additions (`internal/cpa/client.go`)

Three write methods next to `FetchManagementAPIKeys`, reusing
`doManagementJSONRequest` conventions and returning the same
status/body-wrapped result style:

- `ReplaceManagementAPIKeys(ctx, keys []string)` — `PUT`, body
  `{"items":[…]}`.
- `UpdateManagementAPIKey(ctx, oldKey, newKey string)` — `PATCH`, body
  `{"old":…,"new":…}`.
- `DeleteManagementAPIKey(ctx, key string)` — `DELETE ?value=<key>`.

All Keeper-side key mutations (create/regenerate/delete/restore and
enforcement actions) are serialized through a single mutex in the
management service so Keeper never races itself on the replace-all PUT.

### Entities and migration (`internal/entities`, `internal/repository/migration`)

```text
cpa_api_key_policies
  id                PK
  cpa_api_key_id    uniqueIndex, FK → cpa_api_keys.id
  limits            JSON  -- [{"type":"requests|tokens|cost",
                           --    "window":"daily|monthly","value":number}]
  enabled           bool  -- policy switch, default true when created
  admin_disabled    bool  -- manual disable blocks auto-restore
  enforcement_state text  -- "active" | "disabled_by_quota" | "disabled_manual"
  last_evaluated_at time
  created_at/updated_at   -- storageTime serializer (project convention)

api_key_enforcement_logs
  id                PK
  cpa_api_key_id    index
  action            text  -- disabled | restored | skipped_last_key | failed
  reason            text  -- limit_breached | window_reset | policy_updated | admin_action | retry
  limit_type        text? -- breached limit dimension, when applicable
  window            text? -- daily | monthly
  used_value        real? -- usage snapshot at decision time
  limit_value       real?
  detail            text  -- error text for action=failed
  created_at        time
```

Dated migration creating both tables, following the existing migration
conventions; fresh databases receive them via `AutoMigrate(entities.All())`.

Validation on save: `value > 0`, at most one limit per (type, window)
combination, unknown types/windows rejected.

### Key management service (extend `internal/service/cpa_api_keys_service.go`)

- `CreateKey(ctx, alias, customKey?)` — generate (or take) the new key,
  `GET` current list → append → `PUT` back → trigger an immediate metadata
  sync so the local table gains the row → return the full key value **once**.
- `RegenerateKey(ctx, id)` — requires the key to exist in CPA and be
  currently active (see edge cases). Generate a new value → CPA
  `PATCH {"old","new"}` → locally update `cpa_api_keys.api_key` in place
  (alias, policy row, and ranking avatar survive via the unchanged FK) →
  return the new value once.
- `DeleteKey(ctx, id)` — CPA `DELETE ?value=` → trigger sync (which marks
  the row `IsDeleted` and archives the alias) → delete the attached policy
  row.
- `DisableKey(ctx, id)` / `RestoreKey(ctx, id)` — manual variants:
  CPA `DELETE` / `PUT` re-add, set `enforcement_state`, write audit rows
  with `reason=admin_action`. `admin_disabled=true` blocks the runner's
  auto-restore until the admin restores manually.

### Enforcement runner (new `internal/quota` sibling: `internal/keypolicy`)

A background runner wired in `internal/app/app.go` alongside the existing
runners (own goroutine, cancellable context, reverse-order close):

- **Wake sources:** notified after each usage-aggregation pass (same
  notifier pattern the sync service already uses) plus a 1-minute ticker
  fallback.
- **Usage query:** per limited key, aggregate the current today and
  this-month windows from `usage_events` grouped by
  `(TRIM(api_group_key), model)` joined to `cpa_api_keys` — the same SQL
  pattern local ranking uses (`internal/ranking/local_service.go`), with
  an extra `model` grouping so cost can be priced.
  requests = row count, tokens = `SUM(total_tokens)` summed over models;
  cost is computed in Go from the per-model token sums via the pricing
  catalog snapshot (models without pricing contribute 0, consistent with
  the dashboard). Raw events cover ≥90 days, so this-month is always
  fully contained.
- **Evaluation loop (reconcile to desired state):**
  1. Compute usage for every key with an enabled policy.
  2. Breached = any configured limit where `used >= limit`.
  3. Desired state: breached → key absent from CPA; otherwise → present.
  4. Converge: if breached and still listed → **last-key guard**: if this
     is the only key in CPA, log `skipped_last_key` (Warn) and do not
     disable; else CPA `DELETE`, set `disabled_by_quota`, write audit row
     with the usage snapshot.
  5. If not breached and `disabled_by_quota` → CPA re-add (`GET` list →
     append → `PUT`), set `active`, audit `restored` (`window_reset` or
     `policy_updated`).
  6. `admin_disabled` keys are never auto-restored.
- **Failure handling:** CPA call errors leave the state untouched so the
  next tick retries; each failure writes an audit row with `action=failed`
  and the error text. Enforcement never blocks or fails the ingest path.
- Enforcement state lives in SQLite, so decisions survive restarts.

### API routes (`internal/api/cpa_api_keys.go`, admin-protected group)

| Route | Behavior |
| --- | --- |
| `POST /usage/api-keys` | body `{alias?, key?}` → `{id, key}` — full value returned once |
| `POST /usage/api-keys/:id/regenerate` | → `{id, key}` — new value returned once |
| `DELETE /usage/api-keys/:id` | → 204; removes from CPA |
| `POST /usage/api-keys/:id/disable` | manual disable → 204 |
| `POST /usage/api-keys/:id/restore` | manual restore → 204 |
| `GET /usage/api-keys/:id/policy` | policy + live usage vs each limit |
| `PUT /usage/api-keys/:id/policy` | body `{limits, enabled}` → upsert (validated) |
| `GET /usage/api-keys/:id/enforcement-logs?limit=` | recent audit rows |

`GET /usage/api-keys` list payload gains per-key fields:
`policy_enabled`, `enforcement_state`, and the tightest limit's
`{used, limit, window, type}` for the progress bar. Key values remain
redacted everywhere except the create/regenerate responses.

### Wiring (`internal/app/app.go`)

Build the policy repository, management service (with CPA client), and
enforcement runner after the CPA client and session manager; start the
runner goroutine in `App.Run()`; close it in reverse order.

## Frontend

### API client (`web/src/lib/api.ts`, types in `lib/types.ts`)

New functions mirroring the routes above (`createAPIKey`,
`regenerateAPIKey`, `deleteAPIKey`, `disableAPIKey`, `restoreAPIKey`,
`fetchAPIKeyPolicy`, `saveAPIKeyPolicy`, `fetchAPIKeyEnforcementLogs`)
plus the new response types.

### UI (`web/src/components/usage/ApiKeySettingsCard.tsx`)

- **Row actions** per key: 编辑别名 (existing), 重新生成 (confirm modal:
  old key stops working immediately), 禁用/恢复, 删除 (double confirm),
  配额 (opens policy modal).
- **新建 API Key** button → modal with optional alias and optional custom
  key value → success modal shows the full key **once** with a copy
  button and a warning it will not be shown again.
- **Policy modal:** enabled toggle + six numeric inputs (requests / tokens
  / cost × 今日 / 本月; blank = unlimited) + live current usage next to
  each filled input; validation errors inline.
- **List columns/badges:** quota progress bar for the tightest limit
  (red when ≥100%), badge for `已禁用（超限）` / `手动禁用` state,
  restore button on disabled rows.
- **Enforcement log:** expandable per-key section (or modal) listing
  recent audit rows (action, reason, snapshot, time).
- i18n keys added under `usage_stats.api_keys.*` in all three locales
  (en / zh / zh-TW), following existing card conventions.

## Error handling & edge cases

- CPA write failures surface as 502 with the underlying error to the UI;
  the runner retries on its next tick (state unchanged in between).
- Read-modify-PUT races with an external admin editing CPA concurrently:
  accepted for a single-admin tool; the next metadata sync reconciles
  local truth, and enforcement always re-reads the live list before
  acting.
- Regenerating a key that is currently `disabled_by_quota` or
  `disabled_manual` is rejected (400) — the old key is not in CPA, so
  PATCH has nothing to match; the UI disables the action on such rows.
- Cost limits depend on pricing configuration; unpriced models count as
  0 cost (documented in the README).
- Window boundaries follow the project TZ (`Asia/Shanghai` default),
  identical to local-ranking period keys.
- Last active key is never auto-disabled (guard above), so a mis-set
  limit cannot lock every client out.
- Viewer logins with a disabled/deleted key fail naturally once sync
  marks the key deleted.
- Policy rows attached to deleted keys are removed on delete; orphaned
  policies are ignored by the runner.

## Testing

Go (existing patterns — table tests, temp SQLite, httptest fake CPA):

- `internal/cpa` client tests: PUT/PATCH/DELETE request shapes and error
  mapping against an httptest server.
- Service tests: create (append + PUT + sync trigger), regenerate
  (PATCH + local in-place update, alias preserved), delete, manual
  disable/restore, serialization of concurrent mutations.
- Enforcement evaluator tests: breach detection per dimension; multiple
  limits (any-breach wins); window flip auto-restore; last-key guard;
  `admin_disabled` blocks auto-restore; unpriced-model cost = 0;
  reconcile retries after CPA failure.
- Handler tests: route auth (admin-only), validation errors, one-time key
  disclosure only on create/regenerate, redacted list.
- Migration test: new tables on fresh and existing databases.

Frontend (Vitest, existing `.logic.test` patterns):

- Policy modal validation (positive numbers, dedupe), progress
  computation, one-time key modal state machine, regenerate confirm flow.

Manual local verification (the user):

1. Create a key in the UI → appears in CPA config; login with it works.
2. Set a 3-request daily limit → 4th request fails within seconds; badge
   shows 超限禁用; next day (or lowered usage) the key returns.
3. Regenerate → alias and quota policy survive, old key stops working,
   new key works; history stays under the old string.
4. Try to disable the last key via quota → skipped with a warning.

## Security notes

- Full key values are disclosed exactly once (create/regenerate
  responses); every other endpoint keeps the existing redaction.
- All new routes sit behind the admin session middleware.
- Enforcement depends on the CPA management key's write access; failures
  are visible in logs and the audit trail, never silent.

## Documentation

- `README.md` + `README.zh.md`: new "API Key Management" subsection —
  create/regenerate/delete semantics, quota policy (dimensions, windows,
  TZ), auto-disable/restore behavior, last-key guard, cost-requires-
  pricing caveat, and the dropped model-control rationale.
- No new environment variables.

## Future work (out of scope)

- CPA access-provider SDK module for true request-time enforcement
  (blocking, not reactive) — the policy table would remain the source of
  truth.
- Usage history merge across regenerated keys.
- Breach notifications (webhook/email).
