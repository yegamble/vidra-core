package main

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Every wrapper command has to be reachable and discoverable: a subcommand
// nobody can find is a subcommand nobody runs, and the whole point of this wave
// is that an operator learns `vidra <verb>` instead of a directory of scripts.
func TestWrapperCommandsAreInTheDispatchTable(t *testing.T) {
	h := newHarness(t)
	if err := h.run("help"); err != nil {
		t.Fatalf("`vidra help` = %v, want success", err)
	}
	out := h.out.String()
	for _, name := range []string{"deploy", "rollback", "backup", "restore", "release", "logs", "restart", "status"} {
		if !strings.Contains(out, "\n  "+name+" ") {
			t.Errorf("`vidra help` does not list %q:\n%s", name, out)
		}
	}
}

func TestPassthroughRunsTheScriptUnderRepo(t *testing.T) {
	for _, name := range []string{"deploy", "rollback", "backup", "restore", "release"} {
		t.Run(name, func(t *testing.T) {
			f := swapRunner(t, &fakeRunner{})
			dir := fakeDeployment(t, defaultEnv)
			h := newHarness(t)
			if err := h.run(name, "-C", dir); err != nil {
				t.Fatalf("`vidra %s` = %v, want success", name, err)
			}
			spec := f.only(t)
			// bash, not the script's own executable bit: a checkout on a noexec
			// mount or unpacked from a tarball still has to deploy.
			if spec.Path != "bash" {
				t.Errorf("Path = %q, want bash", spec.Path)
			}
			if want := filepath.Join(dir, "deploy", name+".sh"); spec.script() != want {
				t.Errorf("script = %q, want %q", spec.script(), want)
			}
			if spec.Dir != dir {
				t.Errorf("Dir = %q, want the deployment root %q", spec.Dir, dir)
			}
		})
	}
}

// The one usage error these commands have. It must name the path it looked for,
// because the mistake behind it is always the same one — running from a
// component checkout instead of the deployment directory — and the path is what
// makes that obvious.
func TestPassthroughRefusesADirectoryThatIsNotADeployment(t *testing.T) {
	swapRunner(t, &fakeRunner{})
	dir := t.TempDir()
	h := newHarness(t)
	err := h.run("deploy", "-C", dir)
	if err == nil {
		t.Fatal("`vidra deploy` on an empty directory succeeded, want a refusal")
	}
	for _, want := range []string{filepath.Join(dir, "deploy", "deploy.sh"), "docker-compose.yml", "deploy/", "env/"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}
}

// ENV_FILE precedence, which is deploy/lib.sh's and not this CLI's to invent.
func TestPassthroughENVFILE(t *testing.T) {
	dir := fakeDeployment(t, defaultEnv)
	for _, tc := range []struct {
		name    string
		environ []string
		args    []string
		want    string
	}{
		{
			name: "the default is exported when nothing else set it",
			args: []string{"deploy", "-C", dir},
			want: "env/production.env",
		},
		{
			name: "--env wins",
			args: []string{"deploy", "-C", dir, "--env", "env/staging.env"},
			want: "env/staging.env",
		},
		{
			// The one that matters: `ENV_FILE=env/staging.env vidra deploy` has
			// to deploy staging. A flag DEFAULT must never beat something the
			// operator exported on purpose.
			name:    "an exported ENV_FILE survives our default",
			environ: []string{"ENV_FILE=env/staging.env", "PATH=/usr/bin"},
			args:    []string{"deploy", "-C", dir},
			want:    "env/staging.env",
		},
		{
			name:    "an explicit --env still beats an exported one",
			environ: []string{"ENV_FILE=env/staging.env"},
			args:    []string{"deploy", "-C", dir, "--env", "env/other.env"},
			want:    "env/other.env",
		},
		{
			// The scripts read ${ENV_FILE:-...}, so empty means unset to them
			// and must mean unset here.
			name:    "an exported but empty ENV_FILE is not a value",
			environ: []string{"ENV_FILE="},
			args:    []string{"deploy", "-C", dir},
			want:    "env/production.env",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := swapRunner(t, &fakeRunner{environ: tc.environ})
			h := newHarness(t)
			if err := h.run(tc.args...); err != nil {
				t.Fatalf("run = %v, want success", err)
			}
			got, ok := f.only(t).envValue("ENV_FILE")
			if !ok {
				t.Fatalf("ENV_FILE was not in the child environment: %v", f.only(t).Env)
			}
			if got != tc.want {
				t.Errorf("ENV_FILE = %q, want %q", got, tc.want)
			}
		})
	}

	// The rest of the environment is inherited whole, which is what makes
	// `POSTGRES_DB=other vidra backup` work exactly as it does for the script.
	f := swapRunner(t, &fakeRunner{environ: []string{"PATH=/usr/bin", "POSTGRES_DB=other"}})
	h := newHarness(t)
	if err := h.run("backup", "-C", dir); err != nil {
		t.Fatalf("run = %v, want success", err)
	}
	if v, _ := f.only(t).envValue("POSTGRES_DB"); v != "other" {
		t.Errorf("POSTGRES_DB = %q, want the inherited value", v)
	}
}

// Argument passthrough. A FlagSet over the whole argv would eat every one of
// these; the leading-flags loop is what keeps them the script's.
func TestPassthroughPassesArgumentsVerbatim(t *testing.T) {
	dir := fakeDeployment(t, defaultEnv)
	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "restore keeps --yes and the dump path",
			args: []string{"restore", "-C", dir, "--yes", "backups/vidra-20260728T030000Z.dump.gz"},
			want: []string{"--yes", "backups/vidra-20260728T030000Z.dump.gz"},
		},
		{
			name: "rollback keeps its per-repo tag flags, in order",
			args: []string{"rollback", "-C", dir, "--core", "v0.2.1", "--user", "v0.2.0", "--search", "v0.2.0"},
			want: []string{"--core", "v0.2.1", "--user", "v0.2.0", "--search", "v0.2.0"},
		},
		{
			name: "release keeps the tag and the repo list",
			args: []string{"release", "-C", dir, "--yes", "v0.2.0", "vidra-core", "vidra-search"},
			want: []string{"--yes", "v0.2.0", "vidra-core", "vidra-search"},
		},
		{
			// Once the script's arguments have started, ours are no longer
			// ours: a --env here belongs to the script, whatever it makes of it.
			name: "a vidra flag AFTER a script argument is the script's",
			args: []string{"restore", "-C", dir, "dump.gz", "--env", "env/other.env"},
			want: []string{"dump.gz", "--env", "env/other.env"},
		},
		{
			name: "-- ends vidra's flags explicitly",
			args: []string{"restore", "-C", dir, "--", "--repo", "x.dump"},
			want: []string{"--repo", "x.dump"},
		},
		{
			name: "--flag=value spellings are ours when they lead",
			args: []string{"backup", "--repo=" + dir, "--env=env/staging.env"},
			want: nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := swapRunner(t, &fakeRunner{})
			h := newHarness(t)
			if err := h.run(tc.args...); err != nil {
				t.Fatalf("run = %v, want success", err)
			}
			if got := f.only(t).tail(); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("the script was given %q, want %q", got, tc.want)
			}
		})
	}

	// The --flag=value spellings are parsed, not merely tolerated.
	f := swapRunner(t, &fakeRunner{})
	h := newHarness(t)
	if err := h.run("backup", "--repo="+dir, "--env=env/staging.env"); err != nil {
		t.Fatalf("run = %v, want success", err)
	}
	spec := f.only(t)
	if spec.Dir != dir {
		t.Errorf("--repo=<dir> was not honoured: Dir = %q", spec.Dir)
	}
	if v, _ := spec.envValue("ENV_FILE"); v != "env/staging.env" {
		t.Errorf("--env=<file> was not honoured: ENV_FILE = %q", v)
	}
}

// Exit-code fidelity. The number is the contract for `vidra deploy || …`, for
// cron and for CI, and the script has already printed the sentence explaining
// it — so vidra must add no line of its own.
func TestPassthroughPropagatesTheExitCodeSilently(t *testing.T) {
	dir := fakeDeployment(t, defaultEnv)
	f := swapRunner(t, &fakeRunner{
		onPassthrough: func(_ execSpec, s streams) error {
			// What deploy.sh does before exiting non-zero.
			_, _ = s.err.Write([]byte("[deploy] ERROR: refusing to deploy v0.1.9\n"))
			return newExitError(3)
		},
	})
	h := newHarness(t)
	err := h.run("deploy", "-C", dir)

	var exit *exitError
	if !errors.As(err, &exit) {
		t.Fatalf("err = %v (%T), want an exit-code error", err, err)
	}
	if exit.code != 3 {
		t.Errorf("exit code = %d, want the child's 3", exit.code)
	}
	if got := h.err.String(); got != "[deploy] ERROR: refusing to deploy v0.1.9\n" {
		t.Errorf("vidra added output of its own to stderr:\n%s", got)
	}
	if h.out.Len() != 0 {
		t.Errorf("vidra wrote to stdout:\n%s", h.out.String())
	}
	_ = f

	// A signal death reports -1, which is not a code a shell can return. It has
	// to become a FAILURE rather than a success.
	swapRunner(t, &fakeRunner{onPassthrough: func(execSpec, streams) error { return newExitError(-1) }})
	err = h.run("deploy", "-C", dir)
	if !errors.As(err, &exit) || exit.code != 1 {
		t.Errorf("err = %v, want exit code 1 for a signalled child", err)
	}
}

func TestPassthroughHelp(t *testing.T) {
	for _, tc := range []struct{ command, script, mentions string }{
		{"deploy", "deploy/deploy.sh", "VIDRA_CORE_TAG"},
		{"rollback", "deploy/rollback.sh", "does not touch the database"},
		{"backup", "deploy/backup.sh", "pg_dump"},
		{"restore", "deploy/restore.sh", "RESTORE_CONFIRM"},
		{"release", "deploy/release.sh", "GHCR"},
	} {
		t.Run(tc.command, func(t *testing.T) {
			swapRunner(t, &fakeRunner{})
			h := newHarness(t)
			if err := h.run(tc.command, "-h"); err != nil {
				t.Fatalf("`%s -h` = %v, want success", tc.command, err)
			}
			out := h.out.String()
			for _, want := range []string{"usage: vidra " + tc.command, tc.script, tc.mentions, "-C, --repo", "--env"} {
				if !strings.Contains(out, want) {
					t.Errorf("help does not mention %q:\n%s", want, out)
				}
			}
			if h.err.Len() != 0 {
				t.Errorf("help wrote to stderr:\n%s", h.err.String())
			}
		})
	}

	// -h must not reach the script: `vidra release -h` explains the wrapper, and
	// running release.sh to print its own header would be a surprise.
	f := swapRunner(t, &fakeRunner{})
	h := newHarness(t)
	if err := h.run("release", "-h"); err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(f.calls) != 0 {
		t.Errorf("-h ran something: %v", f.calls)
	}
}

func TestWrapperFlagsRejectAMissingValue(t *testing.T) {
	swapRunner(t, &fakeRunner{})
	h := newHarness(t)
	if err := h.run("deploy", "--env"); err == nil {
		t.Fatal("`deploy --env` with no value succeeded, want a usage failure")
	}
	if !strings.Contains(h.err.String(), "needs a value") {
		t.Errorf("stderr does not explain the problem:\n%s", h.err.String())
	}
	if !strings.Contains(h.err.String(), "usage: vidra deploy") {
		t.Errorf("usage was not printed after the error:\n%s", h.err.String())
	}
}
