# Quick Wins - Immediate Improvements Checklist

This document provides a prioritized list of quick, low-risk improvements you can make right now.

## Immediate Actions (< 1 hour)

### 1. Remove Test Binaries from Repository ⚡
**Time:** 5 minutes | **Risk:** None | **Impact:** High

```bash
# Remove test binaries
git rm *.test

# Commit the change
git commit -m "chore: remove test binaries from version control"
```

**Why:** These 10 test binaries total 106MB and should not be in version control. They're already in `.gitignore` but were committed before the rule was added.

---

### 2. Remove Temporary Directories 🧹
**Time:** 15 minutes | **Risk:** None | **Impact:** Medium

```bash
# Remove or document the tmp directory
rm -rf internal/usecase/tmp/

# Move misplaced test file
mkdir -p tests/manual
mv test_encoding_simple.go tests/manual/

# Commit
git add -A
git commit -m "chore: clean up temporary and misplaced files"
```

---

### 3. Move Test Fixtures to Proper Location 📁 ✅ COMPLETED
**Time:** 30 minutes | **Risk:** Low | **Impact:** Medium

**Status:** Resolved - `/internal/httpapi/storage/` has been removed. Tests now correctly use `/storage/` at the project root, which is properly managed via `.gitignore` and `config.StorageDir`.

---

## Short-term Improvements (1-4 hours)

### 4. Archive Sprint Documentation 📚
**Time:** 30 minutes | **Risk:** None | **Impact:** Low

```bash
# Create archive directory
mkdir -p docs/archive

# Move sprint docs
mv docs/sprints docs/archive/sprints

# Update README references if needed

# Commit
git add -A
git commit -m "docs: archive historical sprint documentation"
```

**Why:** 27 sprint documents (552KB) are historical and clutter the docs directory. Archiving maintains history while improving organization.

---

### 5. Consolidate Architecture Documentation 📖
**Time:** 1 hour | **Risk:** None | **Impact:** Medium

**Current Issues:**
- `docs/architecture.md` - Main architecture doc
- `docs/claude/architecture.md` - Duplicate/variant
- `docs/architecture/README.md` - Another variant

**Action Plan:**
1. Compare all three architecture documents
2. Merge unique content into `docs/architecture/README.md`
3. Remove duplicates
4. Update references

```bash
# After manual review and merge
git rm docs/claude/architecture.md
# Update docs/architecture.md if needed

git commit -m "docs: consolidate architecture documentation"
```

---

### 6. Create Documentation Index 📑
**Time:** 45 minutes | **Risk:** None | **Impact:** High

Create a comprehensive `docs/README.md` that serves as the master index:

```markdown
# Athena Documentation

## Getting Started
- [Main README](../README.md)
- [Quick Start Guide](deployment/README.md)

## Architecture
- [Architecture Overview](architecture/README.md)
- [API Design](API_EXAMPLES.md)

## API Reference
- [OpenAPI Specifications](../api/README.md)
- [OAuth2 Guide](OAUTH2.md)
- [Notifications API](NOTIFICATIONS_API.md)

## Deployment
- [Docker Deployment](deployment/docker.md)
- [Production Guide](../PRODUCTION.md)
- [Security Guide](deployment/security.md)

## Development
- [Contributing Guide](claude/contributing.md)
- [Runbooks](claude/runbooks.md)

## Security
- [Security Overview](deployment/security.md)
- [E2EE Implementation](../SECURITY_E2EE.md)
- [Penetration Test Report](../SECURITY_PENTEST_REPORT.md)

## Reference
- [PeerTube Compatibility](PEERTUBE_COMPAT.md)
- [Postman Collections](postman.md)
- [Historical Sprints](archive/sprints/README.md)
```

---

## Medium-term Improvements (4-8 hours)

### 7. Organize HTTP Handlers into Subdirectories 🗂️
**Time:** 3-4 hours | **Risk:** Low | **Impact:** High

**Before:**
```
internal/httpapi/
├── videos.go (1,293 lines)
├── users.go
├── channels.go
... (88 more files)
```

**After:**
```
internal/httpapi/
├── handlers/
│   ├── auth/
│   │   ├── login.go
│   │   ├── register.go
│   │   └── oauth.go
│   ├── video/
│   │   ├── create.go
│   │   ├── list.go
│   │   ├── upload.go
│   │   └── search.go
│   ├── channel/
│   ├── livestream/
│   ├── social/
│   └── federation/
├── routes.go
└── server.go
```

**Implementation Steps:**

1. Create handler subdirectories:
```bash
cd internal/httpapi
mkdir -p handlers/{auth,video,channel,livestream,social,moderation,federation,admin}
```

2. Move handlers by domain (example):
```bash
# Auth handlers
mv oauth.go handlers/auth/
mv register.go handlers/auth/
# etc.

# Video handlers
mv videos.go handlers/video/
mv upload_handlers.go handlers/video/
mv encoding.go handlers/video/
# etc.
```

3. Update package declarations in moved files:
```go
// Change from:
package httpapi

// To:
package auth  // or video, channel, etc.
```

4. Update imports in routes.go:
```go
import (
    "athena/internal/httpapi/handlers/auth"
    "athena/internal/httpapi/handlers/video"
    // etc.
)
```

5. Run tests to verify:
```bash
go test ./internal/httpapi/...
```

---

### 8. Consolidate Integration Tests 🧪
**Time:** 3-4 hours | **Risk:** Low | **Impact:** Medium

**Current:** 19 integration tests scattered in `/internal/httpapi/`

**Target:**
```
tests/
├── integration/
│   ├── auth_test.go
│   ├── video_upload_test.go
│   ├── channel_subscriptions_test.go
│   ├── federation_test.go
│   ├── activitypub_test.go
│   ├── moderation_test.go
│   └── ...
└── fixtures/
    ├── videos/
    ├── images/
    └── data/
```

**Steps:**

1. Create structure:
```bash
mkdir -p tests/integration
mkdir -p tests/fixtures/{videos,images,data}
```

2. Move integration tests:
```bash
mv internal/httpapi/*_integration_test.go tests/integration/
```

3. Update package declarations:
```go
package integration_test
```

4. Update imports and test helpers

5. Run integration tests:
```bash
go test ./tests/integration/...
```

---

### 9. Improve Repository Test Coverage 📈
**Time:** 4-6 hours | **Risk:** Low | **Impact:** High

**Current:** 10% coverage (very low)
**Target:** 40-50% coverage

**Focus Areas:**
1. User repository (authentication paths)
2. Video repository (CRUD operations)
3. Auth repository (session management)
4. Notification repository

**Template for table-driven tests:**
```go
func TestUserRepository_Create(t *testing.T) {
    tests := []struct {
        name    string
        input   *domain.User
        wantErr bool
    }{
        {
            name:    "valid user",
            input:   &domain.User{Username: "test", Email: "test@example.com"},
            wantErr: false,
        },
        {
            name:    "duplicate username",
            input:   &domain.User{Username: "existing", Email: "new@example.com"},
            wantErr: true,
        },
        // Add more cases
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // Test implementation
        })
    }
}
```

---

## Checklist Summary

- [ ] Remove test binaries (5 min)
- [ ] Clean up temporary directories (15 min)
- [ ] Move test fixtures (30 min)
- [ ] Archive sprint documentation (30 min)
- [ ] Consolidate architecture docs (1 hour)
- [ ] Create documentation index (45 min)
- [ ] Organize HTTP handlers (3-4 hours)
- [ ] Consolidate integration tests (3-4 hours)
- [ ] Improve repository test coverage (4-6 hours)

**Total Estimated Time:** 14-20 hours
**Risk Level:** Low overall
**Impact:** High - significantly improved code organization and maintainability

---

## Metrics to Track

**Before:**
- httpapi package: 92 files, 13MB
- Integration tests: 19 files scattered
- Documentation: 55 files across 10+ directories
- Repository test coverage: 10%

**After (Target):**
- httpapi package: Organized into 8 subdirectories
- Integration tests: Consolidated in `/tests/integration/`
- Documentation: Clear structure with master index
- Repository test coverage: 40%+

---

## Notes

- All changes are **low risk** as they primarily involve file movement and organization
- **Test coverage** should remain stable or improve
- **No breaking changes** to external APIs
- **Git history** is preserved through moves
- Can be done **incrementally** over multiple PRs

---

**Last Updated:** October 26, 2025
