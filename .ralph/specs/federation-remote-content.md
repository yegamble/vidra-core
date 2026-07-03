# Federation remote-content model — design-gate resolution

> Resolves the DESIGN GATE recorded in `fix_plan.md` P10 and `federation.md` §6/§9-slice-6:
> how remote videos are stored, surfaced, and moderated; how a local user follows a
> remote channel; and how federated comments work. Decisions below were taken
> 2026-07-03 (PeerTube-parity-guided) under the operator's instruction to complete
> all deferred work. Where this deviates from an older sketch in `federation.md`,
> this document wins (it post-dates the Slice 2a side-table lesson).

## 1. Remote videos — storage

New table `remote_videos`:

- `id UUID PK`, `object_url TEXT UNIQUE` (the AP object id — dedupe key),
  `remote_actor_url TEXT NOT NULL REFERENCES remote_actors(actor_url) ON DELETE CASCADE`
  (the attributed channel actor), `title`, `description`, `duration_seconds INT NULL`,
  `published_at timestamptz`, `watch_url TEXT` (the origin's human watch page, from AS
  `url`), `stream_url TEXT NULL` (best playable URL from the object's `url` links: prefer
  an HLS `application/x-mpegURL` link, else a direct video/mp4 link, else NULL),
  `thumbnail_key TEXT NULL` (locally cached poster — see §5), `fetched_at`, `updated_at`.
- Bounded fields (title ≤ 500, description ≤ 5000 — truncate, never reject on length).
- Remote videos are **metadata only**: Vidra never copies or re-hosts the media file.
  Playback uses `stream_url` from the origin (or links out to `watch_url`).

## 2. Remote videos — ingestion (inbound Create/Announce)

- The inbox dispatch gains `Create{Video}` and `Announce{Video|url}` cases.
- Accept only when: the signer's actor is resolvable AND (signer == attributedTo's
  actor, or for Announce the signer is the announcing channel) AND **the signing
  actor has at least one accepted local follower edge** (anti-spam: we only ingest
  content we asked for by following). Otherwise accept-and-ignore (202, no store).
- Upsert by `object_url` (idempotent). An `Announce` of an unseen object triggers a
  guarded fetch of the object document (SSRF guard, 1 MiB cap) before upsert.
- Every stored remote video records which local follow edges caused ingestion
  implicitly via `remote_actor_url` (no per-user fan-out rows needed).

## 3. Outbound follow of a remote channel

- A local **user** follows a remote **channel** (PeerTube subscription model).
- New table `remote_channel_follows`: `user_id UUID FK users ON DELETE CASCADE`,
  `remote_actor_url TEXT FK remote_actors ON DELETE CASCADE`, `state TEXT CHECK
  (pending|accepted) DEFAULT pending`, `follow_activity_url TEXT` (our Follow id),
  `created_at`; `PK (user_id, remote_actor_url)`.
  (Deviation from federation.md §6's "extend channel_follows": a separate table keeps
  the local-follow PK/FKs and sqlc models untouched — same rationale as Slice 2a.)
- Flow: UI submits `name@domain` (or a full actor URL) → WebFinger on the remote
  domain (SSRF-guarded) → resolve + cache the actor → insert pending row → enqueue a
  signed `Follow` (via `federation_deliveries`, signed as the **user's account actor**).
  Inbound `Accept{Follow}` (matched by `follow_activity_url`, signer == followed actor)
  flips state → accepted. Inbound `Reject` deletes the row. Unfollow = enqueue
  `Undo{Follow}` + delete the row immediately (local intent wins).
- The follower's subscriptions feed (`GET /me/subscriptions/videos`) becomes a UNION of
  local followed-channel videos and `remote_videos` of accepted remote follows, newest
  first, with `remote: true` + origin `domain` on remote cards.

## 4. Surfacing rules

- Remote videos appear in: the follower's subscriptions feed; search (title trigram over
  `remote_videos` UNIONed in, flagged remote); and a dedicated remote-watch surface.
- Remote videos do NOT appear in the public local feed/trending by default. The feed
  gains `scope=local|all` (default `local`); `all` includes remote videos for
  discovery (PeerTube's local/all toggle parity).
- Remote watch surface: `GET /api/v1/remote-videos/:id` returns the stored metadata
  (+ `stream_url`/`watch_url`); the frontend renders playback from `stream_url` when
  present (HLS/mp4 from origin) and always links the origin `watch_url`. No comments,
  ratings, or watch-progress on remote videos in v1 (interactions live at the origin);
  Save/playlists MAY treat them as external links later — out of v1.

## 5. Remote media cache strategy (resolves the P10 checkbox)

- Cache **thumbnails only**: on ingestion, best-effort fetch the object's icon/preview
  through the SSRF guard, cap 2 MiB, store at `remote-thumbnails/<id>.jpg` via the
  storage backend, serve at `GET /api/v1/remote-videos/:id/thumbnail`. Failure is
  non-fatal (card shows the no-preview fallback). Video bytes are NEVER cached/mirrored
  (bandwidth + licensing); revisit only if a real redundancy feature is ever specced.

## 6. Federated comments

- Inbound: `Create{Note}` whose `inReplyTo` resolves to a LOCAL video's AP object URL
  (or to a local comment's URL for threading) stores a row in `comments` with new
  nullable attribution columns: `remote_actor_url TEXT NULL` + snapshot
  `remote_author_name TEXT NULL`; `user_id` becomes nullable with
  `CHECK ((user_id IS NULL) <> (remote_actor_url IS NULL))`. Signer must equal the
  Note's attributedTo actor. Body bounded to the local 5000-char cap (truncate).
  Comment views expose `remote: true` + author domain for remote rows.
- Outbound: a local comment on a local video fans out as `Create{Note}`
  (inReplyTo = the video object URL, attributed to the commenter's account actor) to
  the video channel's remote followers via the delivery queue. Local edits fan out
  `Update{Note}`; deletes fan out `Delete`.
- Inbound `Update{Note}`/`Delete` on a known remote comment: apply when the signer is
  the original attributed actor (or the origin server for Delete). v1 excludes local
  comments on REMOTE videos (they'd need origin addressing/approval — the origin's
  watch page is the place to comment; documented INTENTIONAL_DIFFERENCE for now).
- Moderation: remote comments are subject to the same watched-words flagging, admin
  comments overview, and moderator delete (local delete is local-only — we do not
  federate a Reject upstream in v1).

## 7. Inbound Update/Delete of remote videos

- `Update{Video}`: upsert the `remote_videos` row (signer must be the attributed actor).
- `Delete` (object = a known remote video / actor): delete the row(s); an actor Delete
  removes the actor's videos + comments (cascade via remote_actor_url). Authority:
  signer == the object's attributed actor, or the signer's actor shares the object's
  origin host.

## 8. Instance-level moderation

- **Per-user instance mute** (completes the `muted_accounts/instances` [~] item):
  table `muted_instances (muter_id FK users, domain TEXT, created_at, PK(muter_id,domain))`.
  Effect: remote videos/comments from that domain are filtered out of that user's
  feeds/search/comment lists (same NOT EXISTS pattern as account mutes).
  Endpoints: `POST/DELETE/GET /api/v1/me/mutes/instances[/:domain]`.
- **Admin instance blocklist**: table `blocked_instances (domain TEXT PK, reason,
  blocked_by, created_at)`. Effect: inbound activities from actors on that domain are
  dropped at the inbox (after signature verification — cheap check by actor host);
  existing content from the domain is hidden from all surfaces; outbound deliveries to
  it are cancelled. Endpoints: `GET/POST/DELETE /api/v1/admin/instances/blocked[/:domain]`
  (requireRole admin/moderator), audited.
- Remote videos/comments can be reported locally (`target_type` gains `remote_video`)
  and admin-hidden individually via the existing block pattern (a `remote_video_blocks`
  table mirroring `video_blocks`).

## 9. Testing bar

Every slice above ships: unit tests (dispatch/authority/bounds), handler tests
(auth/404/queue effects), integration tests against real Postgres for each new
table, and — for the ingestion + follow flows — an httptest "remote instance" that
serves WebFinger/actor/object documents and receives signed deliveries, asserting
the full loop (follow → Accept → Create ingested → visible in the subscriptions feed).
Golden-fixture contract tests use real-shaped PeerTube/Mastodon payloads (sanitized).
