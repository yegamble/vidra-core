# Vidra Core — Security

Status: living document. Security is a hard requirement, not decorative.

## Current posture (foundation loop)

Implemented so far:

- **CORS allow-list**: explicit origins via `CORS_ALLOWED_ORIGINS`. Wildcard is
  rejected in production by config validation.
- **Config hygiene**: secrets only via environment; `.env` is gitignored;
  `.env.example` ships dummy values only.
- **Bounded startup**: DB/Redis connections fail fast rather than hanging.
- **Non-root container**: the Docker image runs as an unprivileged user.
- **Request recovery + request IDs**: panics are contained; requests are traceable.
- **Password hashing column** present (`users.password_hash`); raw refresh tokens
  are never stored — only their hash (`sessions.refresh_hash`).

## Planned controls (tracked in fix_plan / feature ledger)

- JWT access tokens (short TTL) + refresh-token rotation and revocation.
- TOTP 2FA — DONE, see "TOTP two-factor authentication" below. OAuth2/OIDC — DONE (P4).
- Rate limiting (Redis) for auth, upload, messaging, search, federation.
- SSRF protection for imports, link previews, federation fetches, webhooks,
  remote media: block localhost, private/link-local/reserved ranges, metadata
  services, non-http schemes, DNS-rebinding, oversized/slow responses.
- Input validation + safe error responses (no internal detail leakage).
- Audit logging for admin/moderation/security-sensitive actions.
- File handling: content-type + size validation, path-traversal prevention,
  ClamAV scanning (fail-closed in production by default).
- No secrets in logs; no plaintext private keys at rest without documented KMS.

## TOTP two-factor authentication (P4)

Implemented in `internal/auth/mfa.go` + `internal/httpapi/auth_mfa.go`
(migration 0056: `user_mfa`, `mfa_recovery_codes`).

- **Profile**: RFC 6238, SHA1 / 6 digits / 30s, ±1 step skew, via
  `github.com/pquerna/otp` (the de-facto standard Go implementation; bespoke
  HMAC-window arithmetic would be the riskier choice).
- **Secret at rest**: envelope-encrypted (`internal/secretbox`, AES-256-GCM)
  under `MFA_KEY_KEK`, falling back to `FEDERATION_KEY_KEK` so one KEK can
  cover both; with neither set (dev) the secret is stored raw and the api
  warns loudly at boot. The secret and `otpauth://` URI leave the server
  exactly once (enrollment response) and are on the log denylist
  (`totp_secret`, `otpauth_uri`, `mfa_token`, `recovery_code(s)`).
- **Enrollment is two-phase**: `POST /auth/mfa/totp` stores the secret with
  `enabled=false` (login unaffected); the first valid code at
  `POST /auth/mfa/totp/verify` flips it on — so a user can never lock
  themselves out by enrolling without a working authenticator.
- **Recovery codes**: 10 issued on enable, shown once, stored SHA-256-hashed,
  strictly single-use (`UPDATE … WHERE used_at IS NULL` execrows).
- **Login split**: with MFA enabled, valid credentials return only
  `{mfa_required, mfa_token}` — a 5-minute single-purpose HS256 token whose
  audience is suffixed `:mfa`, so it can never be used as an access token
  (and an access token can never drive the challenge).
  `POST /auth/mfa/challenge` (TOTP or recovery code) then issues the normal
  session (cookie mode supported) and sits behind the strict auth rate
  limiter like login.
- **Disable requires the account password** (a stolen bearer token cannot
  strip 2FA); it drops the secret and all recovery codes.
- **Audit**: `auth.mfa.enable` / `auth.mfa.disable` / `auth.mfa.challenge`
  success+failure (challenge success records the method `totp`/`recovery_code`);
  never a code, secret, or token.
- **Intentional scope**: OAuth/OIDC logins are not TOTP-gated — the upstream
  IdP owns that factor for its identities.

## Threat-model notes

- **E2EE messaging**: backend treats ciphertext as opaque; no plaintext stored.
  Protocol details are BLOCKED until a written threat model + test vectors exist
  (see fix_plan). Only safe envelope/transport/storage may be built before then.
- **Federation input**: all remote payloads validated and size-bounded; remote
  failures must never crash local playback.

## JWT signing-key rotation

Current scheme: access tokens are **HS256** JWTs signed with a single symmetric
`JWT_SECRET` (`internal/auth/jwt.go`); the verifier pins the algorithm to HMAC
(defeating alg-confusion) and checks issuer/audience/exp/nbf. Refresh tokens are
**opaque random tokens stored hashed** in `sessions` (not JWTs) and are
independently revocable — so they do NOT depend on `JWT_SECRET`.

**Rotation procedure available today (brief, bounded disruption).** Because the
signing secret only protects short-TTL access tokens and refresh is DB-backed:
1. Generate a new strong secret (`openssl rand -base64 48`), set `JWT_SECRET`, and
   restart. (Validation already rejects the dev default and any secret < 32 bytes
   in production.)
2. Outstanding **access** tokens signed with the old secret then fail verification
   (401) — but each client transparently calls `POST /auth/refresh` (its opaque
   refresh token is unaffected) and receives a new access token signed with the new
   secret. Worst-case disruption is one failed request per client within the access
   TTL, not a re-login.
3. Rotate on suspected secret compromise, on operator schedule, or on staff
   offboarding. A rotation does not, by itself, revoke sessions; use logout-all /
   session revocation for that.

**Zero-downtime rotation — DEFERRED (documented).** To eliminate even the brief
refresh burst, the issuer would carry a `kid` header and a small keyring: the
primary key signs, and primary + recently-retired keys all verify, with retired
keys dropped after one access-TTL grace window. This is deferred because (a) the
disruption above is already bounded to a single transparent refresh, and (b) it
adds keyring config + verify-fanout complexity. **Trigger to implement:** an
instance large enough that a rotation's synchronized refresh burst is an
availability concern, or a compliance requirement mandating scheduled key rollover
without any client-visible error. When implemented it ships with issuer keyring
tests (old-key token still verifies within grace; dropped after).

## Cookie-mode refresh sessions (browser clients)

By default the refresh token travels only in the JSON body and the SPA keeps it
in memory — a hard reload signs the user out. Cookie mode is the **opt-in**
browser alternative: register/login/refresh accept `{"cookie_mode": true}` (or
detect an existing `vidra_refresh` cookie) and then carry the rotating refresh
token in an httpOnly cookie instead (`internal/httpapi/auth_cookie.go`).

Design (defense-in-depth):

- **httpOnly** — the raw refresh token is never readable by JavaScript; in
  cookie mode the response body OMITS `refresh_token` so the token never has to
  touch JS-accessible storage at all.
- **Path=/api/v1/auth** — the browser only attaches the cookie to the auth
  endpoints (refresh/logout/…), never the wider API surface.
- **SameSite=Lax** — not attached to cross-site POSTs, blunting CSRF against
  refresh/logout. Rotation semantics are unchanged: reuse of a rotated token
  still revokes all of the user's sessions.
- **Secure** — derived from config (`config.CookieSecure`): on when
  `PUBLIC_BASE_URL` is https, and always in production (fail-secure even when
  `PUBLIC_BASE_URL` is unset). Plain-http local dev keeps it off so
  `http://localhost` works.
- **Max-Age = `JWT_REFRESH_TTL`** — cookie lifetime matches the session row.
- `POST /auth/refresh` falls back to the cookie when the body omits the token
  (explicit body token wins); rotation re-sets the cookie; an invalid
  cookie-presented token is cleared alongside the 401 so a dead cookie is not
  re-presented forever. `logout`/`logout-all` clear the cookie (Max-Age=0).
- **Credentialed CORS** — `Access-Control-Allow-Credentials: true` is sent only
  for the explicit `CORS_ALLOWED_ORIGINS` allow-list and is disabled entirely
  if the list contains `*` (credentials + wildcard is never granted).

## OAuth/OIDC login (P4 + P15)

Generic OIDC over provider discovery (`OAUTH_PROVIDERS` config; disabled by
default). Implementation: `internal/auth/oauth.go` + `internal/httpapi/oauth.go`;
deps `golang.org/x/oauth2` (code + PKCE) and `github.com/coreos/go-oidc/v3`
(discovery, JWKS, id_token verification).

Security invariants (all test-enforced):

- **Server-derived redirect URI (P15)** — the `redirect_uri` sent to the
  provider is always `PUBLIC_BASE_URL + /api/v1/auth/oauth/<provider>/callback`,
  never taken from request parameters; `PUBLIC_BASE_URL` is required by config
  validation whenever any provider is configured.
- **Same-origin return_to** — the post-login `return_to` must be a relative
  path (starts `/`, never `//`, no `\`, no scheme/host); anything else is a
  422 at begin. It travels inside the signed state cookie, not the callback URL.
- **Per-attempt state + nonce + PKCE (S256)** — sealed into the httpOnly,
  HMAC-signed (JWT secret), 10-minute, single-use `vidra_oauth_state` cookie
  (Path=/api/v1/auth/oauth, SameSite=Lax, Secure per `config.CookieSecure`).
  The callback compares state in constant time, verifies the id_token against
  the provider JWKS (signature/iss/aud/exp via go-oidc), and checks its nonce.
- **Account-linking rule** — a new identity links to an existing account ONLY
  when the provider asserts `email_verified` for a matching email; an
  unverified match is refused (`oauth_error=email_conflict`) — otherwise a lax
  IdP registration would be an account-takeover vector.
- **Passwordless accounts** — OAuth-created accounts store an EMPTY
  `password_hash` (bcrypt can never verify it). The last-credential guard
  refuses unlinking the only identity of a passwordless account (422); the
  password-reset flow is the way to add a password first.
- **No provider tokens stored; no secrets logged** — Vidra issues its own
  session (always cookie-mode, since a top-level navigation cannot receive a
  bearer token safely); `client_secret`/`id_token`/`code_verifier` are on the
  sensitive-key denylist; audit events carry `oauth:<provider>` reasons only.

## Rules

- Never commit real secrets, tokens, keys, or personal data anywhere
  (code, fixtures, docs, logs, tests, `.ralph/`).
- Production defaults bias to fail-closed for security-relevant features.
