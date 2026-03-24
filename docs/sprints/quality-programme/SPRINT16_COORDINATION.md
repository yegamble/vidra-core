# Sprint Coordination: Quality Programme - Sprint 16

This document outlines the execution flow for the current sprint.

## Sprint 16: API Contract Reproducibility

**Goal:** Make the API contract stable and reproducible with CI enforcement.

**Prerequisites:** Sprint 15 complete. Mainline stable, all security PRs merged, coverage at 52.9%.

## Execution Sequence

### Phase 1: CI Infrastructure

1. Add CI job to regenerate OpenAPI types and fail on diff
2. Validate existing `make generate-openapi` works on clean checkout

### Phase 2: Smoke Tests

1. Add Postman smoke test workflow to run on PRs
2. Ensure bounded runtime and clear failure reporting

### Phase 3: Documentation

1. Document federation "well-known" endpoints in OpenAPI or explicit exclusion list
2. Add API review checklist to PR template
3. Create API contract policy document

## Acceptance Criteria

- [ ] `make generate-openapi` produces deterministic output
- [ ] CI job fails if generated types change
- [ ] Postman smoke tests run on PR and report clearly
- [ ] Federation endpoints documented or excluded
- [ ] API change review process documented

## References

- [Sprint 15 Complete](../SPRINT15_COMPLETE.md)
- [Quality Programme](../QUALITY_PROGRAMME.md)
- [Sprint Backlog](./SPRINT16_BACKLOG.md)
