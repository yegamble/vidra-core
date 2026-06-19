# Parity Acceptance Rules

A PeerTube or Vidra extension item can move to `VERIFIED` only when the evidence is concrete and reproducible.

## General done criteria

- The behavior is implemented without placeholders.
- Backend and frontend agree through OpenAPI/contracts where applicable.
- Permission/auth/visibility states are implemented.
- Empty/loading/error/success states are implemented for user-visible flows.
- Security/privacy concerns are addressed or the item is blocked/deferred with a reason.
- Docker/local dev instructions still work.
- CI is updated where needed.
- `.ralph/fix_plan.md`, feature ledger, and UI inventory are updated in the same loop.

## Backend evidence

Acceptable evidence includes:

- Unit tests for pure logic.
- Integration tests against live PostgreSQL/Redis.
- Migration tests against a fresh database.
- Newman/Postman API tests for changed endpoints.
- Fuzz tests for risky parsers, URL/URI handling, federation payloads, link previews, importers, and file paths.
- Benchmarks for hot paths where relevant.
- Logs/artifacts from Docker Compose smoke tests.

## Frontend evidence

Acceptable evidence includes:

- Typecheck/lint/build pass.
- Unit/component tests.
- Playwright tests covering the route/control and at least one happy path plus one important failure/permission state.
- Accessibility checks for controls with keyboard navigation and accessible names.
- Screenshot/trace artifacts for critical route changes.
- Contract test against backend or generated types.

## Button/control-level evidence

A UI control is `VERIFIED` only when:

- It has an accessible name.
- It has expected enabled/disabled/loading/error states.
- It performs the expected action or intentionally explains why not.
- Its backend dependency exists or is safely mocked only when the feature is explicitly frontend-only for now.
- It has Playwright coverage where the control is part of a critical flow.

## Intentional differences

Use `INTENTIONAL_DIFFERENCE` only when Vidra deliberately diverges from PeerTube behavior.

Required fields:

- PeerTube behavior.
- Vidra behavior.
- Reason for difference.
- User/admin/operator impact.
- Security/privacy/federation impact.
- Tests proving Vidra behavior.

## Deferred items

Use `DEFERRED` only when the user or spec explicitly defers the item. Premium subscriptions, Inner Circle, custodial payments, creator earnings, and payout logic are currently deferred. Simple verified wallet display is not deferred.
