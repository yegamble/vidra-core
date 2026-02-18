# Documentation Accuracy Audit

**Date:** 2026-02-15

---

## CLAUDE.md Files

### Root CLAUDE.md - `/CLAUDE.md`

**Status: Mostly Accurate**

| Claim | Actual | Match? |
|-------|--------|--------|
| Go (Chi router) | Correct | Yes |
| PostgreSQL (SQLX) | Correct | Yes |
| Redis | Correct | Yes |
| IPFS | Correct | Yes |
| FFmpeg | Correct | Yes |
| IOTA payments (Phase 2) | Correct (partial implementation) | Yes |
| `make validate-all` command | Exists in Makefile | Yes |
| `make docker` command | Exists in Makefile | Yes |
| Project layout paths | All exist | Yes |

**Issues Found:**

- None. Root CLAUDE.md is concise and accurate.

### `internal/httpapi/CLAUDE.md`

**Status: Accurate but Generic**

The documentation describes patterns well but uses generic examples rather than actual code from the codebase. The chunked upload flow description matches the actual routes in `routes.go`.

**Minor Issue:** The upload route described as `PUT /api/v1/uploads/{sessionId}/chunks/{index}` doesn't match the actual route `POST /uploads/{sessionId}/chunks` (POST not PUT, no index param).

### `internal/security/CLAUDE.md`

**Status: Not Checked** (not read in this audit - would need verification)

### `internal/activitypub/CLAUDE.md`

**Status: Not Checked** (not read in this audit - would need verification)

### `migrations/CLAUDE.md`

**Status: Not Checked** (not read in this audit - would need verification)

### `docs/architecture/CLAUDE.md`

**Status: Not Checked** (not read in this audit - would need verification)

---

## PROJECT_COMPLETE.md - Major Discrepancies

**File:** `docs/sprints/PROJECT_COMPLETE.md`
**Status: SIGNIFICANTLY OUTDATED**

This file was written October 2025, before the Quality Programme (Sprints 15-20). It has multiple factual errors:

| Claim | Actual | Action |
|-------|--------|--------|
| "719+ automated tests" | **3,752 tests** (5x higher) | **Update** |
| "42,886 lines of production code" | ~78,329 lines production + ~167,213 lines test | **Update** |
| "14 sprints across 7 months" | **20 sprints** (14 feature + 6 quality) | **Update** |
| "Go 1.21+" | **Go 1.24** (per go.mod) | **Update** |
| "Go-Atlas" for migrations | **Goose** (migrated in Sprint 19) | **Update** |
| "52 database migrations" | **61 migration files** | **Update** |
| Coverage ">85% for core components" | 62.3% average (90%+ for core packages) | **Update** |
| "Total Migrations: 52" in schema section | **61 migration files** | **Update** |
| Sprint 3-4 not documented in sprint list | Missing from completion doc | **Add** |
| Sprint 11 not documented | Missing from completion doc | **Add** |

**Recommendation:** This file should be updated to reflect Sprint 20 final numbers, or marked as historical with a link to CHANGELOG.md as the authoritative source.

---

## CHANGELOG.md

**Status: Accurate and Current**

The CHANGELOG.md was created in Sprint 20 and contains the most up-to-date information:

| Claim | Verified? |
|-------|-----------|
| 3,752 automated tests | Yes (confirmed by test run) |
| 313 test files | Yes (confirmed by file count) |
| 62.3% average coverage | Plausible (not independently verified) |
| Go 1.24 | Matches go.mod |
| Goose for migrations | Correct |
| Zero lint issues | Plausible (not independently verified) |
| Known coverage gaps documented | Yes (federation at 72.2%) |
| GO-2026-4337 vulnerability documented | Yes |

**No issues found.** CHANGELOG.md is the most accurate documentation file.

---

## .claude/rules/project.md

**Status: Accurate**

| Claim | Verified? |
|-------|-----------|
| Go 1.24 | Yes |
| Chi router v5 | Yes |
| PostgreSQL with SQLX | Yes |
| Goose migrations | Yes |
| 3,752 automated tests (313 test files) | Yes |
| 62.3% average coverage | Consistent with CHANGELOG |
| Sprint 20/20 complete | Yes |
| All referenced directory paths exist | Yes |
| All referenced CLAUDE.md files exist | Yes |

**No issues found.**

---

## Sprint Documentation

### Missing Sprint Completion Docs

The following sprints don't have completion docs in `docs/sprints/`:

- Sprint 3 (no SPRINT3_COMPLETE.md)
- Sprint 4 (no SPRINT4_COMPLETE.md)
- Sprint 11 (no SPRINT11_COMPLETE.md)
- Sprint 15 (no SPRINT15_COMPLETE.md)
- Sprint 16 (no SPRINT16_COMPLETE.md)
- Sprint 17 (no SPRINT17_COMPLETE.md)
- Sprint 18 (no SPRINT18_COMPLETE.md)
- Sprint 19 (no SPRINT19_COMPLETE.md)
- Sprint 20 (no SPRINT20_COMPLETE.md)

The Quality Programme sprints (15-20) are documented in CHANGELOG.md but don't have individual completion docs. Sprints 3-4 (Lettered Sprints A-K) are documented in `peertube_compatibility.md` instead.

### Stale Sprint Progress Files

These files reference in-progress work that was completed long ago:

- `SPRINT1_PROGRESS.md`
- `SPRINT5_PROGRESS.md`
- `SPRINT6_PROGRESS.md`
- `SPRINT7_PROGRESS.md`
- `SPRINT8_PROGRESS.md`
- `SPRINT13_PROGRESS.md`

**Recommendation:** These PROGRESS files are historical artifacts. Either delete them or add a note at the top: "Historical: This sprint was completed. See SPRINT*_COMPLETE.md."

---

## API Documentation (OpenAPI)

**18 OpenAPI spec files** found in `api/`:

| Spec File | Coverage |
|-----------|----------|
| `openapi.yaml` | Main spec |
| `openapi_analytics.yaml` | Analytics endpoints |
| `openapi_auth_2fa.yaml` | 2FA endpoints |
| `openapi_captions.yaml` | Caption endpoints |
| `openapi_channels.yaml` | Channel endpoints |
| `openapi_chat.yaml` | Chat endpoints |
| `openapi_comments.yaml` | Comment endpoints |
| `openapi_federation.yaml` | Federation endpoints |
| `openapi_federation_hardening.yaml` | Hardening endpoints |
| `openapi_imports.yaml` | Import endpoints |
| `openapi_livestreaming.yaml` | Live stream endpoints |
| `openapi_moderation.yaml` | Moderation endpoints |
| `openapi_payments.yaml` | Payment endpoints |
| `openapi_plugins.yaml` | Plugin endpoints |
| `openapi_ratings_playlists.yaml` | Ratings/playlists |
| `openapi_redundancy.yaml` | Redundancy endpoints |
| `openapi_uploads.yaml` | Upload endpoints |
| `openapi_2fa.yaml` | 2FA (duplicate?) |

**Potential Issue:** Two 2FA specs (`openapi_2fa.yaml` and `openapi_auth_2fa.yaml`) may be duplicative. Verify and consolidate.

**Missing specs for routes in routes.go:**

- `/api/v1/trending` - Not in any spec
- `/api/v1/views/fingerprint` - Not in any spec
- `/api/v1/messages/*` - Not in any spec
- `/api/v1/conversations/*` - Not in any spec
- `/api/v1/admin/views/*` - Not in any spec
- `/api/v1/admin/instance/config/*` - Not in any spec
- `/api/v1/admin/oauth/clients/*` - Not in any spec
- `/api/v1/instance/about` - Not in any spec
- `/oembed` - Not in any spec
- `/.well-known/atproto-did` - Not in any spec
- `/api/v1/ipfs/*` - Not in any spec

---

## Summary

| Document | Status | Priority |
|----------|--------|----------|
| CLAUDE.md (root) | Accurate | None |
| .claude/rules/project.md | Accurate | None |
| CHANGELOG.md | Accurate and current | None |
| PROJECT_COMPLETE.md | **Significantly outdated** | **High - Update or deprecate** |
| httpapi/CLAUDE.md | Minor route discrepancy | Low |
| Sprint progress files (6 files) | Stale/historical | Low |
| OpenAPI specs | Missing ~11 endpoint groups | Medium |
| 2FA OpenAPI | Possible duplicate specs | Low |
| Sprint 15-20 completion docs | Not created | Low (covered by CHANGELOG) |
