# Sprint Update: 2026-02-13

**Sprint 15 Goal:** Stabilize mainline; integrate PR queue; establish coverage baseline.

## Status: COMPLETE

### Summary

Sprint 15 is fully complete. All acceptance criteria met:

- **P0 Security PRs:** All merged (JWT validation, yt-dlp injection, request size limits)
- **P1 CI/Stability:** All resolved (SQL injection fix, flaky DB pool tests, lint config, ClamAV)
- **P2 Security: CORS:** Applied to main - CORS middleware now uses `CORSAllowedOrigins` config, reflects origin instead of wildcard `*`, adds `Vary: Origin` header. 9 test cases.
- **P2 Security: Privilege Escalation:** Verified on main (`RequireRole("admin")` + regression test). PR #166 closed.
- **OpenAPI generation:** Working (HLS wildcard path, QualitiesData schema fixed)
- **PR queue cleanup:** 50+ open PRs reduced to 15
- **Coverage baseline:** 52.9% established and documented
- **Build/CI:** All Go checks passing (gofmt, lint, tests, build, vet)

### Metrics

| Metric | Before | After |
|--------|--------|-------|
| Open PRs | 50+ | 15 |
| P0 security issues | 3 | 0 |
| P1 CI issues | 4 | 0 |
| P2 security issues | 2 | 0 |
| Coverage | ~48.7% | 52.9% |
| Build | Broken | Passing |
| Test functions | 2,139 | 2,364 |

### Next: Sprint 16 - API Contract Reproducibility

See [Sprint 15 Complete](docs/sprints/SPRINT15_COMPLETE.md) for full details.
See [Sprint Backlog](sprint_backlog.md) for Sprint 16 tasks.
