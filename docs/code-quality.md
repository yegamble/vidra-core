# Code quality checks

Run `make quality` before pushing: gofmt and vet, then Staticcheck v0.8.2
(including unused declarations) over the default, integration, public-IPFS and
private-IPFS build configurations. Tests remain analysis roots. `make ci`
runs the same gate before its contract, generation and race checks, within the
unconditionally required `build-test` job. Generated sqlc files are
maintained through the existing generation check.

`make workflow-lint` runs pinned Actionlint and zizmor against every workflow.
Install Go 1.27, uv and ShellCheck first. The existing CI guard invokes the same
command. `make vuln` includes test code and dependencies as well as application
code; it retains its independent PR, main and daily lane.

Install the managed hooks when setting up a developer checkout:

```sh
make hooks-install
```

Both pre-commit and pre-push run `make quality` and `make workflow-lint`;
pre-commit also checks staged whitespace. Install Go 1.27, uv and ShellCheck
before enabling hooks. The installer refuses to replace custom hooks or an
existing hooks path. Hooks invoke pinned Lefthook through Go and fail if it
cannot run. These checks do not edit files or need a database. Required hosted
gates enforce the same analysis even when local hooks are bypassed.

When updating tools, change the pin in `scripts/ci/go-quality.sh` in both Go
repositories, and keep `scripts/ci/workflow-lint.sh` identical across all three
repositories. The govulncheck pin lives in each Go repository's `Makefile`.
Keep `scripts/install-hooks.sh` identical across all three repositories.
Update its Lefthook pin together with `lefthook` and `min_version` in
`lefthook.yml`, then rerun the corresponding gates and hook validation.
