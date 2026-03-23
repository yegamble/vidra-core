# OpenAPI Audit Summary

**Date:** 2025-11-30
**Auditor:** Claude (Documentation Engineer)
**Project:** Vidra Core Backend API

---

## Quick Stats

| Metric | Count |
|--------|-------|
| **Total Implemented Endpoints** | 100+ |
| **Total Documented Endpoints** | 80+ (across 17 files) |
| **Undocumented Endpoints** | 25+ |
| **Path Mismatches** | 6 |
| **Schema Fields Missing** | 20+ |
| **OpenAPI Files** | 17 |

---

## Top 5 Critical Issues

### 1. Path Prefix Missing (CRITICAL)

**Files:** `openapi_payments.yaml`, `docs/openapi_notifications.yaml`
**Fix:** Add `/api/v1` prefix to all paths
**Impact:** Documentation shows wrong URLs, breaking API client generation
**Time to Fix:** 10 minutes

### 2. Response Wrapper Not Documented (CRITICAL)

**All Files**
**Issue:** Actual API wraps all responses in `{data, success, meta}` but OpenAPI shows direct responses
**Impact:** Generated clients have wrong response types
**Time to Fix:** 30 minutes

### 3. Video Schema Outdated (CRITICAL)

**File:** `openapi.yaml`
**Missing:** 13+ fields (federation fields, S3 storage fields)
**Impact:** Frontend can't access new video features
**Time to Fix:** 1 hour

### 4. User Schema Incomplete (CRITICAL)

**File:** `openapi.yaml`
**Missing:** `subscriber_count`, `twofa_enabled`, `email_verified`, `avatar` structure
**Impact:** User profile features undocumented
**Time to Fix:** 30 minutes

### 5. Uncertain Feature Status (HIGH)

**Files:** `openapi_plugins.yaml`, `openapi_chat.yaml`, `openapi_redundancy.yaml`
**Issue:** Extensively documented but routes not registered in `routes.go`
**Impact:** Confusion about what's implemented vs planned
**Time to Fix:** 1 hour (decide + implement OR move to planned/)

---

## Undocumented Endpoints

### Live Streams

- ❌ `POST /api/v1/streams` (create stream)
- ❌ `GET /api/v1/streams/active` (list active)

### Comments

- ❌ `DELETE /api/v1/comments/{commentId}/flag` (unflag comment)

### Channels

- ❌ `GET /api/v1/users/me/channels` (user's channels)

---

## Path Mismatches

| OpenAPI Path | Actual Implementation |
|--------------|----------------------|
| `/payments/*` | `/api/v1/payments/*` |
| `/notifications/*` | `/api/v1/notifications/*` |
| `/api/v1/user/ratings` | `/api/v1/users/me/ratings` |
| `/api/v1/playlists/watch-later` | `/api/v1/users/me/watch-later` |

---

## Potentially Unimplemented Features

These are documented but routes not found in `routes.go`:

1. **Plugin System** (10+ endpoints) - `openapi_plugins.yaml`
2. **Stream Chat** (8 endpoints) - `openapi_chat.yaml`
3. **Redundancy** (10+ endpoints) - `openapi_redundancy.yaml`
4. **E2EE Messaging** (4 endpoints) - Main `openapi.yaml`
5. **Video Categories** (5 endpoints) - Main `openapi.yaml`

**Decision Required:** Implement or move to `/api/planned/`

---

## Schema Updates Needed

### User Model

Missing in OpenAPI but in `internal/domain/user.go`:

- `subscriber_count: int64`
- `twofa_enabled: bool`
- `email_verified: bool`
- `email_verified_at: timestamp`
- `avatar: { id, ipfs_cid, webp_ipfs_cid }`

### Video Model

Missing in OpenAPI but in `internal/domain/video.go`:

**Federation Fields:**

- `is_remote: bool`
- `remote_uri: string`
- `remote_actor_uri: string`
- `remote_video_url: string`
- `remote_instance_domain: string`
- `remote_thumbnail_url: string`
- `remote_last_synced_at: timestamp`

**S3 Storage Fields:**

- `s3_urls: map[string]string`
- `storage_tier: string`
- `s3_migrated_at: timestamp`
- `local_deleted: bool`

**Nested Objects:**

- `channel: Channel`
- `category: VideoCategory`
- `metadata: VideoMetadata`

---

## Recommended Action Plan

### Day 1: Critical Fixes (2.5 hours)

1. ✅ Fix path prefixes in payments and notifications specs
2. ✅ Add response wrapper schema
3. ✅ Update User schema
4. ✅ Update Video schema (start)

### Day 2: High Priority (2 hours)

5. ✅ Decide on plugin/chat/redundancy/E2EE implementation
6. ✅ Fix path mismatches (user ratings, watch-later)
7. ✅ Document missing live stream endpoints
8. ✅ Update Video schema (complete)

### Day 3: Medium Priority (2 hours)

9. ✅ Standardize pagination parameters
10. ✅ Add security schemes to all files
11. ✅ Verify/implement category endpoints

**Total Effort:** ~6.5 hours

---

## Validation Steps

After fixes:

```bash
# 1. Lint OpenAPI files
npx @stoplight/spectral-cli lint api/*.yaml docs/*.yaml

# 2. Generate TypeScript client to verify schemas
npx @openapitools/openapi-generator-cli generate \
  -i api/openapi.yaml \
  -g typescript-axios \
  -o /tmp/vidra-api-client

# 3. Test compilation
cd /tmp/vidra-api-client && npm install && npm run build

# 4. Update Postman collection
# Import updated OpenAPI files to Postman
```

---

## Documentation Maintenance Going Forward

### Recommended Practices

1. **Pre-Commit Hook:** Remind devs to update OpenAPI when changing endpoints
2. **CI/CD Check:** Add `spectral lint` to GitHub Actions
3. **Quarterly Audits:** Re-run this audit every 3 months
4. **API Changelog:** Maintain `api/CHANGELOG.md` for breaking changes
5. **Schema Sync:** Consider generating Go types from OpenAPI or vice versa

### File Organization

Current structure:

```
api/
  ├── openapi.yaml (main spec)
  ├── openapi_2fa.yaml
  ├── openapi_analytics.yaml
  ├── openapi_auth_2fa.yaml
  ├── openapi_captions.yaml
  ├── openapi_channels.yaml
  ├── openapi_chat.yaml
  ├── openapi_comments.yaml
  ├── openapi_federation.yaml
  ├── openapi_federation_hardening.yaml
  ├── openapi_imports.yaml
  ├── openapi_livestreaming.yaml
  ├── openapi_moderation.yaml
  ├── openapi_payments.yaml
  ├── openapi_plugins.yaml
  ├── openapi_ratings_playlists.yaml
  ├── openapi_redundancy.yaml
  └── openapi_uploads.yaml

docs/
  └── openapi_notifications.yaml
```

Recommended improvement:

```
api/
  ├── openapi.yaml (combined or main entry point)
  ├── schemas/
  │   ├── common.yaml (shared response wrappers)
  │   ├── user.yaml
  │   ├── video.yaml
  │   └── ...
  ├── paths/
  │   ├── auth.yaml
  │   ├── videos.yaml
  │   └── ...
  └── planned/
      ├── plugins.yaml
      └── chat.yaml
```

---

## Files Generated by This Audit

1. **OPENAPI_AUDIT_REPORT.md** - Full detailed report with code examples
2. **OPENAPI_FIXES_CHECKLIST.md** - Step-by-step checklist with checkboxes
3. **OPENAPI_AUDIT_SUMMARY.md** - This file (executive summary)

---

## Conclusion

The Vidra Core API has a solid foundation, but documentation has drifted from implementation. The main issues are:

✅ **Good:**

- Well-structured codebase
- Extensive OpenAPI coverage
- Clear separation of concerns
- Good handler organization

⚠️ **Needs Work:**

- Path prefix consistency
- Response wrapper documentation
- Schema drift (User, Video models)
- Uncertain implementation status for some features

**With ~6.5 hours of focused work, the OpenAPI documentation can be brought fully in sync with the implementation.**

---

**Next Steps:**

1. Review this summary with the team
2. Decide on unimplemented features (implement vs defer)
3. Work through OPENAPI_FIXES_CHECKLIST.md
4. Set up automated validation
5. Establish ongoing maintenance process

**Questions?** See `OPENAPI_AUDIT_REPORT.md` for detailed analysis and code examples.
