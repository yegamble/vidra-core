# PeerTube 8.3.1 source compatibility

Date: **2026-11-03**. **PASS within the native source-fixture scope below.**
This verifies the importer source gate through schema **1130**. It does not
qualify a published Vidra image or the SizeTube production database.

## Upstream identity and fixture

The unmodified compiled server from the official
[PeerTube 8.3.1 release](https://github.com/Chocobozzz/PeerTube/releases/tag/v8.3.1)
initialized a blank PostgreSQL database itself. Its signed upstream commit is
`3324876a0b0f846f8b6632470b3d9d53ef74b8b7`. The release archive SHA-256 matches
GitHub's asset digest:

```text
b2026b99ee6a2e07098230bb68fafbad634b44ce5a67a3e48e0b163ba9b5304a
```

The server reports version 8.3.1 and created **76 public tables**, with
`application.migrationVersion = 1130`. The generated schema-only dump SHA-256 is:

```text
1f06ee1a8a3760cd9b271823d3b707e974a06e443a0e1ce946ada534f2d31220
```

Runtime: native macOS/arm64, Node **24.21.0**, PostgreSQL **16.15**, Redis
**8.6.2**, and Go **1.27.3**. Docker Desktop could not launch. The destination
fixture applied all **183** current Vidra up-migration SQL files directly;
this does not test the production installer or migration-ledger probes.

Upstream initialization created the root user/default channel; the real
PeerTube API populated the remaining graph: two local
users/channels, three published videos with public/private/unlisted privacy,
one suspended user, two comments, one regular playlist/item, one accepted local
follow, two chapters, one rating and one English caption. Two videos use
split-audio HLS; the third is a progressive control. The fixture also has actor
avatars, posters, storyboards and three retained upload originals.

The server was stopped before dumping/restoring a separate frozen snapshot.
The importer role has SELECT-only grants and a read-only default; an explicit
write attempt was denied by table permissions. The original HTTP instance was
restarted against its separate database only for lazy-static artwork reads.
No production data, provider storage or cloud resources were used.

## Executed checks

| Check | Observed result |
| --- | --- |
| Schema reader probe | All 27 exported Source methods exercised without SQL errors; populated video, account, relationship, caption and artwork readers returned the expected rows. Remote followers and category-plugin configuration were unseeded. |
| Normal import gate | Preflight detects and accepts 1130 with no force or version acknowledgement. The regression rejects 1131. |
| Preview | Destination catalogue, ledger and media fingerprints remain unchanged. |
| COPY | Expected catalogue/relationship counts, all three privacy tiers and the suspended-account hold survive; zero failed entities. Two avatars, three posters and three storyboard images/maps are carried. |
| HLS bytes | Two ready trees, ten objects; four separate audio/video binary objects totaling 304,276 bytes match source SHA-256 values. One master is intentionally normalized to remove the source subtitle group; the caption is copied independently. |
| Repeat | Zero new imports or failed entities; catalogue, ledger IDs and destination-media fingerprints remain unchanged. |
| Source integrity | All 76 table fingerprints and the complete 52-object source-media inventory remain unchanged. Tested user emails, password hashes and account private keys are absent from importer logs; channel-key log coverage was not asserted. |

Both source gates failed before the bound change: `TestClassifyVersion` rejected
1130 and `TestPeerTubeImportEndToEnd/1130` stopped at preflight. After the fix,
the committed end-to-end cases for **800, 1040 and 1130** passed against isolated
PostgreSQL databases. The 1130 case checks separate-audio and byte-range HLS
dependency hashes after repeat import. These CI fixtures remain constructed
schema subsets; they are distinct from the upstream-created validation above.

The private disposable capture retains the official archive identity,
schema/data snapshot, API-seeding scripts, reader/import probes and sanitized
results. Their existence does not make the local probe a required CI lane.
To reproduce the source check, boot that exact upstream release against an
empty database, create the stated graph through its API, stop it, restore a
SELECT-only snapshot, and run normal preflight/preview/COPY/repeat with local
media and the original artwork HTTP origin. Check source and destination
fingerprints independently.

## Qualification boundary

This proves native source-query and COPY compatibility on the stated fixture.
It does not establish browser decoding, published-image behavior, PostgreSQL 18
runtime acceptance, S3/B2/Spaces transfer, public TLS, malware scanning, source
MFA enrollment, same-domain federation, production scale or recovery.

The comments are both top-level, so this run supplies no real-upstream threaded
reply evidence. Audio-only ladder descriptors remain unsupported as quality
rungs; their audio bytes are retained in the copied HLS trees. Retained upload
originals require the separate preservation change and independent byte checks;
this schema-bound change alone does not acquire them.

Keep representative operator migration and production acceptance open. Follow
the [migration guide](peertube-migration.md) with the actual source inventory,
and qualify the immutable release that will be deployed.
