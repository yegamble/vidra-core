# ATProto / Bluesky extension — v1 syndication strategy (P10.2)

> Resolves P10.2's "posting/syndication strategy spec before code". Decided 2026-07-03.
> This is a Vidra EXTENSION (vidra-extensions-ledger), independent of ActivityPub and
> gated by its own config (`ATPROTO_ENABLED`, default false).

## v1 scope: outbound cross-posting only

When a creator links a Bluesky account, Vidra can announce their newly published
PUBLIC videos on Bluesky. Nothing else: no AT-native video hosting, no inbound
firehose consumption, no DID-based login, no PDS hosting. Those are explicitly out
of scope until a future spec.

## Identity linking

- Table `atproto_accounts`: `user_id UUID PK FK users CASCADE`, `handle TEXT`,
  `did TEXT`, `pds_url TEXT DEFAULT 'https://bsky.social'`, `app_password_sealed TEXT`
  (secretbox-sealed with the existing `FEDERATION_KEY_KEK`-style KEK — new
  `ATPROTO_KEY_KEK` falls back to `FEDERATION_KEY_KEK`; required in production when
  enabled), `auto_post BOOL DEFAULT false`, `created_at`, `last_posted_at`.
- Linking flow: the user supplies handle + **app password** (never the main password —
  UI copy must say so) → backend calls `com.atproto.server.createSession` on the PDS
  (SSRF-guarded client; the PDS URL must be https + public) to verify and resolve the
  DID → store sealed. Unlink deletes the row. The app password is never returned,
  logged, or exported; the sensitive-key guard list gains `app_password`.
- Endpoints (requireAuth): `GET/PUT/DELETE /api/v1/me/atproto` (status view returns
  handle/did/auto_post only).

## Posting

- On the published transition of a PUBLIC video whose owner has `auto_post`, enqueue a
  post via a durable `atproto_posts` queue table (mirror `federation_deliveries`
  states/backoff) drained by the same worker pattern: create a fresh session, then
  `com.atproto.repo.createRecord` (`app.bsky.feed.post`) with the video title and an
  external-link embed to the public watch URL (thumbnail uploaded as the embed's blob
  when available, ≤1 MiB). Records the resulting post URI on the queue row.
- Rate/abuse safety: at most 1 auto-post per video (dedupe by video_id UNIQUE on the
  queue), backoff on 429s, dead-letter after 6 attempts.
- ActivityPub and ATProto enable independently; a matrix test asserts all four
  combinations boot.

## Frontend

- Settings → "Connected accounts": link/unlink Bluesky (handle + app-password fields,
  clear app-password guidance link), auto-post toggle, last-post status. Protocol
  labeling per ui-inventory: local-only / ActivityPub / ATProto badges where relevant.

## Testing

Unit: sealing round-trip, post-record building, queue state machine (fake XRPC).
Handler: link (fake PDS via httptest, verifies createSession call), status hides
secrets, unlink. Integration: queue table against real PG. No live-network tests.
