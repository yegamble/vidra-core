# Running GitHub Actions Locally with act

Athena workflows can be executed locally using [`act`](https://github.com/nektos/act).

## Prerequisites

- Docker Engine running
- `act` installed
- Local environment variables/secrets configured

## Runner Mapping

The repository `.actrc` maps GitHub runners to local images. Current defaults:

- `self-hosted` -> `catthehacker/ubuntu:act-latest`
- `ubuntu-latest` -> `catthehacker/ubuntu:act-latest`
- `ubuntu-22.04` -> `catthehacker/ubuntu:act-22.04`
- `ubuntu-20.04` -> `catthehacker/ubuntu:act-20.04`

## List Jobs

```bash
act -l
```

## Run Specific Jobs

```bash
act -j test-unit
act -j openapi-validation
```

## Provide Secrets

Use `--secret` flags or a local `.secrets` file:

```bash
act -j test \
  -s DATABASE_URL=postgres://athena:athena@localhost:5432/athena_test?sslmode=disable \
  -s JWT_SECRET=replace-me \
  -s REDIS_ADDR=localhost:6379
```

## Commonly Required Variables

Adjust per workflow as needed:

- `DATABASE_URL`
- `JWT_SECRET`
- `REDIS_ADDR`
- OAuth client IDs/secrets (if auth provider tests run)
- `GITHUB_TOKEN` (for certain GitHub API actions)

## Skipping Unsupported Steps

Some cloud-only steps (artifact upload, external publishing) may not be useful locally.

Options:

- Run specific jobs instead of entire workflows
- Use workflow conditionals to skip publish/upload when running under act
- Pass alternative event payloads with `--eventpath`

## Suggested Local CI Sequence

```bash
act -j lint
act -j test-unit
act -j test-integration
```
