# Product Roadmap: Athena

## Vision
To build a resilient, feature-complete, and decentralized video platform backend that seamlessly bridges the gap between Web2 performance and Web3 freedom (ActivityPub + ATProto).

## 🚧 Current Phase: Operation Bedrock (Stabilization)
**Goal**: Restore 100% test reliability and verification of the core platform.
**Status**: In Progress
**Focus Areas**:
- **Infrastructure**: Fix CI/CD pipelines and local dev environment (Docker limits).
- **Verification**: Ensure all `internal/repository` and `internal/usecase` tests pass.
- **Security**: Validate auth flows and credential rotation.
- **Docs**: Align documentation with verified reality.

**Exit Criteria**:
- CI is Green.
- `make test` passes locally < 5 mins.
- No critical/high security vulnerabilities.

---

## 🚀 Phase 2: Federation & Economy (Next)
**Goal**: Enable true decentralization and creator monetization.
**Status**: Planned (Blocked by Bedrock)

### 2.1 Federation Hardening (ActivityPub)
- **Feature**: Full inbox/outbox processing with signature verification.
- **Verification**: Interop tests with PeerTube instances.
- **Security**: HTTP Signature enforcement + Blind Key Rotation.

### 2.2 AT Protocol Integration (Beta -> Stable)
- **Feature**: Sync user posts to Bluesky/ATProto.
- **Feature**: Import identity (DID) handling.

### 2.3 IOTA Payments (New)
- **Feature**: Micro-payments for content creators.
- **Tech**: IOTA Tangle integration.
- **Scope**: Wallet generation, Transaction monitoring, Withdrawal flows.

---

## 🔮 Phase 3: Scale & Ecosystem (Future)
**Goal**: Horizontal scaling and plugin ecosystem.
- Kubernetes native deployment (Helm charts).
- Plugin Marketplace V2 (Sandbox execution).
- Advanced Analytics (Clickhouse integration).
