# Messaging v2 — Backend Contract Spec (`vidra-core`)

> Target file: `vidra-core/.ralph/specs/messaging-v2.md`
> Purpose: the small set of contract additions the Messaging v2 frontend needs.
> **The messaging pillar is already substantially complete** — attachments (upload/
> scan/storage/retrieval/deletion), read state, link previews, tombstones, reports,
> messaging prefs, and the E2EE split all exist (migrations 0031/0058/0064;
> `internal/messaging`, `internal/httpapi/messaging.go`,
> `internal/store/queries/messaging.sql`). Do NOT rebuild any of that. This spec is
> deltas only, in current conventions (Echo handlers in `internal/httpapi`, sqlc
> queries in `internal/store/queries/messaging.sql`, golang-migrate migrations,
> openapi.yaml as the single contract source, generated client consumed by
> vidra-user).

## Invariants that bind every delta (restate, do not weaken)

- **Never log message plaintext or attachment filenames.** Handlers/service log ids
  only (`conversation_id`, `message_id`, `attachment_id`) — matches the existing
  `notify message failed` pattern and `.ralph/specs/security.md` ("no secrets in
  logs"; "E2EE: backend treats ciphertext as opaque; no plaintext stored"). New code
  paths (dimension probe, cursor pagination, search) must not introduce body/filename
  logging, including error wraps.
- **Encrypted conversations stay opaque.** `conversations.encrypted` is immutable;
  encrypted threads store only per-device Olm envelopes; attachment upload on an
  encrypted conversation remains **422** (ciphertext exchanged out of band). No new
  endpoint may return plaintext-derived data (previews, dimensions, search hits) for
  encrypted threads.
- Participant gating: every read is 404-for-non-participant (existence not leaked).
- Additive, backward-compatible contract changes only (optional fields/params).

## Settled defaults (decided 2026-07-07, documentation-only)

- **Read receipts are ON by default.** The existing contract already carries this
  (`MessagingPrefs.read_receipts`; watermark hidden from peers when false). Confirm
  the server default for a user who has never touched the pref is `true`, and state
  it explicitly in the openapi.yaml descriptions for `GET/PATCH
  /api/v1/me/messaging-prefs` and `MessageListResponse.peer_last_read_message_id`
  ("read receipts default to on; a user may disable them, which hides their
  watermark in both directions of the UI"). No schema or endpoint change.

## D1. Attachment image dimensions (enables intrinsic-ratio media)

**Contract** (`openapi.yaml`): add to `DMAttachment` and `UploadAttachmentResponse`:

```yaml
        width:
          type: integer
          description: Pixel width, present only for kind=image when probed successfully.
        height:
          type: integer
          description: Pixel height, present only for kind=image when probed successfully.
```

**Migration** (`00NN_dm_attachment_dimensions.up.sql`):

```sql
-- 00NN: DM attachment intrinsic dimensions (messaging-v2.md D1). Probed at upload
-- for images only; NULL means unknown (pre-migration rows, non-images, probe failure).
ALTER TABLE message_attachments
    ADD COLUMN width  INTEGER CHECK (width  IS NULL OR width  > 0),
    ADD COLUMN height INTEGER CHECK (height IS NULL OR height > 0);
```
Down: drop both columns.

**Implementation** (`internal/messaging/service.go` `UploadAttachment`): for
`kind == image`, probe with `image.DecodeConfig` (stdlib; register jpeg/png/gif/webp
decoders — config-only decode, no pixel allocation) over a `TeeReader` before the
blob write, AFTER the size/type checks. Probe failure is non-fatal (store NULLs) and
logged as `attachment dimension probe failed` with `attachment_id` only. sqlc:
extend `CreateMessageAttachment` / `GetMessageAttachment` /
`ListAttachmentsForMessages` columns.

**Bounds**: reject absurd dimensions (> 20000px either side) as 422 to keep
`aspect-ratio` math sane and prevent decompression-bomb-shaped metadata (bytes are
already size-capped — 25 MiB today, 100 MiB after D6 — and malware-scanned fail-closed; the ClamAV hook is
unchanged and still runs before the row becomes linkable).

**Tests**: unit — probe png/jpeg/webp fixtures, corrupt image (NULLs, upload still
succeeds), oversize-dimension 422; API — upload echoes width/height, list includes
them; existing scan/415/413 tests stay green. Grep-test: no `filename` in log calls.

## D2. Keyset (cursor) pagination for messages

Offset pages shift while new messages arrive, breaking upward history loading.

**Contract**: add optional `before_id` (uuid) to
`GET /api/v1/conversations/{id}/messages`:

```yaml
        - name: before_id
          in: query
          description: >-
            Return only messages strictly older than this message (keyset cursor;
            pair with limit for stable history paging). Mutually exclusive with
            offset — providing both is a 422. Unknown id or a message outside this
            conversation is a 422.
          schema: { type: string, format: uuid }
```

Response shape unchanged (`MessageListResponse`, newest-first). `offset` stays for
compatibility.

**sqlc** (`messaging.sql`): `ListMessagesBefore :many` — same SELECT/joins as
`ListMessages` plus

```sql
WHERE m.conversation_id = sqlc.arg('conversation_id')
  AND (m.created_at, m.id) < (SELECT c.created_at, c.id FROM messages c
                               WHERE c.id = sqlc.arg('before_id'))
ORDER BY m.created_at DESC, m.id DESC
LIMIT sqlc.arg('result_limit');
```
(the row-value comparison matches the existing
`messages_conversation_created_idx (conversation_id, created_at DESC)` ordering; add
`id DESC` to the index in the same migration if EXPLAIN shows a sort).

Same treatment for `EncryptedMessageListResponse` listing (envelopes are also
newest-first) so encrypted threads page identically.

**Tests**: API — page 1 then `before_id` page 2 with a concurrent insert between
(no duplicate/skip); both-params 422; foreign-conversation cursor 422; non-participant
404 unchanged.

## D3. Avatar flags in messaging payloads

Frontend renders avatars via public `GET /users/{id}/avatar` with 404→initials
fallback; flags remove the guaranteed-404 probes for avatar-less users.

**Contract**: add optional `other_has_avatar: boolean` to `ConversationSummary` and
`sender_has_avatar: boolean` to `Message` ("Whether an avatar is set (served at GET
/users/{id}/avatar)"; same wording as `/auth/me.has_avatar`).

**sqlc**: join `user_profile_images` (or the existing avatar-presence source used by
`/auth/me`) into `ListConversations` and `ListMessages` as
`EXISTS(...) AS other_has_avatar` / `sender_has_avatar`. No migration.

**Tests**: API — flag true/false per fixture; unchanged payloads otherwise.

## D4. Conversation search (rail filter at scale) — SMALL, OPTIONAL

**Contract**: optional `q` on `GET /api/v1/me/conversations` — case-insensitive
substring over the other participant's username/display_name (NOT message bodies —
body search is out of scope and would need E2EE carve-outs).

**sqlc**: extend `ListConversations` with
`AND (sqlc.narg('q')::text IS NULL OR ou.username ILIKE '%'||q||'%' OR
ou.display_name ILIKE '%'||q||'%')`. Tests: filter hits/misses, empty q = no filter.

## D5. Unread rollup for the Inbox tab badge — SMALL, OPTIONAL

**Contract**: `GET /api/v1/me/conversations/unread-count` →
`{ "unread_conversations": int, "unread_messages": int }` (behind auth). Single
aggregate over the existing watermark logic; lets the bottom-tab Inbox dot reflect
DMs without paging conversations. sqlc: one aggregate query mirroring the
`unread_count` subselect. Tests: zero/nonzero, watermark advance drops the count.

## D6. Attachment platform limits — Messenger parity (DECIDED 2026-07-07)

**User decision**: DM attachments do **NOT** count against the user's storage quota.
Instead, Vidra enforces per-file / per-message platform limits "same as Facebook
Messenger" (current published Messenger constraints researched + cited in
`RESEARCH.md` §10: 100 MB per file since April 2025; up to 30 photos per message;
images PNG/JPEG/GIF/WEBP, video MP4/MOV, documents PDF/DOC/DOCX/PPT/XLS). Record in
`product-decisions.md` as an amendment to §14.

**Resolved limits (the contract numbers)**:
- **Per-file cap: 100 MiB (104,857,600 bytes)** — replaces the current 25 MiB
  (`MaxAttachmentBytes`); over-limit remains **413**.
- **Per-message count: 30** — `SendMessageRequest.attachment_ids` `maxItems: 4 → 30`;
  the 31st and beyond remain **422** field errors.
- **Allowlist**: keep `image/*` (jpeg/png/gif/webp), `video/*` (incl. mp4,
  quicktime/MOV), `audio/*`, `application/pdf`, and ADD office documents as a new
  kind **`doc`**: `application/msword` (doc),
  `application/vnd.openxmlformats-officedocument.wordprocessingml.document` (docx),
  `application/vnd.ms-powerpoint` (ppt),
  `application/vnd.openxmlformats-officedocument.presentationml.presentation` (pptx),
  `application/vnd.ms-excel` (xls),
  `application/vnd.openxmlformats-officedocument.spreadsheetml.sheet` (xlsx).
  Anything else remains **415**.
- **Rejection contract** (documented in openapi.yaml on the upload + send endpoints):
  413 over-size, 415 unsupported type, 422 count exceeded / failed malware scan /
  encrypted conversation, 503 blob storage unconfigured — same shape as today, new
  bounds.

**Contract changes** (`openapi.yaml`): `DMAttachment.kind` +
`UploadAttachmentResponse.kind` enums gain `doc`; upload description updated
(100 MiB, kinds); `SendMessageRequest.attachment_ids.maxItems: 30`.

**Migration**: `message_attachments.kind` CHECK constraint gains `'doc'`
(`ALTER TABLE … DROP CONSTRAINT …_kind_check; ADD CHECK (kind IN ('image','video',
'audio','pdf','doc'))`). No other schema change (size lives in config, not schema).

**Implementation**: bump `MaxAttachmentBytes` to 100 MiB in `internal/messaging`
(verify the global HTTP body limit accommodates multipart overhead above 100 MiB);
extend the kind-detection allowlist; **every upload continues to route through the
ClamAV scan hook** (`Scanner`, fail-closed when `MALWARE_SCAN_ENABLED`) — this is
load-bearing now that macro-carrying office formats are accepted, so the scan stays
ordered before the row becomes linkable, unchanged.

**Compensating anti-abuse controls** (since no quota counting): per-user upload
rate limit on `POST /conversations/{id}/attachments` via the existing `ratelimit`
package (suggest 60 uploads / 10 min / user, 429 with the standard envelope), plus
the existing lifecycle deletion (attachments removed on tombstone; unlinked uploads
eligible for GC). Frontend renders `doc` with the existing download-row card.

**Tests**: API — 100 MiB boundary (at/over → 201/413), each new office MIME → 201
with `kind=doc`, 415 for a stray type, 31 attachment_ids → 422, EICAR office doc →
422 fail-closed, rate-limit 429; integration asserts the persisted `kind='doc'` row
and post-tombstone deletion (DB-proof). Existing 25 MiB-era tests updated in the
same slice.

## D7. E2EE threads × attachments — design stance (documentation, no code now)

Messaging v2 does NOT add attachments to encrypted threads. The contract's 422 on
`POST /conversations/{id}/attachments` for encrypted conversations is correct and
stays. For the future E2EE-attachment slice (out of scope here), the recorded design
direction, so today's storage decisions don't preclude it:

- Client encrypts the file with a random content key (XChaCha20-Poly1305 or
  AES-256-GCM), uploads **ciphertext only** to a new opaque endpoint
  (`POST /api/v1/e2ee/blobs`, size-capped, participant-scoped, NO kind/filename/
  dimension metadata — all of that travels inside the Olm envelope with the content
  key). Server stores `e2ee-blobs/<conversation>/<id>` with only size + storage key.
- **What is stored**: ciphertext, byte size, conversation id, uploader id, created_at.
- **What is never stored or logged**: filename, content type, dimensions, hashes of
  plaintext, or the content key. ClamAV cannot scan ciphertext — fail-closed scanning
  is explicitly N/A and the threat model note (client-side responsibility) must be
  written into `e2ee.md` when that slice happens.
- The D1 dimension probe must therefore live in the plaintext-only code path (it
  already does — `UploadAttachment` is plaintext-conversation-only).

## Non-goals (documented so Ralph does not drift)

- **Typing/presence, WebSockets, SSE** — polling model is a product decision
  (product-decisions.md §14; INTENTIONAL_DIFFERENCE). Frontend polls list/thread.
- **Per-message delivered receipts** — no delivery event exists in a polling model;
  the read watermark is the only cross-user state. Do not add a `delivered_at`.
- **Group conversations** — `dm_key` stays 1:1; timeline UI assumes two parties.
- **Message body search** — E2EE carve-outs make this a separate design.

## Rollout / verification

Each delta is an independent vertical slice: openapi.yaml first (contract-first),
regenerate the frontend client types in the consuming vidra-user slice, migration +
sqlc + handler + service tests together, `make ci` green (fmt/vet/lint/unit +
integration against real Postgres), endpoint inventory + ledger rows updated.
DB-proof for D1/D2: integration test asserts the persisted row (width/height columns;
keyset page contents) — not just the HTTP echo.
