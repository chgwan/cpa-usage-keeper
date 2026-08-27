# TOTP Two-Factor Authentication for Admin Login — Design

Date: 2026-08-27
Status: Approved (brainstorming session)

## Context

CPA Usage Keeper protects its dashboard with a single admin password
(`LOGIN_PASSWORD`, constant-time compared in `internal/api/auth.go`) that
creates a session on success. There is no second factor. The user asked for
"2FA and HTTPS support"; HTTPS already exists (`TLS_ENABLED` +
`TLS_CERT_FILE`/`TLS_KEY_FILE` → `ListenAndServeTLS` in `internal/app/app.go`),
so this spec covers only the 2FA half.

## Goals

- Admin login requires a TOTP code from an authenticator app once 2FA is
  enrolled (Google Authenticator, 1Password, Authy, etc. all work).
- Opt-in enrollment through the existing Settings UI: scan a QR code, confirm
  one code, done. Deployments that never opt in see zero change.
- Operator-side lockout recovery without recovery codes: the server operator
  clears enrollment with an env var + restart.

## Non-goals

- Recovery codes — dropped by explicit decision; the operator has server
  access (`AUTH_TOTP_RESET` covers lockout).
- WebAuthn/passkeys, email OTP.
- Changes to `POST /auth/api-key-login` (API-key viewer / CPAMC embed path).
  That login is a bearer credential; TOTP would break embed flows.
- Encrypting the TOTP secret at rest (see Security notes).
- HTTPS work of any kind (already shipped).

## Decisions

| Question | Decision |
| --- | --- |
| Method | TOTP only (RFC 6238), standard parameters |
| Enablement | Opt-in via Settings (QR → confirm code) |
| Lockout reset | `AUTH_TOTP_RESET=true` + restart clears enrollment at startup |
| Library | `github.com/pquerna/otp` (pure Go, no transitive deps) |
| State | Existing `app_settings` KV table — no new migration |
| Login flow | Single `POST /auth/login` endpoint, optional `totp_code` field |
| QR rendering | Client-side, `qrcode.react` npm package |

## Backend

### Configuration (`internal/config/config.go`)

- New field `AuthTOTPReset bool`, parsed from env `AUTH_TOTP_RESET`
  (default `false`), alongside the existing `AUTH_*` variables.
- No additional validation.

### State (`app_settings`, via `internal/repository/app_settings.go`)

Two keys, both stored with `value_type = "json"`:

- `auth.totp` — active enrollment:
  `{"secret": "<base32>", "enabled_at": "<RFC3339>", "last_step": <int64>}`
- `auth.totp.pending` — enrollment in progress:
  `{"secret": "<base32>", "created_at": "<RFC3339>"}`

No schema migration; `app_settings` already exists.

### TOTP manager (`internal/auth/totp.go`, new)

`TOTPManager` holds `*gorm.DB` and an injectable `now func() time.Time`
(for tests). Methods:

- `Enrolled(ctx) bool`
- `CreatePending(ctx) (otpauthURI string, secret string, err error)` —
  generates a new secret (`totp.Generate` with issuer `CPA Usage Keeper`,
  account `admin`, SHA-1, 6 digits, 30 s period) and stores it as pending,
  overwriting any stale pending secret.
- `ConfirmPending(ctx, code string) (bool, error)` — validates `code`
  against the pending secret; on success atomically promotes pending →
  active (`last_step` initialized from the matched timestep) and deletes the
  pending key. Fails if pending is missing or older than 10 minutes.
- `Verify(ctx, code string) (bool, error)` — validates against the active
  secret with ±1 step skew, implemented by regenerating the code at
  `now + {-30s, 0, +30s}` (`totp.GenerateCodeCustom`) with constant-time
  comparison; on a match, records the highest matched timestep
  (`floor(unix/30)`) and rejects any later code whose highest matching step
  is ≤ the stored `last_step` (replay guard). Persists the new `last_step`.
- `Disable(ctx) error` — deletes both keys.
- `ResetAll(ctx) error` — deletes both keys (used by startup reset; same as
  `Disable`).

### Login flow (`internal/api/auth.go`)

`loginRequest` gains `TOTPCode string \`json:"totp_code,omitempty"\``.
Order of checks in `login`:

1. Rate-limit preflight (`allowLoginAttempt`) — unchanged.
2. Password constant-time compare — unchanged failure path.
3. If enrolled: `code = strings.TrimSpace(request.TOTPCode)`
   - empty → `401 {"error":"totp_code_required"}`
   - `Verify` false → `401 {"error":"invalid totp code"}`
4. `loginAttempts.Reset(clientKey)` and session creation happen only after
   full success, so failed code attempts count against the existing
   5/min-per-source login budget.

The `totp_code_required` error string is an API contract consumed by the
frontend. Distinct TOTP errors are only reachable after a correct password,
so enrollment status is not leaked to strangers.

### Enrollment API (`internal/api/auth.go`, behind `adminMiddleware`)

Routes registered in `authHandler.registerRoutes` alongside the existing
auth routes (final paths under `/auth/totp…`):

- `GET /auth/totp` → `{"enabled": bool, "pending": bool}`
- `POST /auth/totp/setup` → `{"otpauth_uri": string, "secret": string}`
  (base32 secret shown for manual entry). Returns `409` when
  `AUTH_TOTP_RESET` is set, so a forgotten reset var cannot wipe a fresh
  enrollment on the next restart.
- `POST /auth/totp/confirm` `{code}` → success `204`; wrong code `401`;
  missing/expired pending `400`. Attempts pass through the same
  `LoginAttemptLimiter` with key `totp-confirm:<client IP>`.
- `POST /auth/totp/disable` `{password, code}` → requires constant-time
  password match **and** a valid TOTP code, then `Disable()`. Failure `401`.

### Wiring (`internal/app/app.go`)

- Build `TOTPManager` from the app DB; pass it plus `AuthConfig.TOTPReset`
  into `NewAuthHandler`.
- At startup, when `cfg.AuthTOTPReset` is set: call `ResetAll` and log a
  `logrus.Warn`: enrollment cleared, remove `AUTH_TOTP_RESET` and restart.

## Frontend

### API client (`web/src/lib/api.ts`)

- `login(password: string, totpCode?: string)` — sends `totp_code` when
  present.
- Exported sentinel `TOTP_CODE_REQUIRED_ERROR = 'totp_code_required'`; the
  error parser maps a server error body equal to that string onto it, so
  callers can distinguish "need a code" from "wrong password".
- New functions: `fetchTOTPStatus()`, `setupTOTP()`, `confirmTOTP(code)`,
  `disableTOTP(password, code)`.

### Login page (`web/src/pages/LoginPage.tsx`, `web/src/App.tsx`)

- `LoginPage` adds `totpCode` state and a code input (numeric,
  `autocomplete="one-time-code"`) rendered only when
  `adminError === TOTP_CODE_REQUIRED_ERROR`; the field stays visible for
  subsequent wrong-code errors.
- `onPasswordSubmit(password, totpCode?)` signature update;
  `App.tsx`'s `handlePasswordLogin` forwards both.
- Submit is enabled with a non-empty password; when the code field is
  visible a non-empty code is also required.
- API-key tab unchanged.

### Settings UI (new `web/src/components/usage/TOTPSettingsCard.tsx`)

Placed in the same settings section as `SessionSettingsCard` on the usage
page. State machine:

- **off** — "Enable 2FA" button → `setupTOTP()`.
- **pending** — modal with a `qrcode.react` `QRCodeSVG` rendering the
  `otpauth_uri`, the base32 secret for manual entry, and a confirm-code
  input → `confirmTOTP(code)`. Wrong code shows an inline error.
- **on** — status row "Enabled" with a "Disable" button → modal asking for
  password + current code → `disableTOTP(password, code)`.

Follows `SessionSettingsCard` conventions: `Card`/`Button`/`Modal`
components, styles in `UsagePage.module.scss`, i18n via `useTranslation`.

### i18n (`web/src/i18n/index.ts`)

New keys under `usage_stats.totp_*` added to all three resource trees
(en, zh, zh-TW).

### Dependency

`qrcode.react` (^4) added to `web/package.json`.

## Documentation

- `.env.example`: `AUTH_TOTP_RESET` entry with bilingual comment matching
  file style.
- `README.md` + `README.zh.md`: Login Protection section gains a "Two-factor
  authentication" subsection — how to enroll, how login changes, the
  `AUTH_TOTP_RESET` recovery procedure (set → restart → log in with password
  only → remove the var → restart), and a server-clock note (codes fail if
  drift exceeds ~±90 s). Security notes updated: TOTP secret is stored
  unencrypted in SQLite alongside other data.

## Error handling & edge cases

- `AUTH_TOTP_RESET` set + `setup` called → `409` (guard against silent wipe
  after re-enrollment).
- Pending secret older than 10 minutes → `confirm` returns `400`; a fresh
  `setup` overwrites it.
- Code reuse within the skew window → rejected via `last_step`.
- Enabling/disabling 2FA does **not** revoke existing admin sessions (they
  were password-authenticated; the sessions settings card can revoke them
  manually).
- Wrong TOTP code at login counts against the per-source login rate limit
  (no `Reset` until password + code both pass).
- Server clock drift beyond step + skew → all codes rejected; documented,
  not auto-corrected.

## Testing

Go (existing patterns — table tests, temp SQLite via `internal/auth/test`):

- `internal/auth/totp_test.go`: pending create/confirm/promote; wrong code;
  pending expiry; replay rejection (same code twice); skew boundary accept;
  `ResetAll`.
- `internal/api/auth_test.go`: enrolled login without code →
  `401 totp_code_required`; with correct code → session created; wrong code
  → `401` and rate-limit budget consumed; `api-key-login` unaffected;
  full setup → confirm → disable cycle; `409` while reset var set.
- `internal/config/config_test.go`: `AUTH_TOTP_RESET` default false, parses
  true.

Frontend (Vitest logic tests, existing patterns):

- `LoginPage.logic.test.ts`: code field reveal on sentinel error; submit
  payload includes `totp_code`.
- New `TOTPSettingsCard` logic test: off → pending → on transitions and
  disable-form validation.

Manual local verification (the user):

1. Run Keeper, log in with password, Settings → enable 2FA, scan QR,
   confirm code.
2. Log out; login now demands password + code; wrong code rejected.
3. Recovery drill: set `AUTH_TOTP_RESET=true`, restart, log in with password
   only, remove the var, restart, re-enroll.

## Security notes

- The TOTP secret lives unencrypted in SQLite. The README already states the
  DB and its backups contain original data; the TOTP entry is consistent
  with that stance. An attacker with DB read access has worse problems
  (usage data, credential metadata).
- TOTP parameters are fixed to SHA-1/6/30 s (the universal authenticator
  defaults); SHA-1 is fine in HMAC mode per RFC 6238.
- Enrollment endpoints sit behind the admin session middleware; confirm and
  disable additionally rate-limit / re-verify the password respectively.
