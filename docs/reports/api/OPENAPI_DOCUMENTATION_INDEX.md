# OpenAPI Documentation Index

**Last Updated:** 2025-11-30

This document provides a comprehensive index of all OpenAPI documentation and related resources for the Vidra Core backend API.

---

## Quick Links

- **[Audit Report](OPENAPI_AUDIT_REPORT.md)** - Full detailed audit with code examples
- **[Fixes Checklist](OPENAPI_FIXES_CHECKLIST.md)** - Step-by-step fixes with checkboxes
- **[Audit Summary](OPENAPI_AUDIT_SUMMARY.md)** - Executive summary of findings
- **[Validation Script](scripts/validate-openapi.sh)** - Automated validation tool

---

## OpenAPI Specification Files

### Main Specification

| File | Description | Endpoints | Status |
|------|-------------|-----------|--------|
| **openapi.yaml** | Main API specification covering auth, videos, users, messaging | 60+ | ⚠️ Needs updates |

### Feature-Specific Specifications

| File | Feature | Endpoints | Status |
|------|---------|-----------|--------|
| **openapi_2fa.yaml** | Two-factor authentication (legacy) | 5 | ✅ Good |
| **openapi_analytics.yaml** | Video analytics and view tracking | 8 | ✅ Good |
| **openapi_auth_2fa.yaml** | Two-factor authentication (current) | 5 | ✅ Good |
| **openapi_captions.yaml** | Video subtitle/caption management | 6 | ✅ Good |
| **openapi_channels.yaml** | Channel management and subscriptions | 9 | ⚠️ Missing 1 endpoint |
| **openapi_chat.yaml** | Live stream chat | 8 | ❌ Not implemented |
| **openapi_comments.yaml** | Video comments | 7 | ⚠️ Missing unflag endpoint |
| **openapi_federation.yaml** | ActivityPub federation | 12 | ✅ Good |
| **openapi_federation_hardening.yaml** | Federation security and monitoring | 11 | ✅ Good |
| **openapi_imports.yaml** | Video imports from external URLs | 4 | ✅ Good |
| **openapi_livestreaming.yaml** | RTMP live streaming and HLS | 10 | ⚠️ Missing 2 endpoints |
| **openapi_moderation.yaml** | Abuse reports and blocklists | 8 | ✅ Good |
| **openapi_payments.yaml** | IOTA cryptocurrency payments | 5 | ❌ Missing /api/v1 prefix |
| **openapi_plugins.yaml** | Plugin system | 12 | ❌ Not implemented |
| **openapi_ratings_playlists.yaml** | Video ratings and playlists | 12 | ⚠️ Path mismatches |
| **openapi_redundancy.yaml** | Video redundancy system | 11 | ❌ Not implemented |
| **openapi_uploads.yaml** | Chunked video uploads | 5 | ✅ Good |

### Documentation Directory

| File | Feature | Endpoints | Status |
|------|---------|-----------|--------|
| **docs/openapi_notifications.yaml** | User notifications | 6 | ❌ Missing /api/v1 prefix |

---

## Endpoint Coverage by Feature

### ✅ Fully Documented & Implemented

1. **Authentication** (openapi.yaml)
   - POST /auth/register
   - POST /auth/login
   - POST /auth/refresh
   - POST /auth/logout

2. **OAuth2** (openapi.yaml)
   - POST /oauth/token
   - GET/POST /oauth/authorize
   - POST /oauth/revoke
   - POST /oauth/introspect

3. **Two-Factor Authentication** (openapi_auth_2fa.yaml)
   - POST /auth/2fa/setup
   - POST /auth/2fa/verify-setup
   - POST /auth/2fa/disable
   - POST /auth/2fa/regenerate-backup-codes
   - GET /auth/2fa/status

4. **Video Management** (openapi.yaml)
   - GET /api/v1/videos
   - GET /api/v1/videos/search
   - GET /api/v1/videos/{id}
   - POST /api/v1/videos
   - PUT /api/v1/videos/{id}
   - DELETE /api/v1/videos/{id}

5. **Chunked Uploads** (openapi_uploads.yaml)
   - POST /api/v1/uploads/initiate
   - POST /api/v1/uploads/{sessionId}/chunks
   - POST /api/v1/uploads/{sessionId}/complete
   - GET /api/v1/uploads/{sessionId}/status
   - GET /api/v1/uploads/{sessionId}/resume

6. **Video Imports** (openapi_imports.yaml)
   - POST /api/v1/videos/imports/
   - GET /api/v1/videos/imports/
   - GET /api/v1/videos/imports/{id}
   - DELETE /api/v1/videos/imports/{id}

7. **Captions** (openapi_captions.yaml)
   - GET /api/v1/videos/{id}/captions
   - POST /api/v1/videos/{id}/captions
   - GET /api/v1/videos/{id}/captions/{captionId}/content
   - PUT /api/v1/videos/{id}/captions/{captionId}
   - DELETE /api/v1/videos/{id}/captions/{captionId}

8. **Analytics** (openapi_analytics.yaml)
   - POST /api/v1/videos/{id}/views
   - GET /api/v1/videos/{id}/analytics
   - GET /api/v1/videos/{id}/stats/daily
   - GET /api/v1/videos/top
   - GET /api/v1/trending
   - POST /api/v1/views/fingerprint

9. **Moderation** (openapi_moderation.yaml)
   - POST /api/v1/abuse-reports
   - GET /api/v1/admin/abuse-reports
   - GET /api/v1/admin/abuse-reports/{id}
   - PUT /api/v1/admin/abuse-reports/{id}
   - DELETE /api/v1/admin/abuse-reports/{id}
   - POST /api/v1/admin/blocklist
   - GET /api/v1/admin/blocklist
   - DELETE /api/v1/admin/blocklist/{id}

10. **Federation** (openapi_federation.yaml, openapi_federation_hardening.yaml)
    - All ActivityPub endpoints
    - Admin federation management
    - Federation hardening and monitoring

### ⚠️ Partially Documented (Needs Updates)

1. **Live Streaming** (openapi_livestreaming.yaml)
   - ✅ GET /api/v1/streams/{id}
   - ✅ PUT /api/v1/streams/{id}
   - ✅ POST /api/v1/streams/{id}/end
   - ✅ HLS endpoints
   - ❌ POST /api/v1/streams (create) - NOT DOCUMENTED
   - ❌ GET /api/v1/streams/active - NOT DOCUMENTED

2. **Channels** (openapi_channels.yaml)
   - ✅ Most endpoints documented
   - ❌ GET /api/v1/users/me/channels - NOT DOCUMENTED

3. **Comments** (openapi_comments.yaml)
   - ✅ GET, POST, PUT, DELETE documented
   - ✅ POST /api/v1/comments/{commentId}/flag documented
   - ❌ DELETE /api/v1/comments/{commentId}/flag - NOT DOCUMENTED

4. **Ratings & Playlists** (openapi_ratings_playlists.yaml)
   - ✅ Endpoints exist
   - ❌ Path mismatches: /api/v1/user/ratings vs /api/v1/users/me/ratings
   - ❌ Path mismatches: /api/v1/playlists/watch-later vs /api/v1/users/me/watch-later

5. **Payments** (openapi_payments.yaml)
   - ✅ Endpoints documented
   - ❌ Missing /api/v1 prefix in all paths

6. **Notifications** (docs/openapi_notifications.yaml)
   - ✅ Endpoints documented
   - ❌ Missing /api/v1 prefix in all paths

### ❌ Documented but Not Implemented

1. **Plugin System** (openapi_plugins.yaml)
   - 12 endpoints documented
   - No routes registered in routes.go
   - **Decision needed:** Implement or move to /api/planned/

2. **Stream Chat** (openapi_chat.yaml)
   - 8 endpoints documented
   - Handlers exist but routes not registered
   - **Decision needed:** Implement or move to /api/planned/

3. **Redundancy** (openapi_redundancy.yaml)
   - 11 endpoints documented
   - No implementation found
   - **Decision needed:** Implement or move to /api/planned/

4. **E2EE Messaging** (openapi.yaml)
   - 4 endpoints documented
   - Handlers exist but routes not registered
   - **Decision needed:** Implement or move to /api/planned/

5. **Video Categories** (openapi.yaml)
   - 5 endpoints documented
   - Handler exists but routes not registered
   - **Decision needed:** Register routes

---

## Schema Documentation

### Core Models

| Model | File | Status | Notes |
|-------|------|--------|-------|
| **User** | openapi.yaml | ⚠️ Incomplete | Missing: subscriber_count, twofa_enabled, email_verified, avatar |
| **Video** | openapi.yaml | ❌ Outdated | Missing: 13+ federation and S3 fields |
| **Channel** | openapi_channels.yaml | ✅ Good | - |
| **Comment** | openapi_comments.yaml | ✅ Good | - |
| **Notification** | docs/openapi_notifications.yaml | ✅ Good | - |
| **LiveStream** | openapi_livestreaming.yaml | ✅ Good | - |
| **VideoCategory** | openapi.yaml | ❓ Unknown | May need verification |
| **Playlist** | openapi_ratings_playlists.yaml | ✅ Good | - |
| **Rating** | openapi_ratings_playlists.yaml | ✅ Good | - |

### Response Wrappers

| Schema | Status | Notes |
|--------|--------|-------|
| **SuccessResponse** | ❌ Missing | Needs to be added to all specs |
| **ErrorResponse** | ❌ Missing | Needs to be added to all specs |
| **ErrorInfo** | ⚠️ Partial | Exists in some specs, not all |
| **Meta** | ⚠️ Partial | Pagination metadata, inconsistent |

---

## Authentication & Security

### Security Schemes

All authenticated endpoints use JWT Bearer tokens:

```yaml
components:
  securitySchemes:
    bearerAuth:
      type: http
      scheme: bearer
      bearerFormat: JWT
```

**Status:** ⚠️ Not all OpenAPI files define this scheme

### Protected Endpoints

Endpoints requiring authentication should specify:

```yaml
security:
  - bearerAuth: []
```

**Status:** ⚠️ Some protected endpoints missing security declaration

---

## Validation & Testing

### Automated Validation

Run the validation script:

```bash
./scripts/validate-openapi.sh
```

This checks for:

- OpenAPI 3.0 compliance
- Path prefix consistency
- Security scheme definitions
- Response wrapper usage
- Common documentation issues

### Manual Testing

1. **Generate API Client:**

   ```bash
   npx @openapitools/openapi-generator-cli generate \
     -i api/openapi.yaml \
     -g typescript-axios \
     -o /tmp/vidra-client
   ```

2. **Import to Postman:**
   - Import any OpenAPI file
   - Test endpoints with generated examples

3. **Lint with Spectral:**

   ```bash
   npx @stoplight/spectral-cli lint api/*.yaml
   ```

---

## Common Issues & Solutions

### Issue 1: Path Prefix Missing

**Problem:** Paths documented as `/payments/wallet` instead of `/api/v1/payments/wallet`

**Files Affected:**

- openapi_payments.yaml
- docs/openapi_notifications.yaml

**Solution:** Add `/api/v1` prefix to all paths

### Issue 2: Response Wrapper Not Documented

**Problem:** Actual API returns `{data, success, meta}` but OpenAPI shows direct response

**Files Affected:** All

**Solution:** Create common response wrapper schema and reference in all responses

### Issue 3: Schema Drift

**Problem:** Go domain models have more fields than OpenAPI schemas

**Models Affected:**

- User (missing 5+ fields)
- Video (missing 13+ fields)

**Solution:** Update OpenAPI schemas to match domain models

### Issue 4: Unimplemented Features

**Problem:** Endpoints documented but routes not registered

**Features Affected:**

- Plugins (12 endpoints)
- Chat (8 endpoints)
- Redundancy (11 endpoints)
- E2EE (4 endpoints)

**Solution:** Either implement or move to /api/planned/

---

## Development Workflow

### When Adding a New Endpoint

1. **Implement Handler** in `/internal/httpapi/handlers/`
2. **Register Route** in `/internal/httpapi/routes.go`
3. **Update OpenAPI** in appropriate `/api/*.yaml` file
4. **Run Validation** with `./scripts/validate-openapi.sh`
5. **Test with Postman** using imported OpenAPI spec

### When Modifying an Endpoint

1. **Update Handler** code
2. **Update OpenAPI** specification
3. **Update Request/Response** examples
4. **Run Validation**
5. **Update Integration Tests**

### When Deprecating an Endpoint

1. **Add `deprecated: true`** to OpenAPI spec
2. **Add Deprecation Notice** in description
3. **Document Migration Path**
4. **Keep Endpoint Active** for at least 2 minor versions
5. **Remove in Next Major** version

Example:

```yaml
/api/v1/old-endpoint:
  get:
    deprecated: true
    summary: "[DEPRECATED] Use /api/v1/new-endpoint instead"
    description: |
      This endpoint is deprecated and will be removed in v2.0.0.
      Please migrate to GET /api/v1/new-endpoint.
```

---

## Maintenance Checklist

### Weekly

- [ ] Check for new endpoints without OpenAPI docs
- [ ] Review pull requests for API changes

### Monthly

- [ ] Run full validation suite
- [ ] Update examples with realistic data
- [ ] Review and update error response documentation

### Quarterly

- [ ] Full audit (like this one)
- [ ] Schema synchronization with domain models
- [ ] Review deprecated endpoints
- [ ] Update Postman collections

### Annually

- [ ] Major OpenAPI version updates
- [ ] Restructure documentation if needed
- [ ] Review and consolidate specification files

---

## Related Documentation

- **API Design Guide** - (TODO: Create this)
- **Error Codes Reference** - (TODO: Create this)
- **Rate Limiting Guide** - (TODO: Create this)
- **Authentication Guide** - (TODO: Create this)
- **Federation Guide** - See `/docs/federation/`
- **Deployment Guide** - See `/docs/deployment/`

---

## Contributing

When contributing to OpenAPI documentation:

1. **Follow OpenAPI 3.0 Specification**
2. **Use Consistent Naming** (kebab-case for paths, camelCase for parameters)
3. **Include Examples** for all request/response bodies
4. **Document All Error Cases**
5. **Add Operation IDs** for code generation
6. **Use Tags** to group related endpoints
7. **Run Validation** before committing

---

## Tools & Resources

### Validation Tools

- [Spectral](https://stoplight.io/open-source/spectral) - OpenAPI linter
- [OpenAPI Generator](https://openapi-generator.tech/) - Client/server code generation
- [Swagger Editor](https://editor.swagger.io/) - Online OpenAPI editor

### Documentation Tools

- [Redoc](https://redocly.github.io/redoc/) - API documentation viewer
- [Swagger UI](https://swagger.io/tools/swagger-ui/) - Interactive API explorer
- [Postman](https://www.postman.com/) - API testing and documentation

### Reference

- [OpenAPI 3.0 Specification](https://spec.openapis.org/oas/v3.0.3)
- [OpenAPI Style Guide](https://apistylebook.com/design/topics/openapi)
- [REST API Best Practices](https://restfulapi.net/)

---

**Last Audit:** 2025-11-30
**Next Audit Due:** 2026-02-28 (quarterly)
**Audit Reports:** See OPENAPI_AUDIT_REPORT.md
