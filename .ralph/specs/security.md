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

## Rules

- Never commit real secrets, tokens, keys, or personal data anywhere
  (code, fixtures, docs, logs, tests, `.ralph/`).
- Production defaults bias to fail-closed for security-relevant features.
