# PeerTube to Athena Migration Guide

This guide describes the current migration posture for moving from a PeerTube instance to Athena.

## Current State

Athena has validated PeerTube-aligned API/runtime coverage for many day-to-day flows, but it does **not** currently ship a production ETL/import tool that can automatically ingest:

- a PeerTube PostgreSQL dump
- PeerTube media and thumbnail storage
- and all related metadata mappings

So this document is still a planning guide, not an operator-ready migration runbook backed by shipped import tooling.

## What Exists Today

- PeerTube-style API and admin route coverage validated in Athena's Go and Newman suites
- ActivityPub discovery and instance follow/unfollow validation
- OpenAPI coverage for the Athena routes exercised in the current runtime test suite
- Athena-only extension coverage for messaging, payments, IPFS, livestreaming, and ATProto

## What Is Still Missing

- A supported import command or service for PeerTube database/media ETL
- A fixture-based migration rehearsal proving a real PeerTube export can be transformed and loaded into Athena
- An automated post-import validation harness for migrated users, channels, videos, comments, playlists, captions, and subscriptions

## Scope

- Database/content migration strategy
- Storage migration strategy
- Config migration checklist
- DNS/proxy cutover sequence

## 1) Pre-Migration Checklist

- Confirm Athena environment is provisioned (database, redis, storage, secrets).
- Ensure Athena schema is current (`make migrate-up`).
- Inventory PeerTube data volume:
  - users/accounts
  - channels
  - videos
  - comments
  - playlists
  - captions
- Define target storage mode in Athena (local/IPFS/S3-compatible).
- Schedule maintenance window for final sync/cutover.

## 2) Database Strategy

### Export PeerTube

- Take a consistent PostgreSQL dump from PeerTube:

```bash
pg_dump -Fc -d "$PEERTUBE_DATABASE_URL" -f peertube.dump
```

### Transform + Import into Athena

Athena and PeerTube are similar in many high-level concepts but not schema-identical. A full migration will require a staged ETL process:

1. Restore the PeerTube dump into a temporary staging database.
2. Run transformation scripts that map staging tables into Athena tables.
3. Validate foreign keys, required fields, and media references before final import.
4. Produce a migration report showing what was imported, skipped, or transformed.

Today, these steps are conceptual. Athena does not yet ship the transformation scripts or migration service.

### Conceptual Mapping

- PeerTube accounts/users -> Athena users
- PeerTube video channels -> Athena channels
- PeerTube videos -> Athena videos (`channel_id` must be set)
- PeerTube comments -> Athena comments (`parent_id` for threading)
- PeerTube playlists + items -> Athena playlists + playlist_items
- PeerTube captions/subtitles -> Athena captions

## 3) Storage Strategy

PeerTube media and thumbnails must be copied to Athena storage and reindexed.

### Local-to-Local

- Copy media directories to Athena storage root.
- Rebuild Athena path references as needed.

### Local-to-IPFS/S3

- Ingest files into the selected Athena backend.
- Persist resulting object keys or CIDs in Athena records.

### Validation

- Spot-check playback for multiple videos.
- Verify thumbnails, captions, and playlist item media references.
- Verify imported records still map to the expected channels and owners.

## 4) Configuration Migration

PeerTube config does not map 1:1; migrate by intent.

- Instance name/description/contact -> Athena instance config (`/api/v1/admin/instance/config/*`)
- OAuth/provider credentials -> Athena environment + admin OAuth client config
- Moderation defaults/blocklists -> Athena moderation endpoints
- Federation settings (ActivityPub/ATProto) -> Athena federation config

## 5) Cutover (DNS/Proxy)

Recommended sequence:

1. Put PeerTube in maintenance/read-only mode.
2. Run final incremental data and storage sync.
3. Run post-import validation in Athena.
4. Switch reverse proxy/DNS to Athena.
5. Monitor errors, playback, auth, and federation behavior.

## 6) Post-Cutover Validation

- Login/auth flow works (including OAuth2 clients).
- Channel pages and subscription feeds load correctly.
- Video playback works across representative content.
- Comments/playlists/captions are present and linked.
- Instance metadata and oEmbed endpoints respond.
- ActivityPub discovery endpoints return expected data.
- Athena-only integrations that are enabled for the target deployment still work as expected.

## 7) Rollback Plan

- Keep PeerTube database and storage snapshots until validation completes.
- Preserve prior proxy config for rapid rollback.
- If critical migration defects are found, route traffic back to PeerTube and remediate offline.

## Notes

- Treat this as an execution framework until Athena ships real migration tooling.
- Production migrations should include scripted ETL and dry-run rehearsals.
- For upstream migration context and operational background, see:
  - <https://docs.joinpeertube.org/maintain/migration>
