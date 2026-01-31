# Scribe's Journal

## 2026-01-31 - Fixing Kubernetes Deployment Documentation Gap
**Context:** The "K8s prep needed" flag was raised in project status. The deployment guide referenced `k8s/ipfs` and `k8s/clamav` directories which did not exist, leading to a broken "Quick Start" experience for Kubernetes.
**Action:** Created missing Kubernetes manifests for ClamAV and IPFS, and updated `KUBERNETES_DEPLOYMENT.md` to include necessary namespace flags.
**Outcome:** Kubernetes deployment instructions are now actionable and consistent with the codebase.
**Lesson:** Documentation that references code files must be verified against the file system. "Ghost references" (linking to non-existent files) are a critical documentation failure that automated link checkers might miss if they don't check file existence.
