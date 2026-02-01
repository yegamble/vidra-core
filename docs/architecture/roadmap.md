# Product Roadmap

This document outlines the strategic direction and development phases for the Athena Video Platform.

## Vision
To build a high-performance, scalable, and secure backend for decentralized video sharing, fully compatible with PeerTube's API while introducing modern features like ATProto federation and hybrid storage.

## Phases

### Phase 1: Stabilization ("Operation Bedrock") 🏗️
**Status**: **Active (Final Stages)**
**Goal**: Ensure the platform is verifiable, secure, and operationally reliable before public release.

**Key Deliverables**:
*   ✅ **Core Feature Parity**: Uploads, Live Streaming, Channels, Comments, Notifications.
*   ✅ **Federation Alpha**: ActivityPub (PeerTube compatible) and ATProto (Beta).
*   ✅ **Security Hardening**: Credential rotation, 2FA, Virus Scanning, Rate Limiting.
*   🔄 **Infrastructure Reliability**: Green CI/CD, reliable local test suites, Docker optimizations.
*   🔄 **Documentation**: Complete API docs, deployment guides, and architecture specs.

**Exit Criteria**:
*   CI pipeline is 100% green and reliable.
*   Local test suite (`make test-local`) runs without skips.
*   Security audit issues resolved.
*   Production credentials rotation workflow established.

---

### Phase 2: Launch & Growth 🚀
**Status**: **Planning**
**Goal**: Public release, user onboarding, and monetization features.

**Key Deliverables**:
*   **IOTA Payments Integration**: Native support for micropayments and donations using IOTA.
*   **ATProto Hardening**: Move ATProto support from BETA to Stable.
*   **Kubernetes Support**: Production-ready Helm charts and K8s manifests.
*   **Performance Tuning**: Load testing refinement for 10k+ concurrent viewers.
*   **Public Beta Launch**: Opening the platform to initial instance admins.

---

### Phase 3: Future & Scale 🌐
**Status**: **Future**
**Goal**: Advanced features for enterprise scale and federation depth.

**Key Deliverables**:
*   **ElasticSearch Integration**: Advanced search capabilities beyond Postgres full-text search.
*   **Video Recommendations**: AI/ML-driven recommendation engine (privacy-preserving).
*   **Transcoding Offload**: Remote worker pools for heavy transcoding jobs.
*   **Mobile SDKs**: Client libraries for mobile app development.
*   **Kubernetes Operator**: Custom operator for managing Athena instances.

## Feature Status Overview

| Feature Area | Phase | Status |
|--------------|-------|--------|
| **Core API** | 1 | ✅ Ready |
| **Live Streaming** | 1 | ✅ Ready |
| **ActivityPub** | 1 | ✅ Ready |
| **ATProto** | 1 | ⚠️ Beta |
| **Payments** | 2 | 📅 Planned |
| **Search** | 1/3 | ⚠️ Basic (PG) |
| **K8s Deploy** | 2 | 📅 Planned |

## Changelog
*   **2025-09-20**: Initial Roadmap formalized. Defining Phase 1 exit criteria.
