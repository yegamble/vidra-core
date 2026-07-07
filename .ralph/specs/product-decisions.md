# Product decisions — resolving the remaining "decision or code" gates

> Decided 2026-07-03 under the operator's instruction to complete all deferred work.
> Each entry names the fix_plan item it resolves and whether it resolves to CODE
> (build it) or DECISION (document + mark INTENTIONAL_DIFFERENCE / config-only).

## 1. Account hard deletion / anonymisation (P4 [~] deactivation item) — CODE

`DELETE /api/v1/auth/me` (password-confirmed, requireAuth) performs an irreversible
delete with this retention policy:
- The `users` row is **anonymised, not removed** (audit/actor integrity): username →
  `deleted-<8char-id>` (unique), email → NULL-equivalent sentinel (unique), password
  hash cleared, display_name/bio/avatar/banner wiped, `is_active=false`, new
  `deleted_at` stamp. All sessions revoked; MFA/OAuth/atproto/e2ee rows deleted.
- Owned channels and their videos are hard-deleted (cascades already exist), media
  blobs best-effort deleted from storage, federated `Delete` activities fanned out
  for previously-public videos and the actors (reuses the P10 delete hooks).
- The user's comments become tombstones: body → "" with a new `deleted_at` on
  `comments` (view renders "[deleted]"; thread structure preserved). Ratings,
  saved/watch-history/playlists, follows, mutes, blocks: hard-deleted.
- DMs: `messages.sender_id` rows remain (recipient's copy of a conversation is their
  data) but sender identity resolves to the anonymised placeholder.
- Emits `auth.account.delete` audit (actor id only). Admin variant:
  `DELETE /api/v1/admin/users/:id` with the same semantics (self-guarded), audited.
- 30-day grace/undo is OUT (deactivation already covers reversible leave).

## 2. Owner super-role (P9) — DECISION (config-only, INTENTIONAL_DIFFERENCE)

Vidra keeps three roles (user/moderator/admin). "Owner" as a distinct super-role is
an INTENTIONAL_DIFFERENCE: single-instance deployments don't need a fourth tier; the
first-account-is-admin bootstrap + the self-demotion guard already prevent lockout.
Revisit only if multi-tenant instance management ever lands.

## 3. Rate-limit management endpoints (P9) — DECISION (config-only)

Rate limits stay configuration (`RATE_LIMIT_*`, `AUTH_RATE_LIMIT_*`), not runtime
admin endpoints: limits are capacity decisions made at deploy time; a runtime knob
adds an attack surface (an admin-compromise can disable protection silently). The
admin system-status page shows the effective config (read-only) instead — expose
them in `GET /api/v1/admin/system` (non-secret values only).

**Status: IMPLEMENTED** (core-sweep, 2026-07-03). `GET /api/v1/admin/system`
carries a `rate_limits` object `{enabled, requests, auth_requests,
window_seconds}` populated from the effective config; there is no runtime
mutation endpoint. See fix_plan P9 (INTENTIONAL_DIFFERENCE) + `SystemStatus` in
`api/openapi.yaml`.

## 4. Torrent/magnet import (P6.1 / user P6.4) — DECISION (INTENTIONAL_DIFFERENCE)

Not implemented: WebTorrent-based import/playback is legacy-PeerTube architecture the
project already replaces with HLS+storage backends (see the Optional bucket's
WebTorrent note). The UI keeps no hidden tab; both fix_plans mark the items
INTENTIONAL_DIFFERENCE referencing this doc. An adapter boundary is unnecessary —
URL import already covers "fetch a file from elsewhere".

## 5. IPFS media mirroring (P6.2 → P19) — DECISION (mirror sidecar, public-only v1)

**Superseded 2026-07-07.** The original P6.2 defer ("IPFS backend adapter or
deferred spec" — pinning economics, GC semantics, and gateway trust need their
own spec) is RESOLVED by the P19 spec `.ralph/specs/ipfs-media.md`, which the
user approved as a public-only v1 on 2026-07-07.

The "IPFS as a third `STORAGE_BACKEND`" framing was the wrong shape and is
abandoned: IPFS is now an orthogonal **mirror sidecar**, not an authoritative
backend. Local/S3 remains the authoritative store; when `IPFS_ENABLED=true` and a
healthy node is reachable, eligible **already-public** media is added+pinned and
its CID exposed additively in API responses. `STORAGE_BACKEND=ipfs` **stays
rejected** at config validation (authority ≠ distribution) — that check is
unchanged and now names this spec.

**Privacy invariant (gating decision, spec §7).** Nothing non-public is ever
enqueued for a public pin: private/unlisted/quarantined videos, DM attachments,
exports, upload chunks, the live edge, and remote-cache thumbnails are all
excluded by the eligibility gate. `IPFS_MIRROR_PRIVATE=true` is a hard config
error unless a private `IPFS_CLUSTER_API_URL` is set, and private-media mirroring
stays out of scope until the user signs off AND the Messaging v2 spec defines the
attachment contract.

**Status 2026-07-07: SHIPPED.** All six slices (P19.1–P19.6) are implemented and
tested against the shipped spec `.ralph/specs/ipfs-media.md`: migration 0071
`media_ipfs_pins`, the `IPFS_*` config surface + privacy guard, the `internal/ipfs`
Kubo client + CID validation, the `internal/ipfsmirror` worker + eligibility privacy
fence + reconciliation + video/HLS pin lifecycle + optional cluster replication, the
additive `ipfs`/`ipfs_pinned` API fields, the admin `GET /api/v1/ipfs/status` and the
one-shot `POST /api/v1/admin/ipfs/reconcile` backfill (audited, idempotent), the
compose `ipfs` profile + tagged integration tests, and the operator runbook in
`docs/operations.md`. Tracked in `fix_plan.md#P19` and the extensions ledger row
`VIDRA-IPFS-STORAGE` (VERIFIED backend). The remaining surface is the cross-repo
vidra-user IPFS badge/admin panel that consumes the shipped `ipfs_pinned` field +
`/ipfs/status` endpoint.

## 6. Signed URLs vs proxy (P6.2) — DECISION (proxy in v1)

All media serving stays proxied through the API (visibility guards, Range support,
uniform local/S3 behaviour). S3 presigned URLs are a later optimisation gated on a
CDN story; revisit when a deployment actually saturates the API egress.

## 7. Notification preferences + email delivery (P8 deferral note) — CODE

- `notification_prefs` table: `(user_id, type)` → `enabled BOOL` (default true for
  all types); `notification.Create` consults it. Endpoints:
  `GET/PATCH /api/v1/me/notification-prefs`.
- **SMTP mailer**: implement the existing `Mailer` interface over SMTP (config
  `SMTP_HOST/PORT/USERNAME/PASSWORD/FROM`, `MAIL_ENABLED`; STARTTLS; validated in
  prod when enabled) so password-reset/verification emails actually send outside dev.
  Templates: plain-text first. Email notification digests remain OUT (only the
  transactional auth mails send).
- Web-push delivery stays deferred (needs a VAPID/service-worker story) — the prefs
  model is delivery-agnostic so it slots in later.

## 8. Creator statistics (user P7) — CODE (minimal honest v1)

New `GET /api/v1/videos/:id/stats` (owner-only) + `GET /api/v1/channels/:handle/stats`
(owner-only): totals from existing data (views, likes/dislikes, comments, followers)
plus a 30-day daily views series from a new `video_view_days` rollup
(`(video_id, day) → count`, incremented alongside the existing dedupe-counted view
recording — cheap upsert, no backfill). No watch-time analytics in v1 (would need
player beacon batching — documented later-work).

## 9. Report hard-delete (P9) — CODE

`DELETE /api/v1/admin/reports/:id` (admin only — moderators resolve, admins can purge),
idempotent, audited (`moderation.report.delete`).

## 10. Admin-set email_verified + bypass-quarantine (P9 user-edit remainder) — CODE

`PATCH /api/v1/admin/users/:id` gains `email_verified?: bool` and
`bypass_quarantine?: bool` (new column; consulted by the quarantine gate in §11).

## 11. Auto-block / quarantine pipeline (P9) — CODE

Instance setting `QUARANTINE_NEW_UPLOADS` (bool, default false; also a dynamic
instance setting once the instance-config slice lands): when on, a finished upload
by a non-privileged user (role=user, `bypass_quarantine=false`) publishes into a new
`quarantined` state instead of `published` — visible to the owner (badged) and
moderators, absent from public surfaces (reuse the video_blocks-style NOT EXISTS or
a state check — implementer's choice, but state must survive re-transcode).
Moderator queue: `GET /api/v1/admin/videos/quarantined`, `POST /api/v1/admin/videos/
:id/approve` (→ published, triggers the publish hooks: federation announce etc.),
`POST /api/v1/admin/videos/:id/reject` (→ failed + reason, notifies the owner).
Audited. Frontend: studio badge + moderation queue tab.

## 12. Watched-word video tagging + auto-hold (P9 remainder) — CODE

On create/edit, a video's title+description are matched against watched words
(same matcher); matches recorded in `watched_word_matches` via new nullable
`video_id` column (CHECK: exactly one of comment_id/video_id). No auto-hold for
videos (quarantine in §11 is the hold mechanism); flagged videos surface in the
existing matches review queue with a type badge.

## 13. Block-hides-content extension (P9/P11 deferral) — CODE

Blocking a user now also hides their comments and videos from the blocker
(extend the existing mute NOT EXISTS filters to also check `user_blocks` where
blocker = viewer). Symmetric DM refusal unchanged.

## 14. DM completeness (P11.1 deferred items) — CODE

- **Attachments**: `message_attachments` table (message FK, storage key
  `dm-attachments/<conversation>/<id>.<ext>`, size ≤ 25 MiB, images/video/audio/pdf
  allowlist); multipart `POST /api/v1/conversations/:id/attachments` returns an
  attachment id to reference in a send; downloads are participant-gated. ClamAV
  scan when `MALWARE_SCAN_ENABLED` (fail-closed like uploads).
- **Link previews**: on send, the backend extracts the first URL, fetches it through
  the SSRF guard (1 MiB cap, html-only), parses OpenGraph title/description/image
  into `link_previews` keyed by URL hash (shared cache), and attaches preview data to
  the message view. Preview fetch is best-effort/async; never blocks the send.
- **Read receipts**: `conversation_participants.last_read_message_id` +
  `POST /api/v1/conversations/:id/read` (idempotent); the inbox view exposes unread
  counts; the thread view exposes the peer's read watermark. Per-user privacy toggle
  rides notification_prefs (`type=read_receipts` reuse — implementer may add a
  dedicated column instead; document choice).
- **Per-message delete + report**: sender may delete own message (tombstone body,
  "[deleted]"); either participant may report a message (`target_type=message`,
  stores the message id + body snapshot into the report for moderator context).
- Typing presence stays OUT (polling model; documented INTENTIONAL_DIFFERENCE).

### 14a. DM attachment limits — Facebook-Messenger parity (AMENDMENT, DECIDED 2026-07-07)

User decision (messaging-v2.md D6; sources cited in messaging-v2 RESEARCH §10):
**DM attachments do NOT count against the user's storage quota.** Instead Vidra
enforces per-file / per-message platform limits "same as Facebook Messenger". The
`quota` package (used for video uploads) is intentionally NOT wired into the
messaging attachment path.

- **Per-file cap: 100 MiB (104,857,600 bytes)** — raised from the original 25 MiB
  (`messaging.MaxAttachmentBytes`); over-limit stays **413**.
- **Per-message count: 30** (`messaging.MaxAttachmentsPerMessage`, raised from 4);
  `SendMessageRequest.attachment_ids.maxItems: 30`; the 31st+ is a **422** field
  error.
- **Allowlist gains office documents** as a new coarse kind **`doc`**
  (migration 0070 widens the `message_attachments.kind` CHECK): `application/msword`,
  `application/vnd.openxmlformats-officedocument.wordprocessingml.document`,
  `application/vnd.ms-powerpoint`,
  `application/vnd.openxmlformats-officedocument.presentationml.presentation`,
  `application/vnd.ms-excel`, and
  `application/vnd.openxmlformats-officedocument.spreadsheetml.sheet`. Images
  (JPEG/PNG/GIF/WEBP), video (incl. MP4/MOV), audio, and PDF remain; anything else
  is **415**.
- **Malware scanning stays load-bearing.** Every upload still routes through the
  ClamAV hook fail-closed (when `MALWARE_SCAN_ENABLED`), ordered BEFORE the row
  becomes linkable — now that macro-carrying office formats are accepted this is a
  hard requirement, not a nicety (an EICAR-in-docx upload is rejected 422).
- **Compensating anti-abuse control (no quota counting):** a per-USER rate limit on
  `POST /conversations/{id}/attachments` via the `ratelimit` package — default
  **60 uploads / 10 minutes → 429** (`ATTACHMENT_UPLOAD_RATE_LIMIT_REQUESTS` /
  `ATTACHMENT_UPLOAD_RATE_LIMIT_WINDOW`, gated by `RATE_LIMIT_ENABLED`). Existing
  lifecycle cleanup is unchanged (attachments removed on tombstone; unlinked
  uploads are GC-eligible).

## 15. Local videos endpoint (P7) — CODE (trivial with §federation scope param)

`GET /api/v1/videos?scope=local|all` (default local) replaces the dedicated
endpoint idea; a `/local` alias route is unnecessary (INTENTIONAL_DIFFERENCE).

## 16. Account/channel-level privacy (P6.1 remainder) — CODE (minimal)

A `users.unlisted BOOL DEFAULT false` ("don't list my channels/videos in public
directories/search; direct links still work"). Channel pages of unlisted users 404
in search/discovery joins but serve directly. Full per-field profile privacy is OUT.

## 17. Scheduled publish (P6.1) — CODE

`videos.publish_at timestamptz NULL`: settable on create/edit when the video is
private/draft; a sweeper (same worker cadence) transitions due videos to published
(running the publish hooks). Public surfaces already filter on state so no query
changes beyond the sweeper. Frontend: a datetime field + "Scheduled" badge.

## 18. Free-form tags (P6.1) — CODE

`video_tags (video_id FK, tag citext/lower-indexed, ≤5 per video, ≤50 chars,
PK(video_id, tag))`; accepted on create/edit as `tags: []`; exposed on detail;
`GET /api/v1/videos?tag=` filter + tags in search matching; rendered as chips on
the watch page; editable in the studio forms.
