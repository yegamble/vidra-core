package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// The env file has to be resolved ONCE.
//
// vidra reads the deployment's env file for itself — to decide whether a service
// is enabled, whether a datastore has been moved off the box, which port to probe
// — and it also exports ENV_FILE to the script it runs. Those were two different
// rules: the child got the operator's exported ENV_FILE, and vidra's own reads
// got the --env flag's value, which has a default. `export ENV_FILE=env/staging.env`
// therefore made vidra refuse, permit and probe against production while the
// script underneath ran staging, which is the worst shape a disagreement can take:
// every sentence printed was about a deployment the command was not acting on.
func TestEnvFileResolvesOnce(t *testing.T) {
	dir := fakeDeployment(t, defaultEnv)
	for _, tc := range []struct {
		name    string
		environ []string
		args    []string
		want    string
	}{
		{"neither: the default", nil, nil, defaultEnvFile},
		{"--env alone", nil, []string{"--env", "env/staging.env"}, "env/staging.env"},
		{"an exported ENV_FILE alone", []string{"ENV_FILE=env/staging.env"}, nil, "env/staging.env"},
		{"--env beats an exported one", []string{"ENV_FILE=env/staging.env"}, []string{"--env", "env/other.env"}, "env/other.env"},
		{"an exported but empty ENV_FILE is not a value", []string{"ENV_FILE="}, nil, defaultEnvFile},
		{"the last exported ENV_FILE wins, as in a real environment", []string{"ENV_FILE=env/a.env", "ENV_FILE=env/b.env"}, nil, "env/b.env"},
		{"--env spelled as the default is still --env", []string{"ENV_FILE=env/staging.env"}, []string{"--env", defaultEnvFile}, defaultEnvFile},
	} {
		t.Run(tc.name, func(t *testing.T) {
			swapRunner(t, &fakeRunner{environ: tc.environ})
			f, err := parseWrapperFlags("status", append([]string{"-C", dir}, tc.args...))
			if err != nil {
				t.Fatalf("parseWrapperFlags: %v", err)
			}
			dep, _, err := resolve("status", f, "compose.sh")
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if dep.envFile != tc.want {
				t.Errorf("vidra reads %q, want %q", dep.envFile, tc.want)
			}
			child, ok := execSpec{Env: dep.env}.envValue("ENV_FILE")
			if !ok {
				t.Fatalf("ENV_FILE was not in the child environment: %v", dep.env)
			}
			// THE INVARIANT. Everything else in this test is a spelling of it.
			if child != dep.envFile {
				t.Errorf("vidra reads %q while the script it runs reads %q — they must be one file", dep.envFile, child)
			}
			if want := filepath.Join(dir, tc.want); dep.envPath != want {
				t.Errorf("envPath = %q, want %q", dep.envPath, want)
			}
		})
	}
}

// stagingDeployment is a deployment whose staging env file disagrees with its
// production one about everything the three regressions below turn on: which
// profiles are enabled, whether Postgres is external, and which ports are
// published.
func stagingDeployment(t *testing.T) string {
	t.Helper()
	dir := fakeDeployment(t, defaultEnv)
	write(t, filepath.Join(dir, "env", "staging.env"), `HTTP_PORT=9099
FRONTEND_PORT=9098
VIDRA_COMPOSE_PROFILES=core
VIDRA_EXTERNAL_POSTGRES=true
VIDRA_EXTERNAL_REDIS=false
`)
	return dir
}

// (a) The profile refusal has to quote the profiles of the file this run uses.
// Under `export ENV_FILE=env/staging.env` it quoted production's, so the refusal
// named a profile list the operator could not find in the file they were editing.
func TestExportedEnvFileDrivesTheProfileRefusal(t *testing.T) {
	dir := stagingDeployment(t)

	swapRunner(t, &fakeRunner{environ: []string{"ENV_FILE=env/staging.env"}})
	h := newHarness(t)
	err := h.run("restart", "-C", dir, "frontend")
	if err == nil {
		t.Fatal("`restart frontend` succeeded against a staging file that does not enable it")
	}
	// The profile list it quotes is staging's ("core"), not production's
	// ("core", "frontend") — which, being the list that contains the profile the
	// service needs, would have made the refusal read as nonsense.
	if !strings.Contains(err.Error(), `enables "core".`) || strings.Contains(err.Error(), `"core", "frontend"`) {
		t.Errorf("the refusal quotes the wrong file's profiles: %v", err)
	}

	// The same command with nothing exported still restarts it: production's file
	// does enable the frontend.
	f := swapRunner(t, &fakeRunner{})
	if err := h.run("restart", "-C", dir, "frontend"); err != nil {
		t.Fatalf("restart frontend on the production file = %v, want success", err)
	}
	if got := strings.Join(f.only(t).tail(), " "); got != "restart frontend" {
		t.Errorf("compose.sh was given %q, want %q", got, "restart frontend")
	}
}

// (b) The external-datastore refusal is the one that MUST NOT be bypassed: a
// deployment that has moved Postgres off the box parks the bundled container on a
// profile nothing enables, so restarting it is meaningless. Reading production's
// file (where it is false) let the command sail past the check and hand compose a
// service that is not there.
func TestExportedEnvFileDrivesTheExternalDatastoreRefusal(t *testing.T) {
	dir := stagingDeployment(t)

	f := swapRunner(t, &fakeRunner{environ: []string{"ENV_FILE=env/staging.env"}})
	h := newHarness(t)
	err := h.run("restart", "-C", dir, "db")
	if err == nil {
		t.Fatal("`restart db` was allowed against a file that says Postgres is external")
	}
	for _, want := range []string{"VIDRA_EXTERNAL_POSTGRES", "env/staging.env", "managed outside"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not mention %q", err, want)
		}
	}
	if len(f.calls) != 0 {
		t.Errorf("compose was run anyway: %v", f.calls)
	}
}

// (c) status probed the ports of a file it was not reporting on, under a header
// naming a third thing. The header names the file the ports came from.
func TestExportedEnvFileDrivesStatusPortsAndHeader(t *testing.T) {
	dir := stagingDeployment(t)
	swapRunner(t, &fakeRunner{environ: []string{"ENV_FILE=env/staging.env"}})

	h := newHarness(t)
	// Nothing is listening on staging's ports, so every probe is a ⚠ naming the
	// port it dialled — which is exactly the evidence this test wants.
	_ = h.run("status", "-C", dir)
	out := h.out.String()
	for _, want := range []string{"(env/staging.env)", "127.0.0.1:9099", "127.0.0.1:9098"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report is missing %q:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"env/production.env", ":8080", ":3000"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("the report still mentions %q, which belongs to the other env file:\n%s", unwanted, out)
		}
	}
}
