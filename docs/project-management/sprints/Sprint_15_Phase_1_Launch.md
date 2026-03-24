# Sprint 15: Phase 1 Launch Readiness

⚠️ **Active Plan Updated**: This sprint plan has been consolidated into [docs/sprints/quality-programme/SPRINT16_PLAN.md](../../sprints/quality-programme/SPRINT16_PLAN.md). Please refer to that file for the latest execution snapshot from the quality programme rollout.

**Sprint Goal:** Secure the platform, formalize the deployment process, and prepare for initial production launch (Phase 1).

## Scope

### 1. Security Hardening (Priority 1)

- **Credential Rotation:** Create a script (`scripts/rotate-credentials.sh`) to automate the generation of secure credentials for production (JWT, Database, etc.) as required by the security advisory.
- **Git History Cleanup:** Create a guide/script (`scripts/clean-git-history.sh`) to assist in removing sensitive files (`.env`) from git history using `git filter-branch` or `bfg`.
- **Config Review:** Audit and update `.env.example` to ensure no production-unsafe defaults are present.

### 2. Product & Documentation (Priority 2)

- **Roadmap Update:** Update `CLAUDE.md` and `README.md` to explicitly categorize "IOTA Payments" as a Phase 2 feature and "ATProto Integration" as BETA, aligning the documentation with the current implementation status.
- **Monitoring Setup:** Create a `docs/deployment/monitoring/` directory containing:
  - `prometheus.yml` configuration.
  - `docker-compose.monitoring.yml` (or similar) to easily spin up Prometheus and Grafana side-by-side with the main app.
  - Instructions in `docs/deployment/MONITORING.md`.

### 3. QA & Testing

- **Load Testing Integration:** Add a `make load-test` target that runs the existing `tests/loadtest/k6-video-platform.js` script using a Dockerized k6 runner, making it easy to validate performance claims.

## Execution Order

1. **Product Architect:** Update Roadmap/Docs (`CLAUDE.md`, `README.md`).
2. **Sentinel (Security):** Create credential rotation and git cleanup scripts.
3. **Builder (DevOps):** Create monitoring configurations.
4. **QA Guardian:** Integrate load testing into `Makefile`.

## Risks

- **Git Cleanup:** The git history cleanup script is destructive and requires a force push. It will be implemented as a *helper script* with strong warnings and will not be executed automatically by the agent.
- **Load Testing:** Running load tests requires a running instance of the application. The `make load-test` target will assume the app is running or start it.

## Definition of Done

- `CLAUDE.md` and `README.md` accurately reflect Phase 1 vs Phase 2 features.
- Helper scripts for security tasks exist in `scripts/`.
- Monitoring configuration is available in `docs/deployment/monitoring/`.
- `make load-test` triggers the k6 load test.
