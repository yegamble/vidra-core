# Documentation Synchronization Guide

**Purpose:** Ensure API documentation stays consistent with code changes
**Audience:** Developers, Claude AI agents, documentation maintainers
**Last Updated:** 2025-11-18

---

## Quick Reference

### When Code Changes, Update These Docs

| Code Change Type | Documentation to Update | Priority |
|------------------|------------------------|----------|
| New API endpoint | OpenAPI spec + Postman collection | **HIGH** |
| Modified endpoint signature | OpenAPI spec + Postman tests | **HIGH** |
| New error code | OpenAPI spec examples | **HIGH** |
| Security feature change | OpenAPI descriptions + security sections | **CRITICAL** |
| Rate limit change | OpenAPI spec + README | **HIGH** |
| New repository interface | Code comments (if internal only) | **MEDIUM** |
| Public-facing interface | OpenAPI spec | **HIGH** |
| New feature | Sprint docs + CLAUDE.md + README | **MEDIUM** |
| Breaking change | OpenAPI spec + migration guide | **CRITICAL** |

---

## Documentation Structure Overview

```
/root/vidra/
├── api/                           # OpenAPI specifications
│   ├── README.md                  # API documentation index
│   ├── openapi.yaml               # Main API spec (auth, videos, core)
│   ├── openapi_*.yaml             # Modular specs by domain (18 files)
│   └── [other OpenAPI files]
├── postman/                       # E2E test collections
│   ├── vidra-auth.postman_collection.json
│   ├── vidra-uploads.postman_collection.json
│   ├── vidra-imports.postman_collection.json
│   ├── vidra-virus-scanner-tests.postman_collection.json
│   └── vidra-analytics.postman_collection.json
├── docs/
│   ├── architecture/
│   │   └── CLAUDE.md              # Architectural patterns & guidelines
│   ├── sprints/
│   │   └── SPRINT*_COMPLETE.md    # Sprint completion reports
│   ├── development/
│   │   └── CI_CD_CONFIGURATION.md
│   ├── API_DOCUMENTATION_AUDIT_REPORT.md
│   └── DOCUMENTATION_SYNC_GUIDE.md (this file)
└── internal/httpapi/
    └── routes.go                  # Source of truth for routes
```

---

## Step-by-Step Sync Process

### 1. Adding a New API Endpoint

#### Steps

1. **Implement handler** in `/root/vidra/internal/httpapi/handlers/`
2. **Register route** in `/root/vidra/internal/httpapi/routes.go`
3. **Choose OpenAPI file** based on domain:
   - Auth/OAuth → `openapi.yaml` or `openapi_auth_2fa.yaml`
   - Uploads → `openapi_uploads.yaml`
   - Imports → `openapi_imports.yaml`
   - Comments → `openapi_comments.yaml`
   - Analytics → `openapi_analytics.yaml`
   - Federation → `openapi_federation.yaml`
   - etc.

4. **Add endpoint to OpenAPI spec**:

   ```yaml
   /api/v1/your/endpoint:
     get:
       summary: Brief description
       description: Detailed description
       tags: [Domain]
       security:
         - bearerAuth: []  # if auth required
       parameters:
         - name: id
           in: path
           required: true
           schema:
             type: string
       responses:
         '200':
           description: Success
           content:
             application/json:
               schema:
                 $ref: '#/components/schemas/YourResponse'
               examples:
                 success_case:
                   value: { "data": {...}, "success": true }
         '400':
           description: Bad request
           content:
             application/json:
               schema:
                 $ref: '#/components/schemas/ErrorResponse'
   ```

5. **Add request/response schemas** to `components/schemas` section

6. **Create Postman request** in appropriate collection:
   - Add test scripts to validate response structure
   - Include positive and negative test cases
   - Save example responses

7. **Update `/root/vidra/api/README.md`**:
   - Add endpoint to coverage table
   - Update endpoint count
   - Note any special features

8. **Write integration test** matching OpenAPI spec

#### Example: Adding a Comment Count Endpoint

**Code:**

```go
// routes.go
r.Get("/{videoId}/comments/count", commentHandlers.GetCommentCount)

// comments.go
func (h *CommentHandlers) GetCommentCount(w http.ResponseWriter, r *http.Request) {
    videoID := chi.URLParam(r, "videoId")
    activeOnly := r.URL.Query().Get("activeOnly") == "true"
    count, err := h.commentService.CountByVideo(r.Context(), videoID, activeOnly)
    // ... error handling
    shared.WriteJSON(w, http.StatusOK, map[string]int{"count": count})
}
```

**OpenAPI:**

```yaml
# In openapi_comments.yaml
/api/v1/videos/{videoId}/comments/count:
  get:
    summary: Get comment count for video
    tags: [Comments]
    parameters:
      - name: videoId
        in: path
        required: true
        schema:
          type: string
          format: uuid
      - name: activeOnly
        in: query
        schema:
          type: boolean
          default: true
    responses:
      '200':
        description: Comment count
        content:
          application/json:
            schema:
              type: object
              properties:
                count:
                  type: integer
```

**Postman:**

```json
{
  "name": "Get Comment Count",
  "request": {
    "method": "GET",
    "url": "{{baseUrl}}/api/v1/videos/{{video_id}}/comments/count?activeOnly=true"
  },
  "event": [{
    "listen": "test",
    "script": {
      "exec": [
        "pm.test('Status is 200', () => { pm.expect(pm.response.code).to.equal(200); });",
        "pm.test('Has count field', () => { pm.expect(pm.response.json()).to.have.property('count'); });"
      ]
    }
  }]
}
```

---

### 2. Modifying an Existing Endpoint

#### Steps

1. **Update handler implementation**
2. **Update OpenAPI spec** to match new signature:
   - Modify parameters
   - Update request body schema
   - Update response schemas
   - Add new error codes if applicable
   - Update examples

3. **Update Postman collection**:
   - Modify request structure
   - Update test assertions
   - Add tests for new error cases

4. **Update integration tests**

5. **If breaking change**:
   - Increment API version in OpenAPI `info.version`
   - Add deprecation notice to old endpoint
   - Create migration guide
   - Update README with breaking change notice

---

### 3. Adding Security Features

**Examples:** Virus scanning, SSRF protection, rate limiting

#### Steps

1. **Implement security feature** in middleware or handler

2. **Document in OpenAPI spec description**:

   ```yaml
   description: |
     This endpoint includes virus scanning for all uploads.

     **Security Features:**
     - ClamAV virus scanning (strict mode)
     - Max file size: 10GB
     - Allowed types: video/mp4, video/webm, etc.

     **Error Codes:**
     - `VIRUS_DETECTED` (422) - File failed virus scan
     - `SCANNER_UNAVAILABLE` (503) - Scanner temporarily down
   ```

3. **Add error response examples**:

   ```yaml
   '422':
     description: Virus detected
     content:
       application/json:
         schema:
           $ref: '#/components/schemas/ErrorResponse'
         examples:
           virus_detected:
             value:
               error:
                 code: "VIRUS_DETECTED"
                 message: "File failed virus scan"
                 details:
                   virus_name: "EICAR-Test-Signature"
   ```

4. **Create Postman security tests**:
   - EICAR test file upload (should fail with 422)
   - Scanner unavailable simulation
   - Rate limit tests

5. **Update main README** and security policy

6. **Update CLAUDE.md** if architectural pattern changes

---

### 4. Changing Rate Limits

#### Steps

1. **Update code** in `routes.go`:

   ```go
   strictImportLimiter := rlManager.CreateRateLimiter(60*time.Second, 10) // 10 per minute
   ```

2. **Update OpenAPI spec** (in relevant spec file):

   ```yaml
   description: |
     **Rate Limiting:** 10 requests per minute
   ```

3. **Update OpenAPI responses** to include 429:

   ```yaml
   '429':
     description: Rate limit exceeded
     content:
       application/json:
         schema:
           $ref: '#/components/schemas/ErrorResponse'
         examples:
           rate_limited:
             value:
               error:
                 code: "RATE_LIMIT_EXCEEDED"
                 message: "Too many requests. Maximum 10 per minute."
   ```

4. **Update Postman collection** with rate limit tests

5. **Update README.md** rate limit table (if exists)

---

### 5. Completing a Sprint with New Features

#### Steps

1. **Create sprint completion document** in `/root/vidra/docs/sprints/`
   - Document all features added
   - Note breaking changes
   - Include code statistics
   - List new endpoints

2. **Update CLAUDE.md** if architectural patterns changed:
   - New middleware patterns
   - New repository patterns
   - New storage patterns
   - New security patterns

3. **Update API README.md**:
   - Add new OpenAPI files to index
   - Update coverage statistics
   - Update changelog section

4. **Update Postman collections** with new workflow tests

---

## Automation Recommendations

### Pre-Commit Hooks

Create a pre-commit hook to validate documentation:

```bash
#!/bin/bash
# .git/hooks/pre-commit

# Check if any API handler files changed
HANDLER_CHANGES=$(git diff --cached --name-only | grep "internal/httpapi/handlers/")

if [ -n "$HANDLER_CHANGES" ]; then
    echo "⚠️  API handler changes detected!"
    echo "Remember to update:"
    echo "  1. OpenAPI specs in /api/"
    echo "  2. Postman collections in /postman/"
    echo "  3. README.md if adding new endpoints"
    echo ""
    read -p "Have you updated documentation? (y/n) " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        echo "❌ Commit aborted. Please update documentation first."
        exit 1
    fi
fi

# Validate OpenAPI specs
for spec in api/openapi*.yaml; do
    if ! swagger-cli validate "$spec" 2>/dev/null; then
        echo "❌ Invalid OpenAPI spec: $spec"
        exit 1
    fi
done

echo "✅ All checks passed"
```

### CI/CD Integration

Add documentation validation to CI pipeline:

```yaml
# .github/workflows/docs-validation.yml
name: Documentation Validation

on: [pull_request]

jobs:
  validate-openapi:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - name: Validate OpenAPI specs
        run: |
          npm install -g @apidevtools/swagger-cli
          for spec in api/openapi*.yaml; do
            echo "Validating $spec..."
            swagger-cli validate "$spec"
          done

  check-coverage:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - name: Check endpoint coverage
        run: |
          # Extract routes from routes.go
          ROUTES=$(grep -r "r\.Get\|r\.Post\|r\.Put\|r\.Delete" internal/httpapi/routes.go | wc -l)
          # Extract paths from OpenAPI specs
          DOCUMENTED=$(grep -h "^  /api/" api/openapi*.yaml | sort -u | wc -l)

          COVERAGE=$((DOCUMENTED * 100 / ROUTES))
          echo "API Documentation Coverage: $COVERAGE%"

          if [ $COVERAGE -lt 95 ]; then
            echo "❌ Coverage below 95%!"
            exit 1
          fi
```

---

## Documentation Quality Checklist

Use this checklist when updating documentation:

### OpenAPI Specification

- [ ] Endpoint path matches code
- [ ] HTTP method is correct
- [ ] All parameters documented (path, query, header)
- [ ] Request body schema defined (if applicable)
- [ ] Response schemas for all status codes
- [ ] Success example provided
- [ ] Error examples for common cases (400, 401, 404, 500)
- [ ] Security requirements specified
- [ ] Tags/categories assigned
- [ ] Summary and description clear
- [ ] Rate limits documented (if applicable)

### Postman Collection

- [ ] Request matches OpenAPI spec
- [ ] Environment variables used correctly
- [ ] Pre-request scripts set up properly
- [ ] Test scripts validate response structure
- [ ] Test scripts check status codes
- [ ] Examples saved for common responses
- [ ] Error cases tested
- [ ] Collection organized logically

### Code Comments

- [ ] Public interfaces documented with GoDoc comments
- [ ] Complex logic explained
- [ ] Security considerations noted
- [ ] Error conditions documented

### Integration Tests

- [ ] Test cases match OpenAPI examples
- [ ] Error cases covered
- [ ] Edge cases tested
- [ ] Test names descriptive

---

## Common Pitfalls to Avoid

### ❌ Don't

1. **Update code without updating OpenAPI**
   - Leads to client SDK generation failures
   - Breaks API consumers' expectations

2. **Add endpoints without Postman tests**
   - No automated validation
   - Regressions go unnoticed

3. **Forget to document error codes**
   - Client developers don't know how to handle errors
   - Poor error handling in integrations

4. **Skip security documentation**
   - Critical for client trust
   - Security features go unused

5. **Use inconsistent naming**
   - Confuses API consumers
   - Breaks automated tooling

### ✅ Do

1. **Update OpenAPI and Postman together**
   - Ensures consistency
   - Catches discrepancies early

2. **Include realistic examples**
   - Helps client developers
   - Serves as integration test templates

3. **Document security features prominently**
   - Builds trust
   - Ensures features are used correctly

4. **Version breaking changes**
   - Clear migration path
   - Prevents client breakage

5. **Use schema references**
   - DRY principle
   - Easier maintenance

---

## Tools and Resources

### OpenAPI Tools

- **Swagger UI:** Interactive API documentation viewer
- **Swagger CLI:** Validation tool (`swagger-cli validate`)
- **OpenAPI Generator:** Generate client SDKs
- **Redoc:** Alternative documentation viewer

### Postman Tools

- **Newman:** CLI test runner
- **Postman CLI:** Command-line testing
- **Collection Runner:** Automated test execution

### Validation Scripts

```bash
# Validate all OpenAPI specs
make validate-openapi

# Run Postman collections
make test-postman

# Check documentation coverage
make docs-coverage
```

---

## Contact and Support

### For Documentation Issues

1. **File an issue** with label `documentation`
2. **Check existing docs** in `/docs/` before asking
3. **Review audit report** at `/docs/API_DOCUMENTATION_AUDIT_REPORT.md`

### For Claude AI Agents

When making code changes:

1. Always read this guide first
2. Update documentation in the same commit
3. Run validation scripts before committing
4. Include documentation changes in PR description

---

## Version History

| Version | Date | Changes |
|---------|------|---------|
| 1.0 | 2025-11-18 | Initial documentation sync guide created |

---

**Next Review:** After next major sprint or API version bump
**Maintained By:** Documentation team and Claude AI agents
