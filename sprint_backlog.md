# Sprint Backlog: Quality Programme - Sprint 16 (API Contract Reproducibility)

**Programme:** Athena Quality Programme (Sprints 15-20)
**Sprint Goal:** Make the API contract stable and reproducible with CI enforcement
**Sprint Duration:** Mar 3 - Mar 16, 2026
**Previous Sprint:** [Sprint 15 Complete](docs/sprints/SPRINT15_COMPLETE.md)
**Full Programme Details:** [docs/sprints/QUALITY_PROGRAMME.md](docs/sprints/QUALITY_PROGRAMME.md)

---

## Sprint 15 Final Status: COMPLETE

- [x] **P0 Security fixes** - Merged (JWT validation, yt-dlp injection, size limits)
- [x] **P1 CI/stability** - Merged/closed (SQL injection, flaky DB pool, lint config, ClamAV)
- [x] **P2 Security: CORS** - Applied to main (origin-aware validation, Vary: Origin)
- [x] **P2 Security: Privilege escalation** - Verified on main, PR #166 closed
- [x] **OpenAPI generation** - Fixed (HLS wildcard, QualitiesData schema)
- [x] **PR queue cleanup** - Closed 25+ stale/duplicate PRs + 2 resolved security PRs
- [x] **Coverage baseline** - 52.9% documented
- [x] **Build/CI** - All Go checks passing (gofmt, lint, tests, build, vet)

---

## Sprint 16 Tasks

| Task | Est. | Acceptance Criteria |
|------|------|---------------------|
| Add CI job: regenerate OpenAPI types and fail on diff | 5 pts | CI fails if generated code changes |
| Add Postman smoke workflow | 8 pts | Runs on PR; reports failures clearly |
| Document federation "well-known" endpoints | 5 pts | Endpoints in OpenAPI or documented exclusion |
| Add "API review checklist" to PR template | 2 pts | Checklist forces schema review |
| Create API contract policy doc | 3 pts | Source of truth documented |

---

## Remaining Open PRs

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

## Metrics

| Metric | Sprint 15 Start | Sprint 15 End | Sprint 16 Target |
|--------|----------------|---------------|------------------|
| Open PRs | 50+ | 15 | 14 (close Dependabot) |
| P0/P1 issues | 7 | 0 | 0 |
| P2 security | 2 | 0 | 0 |
| Coverage | ~48.7% | 52.9% | 52.9%+ |
| Build | Broken | Passing | Passing |
| OpenAPI CI | None | None | Enforced |

---

## How to Use This Backlog

1. **Pick a task** from the Sprint 16 task list
2. **Update status** in this file when starting work
3. **Create PR** following the checklist in task
4. **Update status** when PR is merged
5. **Run `make validate-all`** before claiming done

### Validation Command
```bash
make validate-all   # or ./scripts/validate-all.sh
```
