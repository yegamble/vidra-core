# Sprint Backlog: Quality Programme - Sprint 15 (Stabilize & Integrate)

**Programme:** Athena Quality Programme (Sprints 15-20)
**Sprint Goal:** Merge/close/resolve the high-impact PR queue; stabilize mainline
**Sprint Duration:** Feb 16 - Mar 2, 2026
**Full Programme Details:** [docs/sprints/QUALITY_PROGRAMME.md](docs/sprints/QUALITY_PROGRAMME.md)

---

## Sprint 15 Progress Summary

### Completed

- [x] **P0 Security fixes** - Already merged to main (JWT validation, ytdlp, size limits)
- [x] **OpenAPI generation** - Fixed on main (HLS wildcard path, QualitiesData schema)
- [x] **PR queue cleanup** - Closed 25+ stale/duplicate PRs
- [x] **README update** - Reflects Quality Programme status
- [x] **Build verification** - `make build` and `make lint` pass
- [x] **PR #206** - SQL injection fix merged (parameterized ORDER BY clause)
- [x] **PR #238** - Flaky DB pool tests fixed (timeouts, retry loops)
- [x] **PR #234** - Closed (lint config already working on main)
- [x] **PR #240** - Closed (ClamAV CI optimization deferred - had conflicts)

### Remaining Work

| Priority | PR(s) | Focus | Status |
|----------|-------|-------|--------|
| **P2** | #244, #228 | Comment repo/service tests | Coverage uplift |
| **P2** | #184 | CORS defaults fix | Security review |
| **P2** | #166 | Privilege escalation fix | Security review |

---

## Remaining Open PRs (Triage)

### P1: CI & Security (Complete)

All P1 items resolved:
- ✅ #206 merged - SQL injection fix
- ✅ #238 merged - Flaky DB pool tests fix
- ✅ #234 closed - Lint config already working
- ✅ #240 closed - ClamAV CI optimization deferred

### P2: Test Coverage (Sprint 17-18)

| PR # | Title | Action |
|------|-------|--------|
| #244 | CommentRepository tests | Review for Sprint 17 |
| #228 | CommentService tests | Review for Sprint 17 |
| #211 | UpdateVideoHandler tests | Review for Sprint 17 |
| #194 | Video update/delete handler tests | Review for Sprint 17 |
| #177 | VideoRepository schema tests | Review for Sprint 17 |
| #201 | PluginHandler tests | Review for Sprint 17 |
| #188 | Plugin handler tests | Review for Sprint 17 |
| #204 | Admin Instance Handler tests | Review for Sprint 17 |
| #170 | Upload handler test refactor | Review for Sprint 17 |
| #171 | Auth rate limiting tests | Review for Sprint 17 |

### P2: Security (Sprint 15-16)

| PR # | Title | Action |
|------|-------|--------|
| #184 | Fix insecure CORS defaults | Security review |
| #166 | Fix privilege escalation in user creation | Security review |

### P3: Features/Fixes (Backlog)

| PR # | Title | Action |
|------|-------|--------|
| #239 | Production environment setup script | Ops review |
| #183 | User ID extraction for analytics | Feature review |
| #181 | Fix missing ChannelID in queries | Bug fix review |
| #164 | Batch stream reminder notifications | Performance review |
| #155 | User ID in video analytics | Feature review |

### Dependabot (Auto-merge when CI green)

| PR # | Title |
|------|-------|
| #144 | Bump golang.org/x/crypto |
| #143 | Bump aws-sdk-go-v2 |
| #142 | Bump aws-sdk-go-v2/service/s3 |
| #141 | Bump otel/sdk |
| #140 | Bump aws-sdk-go-v2/credentials |
| #139 | Bump aws-sdk-go-v2/feature/s3/manager |
| #138 | Bump actions/cache |
| #137 | Bump actions/download-artifact |
| #136 | Bump actions/upload-artifact |

---

## Sprint 15 Metrics

### Completed

| Metric | Before | After |
|--------|--------|-------|
| Open PRs | 50+ | 21 |
| P0 security issues | 3 | 0 |
| P1 PRs | 4 | 0 ✅ |
| OpenAPI generation | Broken | Working |
| Build status | Broken | Passing |
| Lint status | Unknown | Passing |

### Targets for Sprint End

| Metric | Target | Status |
|--------|--------|--------|
| Open P1 PRs | 0 | ✅ Complete |
| CI passing on main | Yes | ✅ Complete |
| Coverage baseline documented | Yes | ✅ **52.9%** |

---

## Coverage Baseline (Sprint 15)

**Overall: 52.9%** (target for Sprint 20: 60%)

| Package | Coverage | Notes |
|---------|----------|-------|
| usecase/analytics | 97.9% | High |
| scheduler | 90.6% | High |
| middleware | 89.7% | High |
| importer | 87.1% | High |
| storage | 86.2% | High |
| worker | 85.7% | High |
| payments | 83.9% | High |
| rating | 82.8% | High |
| handlers/payments | 82.6% | High |
| channel | 79.2% | Good |
| notification | 79.2% | Good |
| message | 79.7% | Good |
| plugin | 74.0% | Good |
| usecase/payments | 71.9% | Good |
| encoding | 67.9% | Medium |
| handlers/plugin | 67.5% | Medium |
| security | 66.8% | Medium |
| playlist | 65.2% | Medium |
| comment | 63.0% | Medium |
| ipfs | 63.6% | Medium |
| upload | 60.9% | Medium |
| repository | 59.6% | Medium |
| migration | 57.6% | Medium |
| handlers/video | 51.2% | Low - needs tests |
| handlers/social | 49.5% | Low - needs tests |
| usecase | 49.4% | Low - needs tests |
| activitypub | 48.7% | Low - needs tests |
| import | 48.6% | Low - needs tests |

---

## Next Steps

1. ~~**Review P1 PRs** (#240, #238, #234, #206)~~ ✅ Complete
2. ~~**Establish coverage baseline**~~ ✅ **52.9%**
3. **Review P2 Security PRs** (#184, #166) - CORS and privilege escalation
4. **Plan Sprint 16** - API contract reproducibility

---

## How to Use This Backlog

1. **Pick a task** from your assigned priority level
2. **Update status** in this file when starting work
3. **Create PR** following the checklist in task
4. **Update status** when PR is merged
5. **Run `make validate-all`** before claiming done

### Validation Command
```bash
make validate-all   # or ./scripts/validate-all.sh
```

This runs: `gofmt`, `goimports`, `golangci-lint` (with gosec), unit tests, and build verification.
