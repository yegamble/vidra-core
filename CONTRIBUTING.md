# Contributing to Athena

Thanks for contributing to Athena.

## Development Workflow

1. Create a feature branch from `main`.
2. Make focused commits with clear messages.
3. Run validation before opening a PR.
4. Ensure docs and tests are updated with code changes.

## Setup

See [README.md](README.md) Quick Start and [docs/README.md](docs/README.md).

## Validation Requirements

Run:

```bash
make validate-all
```

At minimum for most changes:

```bash
make test-unit
make lint
```

For infrastructure-dependent changes (DB/Redis/queues):

```bash
make test
```

## Running CI Workflows Locally

Use [`act`](https://github.com/nektos/act) and follow [docs/development/ACT_LOCAL_CI.md](docs/development/ACT_LOCAL_CI.md).

## Pull Request Expectations

- Explain the problem and solution.
- Include test evidence (commands + outcomes).
- Note any follow-up work.
- Keep PRs reviewable in size.

## Security

Do not commit secrets. Use environment variables and `.env` files excluded from git.

If you discover a vulnerability, follow the policy in [docs/security/SECURITY.md](docs/security/SECURITY.md).

## AI-Assisted Contributions

If using AI tools, follow [VALIDATION_REQUIRED.md](VALIDATION_REQUIRED.md) and verify generated output with tests and linting.
