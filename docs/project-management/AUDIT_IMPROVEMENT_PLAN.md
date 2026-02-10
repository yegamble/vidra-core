# Athena Project Audit & Improvement Plan v2

**Date**: 2026-02-10
**Status**: Active
**Based on**: Full codebase exploration (architecture, testing, CI/CD, PeerTube compat)

---

## Executive Summary

This audit found the Athena codebase is **significantly more complete than documentation suggests**. The code has 31 implemented subsystems, but `docs/architecture.md` only documents ~17 and marks 3 completed features as "planned." The most critical finding is a **61.2 percentage point gap** between claimed test coverage (85%+) and actual coverage (23.8%). This plan is prioritized by risk and sequenced by dependencies.

### Key Metrics (Actual vs. Documented)

| Metric | Documented | Actual | Gap |
|--------|-----------|--------|-----|
| Test coverage | 85%+ | 23.8% | -61.2pp |
| Subsystems documented | ~17 | 31 | 14 missing |
| "Planned" features | 3 (auth, federation, e2ee) | 0 (all implemented) | 3 stale |
| Handler test coverage | 60%+ target | 7-32% | Severe |
| Repository test coverage | Not mentioned | 9.6% | Critical |
| Moderation handler tests | Expected | 27.1% | Improved, below target |
| Social handler tests | Expected | 31.6% | Improved, below target |

---

## Phase 0: Immediate Corrections (P0 - Day 1)

**Goal**: Fix factual inaccuracies that could mislead contributors or users.

### 0.1 Fix Coverage Claims

The README and TESTING_STRATEGY.md claim 85%+ coverage. The TEST_BASELINE_REPORT.md (Nov 16) shows 23.8%. This discrepancy must be resolved immediately.

- [x] **P0** Update README.md test metrics table to reflect actual coverage (23.8% baseline, target 60%+)
- [x] **P0** Update TESTING_STRATEGY.md coverage targets to be realistic (current: 23.8%, near-term target: 60%, stretch: 80%)
- [ ] **P0** Run `go test -coverprofile=coverage.out ./...` to establish current baseline
- [x] **P0** Add coverage-by-package breakdown to TEST_BASELINE_REPORT.md

Note (2026-02-10): a fresh full-repo `go test -coverprofile=coverage.out ./...` run was attempted but did not complete in this environment due long-running package stalls; current baseline remains 23.8% from `TEST_BASELINE_REPORT.md`.

**Success Criteria**: All documentation reflects actual measured coverage. No aspirational numbers presented as current state.

### 0.2 Fix "Planned" Labels on Completed Features

Architecture doc marks implemented features as planned.

- [x] **P0** In `docs/architecture.md` lines 57-59: Change `auth/ # Authentication (planned)` to `auth/ # Authentication`
- [x] **P0** Change `federation/ # Federation (planned)` to `federation/ # Federation (ATProto + ActivityPub)`
- [x] **P0** Change `e2ee/ # End-to-end encryption (planned)` to `e2ee/ # End-to-end encryption`
- [x] **P0** Remove `pkg/ # Shared utilities (planned)` (line 79) since it doesn't exist and utilities are in subsystem packages

**Success Criteria**: No implemented features marked as "planned" in documentation.

---

## Phase 1: Critical Testing Gaps (P1 - Week 1-2)

**Goal**: Raise coverage from 23.8% to 60%+ by targeting highest-risk zero-coverage areas.

### 1.1 Repository Layer Tests (9.6% -> 60%+)

The repository layer handles all database CRUD. At 9.6% coverage, virtually all persistence logic is untested.

- [x] **P1** Add unit tests for `repository/video_repository.go` (core CRUD + list/search/migration/remote branches via sqlmock)
- [ ] **P1** Add unit tests for `repository/user_repository.go` (auth flows)
- [ ] **P1** Add unit tests for `repository/channel_repository.go` (channel CRUD)
- [ ] **P1** Add unit tests for `repository/comment_repository.go`
- [ ] **P1** Add unit tests for `repository/playlist_repository.go`
- [ ] **P1** Add unit tests for remaining repositories using sqlmock pattern
- [ ] **P1** Verify integration tests work with Docker services (`make test-local`)

**Success Criteria**: Repository package coverage >= 60%. All CRUD operations have at least happy-path + error-path tests.

Note (2026-02-10): added `internal/repository/video_repository_unit_more_test.go` to cover previously untested `video_repository.go` and `video_repository_count.go` methods (GetByUserID, Update, Delete, processing updates, List, Search, migration, remote, Count, and GetByID fallback/error branches). Verified with:
- `go test -coverprofile=/tmp/video_repo_unit_after.out ./internal/repository -run 'TestVideoRepository_Unit' -count=1`
- `go tool cover -func=/tmp/video_repo_unit_after.out | rg 'internal/repository/video_repository.go|internal/repository/video_repository_count.go|total:'`
  - `internal/repository/video_repository.go`: mostly 77.8%–100.0% per function, with several at 90%+.
  - `internal/repository/video_repository_count.go:Count`: 100.0%.

### 1.2 Handler Tests (7-21% -> 50%+)

API handlers are the system boundary — where user input enters. Low coverage here means injection/validation bugs go undetected.

- [x] **P1** Add tests for `httpapi/handlers/moderation/` (validation/auth paths now covered; package at 27.1%)
- [x] **P1** Add tests for `httpapi/handlers/social/` (validation/auth paths now covered across ratings/playlists/captions; package at 31.6%)
- [ ] **P1** Improve tests for `httpapi/handlers/video/` (currently ~21%)
- [ ] **P1** Improve tests for `httpapi/handlers/channel/` (only subscription pagination test exists)
- [ ] **P1** Improve tests for `httpapi/handlers/livestream/` (only waiting room test exists)
- [ ] **P1** Add tests for `httpapi/handlers/plugin/` (no tests, .go.bak file suggests instability)
- [ ] **P1** Each handler test should cover: valid input, invalid input, auth required, not found

**Success Criteria**: Every handler package has at least one test file. Average handler coverage >= 50%.

### 1.3 Untested Business Logic

- [x] **P1** Add tests for `usecase/import/` (real `service.go` coverage added; package at 48.6%)
- [ ] **P1** Add tests for `internal/importer/` (no test file exists)
- [ ] **P1** Add tests for `internal/health/` (no dedicated test file)
- [ ] **P1** Improve `internal/storage/` tests (currently 16.8%)
- [ ] **P1** Improve `usecase/encoding/` tests (currently 27.3%)

**Success Criteria**: No usecase or infrastructure package at 0% coverage.

Note (2026-02-10): added no-infra unit tests for moderation/social handlers and added real-service tests for `internal/usecase/import/service.go` (instead of test-wrapper-only coverage). Verified with:
- `go test -coverprofile=/tmp/mod_new.out ./internal/httpapi/handlers/moderation && go tool cover -func=/tmp/mod_new.out | tail -n 1` (27.1%)
- `go test -coverprofile=/tmp/social_new.out ./internal/httpapi/handlers/social && go tool cover -func=/tmp/social_new.out | tail -n 1` (31.6%)
- `go test -coverprofile=/tmp/import_new.out ./internal/usecase/import && go tool cover -func=/tmp/import_new.out | tail -n 1` (48.6%)

---

## Phase 2: CI/CD & act Compatibility (P2 - Week 2-3)

**Goal**: Ensure all CI workflows run locally with `act` and optimize pipeline.

### Current State (from exploration)

9 workflows exist. Act readiness:
| Workflow | Act Ready | Issue |
|----------|-----------|-------|
| test.yml | 95% | Docker socket, secrets |
| goose-migrate.yml | 85% | GitHub API comments |
| security-tests.yml | 90% | External tools |
| e2e-tests.yml | 70% | Service health |
| registration-api-tests.yml | 75% | Newman startup |
| virus-scanner-tests.yml | 65% | ClamAV startup |
| video-import.yml | 90% | Codecov optional |
| openapi-ci.yml | 95% | Node tools |
| blue-green-deploy.yml | 0% | Requires K8s (expected) |

### 2.1 Fix .actrc Configuration

- [x] **P2** Expand `.actrc` with proper configuration:
  ```
  -P self-hosted=catthehacker/ubuntu:act-latest
  -P ubuntu-latest=catthehacker/ubuntu:act-latest
  --container-daemon-socket /var/run/docker.sock
  ```
- [ ] **P2** Create `.secrets.example` template (not committed) listing required secrets
- [ ] **P2** Verify `act -j test` runs the main test workflow successfully
- [ ] **P2** Verify `act -j lint` runs formatting/linting checks

Note (2026-02-10): `act -n -j unit` and `act -n -j lint` dry-runs pass with the updated `.actrc`; full non-dry execution is still pending.

### 2.2 Document Local CI Execution

- [x] **P2** Add "Running CI Locally" section to README or CONTRIBUTING.md
- [x] **P2** Document which workflows work with act and which don't (blue-green-deploy: N/A)
- [x] **P2** Document required env vars: DATABASE_URL, REDIS_URL, JWT_SECRET, etc.
- [x] **P2** Add `make act-test` target to Makefile for common local CI run

### 2.3 CI Optimization

- [ ] **P2** Consolidate 3 docker-compose files into single file with profiles
- [ ] **P2** Add coverage threshold enforcement in CI (fail if coverage drops below baseline)
- [ ] **P2** Ensure `paths-ignore` is set on all workflows for docs-only changes
- [ ] **P2** Review custom GitHub Actions in `.github/actions/` for reuse opportunities

**Success Criteria**: `act -j test` and `act -j lint` pass locally. CONTRIBUTING.md documents setup.

---

## Phase 3: Architecture Documentation (P2 - Week 3-4)

**Goal**: Make docs match code reality. Add visual aids.

### 3.1 Update docs/architecture.md

The architecture doc is missing 14+ subsystems that exist in code.

- [x] **P2** Add missing subsystems to project structure:
  - `internal/chat/` - WebSocket-based live chat
  - `internal/database/` - Connection pool management
  - `internal/health/` - Liveness/readiness probes
  - `internal/importer/` - Video import (yt-dlp integration)
  - `internal/livestream/` - RTMP server, HLS streaming
  - `internal/obs/` - OBS/Streamlabs integration
  - `internal/payments/` - IOTA cryptocurrency
  - `internal/plugin/` - Hook-based plugin system (30+ events)
  - `internal/torrent/` - WebTorrent P2P distribution
  - `internal/validation/` - Input validation
  - `internal/whisper/` - Auto-caption generation
  - `internal/worker/` - FFmpeg workers, async jobs
  - `internal/testutil/` - Test utilities
  - `internal/generated/` - OpenAPI generated types
- [x] **P2** Add Key Subsystems sections for: Live Streaming, Plugin System, P2P Distribution, Chat
- [x] **P2** Update technology stack (Go version is now 1.24, not 1.22+)
- [x] **P2** Add reference to `docs/architecture/README.md` which has more detailed mermaid diagrams

### 3.2 Add Architecture Diagrams

Currently NO visual diagrams exist (only text-based mermaid in markdown).

- [ ] **P2** Create system architecture diagram (mermaid in docs/architecture/):
  - HTTP clients -> Chi Router -> Handlers -> Usecases -> Repositories -> PostgreSQL
  - Background workers -> Redis -> Schedulers
  - External: IPFS, ClamAV, FFmpeg, ATProto
- [ ] **P2** Create database ER diagram (key entities: User, Channel, Video, Comment, Playlist, Subscription)
- [ ] **P2** Create video upload/encoding flow diagram
- [ ] **P2** Create federation architecture diagram (ATProto + ActivityPub)

### 3.3 Fix Documentation Cross-References

- [x] **P2** Verify all links in README Documentation section point to existing files
- [x] **P2** Create docs/README.md index linking all documentation
- [x] **P2** Ensure docs/architecture.md references per-subsystem CLAUDE.md files

Note (2026-02-10): README link sweep identified and fixed one stale path (`docs/project-management/sprints/README.md` -> `docs/sprints/README.md`).

**Success Criteria**: Architecture doc lists all 31 subsystems. At least 4 diagrams exist. No dead links.

---

## Phase 4: PeerTube Compatibility Verification (P2 - Week 4-5)

**Goal**: Verify and document what's implemented vs. what's missing.

### Current State (from exploration)

The sprint documents show Sprints A-K ALL COMPLETE:

| Sprint | Feature | Status | Verified |
|--------|---------|--------|----------|
| A | Channels | Complete | `domain/channel.go`, migration 026-027 |
| B | Subscriptions->Channels | Complete | `domain/subscription.go` has ChannelID |
| C | Threaded Comments | Complete | migration 031, `usecase/comment` |
| D | Ratings + Playlists | Complete | migration 032, handlers/social |
| E | Captions/Subtitles | Complete | migration 033, handlers/social/captions.go |
| F | OAuth2 | Complete | migration 025, 028, handlers/auth/oauth.go |
| G | Admin + oEmbed | Complete | migration 034 |
| H-K | Federation (4 sprints) | Complete | ActivityPub + ATProto |
| 5 | Live Streaming | Complete | internal/livestream, migration 048-051 |

### 4.1 Verify API Shape Compatibility

- [x] **P2** Compare OpenAPI specs against PeerTube's API for channels endpoints
- [x] **P2** Compare comment endpoints (GET/POST/DELETE) against PeerTube shapes
- [x] **P2** Verify playlist endpoints return PeerTube-compatible JSON
- [x] **P2** Verify caption endpoints accept/return PeerTube formats
- [x] **P2** Check instance info endpoint matches PeerTube's NodeInfo format

### 4.2 Update Compatibility Documentation

- [x] **P2** Update `docs/PEERTUBE_COMPAT.md` to reflect all sprints are complete
- [x] **P2** Add API shape comparison table (Athena endpoint vs PeerTube endpoint)
- [x] **P2** Document any intentional deviations from PeerTube API (and why)
- [x] **P2** Add compatibility tag to OpenAPI specs for PeerTube-matching routes

### 4.3 Add Compatibility Tests

- [x] **P2** Validate handler tests that cover PeerTube-compatible response shapes
- [x] **P2** Validate channel-based subscriptions (not user-based)
- [x] **P2** Validate threaded comment creation and retrieval
- [x] **P2** Validate admin/instance endpoint responses

### 4.4 Migration Documentation (Separate Initiative)

**Note**: Building actual PeerTube-to-Athena migration tooling is a significant engineering project. For this audit, only document the conceptual approach.

- [x] **P3** Write `docs/PEERTUBE_MIGRATION.md` with high-level guidance:
  - Database schema mapping (PeerTube tables -> Athena tables)
  - Storage migration considerations (local -> IPFS/S3)
  - Config migration checklist
  - DNS/reverse-proxy switchover steps
- [x] **P3** Reference PeerTube's own migration guide for context

Note (2026-02-10): verified by updating `docs/PEERTUBE_COMPAT.md`, adding `PeerTube-Compat` tags across OpenAPI specs, and running targeted compatibility tests:
- `go test ./internal/httpapi/handlers/channel -run 'TestChannelSubscriptions_Integration|TestSubscriptionsBackwardCompatibility_Integration|TestListMySubscriptions_Pagination_PageParams|TestListSubscriptionVideos_Pagination_PageParams' -count=1`
- `go test ./internal/httpapi/handlers/social -run 'TestComments_Integration|TestRatingsPlaylists_Integration|TestCaptionsIntegration' -count=1`
- `go test ./internal/httpapi/handlers/moderation -run 'TestInstanceHandlers|TestOEmbed|TestInstanceConfigIntegration|TestInstanceAboutIntegration|TestOEmbedIntegration' -count=1`
- `go test ./internal/httpapi/handlers/federation -run 'TestNodeInfo|TestNodeInfo20' -count=1`

**Success Criteria**: PEERTUBE_COMPAT.md accurately reflects what's implemented. At least 10 compatibility tests exist.

---

## Phase 5: General Improvements (P3 - Ongoing)

### 5.1 Repository Cleanup

- [x] **P3** Remove `internal/httpapi/handlers/plugin/plugin_handlers.go.bak`
- [x] **P3** Check for and remove any `.rej` patch files in repo root
- [ ] **P3** Verify no hardcoded credentials in code (run `make validate-all`)
- [ ] **P3** Remove or update any stale TODO comments

### 5.2 Community Documentation

- [x] **P3** Create CONTRIBUTING.md with:
  - Setup instructions (link to Quick Start)
  - Code style (golangci-lint, gofmt)
  - Testing requirements (run `make validate-all` before PR)
  - Branch naming / commit message conventions
  - How to run CI locally with act
- [x] **P3** Add CODE_OF_CONDUCT.md (Contributor Covenant)

### 5.3 README Accuracy

- [x] **P3** Review feature list: mark what's implemented vs. roadmap
- [x] **P3** Verify all badge links work
- [x] **P3** Update "Project Metrics" table with accurate numbers
- [x] **P3** Clearly separate "implemented" features from "planned" features

Note (2026-02-10): README local/documentation links were verified against filesystem paths. Badge URLs were validated against existing workflows via `gh workflow list`; unauthenticated HTTP checks return 404 because the repository is private.

**Success Criteria**: No stale files. CONTRIBUTING.md exists. README is accurate.

---

## Execution Order & Dependencies

```
Phase 0 (Day 1)      ── Fix factual errors in docs
    │
Phase 1 (Week 1-2)   ── Critical testing gaps
    │                    (validates with existing CI)
Phase 2 (Week 2-3)   ── CI/CD & act compatibility
    │                    (enables local testing of Phase 1 work)
Phase 3 (Week 3-4)   ── Architecture documentation
    │                    (reflects reality discovered in Phase 1-2)
Phase 4 (Week 4-5)   ── PeerTube compatibility
    │                    (builds on testing from Phase 1)
Phase 5 (Ongoing)    ── General improvements
```

Phases 1 and 2 can run in parallel. Phase 3 benefits from insights in 1-2. Phase 4 depends on tests from Phase 1.

---

## Risk Register

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| Coverage can't reach 60% in 2 weeks | Medium | Medium | Prioritize highest-risk packages first |
| act incompatibilities block local CI | Low | Low | Most workflows already 85%+ compatible |
| PeerTube API shape mismatches found | Medium | High | Document deviations, provide adapters |
| Architecture diagrams become stale | High | Low | Generate from code where possible |
| Repository tests need Docker | Medium | Medium | Use sqlmock for unit, Docker for integration |

---

## Tracking

Each phase has clear success criteria. Track progress by updating checklist items in this document. Run `/context analyze` every 50 messages to manage context health during implementation.

**Owner**: Project team
**Review Cadence**: Weekly at minimum, daily during active phases
