# Feature: Single-User Auth With Device Pairing

## Summary

Replace URL token auth with a simple single-user login plus device pairing as a core
flow. Primary auth uses a secure session cookie after password login. Device pairing
lets isolated devices join without sharing long URLs or the main password.

## Goals

- Single-user security without third-party identity providers.
- Easy access from multiple isolated devices.
- No secrets in URLs (no token leakage via Referer, logs, or copy/paste).
- Strong defaults (HTTPS, secure cookies, rate limiting).

## Non-Goals

- Multi-user accounts or role management.
- Federated login (OIDC, SSO).
- UI-based password setup (password provided via environment).

## UX Flows

### 1) Standard Login (default)

1. User visits `https://host:port/`.
2. Login form prompts for password.
3. Server verifies password hash.
4. Server sets a session cookie and redirects to dashboard.

### 2) Device Pairing (core)

For isolated devices, pairing avoids reusing the password everywhere.

1. On a trusted, logged-in device: "Generate pairing code".
2. Server shows a short-lived pairing code (e.g., 10 minutes, single-use).
3. On a new device: visit `/pair`, enter code.
4. Server creates a long-lived "device session" directly (no intermediate token).
5. Device can be revoked from the dashboard.

## Design Details

### Authentication Modes

- **Password login** (primary):
  - Password provided via `AG_WEB_PASSWORD` environment variable.
  - Stored in memory as Argon2id hash on startup (never written to disk).
  - Login rate limiting enforced per IP.
- **Device pairing**:
  - Pairing code directly mints a long-lived session marked as "device session".
  - No intermediate device token—simpler storage model.

### Session Management

- Server-side session store keyed by session ID.
- Two session types:
  - **AuthSession**: Short-lived (12h) with sliding refresh, from password login.
  - **DeviceSession**: Long-lived, from pairing, revocable.
- Session cookie:
  - `HttpOnly`, `Secure`, `SameSite=Strict`.
- Logout clears session.
- Device revocation invalidates the device session.
- Password change invalidates all sessions (auth and device).

### Naming

To avoid collision with existing `Session`/`SessionStore` (used for task sessions):
- Auth sessions use `AuthSession` / `AuthSessionStore`.
- Existing task session types remain unchanged.

### Endpoints (proposed)

- `GET /login` -> login form
- `POST /login` -> create auth session (password)
- `POST /logout` -> destroy session
- `GET /pair` -> pairing form
- `POST /pair` -> exchange code for device session
- `POST /api/pair/code` -> create pairing code (requires session)
- `GET /api/devices` -> list paired devices (requires session)
- `DELETE /api/devices/:id` -> revoke device (requires session)

### Storage

- `~/.agency/auth-sessions.json` (or under `AGENCY_ROOT`)
  - `sessions[]` with `id`, `type` (auth|device), `label`, `created_at`, `last_seen`, `expires_at`
  - `pairing_codes[]` with `code_hash`, `expires_at`, `used`
- Password hash kept in memory only (derived from env var on startup).

### CSRF Protection

`SameSite=Strict` on session cookies is sufficient for same-origin forms. All
state-changing endpoints use POST/PUT/DELETE which browsers won't send cross-origin
with cookies due to SameSite. No additional CSRF tokens needed.

## Security Considerations

- No secret in URL or logs.
- Pairing code:
  - 8-10 char base32 code.
  - Single use, short TTL (10 min).
  - Rate limited and logged.
- Sessions:
  - Stored server-side, cookie contains only session ID.
  - Device sessions revocable from UI.
  - Password change invalidates all sessions.
- TLS required (self-signed OK for local).
- `/status` remains unauthenticated (required for discovery).

## Implementation Plan

1. Add auth session storage:
   - Create `internal/view/web/auth_store.go` for session and pairing code storage.
   - Hash password from `AG_WEB_PASSWORD` on startup using Argon2id.
   - Rename types to `AuthSession`/`AuthSessionStore` to avoid collision.
2. Add session middleware:
   - New middleware `RequireAuthSession` for protected routes.
   - Replace existing `AuthMiddleware` (token-based).
   - Use secure cookies with `SameSite=Strict`.
3. Add login UI:
   - `GET /login` + `POST /login` form.
   - Redirect to dashboard on success.
4. Add pairing flow:
   - `GET /pair` + `POST /pair` form (code entry).
   - "Generate pairing code" button in dashboard.
   - Pairing creates device session directly.
5. Add device management:
   - `GET /api/devices` list and `DELETE /api/devices/:id` revoke.
   - Show device label, last seen, created at.
6. Remove URL token support:
   - Remove `AG_WEB_TOKEN` and query parameter auth entirely.
   - Clean up related code in `auth.go`.

## Testability

- Unit tests:
  - Password hashing/verification.
  - Pairing code lifecycle (single use, expiration).
  - Session creation, refresh, and expiry.
  - Device revoke invalidates sessions.
- Handler tests:
  - Login success/failure with rate limiting.
  - Pairing flow end-to-end with a short TTL.
  - Protected routes reject unauthenticated requests.
- Integration tests:
  - Web server boot with password set and login flow.
  - Device pairing from a fresh client creates a new session.

## Configuration

Environment variable:
- `AG_WEB_PASSWORD`: Required. Password for web UI login.

No YAML config needed—password is the only setting, provided via environment.
