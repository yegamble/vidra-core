# API Contract Policy

This document defines how API contracts are managed, enforced, and changed in Athena.

## Source of Truth

The **OpenAPI specifications in `api/`** are the single source of truth for all API contracts.

| File | Scope |
|------|-------|
| `api/openapi.yaml` | Core auth, video, and user endpoints |
| `api/openapi_federation.yaml` | ActivityPub, ATProto, and well-known endpoints |
| `api/openapi_livestreaming.yaml` | RTMP, HLS, stream scheduling, chat |
| `api/openapi_uploads.yaml` | Chunked uploads and encoding status |
| `api/openapi_analytics.yaml` | View tracking, retention, channel stats |
| `api/openapi_*.yaml` | Other domain-specific specs (comments, channels, etc.) |

Generated Go types live in `internal/generated/types.go` and are produced from `api/openapi.yaml` using `oapi-codegen v2`.

## Change Process

### Adding or Modifying an Endpoint

1. **Update the OpenAPI spec first** - Edit the relevant `api/openapi_*.yaml` file
2. **Regenerate types** - Run `make generate-openapi`
3. **Implement the handler** - Write or update the handler to match the spec
4. **Add tests** - Unit tests for the handler, verifying request/response shape
5. **Verify locally** - Run `make verify-openapi` to confirm no drift
6. **Submit PR** - CI will enforce spec validity and codegen consistency

### CI Enforcement

The following checks run automatically:

| Check | Workflow | Trigger |
|-------|----------|---------|
| Spec validation (Swagger CLI) | `openapi-ci.yml` | Push/PR touching `api/*.yaml` |
| Generated types drift check | `openapi-ci.yml` | Push/PR touching `api/*.yaml` or `internal/generated/` |
| Postman smoke tests | `postman-smoke.yml` | PR to main |

### Make Targets

```bash
make validate-openapi    # Validate spec syntax
make generate-openapi    # Regenerate types from spec
make verify-openapi      # Regenerate + fail if uncommitted changes
```

## Breaking Changes

A breaking change is any modification that could cause existing clients to fail:

- Removing an endpoint or HTTP method
- Removing or renaming a required field
- Changing a field type
- Narrowing an enum (removing values)
- Changing authentication requirements
- Modifying error response structure

### Breaking Change Process

1. **Avoid if possible** - Add new fields/endpoints instead of modifying existing ones
2. **Document in PR summary** - Clearly state what breaks and why
3. **Provide migration guidance** - How should clients update?
4. **Version if necessary** - For major contract changes, consider a versioned endpoint (e.g., `/api/v2/`)

### Non-Breaking Changes (Safe)

- Adding new optional fields to responses
- Adding new endpoints
- Adding new optional query parameters
- Widening an enum (adding values)
- Adding new error codes (if clients handle unknown codes)

## Federation Endpoints

Federation well-known endpoints follow external specifications and have stricter compatibility requirements:

| Endpoint | Specification | Compatibility |
|----------|--------------|---------------|
| `/.well-known/webfinger` | RFC 7033 | Must match exactly |
| `/.well-known/nodeinfo` | NodeInfo 2.0 | Must match schema |
| `/.well-known/host-meta` | RFC 6415 (LRDD) | Must return valid XRD |
| `/.well-known/atproto-did` | AT Protocol | Must match DID spec |
| `/nodeinfo/2.0` | NodeInfo 2.0 (Diaspora) | Must match schema |

Changes to federation endpoints require extra care:

- Must remain compatible with PeerTube, Mastodon, and other ActivityPub implementations
- Must follow the relevant RFC or protocol specification
- Should be tested against real federation peers when possible

## Review Checklist

Every PR that modifies API contracts should confirm:

- [ ] OpenAPI spec updated to reflect changes
- [ ] Generated types regenerated and committed
- [ ] Request/response schemas include examples
- [ ] Error codes documented
- [ ] Breaking changes identified and documented
- [ ] Federation compatibility preserved (if applicable)

See also: [PR template](../.github/PULL_REQUEST_TEMPLATE.md) for the full checklist.
