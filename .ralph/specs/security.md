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
- TOTP 2FA; OAuth2/OIDC where specs require.
- Rate limiting (Redis) for auth, upload, messaging, search, federation.
- SSRF protection for imports, link previews, federation fetches, webhooks,
  remote media: block localhost, private/link-local/reserved ranges, metadata
  services, non-http schemes, DNS-rebinding, oversized/slow responses.
- Input validation + safe error responses (no internal detail leakage).
- Audit logging for admin/moderation/security-sensitive actions.
- File handling: content-type + size validation, path-traversal prevention,
  ClamAV scanning (fail-closed in production by default).
- No secrets in logs; no plaintext private keys at rest without documented KMS.

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

## Rules

- Never commit real secrets, tokens, keys, or personal data anywhere
  (code, fixtures, docs, logs, tests, `.ralph/`).
- Production defaults bias to fail-closed for security-relevant features.
