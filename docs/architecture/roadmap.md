# Athena Product Roadmap

This document outlines the strategic roadmap for the Athena backend, moving from stabilization to production launch and future growth.

## 🧭 Phase 1: Stabilization (Operation Bedrock) - **CURRENT**

**Goal:** Establish a reliable, verified foundation. We cannot ship until we trust our tests and CI.

*   **Status:** In Progress (~88% Complete)
*   **Focus Areas:**
    *   **Infrastructure Reliability:** Ensure tests run reliably in local and CI environments (Fail-fast logic).
    *   **Test Verification:** Verify integrity of `internal/repository` and `internal/ipfs` integration tests.
    *   **Documentation Accuracy:** Align `README.md` and dev docs with reality.
    *   **CI Stability:** Eliminate flaky tests and Docker rate limit issues.

**Key Deliverables:**
*   ✅ "Fail Fast" Test Infrastructure (Done)
*   [ ] Verified Identity Layer Tests (`user`, `auth`)
*   [ ] Verified Content Layer Tests (`video`, `upload`)
*   [ ] Verified Federation Layer Tests (`activitypub`)
*   [ ] Updated Developer Onboarding Docs

---

## 🚀 Phase 2: Launch Prep (Operation Liftoff)

**Goal:** Prepare the stabilized artifact for production deployment.

*   **Status:** Pending Completion of Phase 1
*   **Focus Areas:**
    *   **Security Hardening:** Credential rotation, final security audit, penetration testing validation.
    *   **Deployment Readiness:** Finalize Kubernetes Helm charts/manifests.
    *   **Performance:** Load testing (Simulate 10k concurrent viewers), Database tuning.
    *   **Observability:** Finalize Prometheus/Grafana dashboards.

**Key Deliverables:**
*   [ ] Credential Rotation (AWS keys, DB passwords)
*   [ ] Production Kubernetes Configuration
*   [ ] Load Test Report (10k user simulation)
*   [ ] Final Security Sign-off

---

## 🔮 Phase 3: Future Growth

**Goal:** Expand feature set and capabilities.

*   **Status:** Planned
*   **Focus Areas:**
    *   **Monetization:** IOTA Micropayments integration.
    *   **Federation:** ATProto (Bluesky) move from BETA to Stable.
    *   **Analytics:** Advanced behavioral analytics and retention heatmaps.
    *   **AI:** Enhanced caption generation and content moderation.

**Key Deliverables:**
*   [ ] IOTA Payment Gateway
*   [ ] Stable ATProto Federation
*   [ ] Advanced Analytics Suite
