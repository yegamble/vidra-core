# Sprint Plan: Quality Programme - Sprint 16

**Sprint Goal**: Make the API contract stable and reproducible with CI enforcement.

## Context

Sprint 15 (Stabilize & Integrate) is complete. All P0-P2 security PRs are resolved, CI is green, and the coverage baseline is established at 52.9%. Sprint 16 focuses on API contract reproducibility.

## Sprint 16 Tasks

### 1. OpenAPI CI Enforcement

- Add CI job to regenerate OpenAPI types and fail if generated code differs
- Ensures API contract cannot drift from specification

### 2. Postman Smoke Tests

- Add Postman smoke test workflow that runs on PRs
- Reports failures clearly with bounded runtime

### 3. Federation Endpoint Documentation

- Document federation "well-known" endpoints in OpenAPI
- Or explicitly document exclusions

### 4. API Review Checklist

- Add API review checklist to PR template
- Forces schema and error code review

### 5. API Contract Policy

- Create API contract policy document
- Define source of truth and change process

## Acceptance Criteria

- [ ] OpenAPI generation enforced in CI
- [ ] Postman smoke tests pass on PR
- [ ] Federation endpoints documented or explicitly excluded
- [ ] API change review process documented

## References

- [Sprint 15 Complete](../SPRINT15_COMPLETE.md)
- [Quality Programme](../QUALITY_PROGRAMME.md)
- [Sprint Backlog](./SPRINT16_BACKLOG.md)
