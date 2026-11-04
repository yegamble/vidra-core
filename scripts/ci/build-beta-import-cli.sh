#!/usr/bin/env bash
# Release-free beta artifacts must name the source GitHub actually executed.
set -euo pipefail
die() { printf 'beta-import-cli: %s\n' "$*" >&2; exit 1; }
: "${BETA_TAG:?}" "${EXPECTED_SHA:?}" "${OUT_DIR:?}" "${WORKFLOW_SHA:?}"
[[ "$BETA_TAG" =~ ^beta-[A-Za-z0-9][A-Za-z0-9._-]*$ && ${#BETA_TAG} -le 128 ]] || die 'invalid beta tag'
[[ "$EXPECTED_SHA" =~ ^[0-9a-f]{40}$ ]] || die 'expected SHA must be full40hex'
[[ "$GITHUB_REF" == "refs/tags/$BETA_TAG" && "$GITHUB_SHA" == "$EXPECTED_SHA" ]] || die 'dispatch ref/SHA mismatch'
[[ "$GITHUB_WORKFLOW_REF" == "$GITHUB_REPOSITORY/.github/workflows/publish-beta-import-cli.yml@refs/tags/$BETA_TAG" && "$WORKFLOW_SHA" == "$EXPECTED_SHA" ]] || die 'workflow source mismatch'
[[ "$(git rev-parse HEAD)" == "$EXPECTED_SHA" && "$(git rev-parse "refs/tags/$BETA_TAG^{commit}")" == "$EXPECTED_SHA" ]] || die 'checkout/tag mismatch'
[[ -z "$(git status --porcelain --untracked-files=normal)" ]] || die 'source checkout is dirty'
[[ "$OUT_DIR" == /* && "$OUT_DIR" != "$PWD" && "$OUT_DIR" != "$PWD/"* ]] || die 'outputs must be outside the checkout'
tree=$(git rev-parse 'HEAD^{tree}')
mkdir -p "$OUT_DIR"
binary="peertube-import_${BETA_TAG}_linux_amd64"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOFLAGS=-mod=readonly \
  go build -trimpath -buildvcs=true -o "$OUT_DIR/$binary" ./cmd/peertube-import
go version -m -json "$OUT_DIR/$binary" >"$OUT_DIR/buildinfo.json"
python3 - "$OUT_DIR" "$binary" "$tree" <<'PY'
import json,os,pathlib,sys
root=pathlib.Path(sys.argv[1]);info=json.loads((root/'buildinfo.json').read_text())
settings={row['Key']:row['Value'] for row in info['Settings']}
expected={'GOOS':'linux','GOARCH':'amd64','CGO_ENABLED':'0','-trimpath':'true','vcs.revision':os.environ['EXPECTED_SHA'],'vcs.modified':'false'}
if info['Path']!='github.com/vidra/vidra-core/cmd/peertube-import' or not info['GoVersion'].startswith('go1.27.') or any(settings.get(k)!=v for k,v in expected.items()):
    raise SystemExit('beta-import-cli: binary buildinfo mismatch')
provenance=dict(binary=sys.argv[2],tag=os.environ['BETA_TAG'],source_commit=os.environ['EXPECTED_SHA'],source_tree=sys.argv[3],
 toolchain=info['GoVersion'],platform='linux/amd64',cgo_enabled=False,workflow_ref=os.environ['GITHUB_WORKFLOW_REF'],
 workflow_sha=os.environ['WORKFLOW_SHA'],run_id=os.environ['GITHUB_RUN_ID'],run_attempt=os.environ['GITHUB_RUN_ATTEMPT'])
(root/'provenance.json').write_text(json.dumps(provenance,sort_keys=True,indent=2)+'\n')
PY
env -i PATH=/usr/bin:/bin "$OUT_DIR/$binary" --help >/dev/null 2>&1
[[ -z "$(git status --porcelain --untracked-files=normal)" ]] || die 'build changed source checkout'
(
  cd "$OUT_DIR"
  sha256sum "$binary" buildinfo.json provenance.json > SHA256SUMS
)
