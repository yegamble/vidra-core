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

For GrumPHP-style local hooks, install the pinned runner and opt in:

```sh
go install github.com/evilmartians/lefthook/v2@v2.2.2
lefthook install
```

The pre-commit hook checks staged whitespace without editing files. Pre-push
runs `make quality`; it needs no database and does not run vulnerability scans.
Hooks supplement the required hosted checks.

When updating tools, change the pin in `scripts/ci/go-quality.sh` in both Go
repositories, and keep `scripts/ci/workflow-lint.sh` identical across all three
repositories. The govulncheck pin lives in each Go repository's `Makefile`.
Update Lefthook's install command here together with `min_version` in
`lefthook.yml`, then rerun the corresponding gates and hook validation.
