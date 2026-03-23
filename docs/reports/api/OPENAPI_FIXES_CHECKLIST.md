# OpenAPI Documentation Fixes - Quick Checklist

**Generated:** 2025-11-30
**Full Report:** See `OPENAPI_AUDIT_REPORT.md`

## Critical Fixes (Do First - ~2.5 hours)

### 1. Fix Path Prefixes (10 minutes)

- [ ] **openapi_payments.yaml** - Add `/api/v1` prefix to all paths
  - Change `/payments/wallet` → `/api/v1/payments/wallet`
  - Change `/payments/intents` → `/api/v1/payments/intents`
  - Change `/payments/transactions` → `/api/v1/payments/transactions`

- [ ] **docs/openapi_notifications.yaml** - Add `/api/v1` prefix to all paths
  - Change `/notifications` → `/api/v1/notifications`
  - Change `/notifications/{id}` → `/api/v1/notifications/{id}`
  - Change `/notifications/unread-count` → `/api/v1/notifications/unread-count`
  - Change `/notifications/stats` → `/api/v1/notifications/stats`
  - Change `/notifications/{id}/read` → `/api/v1/notifications/{id}/read`
  - Change `/notifications/read-all` → `/api/v1/notifications/read-all`

### 2. Add Response Wrapper Schema (30 minutes)

- [ ] Create `api/schemas/common.yaml` with:

  ```yaml
  SuccessResponse:
    type: object
    required: [success]
    properties:
      data: {}
      success:
        type: boolean
      meta:
        $ref: '#/components/schemas/Meta'

  ErrorResponse:
    type: object
    required: [success, error]
    properties:
      success:
        type: boolean
        example: false
      error:
        $ref: '#/components/schemas/ErrorInfo'

  ErrorInfo:
    type: object
    properties:
      code:
        type: string
      message:
        type: string
      details:
        type: string

  Meta:
    type: object
    properties:
      total:
        type: integer
        format: int64
      limit:
        type: integer
      offset:
        type: integer
      page:
        type: integer
      pageSize:
        type: integer
  ```

- [ ] Update all endpoint responses to wrap data in this structure
- [ ] Reference common schemas from all OpenAPI files

### 3. Update User Schema (30 minutes)

- [ ] **openapi.yaml** - Add missing User fields:

  ```yaml
  User:
    properties:
      # ... existing fields ...
      subscriber_count:
        type: integer
        format: int64
      twofa_enabled:
        type: boolean
      email_verified:
        type: boolean
      email_verified_at:
        type: string
        format: date-time
        nullable: true
      avatar:
        type: object
        nullable: true
        properties:
          id:
            type: string
          ipfs_cid:
            type: string
            nullable: true
          webp_ipfs_cid:
            type: string
            nullable: true
  ```

### 4. Update Video Schema (1 hour)

- [ ] **openapi.yaml** - Add federation fields:

  ```yaml
  Video:
    properties:
      # ... existing fields ...
      is_remote:
        type: boolean
      remote_uri:
        type: string
        nullable: true
      remote_actor_uri:
        type: string
        nullable: true
      remote_video_url:
        type: string
        nullable: true
      remote_instance_domain:
        type: string
        nullable: true
      remote_thumbnail_url:
        type: string
        nullable: true
      remote_last_synced_at:
        type: string
        format: date-time
        nullable: true
  ```

- [ ] Add S3 storage fields:

  ```yaml
      s3_urls:
        type: object
        additionalProperties:
          type: string
      storage_tier:
        type: string
      s3_migrated_at:
        type: string
        format: date-time
        nullable: true
      local_deleted:
        type: boolean
  ```

- [ ] Add nested objects:

  ```yaml
      channel:
        $ref: '#/components/schemas/Channel'
      category:
        $ref: '#/components/schemas/VideoCategory'
      metadata:
        $ref: '#/components/schemas/VideoMetadata'
  ```

- [ ] Create VideoMetadata schema if missing

## High Priority Fixes (~2 hours)

### 5. Resolve Undocumented/Unimplemented Features (1 hour)

For each feature, decide: **Implement** or **Move to /api/planned/**

- [ ] **Plugins** - 10+ endpoints in `openapi_plugins.yaml`
  - Decision: [ ] Implement routes OR [ ] Move to planned
  - If implementing: Register routes in `routes.go`
  - If planning: `mkdir -p api/planned && mv api/openapi_plugins.yaml api/planned/`

- [ ] **Chat** - 8 endpoints in `openapi_chat.yaml`
  - Decision: [ ] Implement routes OR [ ] Move to planned
  - Handler exists: `handlers/messaging/chat_handlers.go`

- [ ] **Redundancy** - 10+ endpoints in `openapi_redundancy.yaml`
  - Decision: [ ] Implement routes OR [ ] Move to planned

- [ ] **E2EE Messaging** - 4 endpoints in `openapi.yaml`
  - Decision: [ ] Implement routes OR [ ] Move to planned
  - Handler exists: `handlers/messaging/secure_messages.go`

### 6. Fix Path Mismatches (10 minutes)

- [ ] **openapi_ratings_playlists.yaml**
  - Change `/api/v1/user/ratings` → `/api/v1/users/me/ratings`
  - Change `/api/v1/playlists/watch-later` → `/api/v1/users/me/watch-later`

### 7. Document Missing Endpoints (20 minutes)

- [ ] **openapi_livestreaming.yaml** - Add:

  ```yaml
  /api/v1/streams:
    post:
      summary: Create a new live stream
      # ... full spec ...

  /api/v1/streams/active:
    get:
      summary: Get active live streams
      # ... full spec ...
  ```

- [ ] **openapi_comments.yaml** - Add:

  ```yaml
  /api/v1/comments/{commentId}/flag:
    delete:
      summary: Unflag a comment
      operationId: unflagComment
      responses:
        '204':
          description: Flag removed successfully
  ```

## Medium Priority (~2 hours)

### 8. Standardize Pagination (1 hour)

- [ ] Create shared pagination parameter definitions
- [ ] Update all list endpoints to use consistent pagination
- [ ] Document that both page/pageSize AND limit/offset are supported

### 9. Add Security Schemes (30 minutes)

- [ ] Ensure all OpenAPI files have:

  ```yaml
  components:
    securitySchemes:
      bearerAuth:
        type: http
        scheme: bearer
        bearerFormat: JWT
  ```

- [ ] Add `security: [{bearerAuth: []}]` to protected endpoints

### 10. Category Endpoints (30 minutes)

- [ ] Verify `video_category_handler.go` implementation
- [ ] Decision: [ ] Register routes OR [ ] Remove from OpenAPI
- [ ] If registering, add to `routes.go`:

  ```go
  r.Get("/api/v1/categories", video.ListCategoriesHandler(...))
  r.Get("/api/v1/categories/{id}", video.GetCategoryHandler(...))
  r.Route("/api/v1/admin/categories", func(r chi.Router) {
      r.Use(middleware.RequireRole("admin"))
      r.Post("/", video.CreateCategoryHandler(...))
      r.Put("/{id}", video.UpdateCategoryHandler(...))
      r.Delete("/{id}", video.DeleteCategoryHandler(...))
  })
  ```

## Verification Steps

After making fixes:

- [ ] Run OpenAPI linter:

  ```bash
  npm install -g @stoplight/spectral-cli
  spectral lint api/*.yaml docs/*.yaml
  ```

- [ ] Generate API client to test:

  ```bash
  npx @openapitools/openapi-generator-cli generate \
    -i api/openapi.yaml \
    -g typescript-axios \
    -o /tmp/api-client-test
  ```

- [ ] Test critical endpoints with updated specs
- [ ] Update Postman collection from OpenAPI
- [ ] Run integration tests

## Files Modified Checklist

Mark files as you update them:

- [ ] `/Users/yosefgamble/github/athena/api/openapi.yaml`
- [ ] `/Users/yosefgamble/github/athena/api/openapi_payments.yaml`
- [ ] `/Users/yosefgamble/github/athena/docs/openapi_notifications.yaml`
- [ ] `/Users/yosefgamble/github/athena/api/openapi_ratings_playlists.yaml`
- [ ] `/Users/yosefgamble/github/athena/api/openapi_livestreaming.yaml`
- [ ] `/Users/yosefgamble/github/athena/api/openapi_comments.yaml`
- [ ] `/Users/yosefgamble/github/athena/api/openapi_plugins.yaml` (or moved to planned/)
- [ ] `/Users/yosefgamble/github/athena/api/openapi_chat.yaml` (or moved to planned/)
- [ ] `/Users/yosefgamble/github/athena/api/openapi_redundancy.yaml` (or moved to planned/)
- [ ] `/Users/yosefgamble/github/athena/api/schemas/common.yaml` (new file)

## Routes File Updates (if implementing missing features)

- [ ] `/Users/yosefgamble/github/athena/internal/httpapi/routes.go`
  - Add plugin routes (if implementing)
  - Add chat routes (if implementing)
  - Add redundancy routes (if implementing)
  - Add E2EE routes (if implementing)
  - Add category routes (if missing)

---

**Total Estimated Time:**

- Critical: ~2.5 hours
- High Priority: ~2 hours
- Medium Priority: ~2 hours
- **Grand Total: ~6.5 hours**

**Recommended Approach:**

1. Start with Critical fixes (paths and schemas) - Day 1
2. Make implementation decisions for missing features - Day 1
3. Complete High Priority fixes - Day 2
4. Medium Priority as time permits - Day 3

**Note:** See `OPENAPI_AUDIT_REPORT.md` for detailed explanations and code examples.
