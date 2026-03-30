# Stop Hooks — Autonomous Guardrails

**Purpose:** Prevent code drift from project vision during autonomous operation. These are hard constraints — violations block completion.

**Enforcement:** These rules are wired into `.claude/settings.json`, `.githooks/pre-commit`, and `scripts/verify-autonomous-stop-hooks.sh` so autonomous work is blocked before commit when parity, tests, docs, or feature tracking drift.

## Stop Conditions (MUST halt and fix before proceeding)

### 1. Feature Removal Stop

**Trigger:** Any change that removes, disables, stubs out, or makes unreachable an existing feature listed in `feature-parity-registry.md`.

**Action:** STOP. Revert the removal. Existing features are load-bearing — removing one breaks downstream consumers, federation interop, or API contracts.

**Exception:** Explicit user instruction to deprecate a specific feature, with a migration path.

### 2. Test Coverage Stop

**Trigger:** Any new or modified production code (`internal/`, `cmd/`, `pkg/`) that lacks a corresponding test update.

**Action:** STOP. Write failing test first (TDD), then implement. Every handler, service method, and repository function must have table-driven tests.

**Minimum thresholds:**
- New packages: 80% coverage
- Modified packages: coverage must not decrease
- Domain models: 100% coverage
- HTTP handlers: every status code path tested
- Repository methods: sqlmock-based tests for all queries

### 3. API Contract Stop

**Trigger:** Any change that modifies the shape of an existing API response (field rename, removal, type change) without a migration strategy.

**Action:** STOP. Existing PeerTube clients and federation peers depend on response shapes. Changes require:
1. Additive change (new field) — OK, no stop
2. Breaking change — requires versioned endpoint or deprecation period
3. Response envelope must remain `{success, data, error, meta}`

### 4. Architecture Violation Stop

**Trigger:** Code that violates clean architecture boundaries:
- Domain importing infrastructure packages
- Repository importing HTTP/handler packages
- Global mutable state (package-level vars with side effects)
- Constructor-bypassing direct struct initialization of service types

**Action:** STOP. Refactor to respect layer boundaries:
```
domain/ → NO imports from infrastructure
usecase/ → imports domain/, port/
repository/ → imports domain/, implements port/ interfaces
httpapi/ → imports usecase/, domain/
```

### 5. Security Regression Stop

**Trigger:** Any of:
- SQL string concatenation with user input
- Missing input validation on new endpoints
- Secrets/credentials in code or logs
- SSRF-vulnerable URL handling (see `internal/security/CLAUDE.md`)
- Missing auth middleware on protected endpoints
- File path traversal vulnerabilities

**Action:** STOP. Fix immediately. Security regressions are never acceptable.

### 6. Federation Compatibility Stop

**Trigger:** Changes that break:
- WebFinger discovery (`/.well-known/webfinger`)
- NodeInfo endpoints (`/.well-known/nodeinfo`, `/nodeinfo/2.0`)
- ActivityPub inbox/outbox processing
- HTTP Signature verification
- PeerTube-compatible route aliases

**Action:** STOP. Federation interop is a core requirement. Changes to federation endpoints must be validated against the ActivityPub spec and PeerTube's behavior.

### 7. Build/Lint/Test Stop

**Trigger:** `make validate-all` fails after changes.

**Action:** STOP. Fix all errors before claiming completion. This is non-negotiable:
- `gofmt` — auto-fix formatting
- `goimports` — fix import groups
- `golangci-lint` — fix all warnings (including gosec)
- `go test -short ./...` — all unit tests pass
- `go build ./...` — binary compiles

### 8. Migration Safety Stop

**Trigger:** Any database migration that:
- Drops a column/table without data migration
- Adds NOT NULL column without DEFAULT
- Modifies existing column type without explicit up/down
- Missing `-- +goose Down` section

**Action:** STOP. Migrations must be reversible. See `migrations/CLAUDE.md` for patterns.

### 9. PeerTube Parity Drift Stop

**Trigger:** Any change that claims PeerTube parity while one of the following is true:
- Response or route behavior no longer matches the intended PeerTube contract
- PeerTube-compatible aliases are removed or changed without replacement
- `.claude/rules/feature-parity-registry.md` is not updated to reflect the parity status
- Upstream behavior was not checked for new parity work

**Action:** STOP. Compare the change against PeerTube's docs or implementation, update the parity registry, and document any intentional divergence before proceeding.

### 10. Requested Feature Completion Stop

**Trigger:** A user-requested feature is only partially landed. Examples:
- Implementation exists, but the feature is missing from the parity registry
- Production code changed, but Go tests were not added or updated
- API behavior changed, but OpenAPI and Postman/Newman coverage were not updated
- User-visible behavior changed, but README/CLAUDE/docs were not updated

**Action:** STOP. Requested features must ship as an end-to-end slice: implementation, tests, docs, contract artifacts, and registry tracking together.

## Pre-Change Checklist (Run Before Every Significant Change)

Before modifying any package, verify:

1. **Blast radius:** What calls this code? Check callers/callees
2. **Test coverage:** Does a test exist for the behavior being changed?
3. **API impact:** Does this change any HTTP response shape?
4. **Migration impact:** Does this require a schema change?
5. **Federation impact:** Does this affect ActivityPub/WebFinger/NodeInfo?
6. **Feature registry:** Is the feature being modified in the registry? If so, ensure it stays functional
7. **Upstream parity:** For PeerTube-facing work, what is the expected upstream behavior?
8. **Request traceability:** Has the requested feature been added or updated in the registry before coding?
9. **Completeness artifacts:** Which tests, docs, OpenAPI files, and Postman collections must move with this change?

## Post-Change Verification (Run After Every Significant Change)

```bash
# 1. Run the mandatory full validation suite
make validate-all

# 2. Run affected package tests while iterating
go test -short ./internal/package/... -count=1

# 3. Verify OpenAPI drift when routes/contracts change
make verify-openapi
```

Also confirm:
- `.claude/rules/feature-parity-registry.md` reflects the final status
- Postman/Newman collections were updated for API surface changes
- README, `CLAUDE.md`, and domain docs match the shipped behavior

## Vision Drift Detection

**Signs of drift (any one triggers investigation):**

- Adding a dependency that duplicates existing functionality
- Creating a new package that overlaps with an existing one
- Implementing a feature that contradicts PeerTube compatibility
- Shipping a requested feature without registry, doc, or test updates
- Changing error handling patterns away from `domain.ErrX` sentinels
- Introducing REST endpoint patterns that don't match existing conventions
- Adding configuration that lacks validation or sensible defaults

**Response to drift detection:** Stop, trace back to the requirement, verify against the feature registry and architecture docs. If the change is genuinely needed, update the registry and architecture docs to reflect the new direction.

## Autonomous Mode Rules

When running without user interaction:

1. **Never remove features** — only add or improve
2. **Never break existing tests** — fix, don't skip or delete
3. **Never introduce breaking API changes** — additive only
4. **Never skip validation** — `make validate-all` is mandatory
5. **Never commit secrets** — check before staging
6. **Always write tests first** — TDD is not optional
7. **Always check the feature registry** — ensure parity is maintained
8. **Always verify federation endpoints** — after any routing change
9. **Always run affected tests** — after any code change
10. **Always update OpenAPI specs** — when adding/modifying endpoints
11. **Always update Postman/Newman coverage** — for API surface changes
12. **Always update docs and registry together** — if the behavior is new or user-visible
