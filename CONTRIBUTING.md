# Contributing to Athena

Thanks for contributing to Athena.

## Prerequisites

- Go 1.24
- Docker + Docker Compose
- GNU Make
- PostgreSQL client tools (`psql`, `pg_isready`)

## Local Setup

```bash
git clone https://github.com/yegamble/athena.git
cd athena
cp .env.example .env
make deps
make migrate-up
```

## Development Workflow

1. Create a branch from `main`.
2. Implement your change with tests.
3. Run validation locally.
4. Open a pull request with a clear description.

Recommended branch naming:

- `feature/<short-topic>`
- `fix/<short-topic>`
- `chore/<short-topic>`
- `codex/<short-topic>`

Recommended commit style:

- `feat: add channel moderation filters`
- `fix: validate caption language codes`
- `docs: update architecture links`

## Code Quality Requirements

Run before pushing:

```bash
make validate-all
```

Useful focused targets:

```bash
make test-unit
make test-local
make lint
make test-cleanup
```

If you use AI coding tools, read:

- [Validation Requirements](docs/development/VALIDATION_REQUIRED.md)

## Running CI Locally with `act`

Athena workflows are designed for GitHub Actions and can be exercised locally with [`act`](https://github.com/nektos/act).

### 1. Configure runner mappings

`.actrc` in this repository already provides defaults:

- `self-hosted` -> `catthehacker/ubuntu:act-latest`
- `ubuntu-latest` -> `catthehacker/ubuntu:act-latest`

### 2. Provide secrets

Copy `.secrets.example` to `.secrets` (do not commit) and customize values:

```bash
cp .secrets.example .secrets
DATABASE_URL=postgres://test_user:test_password@localhost:5432/athena_test?sslmode=disable
REDIS_URL=redis://localhost:6379/0
JWT_SECRET=local-test-secret
GITHUB_TOKEN=<optional, for workflows that call GitHub API>
```

### 3. Run jobs

```bash
act -l
act -j unit --secret-file .secrets
act -j lint --secret-file .secrets
act -j integration --secret-file .secrets
```

Notes:

- `blue-green-deploy.yml` is not expected to run locally with `act` because it depends on cluster/deploy infrastructure.
- Jobs that upload artifacts or publish external reports may need to be skipped locally.

## Pull Request Checklist

- [ ] Change is scoped and explained in PR description
- [ ] Tests added/updated for behavior changes
- [ ] `make validate-all` passes locally (or failures are explained)
- [ ] Documentation updated when APIs, behavior, or configuration changed
- [ ] No secrets or credentials committed
