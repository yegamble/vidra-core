# Documentation Map - Source of Truth

This map identifies the **canonical source** for each major topic area. When information conflicts, the canonical source is authoritative.

## Navigation Table

| Topic | Canonical Source | Also Referenced In |
|-------|-----------------|-------------------|
| **Project Overview** | `README.md` | `CLAUDE.md`, `.claude/rules/project.md` |
| **Architecture** | `docs/architecture/CLAUDE.md` | `CLAUDE.md` (high-level), `README.md` (stack) |
| **Database Migrations** | `migrations/CLAUDE.md` | `docs/claude/runbooks.md` (operational commands), `docs/development/README.md` (setup section) |
| **API & HTTP Layer** | `internal/httpapi/CLAUDE.md` | `docs/architecture/CLAUDE.md` (overview), `api/` (OpenAPI specs) |
| **Security** | `internal/security/CLAUDE.md` | `docs/security/` (policies, checklists), `docs/architecture/CLAUDE.md` (overview) |
| **Federation (ActivityPub)** | `internal/activitypub/CLAUDE.md` | `docs/federation/` (integration guides), `docs/architecture/CLAUDE.md` (overview) |
| **Testing** | `docs/development/TEST_INFRASTRUCTURE.md` | `docs/development/TEST_EXECUTION_GUIDE.md`, `.claude/rules/testing-patterns.md`, `CLAUDE.md` (quick reference) |
| **Deployment** | `docs/deployment/README.md` | `docs/operations/RUNBOOK.md` (operational procedures), `docker-compose.yml` (service config) |
| **Docker Setup** | `docker-compose.yml` | `CLAUDE.md` (quick commands), `docs/deployment/README.md`, `docs/claude/runbooks.md` |
| **CI/CD** | `.github/workflows/` | `docs/development/TEST_INFRASTRUCTURE.md` (CI test commands), `CONTRIBUTING.md` (workflow) |
| **Monitoring** | `docs/operations/MONITORING.md` | `docs/operations/RUNBOOK.md` (health endpoints), `docs/architecture/CLAUDE.md` (observability stack) |
| **Operations & Troubleshooting** | `docs/operations/RUNBOOK.md` | `docs/claude/runbooks.md` (development commands), `docs/operations/PERFORMANCE.md` (tuning) |
| **Sprint Status & Metrics** | `.claude/rules/project.md` | `README.md` (quick stats), `docs/sprints/QUALITY_PROGRAMME.md` (acceptance criteria) |
| **Code Quality & Validation** | `docs/development/VALIDATION_REQUIRED.md` | `.claude/rules/project.md`, `CLAUDE.md` (mandatory validation section) |
| **Development Workflow** | `CONTRIBUTING.md` | `docs/development/README.md`, `CLAUDE.md` (common commands) |

## How to Use This Map

1. **When writing documentation**: Link to the canonical source rather than duplicating content
2. **When information conflicts**: The canonical source is correct; update other references
3. **When creating new docs**: Add a row to this table to prevent duplication
4. **When unsure**: Check the canonical source first, then cross-references

## Document Categories

### Living Documentation (Actively Maintained)

- All canonical sources in the table above
- All module CLAUDE.md files
- README files (`README.md`, `docs/*/README.md`)
- Operational runbooks (`docs/operations/`, `docs/claude/runbooks.md`)
- Current sprint docs (`docs/sprints/QUALITY_PROGRAMME.md`)

### Historical Documentation (Point-in-Time Records)

- Sprint completion reports (`docs/sprints/SPRINT*_COMPLETE.md`)
- Historical progress files (`docs/project-management/*_PROGRESS.md`)
- Test baseline reports (`docs/development/TEST_BASELINE_REPORT.md`)
- Migration guides for completed migrations (`docs/MIGRATION_TO_GOOSE.md`)

## Topic Overlaps

Some topics naturally appear in multiple places. Here's guidance on what each document should cover:

### Project Overview

- **README.md**: Quick start, installation, key features, project metrics
- **CLAUDE.md**: Claude-specific guidance, validation requirements
- **.claude/rules/project.md**: Full context for Claude including sprint status

### Testing

- **TEST_INFRASTRUCTURE.md**: How to run tests, infrastructure requirements, CI config
- **TEST_EXECUTION_GUIDE.md**: Detailed test execution procedures
- **.claude/rules/testing-patterns.md**: Test writing patterns for Claude

### Operations

- **RUNBOOK.md**: Incident response, troubleshooting, maintenance procedures
- **runbooks.md**: Day-to-day operational commands for development
- **MONITORING.md**: Monitoring setup (Prometheus, Grafana)
- **PERFORMANCE.md**: Performance tuning configuration

## Maintenance

**When updating this map:**

1. Update the canonical source first
2. Update all cross-references to match
3. Add new rows for new topics
4. Remove rows when topics are consolidated

**Last Updated**: 2026-02-15 (Sprint 19)
