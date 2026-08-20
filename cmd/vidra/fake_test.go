package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeRunner is the exec seam under test: it records what would have been run
// and answers with whatever the test says the host would have answered.
//
// Recording {path, args, env, dir} rather than stubbing at a higher level is
// deliberate. The whole value of the wrapper commands is that the command line
// reaching a script is the one the operator typed, in the order they typed it,
// with the environment the scripts document — and that is a property of the
// exact argv, not of anything a coarser fake could observe.
type fakeRunner struct {
	// environ is the process environment the commands inherit. Empty by
	// default, so a test that says nothing about ENV_FILE is testing a host
	// where nobody exported one.
	environ []string
	// calls is every spec, in order, from both methods.
	calls []execSpec
	// onPassthrough / onCapture are the canned outcomes. nil means "it worked
	// and printed nothing".
	onPassthrough func(spec execSpec, s streams) error
	onCapture     func(spec execSpec) (execResult, error)
}

func (f *fakeRunner) Environ() []string { return f.environ }

func (f *fakeRunner) Passthrough(spec execSpec, s streams) error {
	f.calls = append(f.calls, spec)
	if f.onPassthrough != nil {
		return f.onPassthrough(spec, s)
	}
	return nil
}

func (f *fakeRunner) Capture(_ context.Context, spec execSpec) (execResult, error) {
	f.calls = append(f.calls, spec)
	if f.onCapture != nil {
		return f.onCapture(spec)
	}
	return execResult{}, nil
}

// only returns the single call the test expected there to be.
func (f *fakeRunner) only(t *testing.T) execSpec {
	t.Helper()
	if len(f.calls) != 1 {
		t.Fatalf("want exactly 1 command run, got %d: %v", len(f.calls), f.calls)
	}
	return f.calls[0]
}

// script is the script a spec runs: `bash <script> …`, so the script is the
// first argument rather than the path.
func (c execSpec) script() string {
	if len(c.Args) == 0 {
		return ""
	}
	return c.Args[0]
}

// tail is everything after the script — the arguments the script itself sees.
func (c execSpec) tail() []string {
	if len(c.Args) <= 1 {
		return nil
	}
	return c.Args[1:]
}

// envValue reads one variable out of a spec's child environment.
func (c execSpec) envValue(key string) (string, bool) {
	for _, kv := range c.Env {
		if v, ok := strings.CutPrefix(kv, key+"="); ok {
			return v, true
		}
	}
	return "", false
}

// swapRunner installs a fake for one test and puts the real one back after.
func swapRunner(t *testing.T, f *fakeRunner) *fakeRunner {
	t.Helper()
	prev := theRunner
	theRunner = f
	t.Cleanup(func() { theRunner = prev })
	return f
}

// fakeDeployment writes a directory that LOOKS like a Vidra deployment: the
// scripts the wrapper commands resolve, and an env file. The scripts' contents
// do not matter — nothing here ever runs one — but their existence does, because
// that is the check that tells "wrong --repo" from "the deployment is fine".
func fakeDeployment(t *testing.T, env string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "deploy"), 0o755); err != nil {
		t.Fatalf("mkdir deploy: %v", err)
	}
	for _, name := range []string{"deploy.sh", "rollback.sh", "backup.sh", "restore.sh", "release.sh", "compose.sh"} {
		write(t, filepath.Join(dir, "deploy", name), "#!/usr/bin/env bash\nexit 0\n")
	}
	// rollback.sh is the one script whose CONTENT is read rather than executed:
	// `vidra update` parses its MIN_EMBEDDED_MIGRATE_TAG floor out of it instead
	// of carrying a fourth copy of that number. So the fake carries the assignment
	// in the shape the real script has it, decoys and all.
	write(t, filepath.Join(dir, "deploy", "rollback.sh"), fakeRollbackScript)
	if env != "" {
		if err := os.MkdirAll(filepath.Join(dir, "env"), 0o755); err != nil {
			t.Fatalf("mkdir env: %v", err)
		}
		write(t, filepath.Join(dir, "env", "production.env"), env)
	}
	return dir
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// fakeRollbackScript is deploy/rollback.sh reduced to the part this CLI reads,
// with the shapes that must NOT be read kept around it: the comment paragraph
// that names the variable, and the `die` sentence that interpolates it. Both
// appear in the real script above and below the assignment, and a parser that
// took either of them would report a floor of "MIN_EMBEDDED_MIGRATE_TAG" or of
// nothing at all.
const fakeRollbackScript = `#!/usr/bin/env bash
# It REFUSES a core/search tag below MIN_EMBEDDED_MIGRATE_TAG (set below): those
# images have no embedded ` + "`migrate`" + ` subcommand.
#MIN_EMBEDDED_MIGRATE_TAG="v9.9.9"
MIN_EMBEDDED_MIGRATE_TAG="v0.2.0"   # ADJUST THIS AT RELEASE TIME
require_embedded_migrate_tag() {
  semver_ge "$tag" "$MIN_EMBEDDED_MIGRATE_TAG" || rc=$?
  die "$what=$tag is older than $MIN_EMBEDDED_MIGRATE_TAG, the first release whose image carries the embedded migrator"
}
exit 0
`

// defaultEnv is an env file for a stock single-host deployment: bundled
// datastores, the two profiles the deploy scripts default to.
const defaultEnv = `HTTP_PORT=8080
FRONTEND_PORT=3000
VIDRA_COMPOSE_PROFILES=core frontend
VIDRA_EXTERNAL_POSTGRES=false
VIDRA_EXTERNAL_REDIS=false
`
