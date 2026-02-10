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
| Handler test coverage | 60%+ target | 7-21% | Severe |
| Repository test coverage | Not mentioned | 9.6% | Critical |
| Moderation handler tests | Expected | 0% | Zero |
| Social handler tests | Expected | 0% | Zero |

---

## Phase 0: Immediate Corrections (P0 - Day 1)

**Goal**: Fix factual inaccuracies that could mislead contributors or users.

### 0.1 Fix Coverage Claims

The README and TESTING_STRATEGY.md claim 85%+ coverage. The TEST_BASELINE_REPORT.md (Nov 16) shows 23.8%. This discrepancy must be resolved immediately.

- [ ] **P0** Update README.md test metrics table to reflect actual coverage (23.8% baseline, target 60%+)
- [ ] **P0** Update TESTING_STRATEGY.md coverage targets to be realistic (current: 23.8%, near-term target: 60%, stretch: 80%)
- [ ] **P0** Run `go test -coverprofile=coverage.out ./...` to establish current baseline
- [ ] **P0** Add coverage-by-package breakdown to TEST_BASELINE_REPORT.md

**Success Criteria**: All documentation reflects actual measured coverage. No aspirational numbers presented as current state.

### 0.2 Fix "Planned" Labels on Completed Features

Architecture doc marks implemented features as planned.

- [ ] **P0** In `docs/architecture.md` lines 57-59: Change `auth/ # Authentication (planned)` to `auth/ # Authentication`
- [ ] **P0** Change `federation/ # Federation (planned)` to `federation/ # Federation (ATProto + ActivityPub)`
- [ ] **P0** Change `e2ee/ # End-to-end encryption (planned)` to `e2ee/ # End-to-end encryption`
- [ ] **P0** Remove `pkg/ # Shared utilities (planned)` (line 79) since it doesn't exist and utilities are in subsystem packages

**Success Criteria**: No implemented features marked as "planned" in documentation.

---

## Phase 1: Critical Testing Gaps (P1 - Week 1-2)

**Goal**: Raise coverage from 23.8% to 60%+ by targeting highest-risk zero-coverage areas.

### 1.1 Repository Layer Tests (9.6% -> 60%+)

The repository layer handles all database CRUD. At 9.6% coverage, virtually all persistence logic is untested.

- [ ] **P1** Add unit tests for `repository/video_repository.go` (core CRUD)
- [ ] **P1** Add unit tests for `repository/user_repository.go` (auth flows)
- [ ] **P1** Add unit tests for `repository/channel_repository.go` (channel CRUD)
- [ ] **P1** Add unit tests for `repository/comment_repository.go`
- [ ] **P1** Add unit tests for `repository/playlist_repository.go`
- [ ] **P1** Add unit tests for remaining repositories using sqlmock pattern
- [ ] **P1** Verify integration tests work with Docker services (`make test-local`)

**Success Criteria**: Repository package coverage >= 60%. All CRUD operations have at least happy-path + error-path tests.

### 1.2 Handler Tests (7-21% -> 50%+)

API handlers are the system boundary — where user input enters. Low coverage here means injection/validation bugs go undetected.

- [ ] **P1** Add tests for `httpapi/handlers/moderation/` (currently 0%)
- [ ] **P1** Add tests for `httpapi/handlers/social/` (ratings, playlists, captions - currently 0%)
- [ ] **P1** Improve tests for `httpapi/handlers/video/` (currently ~21%)
- [ ] **P1** Improve tests for `httpapi/handlers/channel/` (only subscription pagination test exists)
- [ ] **P1** Improve tests for `httpapi/handlers/livestream/` (only waiting room test exists)
- [ ] **P1** Add tests for `httpapi/handlers/plugin/` (no tests, .go.bak file suggests instability)
- [ ] **P1** Each handler test should cover: valid input, invalid input, auth required, not found

**Success Criteria**: Every handler package has at least one test file. Average handler coverage >= 50%.

### 1.3 Untested Business Logic

- [ ] **P1** Add tests for `usecase/import/` (video import - currently 0%)
- [ ] **P1** Add tests for `internal/importer/` (no test file exists)
- [ ] **P1** Add tests for `internal/health/` (no dedicated test file)
- [ ] **P1** Improve `internal/storage/` tests (currently 16.8%)
- [ ] **P1** Improve `usecase/encoding/` tests (currently 27.3%)

**Success Criteria**: No usecase or infrastructure package at 0% coverage.

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

- [ ] **P2** Expand `.actrc` with proper configuration:
  ```
  -P self-hosted=catthehacker/ubuntu:act-latest
  -P ubuntu-latest=catthehacker/ubuntu:act-latest
  --container-daemon-socket /var/run/docker.sock
  ```
- [ ] **P2** Create `.secrets.example` template (not committed) listing required secrets
- [ ] **P2** Verify `act -j test` runs the main test workflow successfully
- [ ] **P2** Verify `act -j lint` runs formatting/linting checks

### 2.2 Document Local CI Execution

- [ ] **P2** Add "Running CI Locally" section to README or CONTRIBUTING.md
- [ ] **P2** Document which workflows work with act and which don't (blue-green-deploy: N/A)
- [ ] **P2** Document required env vars: DATABASE_URL, REDIS_URL, JWT_SECRET, etc.
- [ ] **P2** Add `make act-test` target to Makefile for common local CI run

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

- [ ] **P2** Add missing subsystems to project structure:
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
- [ ] **P2** Add Key Subsystems sections for: Live Streaming, Plugin System, P2P Distribution, Chat
- [ ] **P2** Update technology stack (Go version is now 1.24, not 1.22+)
- [ ] **P2** Add reference to `docs/architecture/README.md` which has more detailed mermaid diagrams

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

- [ ] **P2** Verify all links in README Documentation section point to existing files
- [ ] **P2** Create docs/README.md index linking all documentation
- [ ] **P2** Ensure docs/architecture.md references per-subsystem CLAUDE.md files

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

- [ ] **P2** Compare OpenAPI specs against PeerTube's API for channels endpoints
- [ ] **P2** Compare comment endpoints (GET/POST/DELETE) against PeerTube shapes
- [ ] **P2** Verify playlist endpoints return PeerTube-compatible JSON
- [ ] **P2** Verify caption endpoints accept/return PeerTube formats
- [ ] **P2** Check instance info endpoint matches PeerTube's NodeInfo format

### 4.2 Update Compatibility Documentation

- [ ] **P2** Update `docs/PEERTUBE_COMPAT.md` to reflect all sprints are complete
- [ ] **P2** Add API shape comparison table (Athena endpoint vs PeerTube endpoint)
- [ ] **P2** Document any intentional deviations from PeerTube API (and why)
- [ ] **P2** Add compatibility tag to OpenAPI specs for PeerTube-matching routes

### 4.3 Add Compatibility Tests

- [ ] **P2** Add handler tests that validate PeerTube-compatible response shapes
- [ ] **P2** Test channel-based subscriptions (not user-based)
- [ ] **P2** Test threaded comment creation and retrieval
- [ ] **P2** Test admin/instance endpoint responses

### 4.4 Migration Documentation (Separate Initiative)

**Note**: Building actual PeerTube-to-Athena migration tooling is a significant engineering project. For this audit, only document the conceptual approach.

- [ ] **P3** Write `docs/PEERTUBE_MIGRATION.md` with high-level guidance:
  - Database schema mapping (PeerTube tables -> Athena tables)
  - Storage migration considerations (local -> IPFS/S3)
  - Config migration checklist
  - DNS/reverse-proxy switchover steps
- [ ] **P3** Reference PeerTube's own migration guide for context

**Success Criteria**: PEERTUBE_COMPAT.md accurately reflects what's implemented. At least 10 compatibility tests exist.

---

## Phase 5: General Improvements (P3 - Ongoing)

### 5.1 Repository Cleanup

- [ ] **P3** Remove `internal/httpapi/handlers/plugin/plugin_handlers.go.bak`
- [ ] **P3** Check for and remove any `.rej` patch files in repo root
- [ ] **P3** Verify no hardcoded credentials in code (run `make validate-all`)
- [ ] **P3** Remove or update any stale TODO comments

### 5.2 Community Documentation

- [ ] **P3** Create CONTRIBUTING.md with:
  - Setup instructions (link to Quick Start)
  - Code style (golangci-lint, gofmt)
  - Testing requirements (run `make validate-all` before PR)
  - Branch naming / commit message conventions
  - How to run CI locally with act
- [ ] **P3** Add CODE_OF_CONDUCT.md (Contributor Covenant)

### 5.3 README Accuracy

- [ ] **P3** Review feature list: mark what's implemented vs. roadmap
- [ ] **P3** Verify all badge links work
- [ ] **P3** Update "Project Metrics" table with accurate numbers
- [ ] **P3** Clearly separate "implemented" features from "planned" features

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
