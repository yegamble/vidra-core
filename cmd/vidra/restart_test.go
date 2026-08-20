package main

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRestartCommandLine(t *testing.T) {
	dir := fakeDeployment(t, defaultEnv)
	for _, tc := range []struct{ typed, want string }{
		{"api", "api"},
		{"backend", "api"},
		{"web", "frontend"},
		{"cache", "redis"},
	} {
		f := swapRunner(t, &fakeRunner{})
		h := newHarness(t)
		if err := h.run("restart", "-C", dir, tc.typed); err != nil {
			t.Fatalf("`restart %s` = %v, want success", tc.typed, err)
		}
		spec := f.only(t)
		if want := filepath.Join(dir, "deploy", "compose.sh"); spec.script() != want {
			t.Errorf("script = %q, want %q", spec.script(), want)
		}
		if got, want := spec.tail(), []string{"restart", tc.want}; !reflect.DeepEqual(got, want) {
			t.Errorf("compose.sh was given %q, want %q", got, want)
		}
	}
}

// Every refusal, and the sentence each one owes the operator. They exist because
// the compose error they replace sends the operator looking in the wrong place.
func TestRestartRefusals(t *testing.T) {
	stock := fakeDeployment(t, defaultEnv)
	external := fakeDeployment(t, `HTTP_PORT=8080
VIDRA_COMPOSE_PROFILES=core frontend
VIDRA_EXTERNAL_POSTGRES=true
VIDRA_EXTERNAL_REDIS=yes
`)
	coreOnly := fakeDeployment(t, "VIDRA_COMPOSE_PROFILES=core\n")

	for _, tc := range []struct {
		name     string
		dir      string
		args     []string
		mentions []string
	}{
		{
			name:     "a one-shot migrator",
			dir:      stock,
			args:     []string{"migrate"},
			mentions: []string{"one-shot", "exits", "vidra deploy"},
		},
		{
			name:     "the search migrator",
			dir:      stock,
			args:     []string{"search-migrate"},
			mentions: []string{"one-shot"},
		},
		{
			name:     "the volume preparer",
			dir:      stock,
			args:     []string{"prep-volumes"},
			mentions: []string{"one-shot", "volume ownership"},
		},
		{
			name:     "postgres when it is managed elsewhere",
			dir:      external,
			args:     []string{"db"},
			mentions: []string{"VIDRA_EXTERNAL_POSTGRES", "managed outside", "env/production.env"},
		},
		{
			// yes/1/on all mean true to deploy/lib.sh's is_true, and setup.IsTrue
			// is the same list. The two MUST agree or vidra restarts a container
			// the deploy scripts have disabled.
			name:     "redis when the env file spells true as yes",
			dir:      external,
			args:     []string{"cache"},
			mentions: []string{"VIDRA_EXTERNAL_REDIS", "managed outside"},
		},
		{
			name:     "a service outside the enabled profiles",
			dir:      coreOnly,
			args:     []string{"frontend"},
			mentions: []string{"VIDRA_COMPOSE_PROFILES", "\"frontend\"", "vidra deploy"},
		},
		{
			name:     "a service behind an optional profile",
			dir:      stock,
			args:     []string{"captions"},
			mentions: []string{"whisper", "\"captions\"", "VIDRA_COMPOSE_PROFILES"},
		},
		{
			name:     "a name nobody has",
			dir:      stock,
			args:     []string{"nginx"},
			mentions: []string{"not a service", "api", "postgres"},
		},
		{
			name:     "more than one service",
			dir:      stock,
			args:     []string{"api", "frontend"},
			mentions: []string{"one service at a time", "vidra deploy"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := swapRunner(t, &fakeRunner{})
			h := newHarness(t)
			err := h.run(append([]string{"restart", "-C", tc.dir}, tc.args...)...)
			if err == nil {
				t.Fatalf("`restart %s` succeeded, want a refusal", strings.Join(tc.args, " "))
			}
			for _, want := range tc.mentions {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not mention %q: %v", want, err)
				}
			}
			if len(f.calls) != 0 {
				t.Errorf("a refused restart still ran something: %v", f.calls)
			}
		})
	}
}

// An env file that cannot be read is a refusal too: without it there is no way
// to know whether postgres is even part of this deployment, and restarting on a
// guess is how a managed-datastore install gets a second, empty database
// started next to the real one.
func TestRestartNeedsTheEnvFile(t *testing.T) {
	f := swapRunner(t, &fakeRunner{})
	dir := fakeDeployment(t, "")
	h := newHarness(t)
	err := h.run("restart", "-C", dir, "api")
	if err == nil {
		t.Fatal("restart with no env file succeeded, want a refusal")
	}
	for _, want := range []string{"env/production.env", "does not exist", "vidra setup"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}
	if len(f.calls) != 0 {
		t.Errorf("it ran something anyway: %v", f.calls)
	}
}

// Restarting caddy takes the whole site down for a moment, and the operator
// usually wanted the zero-downtime reload instead. Say so BEFORE, not after.
func TestRestartCaddyWarnsAboutTheEdge(t *testing.T) {
	f := swapRunner(t, &fakeRunner{})
	dir := fakeDeployment(t, defaultEnv)
	h := newHarness(t)
	if err := h.run("restart", "-C", dir, "proxy"); err != nil {
		t.Fatalf("err = %v, want success", err)
	}
	out := h.out.String()
	for _, want := range []string{"vidra deploy", "reload", "TLS edge"} {
		if !strings.Contains(out, want) {
			t.Errorf("the note does not mention %q:\n%s", want, out)
		}
	}
	// It is a note, not a refusal: the restart still happens.
	if got, want := f.only(t).tail(), []string{"restart", "caddy"}; !reflect.DeepEqual(got, want) {
		t.Errorf("compose.sh was given %q, want %q", got, want)
	}
}

func TestRestartUsage(t *testing.T) {
	swapRunner(t, &fakeRunner{})
	h := newHarness(t)
	if err := h.run("restart", "-h"); err != nil {
		t.Fatalf("`restart -h` = %v, want success", err)
	}
	for _, want := range []string{"usage: vidra restart", "api/backend", "one-shot", "vidra deploy"} {
		if !strings.Contains(h.out.String(), want) {
			t.Errorf("help does not mention %q:\n%s", want, h.out.String())
		}
	}

	h2 := newHarness(t)
	if err := h2.run("restart", "-C", t.TempDir()); err == nil {
		t.Fatal("`restart` with no service succeeded, want a usage failure")
	}
	if !strings.Contains(h2.err.String(), "name one service") {
		t.Errorf("stderr does not say what is missing:\n%s", h2.err.String())
	}
}

// vidra's flags come BEFORE the service name — parseWrapperFlags stops at the
// first argument it does not recognise — so `vidra restart whisper -C /srv/vidra`
// leaves -C and its value in the positional list. Reporting them as extra
// SERVICES ("whisper, -C, /srv/vidra were given") sends the operator looking for
// a service called "-C"; the flags are this command's own.
func TestRestartNamesAMisplacedFlag(t *testing.T) {
	dir := fakeDeployment(t, defaultEnv)
	swapRunner(t, &fakeRunner{})
	h := newHarness(t)

	err := h.run("restart", "whisper", "-C", dir)
	if err == nil {
		t.Fatal("`restart whisper -C <dir>` succeeded, want a refusal")
	}
	for _, want := range []string{"-C", "flag", "BEFORE the service name"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not mention %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "one service at a time") {
		t.Errorf("a misplaced flag was reported as an extra service: %v", err)
	}

	// Two real services still get the message that IS about services.
	err = h.run("restart", "-C", dir, "api", "frontend")
	if err == nil || !strings.Contains(err.Error(), "one service at a time") {
		t.Fatalf("err = %v, want the one-service-at-a-time refusal", err)
	}
	if strings.Contains(err.Error(), "BEFORE the service name") {
		t.Errorf("two services were reported as a misplaced flag: %v", err)
	}
}
