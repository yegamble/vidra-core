## Summary

<!-- Describe WHAT changed and WHY. Link related issues with "Closes #123". -->

## Changes

-

## Checklist

### General

- [ ] Tests added/updated and passing (`make test`)
- [ ] Linter passes (`make lint`)
- [ ] Code formatted (`make fmt`)
- [ ] No secrets, credentials, or .env files committed
- [ ] `make build` succeeds

### API Changes (if applicable)

- [ ] OpenAPI spec updated in `api/` to reflect new/modified endpoints
- [ ] Generated types regenerated (`make generate-openapi`) and committed
- [ ] Request/response schemas documented with examples
- [ ] Error codes and status codes are correct and documented
- [ ] Breaking changes noted in summary with migration guidance

### Security (if applicable)

- [ ] User input validated and sanitized
- [ ] Authentication/authorization requirements correct
- [ ] No SQL injection (parameterized queries only)
- [ ] No command injection (arguments sanitized)
- [ ] Rate limiting applied to new endpoints

### Database (if applicable)

- [ ] Migration is reversible (has `Down` section)
- [ ] Indexes added for new query patterns
- [ ] Migration tested locally (`make migrate-up`)

## Test Plan

<!-- How was this tested? Include steps to reproduce or verify. -->

-
