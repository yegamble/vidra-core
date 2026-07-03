# E2EE messaging — threat model and protocol decision (P11.2)

> Resolves the P11.2 gate ("write threat model before implementation; choose audited
> protocol/library; do not invent crypto"). Decided 2026-07-03 under the operator's
> instruction to complete deferred work. This design keeps ALL cryptography client-side
> with an audited implementation; vidra-core is a dumb key-directory + ciphertext store.

## 1. Threat model

Protected against:
- **Server compromise / subpoena of message content**: the backend stores only
  ciphertext for E2EE conversations; it never possesses decryption keys.
- **Passive network observers**: everything rides TLS; message content additionally
  end-to-end encrypted.
- A malicious counterparty replaying old messages (double-ratchet forward secrecy /
  post-compromise security at the session level).

Explicitly NOT protected against (documented to users in the UI):
- **Metadata**: the server sees who talks to whom, when, message sizes, device counts.
- **A malicious server substituting keys** (MITM at key distribution): v1 mitigates via
  user-visible device-fingerprint verification (safety numbers) but has no cross-signing;
  users who never compare fingerprints trust the server's key directory.
- **Compromised endpoints**: plaintext exists on user devices.
- Message history for new devices: a newly registered device cannot read past messages
  (no encrypted backup in v1).

## 2. Protocol / library decision

- **Olm (double ratchet) via the `@matrix-org/olm` WASM package, client-side in
  vidra-user.** Olm is an independently audited (NCC Group, 2016) double-ratchet
  implementation with a stable standalone API (Session/Account/one-time-keys),
  deliberately usable outside Matrix. We use Olm 1:1 sessions only (no Megolm — DMs
  are 1:1). The package is in maintenance; the documented migration path is the
  vodozemac (audited 2022) WASM bindings once they are stable standalone — the wire
  format (pickled accounts stay client-side; ciphertext is opaque) does not constrain
  the backend either way.
- vidra-core implements **no cryptography** for E2EE beyond what already exists
  (TLS assumed at the proxy; at-rest secretbox is unrelated). Go code treats all
  E2EE payloads as opaque bounded strings.
- **Do-not-invent rule holds**: no hand-rolled ratchets, no custom KDFs. The only
  "protocol" vidra defines is the JSON envelope in §4.

## 3. Backend data model (ciphertext-only)

- `e2ee_devices`: `id UUID PK`, `user_id FK`, `device_name` (bounded, user-labelled),
  `identity_key TEXT` (Curve25519, base64), `signing_key TEXT` (Ed25519), `created_at`,
  `last_seen_at`. A user may register multiple devices.
- `e2ee_one_time_keys`: `id`, `device_id FK CASCADE`, `key_id TEXT`, `key TEXT`,
  `claimed_at NULL`. Uploaded in batches by the owning device; **claiming is atomic**
  (single-use: `UPDATE … SET claimed_at = now() WHERE claimed_at IS NULL … RETURNING`).
- `e2ee_messages`: `id`, `conversation_id FK CASCADE`, `sender_device_id FK`,
  `recipient_device_id FK`, `message_type INT` (Olm prekey=0/normal=1), `ciphertext
  TEXT` (bounded 64 KiB), `created_at`, `expires_at timestamptz NULL`. One row per
  recipient device (client fans out). The existing plaintext `messages` table is
  untouched — a conversation is either plaintext OR E2EE (`conversations.encrypted
  BOOL DEFAULT FALSE`, chosen at creation, immutable).
- Storage invariant test: no column anywhere in the E2EE tables may carry plaintext;
  handler tests assert the server returns ciphertext byte-identical to what was posted.

## 4. HTTP surface (all requireAuth; drift-guarded in openapi)

- `POST /api/v1/e2ee/devices` (register: identity/signing keys + name),
  `GET /api/v1/e2ee/devices` (own), `DELETE /api/v1/e2ee/devices/:id` (own),
  `GET /api/v1/users/:id/e2ee/devices` (public keys of a user you share a conversation
  with — participant-gated to limit enumeration).
- `POST /api/v1/e2ee/devices/:id/one-time-keys` (owner uploads batch),
  `GET /api/v1/e2ee/devices/:id/one-time-keys/count` (owner),
  `POST /api/v1/users/:id/e2ee/claim` (claim one OTK per device of that user —
  participant-gated, atomic, audited count only).
- `POST /api/v1/conversations` gains `{encrypted: true}`;
  `POST /api/v1/conversations/:id/messages` on an encrypted conversation accepts
  `{envelopes: [{recipient_device_id, message_type, ciphertext}], expires_in_seconds?}`
  instead of `{body}` (422 if the wrong shape for the conversation type);
  `GET .../messages` returns only envelopes addressed to the CALLER's devices.
- Disappearing messages: optional `expires_in_seconds` (bounded 30s–90d) stamps
  `expires_at`; an in-process sweeper (ticker, mirrors the transcode worker pattern)
  hard-deletes expired rows; reads also filter `expires_at > now()` so expiry is
  correct between sweeps. Tests cover both paths.
- Block/mute integration: the existing symmetric user-block check applies unchanged.

## 5. Frontend (vidra-user)

- `@matrix-org/olm` WASM, dynamically imported only when an E2EE conversation is
  opened. The Olm account (identity keys, sessions) is pickled with a random local
  key and kept in IndexedDB — device-bound by design; tokens are NOT reused for this.
- UX per the fix_plan P8.2 checkboxes: device setup on first use (name + key upload +
  OTK replenishment), an "Encrypted" indicator on encrypted threads, a disappearing-
  message timer control, fingerprint (safety-number) display per device, and honest
  copy for the §1 limitations ("new devices can't read older messages", metadata note).
- The composer encrypts per recipient device (Olm session per device pair, created
  from a claimed OTK on first message). Decryption failures render a per-message
  "undecryptable" state, never a crash.
- No pretending: the encrypted-mode toggle appears ONLY when the backend advertises
  the contract (probe `GET /api/v1/e2ee/devices` availability), per the fix_plan rule.

## 6. Acceptance

P11.2's "Block completion if no acceptable audited crypto approach is selected" is
satisfied by the Olm decision above. The backend slices are testable without any
crypto (opaque strings); the frontend E2EE e2e uses two real browser contexts
exchanging an encrypted message through the real backend and asserts (a) the message
renders plaintext on both ends, (b) the DB row contains only ciphertext (psql), and
(c) an expired message disappears.
