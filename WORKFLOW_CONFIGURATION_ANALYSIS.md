# GitHub Actions Workflow Configuration Analysis

## 🔍 Issue Summary

**Only E2E tests are running** because the workflows have **inconsistent trigger configurations**. Most workflows only run on PRs to `main`, while E2E tests accept PRs to both `main` and `develop`.

---

## 📊 Complete Workflow Trigger Matrix

| Workflow | Push Branches | PR Branches | Additional Conditions | Status |
|----------|---------------|-------------|----------------------|--------|
| **e2e-tests.yml** | `main` | `main, develop` | None | ✅ **RUNS** |
| **test.yml** (Main CI) | `main, develop` | `main` only | None | ❌ **BLOCKED** |
| **security-tests.yml** | `main, develop` | `main, develop` | Requires `security` label | ❌ **BLOCKED** (no label) |
| **virus-scanner-tests.yml** | None | `main` only | None | ❌ **BLOCKED** |
| **video-import.yml** | `main, develop` | `main, develop` | Path filters | ⚠️ **CONDITIONAL** |
| **registration-api-tests.yml** | `main, develop` | Any branch | Path filters | ⚠️ **CONDITIONAL** |
| **openapi-ci.yml** | `main, develop` | `main` only | Path filters | ❌ **BLOCKED** |
| **goose-migrate.yml** | Not checked | Not checked | ? | ❓ **UNKNOWN** |
| **blue-green-deploy.yml** | Not checked | Not checked | ? | ❓ **UNKNOWN** |

---

## 🎯 Root Cause Analysis

### Why Only E2E Tests Run

**e2e-tests.yml (lines 6-7):**
```yaml
pull_request:
  branches: [main, develop]  # ✅ Accepts PRs targeting EITHER main OR develop
```

### Why Main CI Tests Don't Run

**test.yml (lines 9-10):**
```yaml
pull_request:
  branches: [ main ]  # ❌ Only accepts PRs targeting main (NOT develop)
```

### Why Security Tests Don't Run

**security-tests.yml (lines 12-18 + line 45):**
```yaml
pull_request:
  branches: [main, develop]  # Would match
  # BUT...
# Line 45:
if: github.event_name != 'pull_request' || contains(github.event.pull_request.labels.*.name, 'security')
# ❌ Requires 'security' label on PR
```

### Why Virus Scanner Tests Don't Run

**virus-scanner-tests.yml (lines 4-5):**
```yaml
pull_request:
  branches: [ main ]  # ❌ Only accepts PRs to main
```

---

## 🚨 Problems with Current Configuration

### 1. **Inconsistency**
Different workflows have different rules for when they run:
- Some accept PRs to `main` only
- Some accept PRs to `main, develop`
- Some require labels
- Some have path filters that may silently skip

### 2. **Poor Developer Experience**
Developers get confused when:
- E2E tests run but unit tests don't
- Security tests don't run even though the code changed
- No clear feedback about WHY tests aren't running

### 3. **Security Risk**
Critical security tests require manual label application:
- Developers might forget to add the `security` label
- Security vulnerabilities could be merged without testing
- The `security` label requirement is not documented in PR templates

### 4. **Wasted CI Resources on E2E Tests**
E2E tests are the MOST expensive to run (45 minutes timeout):
- They run on every PR to develop
- But cheaper unit/integration tests DON'T run
- This is backwards - fast tests should run first

---

## ✅ Recommended Solutions

### Option 1: **Standardize All Workflows** (Recommended)

Make all test workflows consistent and run on the same conditions:

```yaml
# Standard trigger for ALL test workflows
on:
  push:
    branches: [main, develop]
  pull_request:
    branches: [main, develop]  # Run on PRs to either branch
  workflow_dispatch:  # Allow manual triggering
```

**Apply to:**
- ✅ test.yml (Main CI)
- ✅ security-tests.yml (remove label requirement)
- ✅ virus-scanner-tests.yml
- ✅ openapi-ci.yml
- ✅ All other test workflows

**Keep E2E tests as-is** (already correct)

### Option 2: **Use Required Status Checks**

Configure GitHub branch protection to require specific workflows:
1. Go to Settings → Branches → Branch protection rules
2. Require status checks before merging:
   - ✅ CI (test.yml)
   - ✅ Security Tests
   - ✅ Virus Scanner Tests
   - ✅ E2E Tests

This makes it obvious when tests are missing.

### Option 3: **Improve Security Tests Label Requirement**

If you want to keep the label requirement, improve it:

**A. Document in PR Template**
```markdown
## Security Checklist
- [ ] If this PR modifies security-sensitive code, add the `security` label
```

**B. Auto-label based on paths**
```yaml
# .github/workflows/auto-label-security.yml
on:
  pull_request:
    types: [opened, synchronize]

jobs:
  auto-label:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/labeler@v4
        with:
          configuration-path: .github/labeler.yml
```

```yaml
# .github/labeler.yml
security:
  - internal/security/**
  - internal/crypto/**
  - internal/middleware/auth.go
  - internal/middleware/oauth*.go
```

### Option 4: **Two-Tier Testing Strategy**

**Fast tests** (always run):
- Unit tests
- Security tests (no label requirement)
- OpenAPI validation

**Slow tests** (conditional):
- E2E tests (only on `main` branch or with `e2e` label)
- Virus scanner tests (only on `main` branch or affecting relevant paths)
- Performance benchmarks (only on schedule or manual)

---

## 🛠️ Proposed Fixes

### Fix 1: Standardize test.yml

**File:** `.github/workflows/test.yml`

**Change line 9-10 from:**
```yaml
pull_request:
  branches: [ main ]
```

**To:**
```yaml
pull_request:
  branches: [ main, develop ]
```

### Fix 2: Remove Security Label Requirement

**File:** `.github/workflows/security-tests.yml`

**Remove or comment out line 45:**
```yaml
# OLD (line 45):
if: github.event_name != 'pull_request' || contains(github.event.pull_request.labels.*.name, 'security')

# NEW (remove this condition entirely, or change to):
# Run on all PR events without label requirement
```

### Fix 3: Standardize virus-scanner-tests.yml

**File:** `.github/workflows/virus-scanner-tests.yml`

**Change lines 4-5 from:**
```yaml
pull_request:
  branches: [ main ]
```

**To:**
```yaml
pull_request:
  branches: [ main, develop ]
```

### Fix 4: Standardize openapi-ci.yml

**File:** `.github/workflows/openapi-ci.yml`

**Change line 11-12 from:**
```yaml
pull_request:
  branches: [ main ]
```

**To:**
```yaml
pull_request:
  branches: [ main, develop ]
```

---

## 📋 Implementation Checklist

- [ ] **Fix 1:** Update `test.yml` to accept PRs to `develop`
- [ ] **Fix 2:** Remove `security` label requirement from `security-tests.yml`
- [ ] **Fix 3:** Update `virus-scanner-tests.yml` to accept PRs to `develop`
- [ ] **Fix 4:** Update `openapi-ci.yml` to accept PRs to `develop`
- [ ] **Document:** Add PR template with security checklist
- [ ] **Configure:** Set up required status checks in branch protection
- [ ] **Test:** Create a test PR to verify all workflows run
- [ ] **Monitor:** Check workflow run times and adjust if needed

---

## 📈 Expected Outcomes After Fixes

### Before (Current State):
```
PR to develop branch:
✅ E2E Tests (45 min)
❌ Main CI Tests
❌ Security Tests
❌ Virus Scanner Tests
❌ OpenAPI Validation
Total: 45 minutes, 5 workflows blocked
```

### After (With Fixes):
```
PR to develop branch:
✅ Main CI Tests (10 min) - Unit, lint, build
✅ Security Tests (15 min) - 6 security categories
✅ Virus Scanner Tests (30 min) - Comprehensive validation
✅ E2E Tests (45 min) - End-to-end scenarios
✅ OpenAPI Validation (10 min) - API spec validation
Total: ~45 minutes (parallel execution), 0 workflows blocked
```

---

## 🎓 Best Practices Recommendations

1. **Consistency First**: All test workflows should have the same trigger conditions
2. **Fast Feedback**: Cheaper tests (unit, lint) should run before expensive tests (E2E)
3. **Clear Communication**: If tests are skipped, log WHY (path filters, labels, etc.)
4. **Required Checks**: Use GitHub branch protection to enforce critical tests
5. **Documentation**: Document workflow requirements in CONTRIBUTING.md
6. **Monitoring**: Track workflow run times and optimize slow tests
7. **Cost Management**: Use `concurrency.cancel-in-progress: true` for all workflows

---

## 🔗 Related Files

- `.github/workflows/test.yml` - Main CI pipeline
- `.github/workflows/e2e-tests.yml` - E2E tests (working correctly)
- `.github/workflows/security-tests.yml` - Security test suite
- `.github/workflows/virus-scanner-tests.yml` - Virus scanner validation
- `.github/workflows/openapi-ci.yml` - API specification validation
- `COMPREHENSIVE_SECURITY_ANALYSIS.md` - Security audit findings

---

## 📞 Next Steps

1. **Review this analysis** with the team
2. **Decide on a strategy** (Option 1 recommended)
3. **Implement fixes** (estimated 1-2 hours)
4. **Test with a PR** to verify all workflows run
5. **Update documentation** to reflect new workflow behavior
6. **Monitor CI/CD costs** after changes

---

**Generated:** 2025-11-22
**Issue:** Only E2E tests running, other workflows blocked by inconsistent triggers
**Priority:** HIGH - Affects code quality and security testing coverage
