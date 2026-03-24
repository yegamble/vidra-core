# Sprint Documentation

This directory contains comprehensive documentation for all completed sprints in the Vidra Core project.

## 🎉 Project Complete - 100% Feature Delivery

All 20 sprints (14 feature + 6 quality) have been successfully completed, delivering a full-featured PeerTube-compatible video platform with advanced P2P, live streaming, and plugin capabilities.

## Quick Reference

### High-Level Overview

- **[SPRINT_PLAN.md](./SPRINT_PLAN.md)** - Comprehensive sprint plan with all 20 sprints, timelines, and acceptance criteria
- **[PROJECT_COMPLETE.md](./PROJECT_COMPLETE.md)** - 100% completion summary and final project metrics
- **[peertube_compatibility.md](./peertube_compatibility.md)** - PeerTube API compatibility tracking (Sprints A-K)
- **[SPRINT_MONITORING_PLAN.md](./SPRINT_MONITORING_PLAN.md)** - Monitoring and observability strategy

## Sprint Completion Documentation

### Core Platform (Sprints A-G)

See [peertube_compatibility.md](./peertube_compatibility.md) for:

- **Sprint A:** Channels (Foundations)
- **Sprint B:** Subscriptions → Channels
- **Sprint C:** Comments (Threads) + Moderation
- **Sprint D:** Ratings + Playlists
- **Sprint E:** Captions/Subtitles
- **Sprint F:** OAuth2 (Auth Code + Scopes)
- **Sprint G:** Admin + Instance Info + oEmbed

### Federation (Sprints H-K)

See [peertube_compatibility.md](./peertube_compatibility.md) for:

- **Sprint H:** ATProto Foundations
- **Sprint I:** ATProto Videos (Publish/Consume)
- **Sprint J:** ATProto Social (Follows, Likes, Comments)
- **Sprint K:** Federation Hardening

### Advanced Features (Sprints 1-14)

#### Sprint 1-2: Video Import & Transcoding

- **[SPRINT1_COMPLETE.md](./SPRINT1_COMPLETE.md)** - Video import system (yt-dlp, 1000+ platforms)
- **[SPRINT2_COMPLETE.md](./SPRINT2_COMPLETE.md)** - Advanced transcoding (VP9, AV1, multi-codec)

**Progress Files:**

- [SPRINT1_PROGRESS.md](./SPRINT1_PROGRESS.md)
- [SPRINT1_TEST_SUMMARY.md](./SPRINT1_TEST_SUMMARY.md)

#### Sprint 5-7: Live Streaming

- **[SPRINT5_COMPLETE.md](./SPRINT5_COMPLETE.md)** - RTMP server & stream ingestion
- **[SPRINT6_COMPLETE.md](./SPRINT6_COMPLETE.md)** - HLS transcoding & playback
- **[SPRINT7_COMPLETE.md](./SPRINT7_COMPLETE.md)** - Enhanced live streaming (chat, scheduling, analytics)

**Progress & Planning Files:**

- [SPRINT5_PROGRESS.md](./SPRINT5_PROGRESS.md)
- [SPRINT6_PLAN.md](./SPRINT6_PLAN.md) | [SPRINT6_PROGRESS.md](./SPRINT6_PROGRESS.md)
- [SPRINT7_PLAN.md](./SPRINT7_PLAN.md) | [SPRINT7_PROGRESS.md](./SPRINT7_PROGRESS.md)

#### Sprint 8-9: P2P Distribution

- **[SPRINT8_COMPLETE.md](./SPRINT8_COMPLETE.md)** - WebTorrent P2P distribution
- **[SPRINT9_COMPLETE.md](./SPRINT9_COMPLETE.md)** - Advanced P2P (DHT, PEX, smart seeding)

**Progress & Planning Files:**

- [SPRINT8_PLAN.md](./SPRINT8_PLAN.md) | [SPRINT8_PROGRESS.md](./SPRINT8_PROGRESS.md)

#### Sprint 10-11: Analytics

- **[SPRINT10_COMPLETE.md](./SPRINT10_COMPLETE.md)** - Video analytics system with retention curves

#### Sprint 12-14: Plugins & Redundancy

- **[SPRINT12_COMPLETE.md](./SPRINT12_COMPLETE.md)** - Plugin system architecture
- **[SPRINT13_COMPLETE.md](./SPRINT13_COMPLETE.md)** - Plugin security & marketplace
- **[SPRINT14_COMPLETE.md](./SPRINT14_COMPLETE.md)** - Video redundancy system

**Progress Files:**

- [SPRINT13_PROGRESS.md](./SPRINT13_PROGRESS.md)

### Quality Programme (Sprints 15-20)

- **[SPRINT15_COMPLETE.md](./SPRINT15_COMPLETE.md)** - Stabilize mainline; integrate PR queue
- **[SPRINT16_COMPLETE.md](./SPRINT16_COMPLETE.md)** - API contract reproducibility (CI enforcement, Postman smoke tests)
- **[SPRINT17_COMPLETE.md](./SPRINT17_COMPLETE.md)** - Coverage uplift I: Core services (all usecase packages at 80%+)
- **[SPRINT18_COMPLETE.md](./SPRINT18_COMPLETE.md)** - Coverage uplift II: Handlers & repositories (repository 90%, handlers 80%+)
- **[SPRINT19_COMPLETE.md](./SPRINT19_COMPLETE.md)** - Documentation accuracy (zero broken links, runbook validation, source-of-truth map)
- **[SPRINT20_COMPLETE.md](./SPRINT20_COMPLETE.md)** - Release hardening (full regression, coverage sign-off, CHANGELOG.md, maintenance plan, release checklist)
- **[QUALITY_PROGRAMME.md](./QUALITY_PROGRAMME.md)** - Full Quality Programme roadmap (100% complete)
- **[quality-programme/README.md](./quality-programme/README.md)** - Archived Sprint 15-16 working docs (plan, backlog, coordination, update)

## Project Metrics

### Feature Parity Statistics (Sprints 1-14)

- **Feature Sprints:** 14 (100% complete)
- **Total Code at Sprint 14:** ~151,000+ lines (75K production + 76K tests)
- **Tests at Sprint 14:** 750+ passing
- **Development Time:** ~7 months (28 weeks)

> **Current project metrics:** See [README.md](../../README.md) for up-to-date counts (618 Go files, 3,752 tests, 313 test files).

### Feature Breakdown by Lines of Code

- Sprint 1 (Video Import): ~3,200 lines + 23 tests
- Sprint 2 (Transcoding): ~1,231 lines + 29 tests
- Sprint 5 (RTMP): ~3,000 lines + 63 tests
- Sprint 6 (HLS): ~2,500 lines + 25 tests
- Sprint 7 (Live Enhanced): ~9,235 lines + 100 tests
- Sprint 8 (WebTorrent): ~4,440 lines + 73 tests
- Sprint 9 (Advanced P2P): ~322 lines + 77 tests
- Sprint 10-11 (Analytics): ~1,913 lines
- Sprint 12 (Plugins): ~3,200 lines + 36 tests
- Sprint 13 (Plugin Security): ~1,372 lines + 44 tests
- Sprint 14 (Redundancy): ~7,800 lines + 42 tests

## Navigation

### By Topic

- **Video Platform:** Sprints A-G, 1-2
- **Live Streaming:** Sprints 5-7
- **P2P & Distribution:** Sprints 8-9, 14
- **Federation:** Sprints H-K
- **Analytics & Monitoring:** Sprints 10-11
- **Extensibility:** Sprints 12-13

### By Development Phase

- **Foundation (Sprints A-G):** Basic PeerTube features
- **Advanced Video (1-2):** Import and transcoding
- **Live Streaming (5-7):** Real-time capabilities
- **Federation (H-K):** Multi-protocol federation
- **Distribution (8-9, 14):** P2P and redundancy
- **Platform (10-13):** Analytics and plugins
- **Quality Programme (15-20):** Stabilization, coverage, docs, release

## Quick Links

### Starting Points

- New to the project? Start with [SPRINT_PLAN.md](./SPRINT_PLAN.md)
- Want completion summary? See [PROJECT_COMPLETE.md](./PROJECT_COMPLETE.md)
- Need API compatibility info? Check [peertube_compatibility.md](./peertube_compatibility.md)

### Deep Dives

- Live Streaming: [SPRINT7_COMPLETE.md](./SPRINT7_COMPLETE.md)
- P2P Distribution: [SPRINT8_COMPLETE.md](./SPRINT8_COMPLETE.md) + [SPRINT9_COMPLETE.md](./SPRINT9_COMPLETE.md)
- Plugin System: [SPRINT12_COMPLETE.md](./SPRINT12_COMPLETE.md) + [SPRINT13_COMPLETE.md](./SPRINT13_COMPLETE.md)
- Video Redundancy: [SPRINT14_COMPLETE.md](./SPRINT14_COMPLETE.md)

## Document Types

### Complete (✅ Final)

Files ending in `_COMPLETE.md` represent finished sprints with:

- ✅ All acceptance criteria met
- ✅ Full test coverage
- ✅ Production-ready code
- ✅ Complete documentation

### Progress (📝 Historical)

Files ending in `_PROGRESS.md` show historical development tracking:

- Work-in-progress snapshots
- Issue tracking
- Intermediate milestones
- Now superseded by completion docs

### Plan (📋 Historical)

Files ending in `_PLAN.md` show original sprint planning:

- Initial scope and estimates
- Task breakdowns
- Design decisions
- Now superseded by completion docs

## Contributing

When adding new sprint documentation:

1. Create `SPRINT{N}_COMPLETE.md` for final documentation
2. Include acceptance criteria, test results, and code metrics
3. Update this README with navigation links
4. Cross-reference related sprints

## Archive Policy

Progress and plan files are retained for historical reference but are not actively maintained. All current information should be in completion documents.
