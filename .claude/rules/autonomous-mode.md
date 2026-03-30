# Autonomous Mode Requirements

**Purpose:** Rules that apply whenever Claude operates autonomously (without user interaction). These ensure completeness, documentation, and testing parity.

## Documentation Updates (MANDATORY)

**Every code change MUST include corresponding documentation updates.** Documentation is not optional — it is part of the definition of done.

### README updates
- New features: add to feature list in `README.md`
- New endpoints: add to API section
- New configuration: add to configuration section
- New dependencies: add to requirements section
- Changed behavior: update relevant sections

### CLAUDE.md updates
- New packages: add to project layout
- Changed architecture: update architecture notes
- New commands: add to common commands
- Test count changes: update test statistics after significant additions

### Domain-specific docs
- API changes: update `internal/httpapi/CLAUDE.md`
- Security changes: update `internal/security/CLAUDE.md`
- Federation changes: update `internal/activitypub/CLAUDE.md`
- Migration changes: update `migrations/CLAUDE.md`
- Architecture changes: update `docs/architecture/CLAUDE.md`

### Feature registry
- Any feature added/modified: update `.claude/rules/feature-parity-registry.md`

### OpenAPI specs
- New endpoints: add to appropriate `api/openapi_*.yaml` spec file
- Modified endpoints: update the spec
- Verify after changes: `make verify-openapi`

## Postman/Newman Test Updates (MANDATORY)

**Every new or modified API endpoint MUST have a corresponding Postman collection update.**

### When to update Postman collections:
- New endpoint added → add request + tests to appropriate collection in `postman/`
- Endpoint behavior changed → update existing tests
- Endpoint removed → remove from collection
- New error responses → add negative test cases

### Collection organization:
- Collections in `postman/` mirror PeerTube API categories
- Each collection has pre-request scripts for auth setup
- Tests verify response status, body shape, and key fields
- Use collection variables for dynamic data (IDs, tokens)

### Test requirements per endpoint:
1. **Happy path** — correct request returns expected response
2. **Auth check** — unauthenticated request returns 401
3. **Validation** — invalid input returns 400 with error details
4. **Not found** — missing resource returns 404
5. **Permission** — unauthorized role returns 403 (where applicable)

### Newman CI validation:
```bash
# Run all collections
make test-newman

# Run specific collection
newman run postman/collection.json -e postman/env.json
```

## PeerTube Upstream Awareness

**Reference:** https://github.com/Chocobozzz/PeerTube

When implementing features, always check PeerTube's implementation for:
- API response shapes and field names
- Query parameter conventions
- Pagination format
- Error response format
- Default values and limits

### Key PeerTube source locations:
- REST API: `server/core/controllers/api/`
- Models: `server/core/models/`
- Validators: `server/core/middlewares/validators/`
- OpenAPI: PeerTube's REST API docs at https://docs.joinpeertube.org/api-rest-reference.html

### Response shape alignment:
PeerTube returns `{ total, data }` for lists. Vidra Core uses `{ success, data, error, meta }`. The `meta.total` field serves the same purpose. Maintain this mapping.

## Autonomous Execution Checklist

Before marking ANY task complete in autonomous mode, verify ALL of these:

- [ ] Code compiles: `make build`
- [ ] Code formatted: `make fmt`
- [ ] Linter passes: `make lint`
- [ ] Unit tests pass: `make test`
- [ ] OpenAPI specs updated (if API change): `make verify-openapi`
- [ ] Postman collection updated (if API change)
- [ ] README updated (if user-visible change)
- [ ] CLAUDE.md updated (if architecture/structure change)
- [ ] Feature registry updated (if feature added/modified)
- [ ] Domain-specific docs updated (if relevant)
- [ ] No TODO/FIXME/XXX comments left in new code
- [ ] No secrets or credentials in code
- [ ] All new functions have tests
- [ ] All modified functions have updated tests

## Stop Hook Integration

These autonomous mode rules integrate with the stop hooks in `stop-hooks.md`:

| Stop Hook | Autonomous Mode Addition |
|-----------|------------------------|
| Feature Removal Stop | Also update feature registry when adding |
| Test Coverage Stop | Also update Postman collections |
| API Contract Stop | Also update OpenAPI specs |
| Build/Lint/Test Stop | Full `make validate-all` required |
| Architecture Violation Stop | Also update architecture docs |

## Error Recovery in Autonomous Mode

When something fails during autonomous execution:

1. **Test failure:** Fix the test or the code, never delete the test
2. **Lint failure:** Fix the code, never disable the lint rule
3. **Build failure:** Fix the compilation error, check imports
4. **OpenAPI drift:** Regenerate with `make generate-openapi`, fix spec if needed
5. **Postman test failure:** Fix the test or the endpoint, not the collection structure

If 3+ attempts fail on the same issue, STOP and report the problem. Do not brute-force.
