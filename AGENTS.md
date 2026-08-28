# AGENTS.md — vidra-core

Go backend for vidra, a self-hostable video platform: one Echo v4 HTTP API
(every route registers in `internal/httpapi/server.go` `routes()`), sqlc for
all SQL, golang-migrate migrations, in-process workers (transcode, imports,
search outbox, media GC). The frontend (yegamble/vidra-user) consumes this
repo only through `api/openapi.yaml` — the spec is the contract.

## Verification gates (run before opening any PR; paste the output tail into the PR body)

```
make ci        # fmt-check, vet, openapi-verify, sqlc-verify, test-race
```

Integration tests need live Postgres + Redis:

```
docker compose --profile core up -d postgres redis migrate
DATABASE_URL=postgres://vidra:vidra@localhost:5432/vidra?sslmode=disable \
REDIS_URL=redis://localhost:6379/0 \
go test -tags=integration ./internal/store/... ./internal/federation/...
```

If docker is unavailable, say so plainly in the PR — and ALWAYS run
`go vet -tags=integration ./...` so tagged tests at least compile (a
`:execrows` → `:one` change once broke only the federation integration lane).

## Hard rules

1. **One small PR per session** (< 300 changed lines). List every other
   finding in the PR body under "Also found (not fixed here)".
2. **TDD**: failing test first. House test idioms: in-memory fake repos that
   mirror SQL semantics (duplicates return `pgx.ErrNoRows` where
   `ON CONFLICT DO NOTHING` applies); audit events proven via the capture
   buffer + `findAudit` pattern in httpapi tests; SQL fan-out rules proven in
   `internal/store` integration tests, not unit fakes.
3. **Never hand-edit `internal/store/sqlcgen/**`** — edit
   `internal/store/queries/*.sql` and run `make sqlc` (sqlc pinned v1.31.1).
4. **Migrations are append-only**: new file with the next 4-digit number and
   a matching `.down.sql`. Never edit an existing migration. Set-based
   fan-outs with idempotency indexes follow the 0101/0103 pattern.
5. **OpenAPI stays in lock-step**: any route or shape change updates
   `api/openapi.yaml`; `TestOpenAPIContract` fails in BOTH directions (route
   without doc, doc without route).
6. **Never log tokens, passwords, email addresses, message bodies, or report
   reasons** — the observability sensitive-key denylist discipline. Auth
   middleware is mandatory: admin routes `requireRole`, user routes
   `requireAuth`/`optionalAuth` plus ownership checks in the handler.
   Shared-secret comparisons use `subtle.ConstantTimeCompare`.
7. **Do not bump dependencies** (Dependabot owns bumps), do not touch
   `.github/workflows`, never commit secrets or `.env` files.

## Git hygiene — finished means merged (all agents / AI tools)

These rules bind every AI tool working in this repo (Claude, Jules, Codex, …):

1. **Commit early, push often.** Work on a short-lived branch off `main`.
   Prefer several small, scoped commits over one session-end mega-commit, and
   push the branch at every green checkpoint — unpushed work does not exist.
2. **A task is finished only when its work is merged to `main` and pushed.**
   Once the verification gates and the PR's CI are green, merge the PR before
   declaring the task done. If you cannot merge (no permission, review
   requested, red CI), report the task as **open — awaiting merge**, never as
   finished/complete/done.
3. **Delete merged branches.** Immediately after a merge: delete the work
   branch on the remote (`git push origin --delete <branch>`), delete it
   locally (`git branch -d <branch>`), then `git fetch --prune`. Also sweep
   for leftovers each session: delete any local (`git branch --merged
   origin/main`) or remote (`git branch -r --merged origin/main`) branch
   already merged into `origin/main`. Never delete `main`, the branch you are
   on, or an unmerged branch — an unmerged stray is reported for triage, not
   deleted.

## Architecture conventions

- Services are HTTP-agnostic packages under `internal/`, wired in
  `cmd/api/main.go` via options; handlers stay thin.
- Notifications/emails are best-effort side effects: a notification or mail
  failure must never fail the underlying action.
- Feature gates: env config + the instance-settings DB overlay
  (`internal/instancesettings`); a new bool setting needs a registry row and
  shows up in the settings-count test.

## PR conventions

- Title: `[<agent>] <area>: <summary>`.
- Body opens with a one-line WHY, then the verification output tail.
- Never describe an exploitable-but-unfixed security issue in detail in a
  public PR or issue — flag it as "security: needs owner attention" with
  minimal detail.
