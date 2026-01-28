# Product Roadmap

## Vision
To build a high-performance, feature-complete PeerTube backend in Go that matches the official implementation's capabilities while adding unique features like hybrid IPFS/P2P distribution, robust live streaming, and multi-protocol federation (ActivityPub + ATProto).

## Phases

### Phase 1: Stabilization (Operation Bedrock) 🏗️
**Status:** In Progress (Current Sprint)
**Goal:** Establish a rock-solid, verifiable foundation.

*   **Test Infrastructure**:
    *   [ ] Implement "Fail Fast" logic for DB connections in tests (Local Dev experience).
    *   [ ] Solve Docker Hub rate limits for CI.
    *   [ ] Fix flaky tests in `internal/repository` and `internal/ipfs`.
*   **Code Integrity**:
    *   [ ] Verify all SQL queries against actual Postgres schema.
    *   [ ] Standardize error handling and logging patterns.
*   **Documentation**:
    *   [ ] Accurate `README.md` with strict setup requirements.

### Phase 2: Launch Readiness 🚀
**Status:** Planned (Next Sprint)
**Goal:** Prepare for production deployment and public release.

*   **Security Hardening**:
    *   [ ] Credential Rotation (rotate all dev secrets).
    *   [ ] Final Security Audit (Sentinel).
*   **Infrastructure**:
    *   [ ] Kubernetes Manifests (Helm Charts).
    *   [ ] Production `docker-compose.prod.yml`.
*   **Performance**:
    *   [ ] Load Testing (10k concurrent users).
    *   [ ] Database Index Optimization.

### Phase 3: Federation & Innovation 🌐
**Status:** Future
**Goal:** Expand ecosystem reach and add cutting-edge features.

*   **Federation**:
    *   [ ] ActivityPub: Full compliance test suite.
    *   [ ] ATProto: Move from BETA to Stable (Write support).
*   **Features**:
    *   [ ] IOTA Micropayments integration.
    *   [ ] Advanced Plugin Marketplace.
    *   [ ] AI-driven Content Moderation.

## Principles
1.  **Stability First**: No new features until tests are green.
2.  **Security by Default**: Every endpoint is secure, every input validated.
3.  **Performance Matters**: We optimize for low-end hardware.
