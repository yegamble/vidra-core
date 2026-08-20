package main

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// `vidra logs` is one command line, and the whole test is that command line: the
// tail is the meta repo's `make prod-logs` (-f --tail=100), it goes through
// deploy/compose.sh so it addresses the deploy scripts' own compose project, and
// the service names are translated on the way.
func TestLogsCommandLine(t *testing.T) {
	dir := fakeDeployment(t, defaultEnv)
	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "no service follows everything",
			args: []string{"logs", "-C", dir},
			want: []string{"logs", "-f", "--tail=100"},
		},
		{
			name: "product names are translated",
			args: []string{"logs", "-C", dir, "backend", "db", "proxy"},
			want: []string{"logs", "-f", "--tail=100", "api", "postgres", "caddy"},
		},
		{
			name: "compose names pass through as themselves",
			args: []string{"logs", "-C", dir, "api", "otel-collector"},
			want: []string{"logs", "-f", "--tail=100", "api", "otel-collector"},
		},
		{
			// A name the table has not heard of still reaches compose, which
			// owns the "no such service" answer — and which is also how a
			// service nobody has aliased yet stays reachable.
			name: "an unknown name is left for compose to judge",
			args: []string{"logs", "-C", dir, "minio"},
			want: []string{"logs", "-f", "--tail=100", "minio"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := swapRunner(t, &fakeRunner{})
			h := newHarness(t)
			if err := h.run(tc.args...); err != nil {
				t.Fatalf("run = %v, want success", err)
			}
			spec := f.only(t)
			if want := filepath.Join(dir, "deploy", "compose.sh"); spec.script() != want {
				t.Errorf("script = %q, want %q", spec.script(), want)
			}
			if got := spec.tail(); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("compose.sh was given %q, want %q", got, tc.want)
			}
			if v, _ := spec.envValue("ENV_FILE"); v != defaultEnvFile {
				t.Errorf("ENV_FILE = %q, want %q", v, defaultEnvFile)
			}
		})
	}
}

func TestLogsHelpAndBadRepo(t *testing.T) {
	swapRunner(t, &fakeRunner{})
	h := newHarness(t)
	if err := h.run("logs", "-h"); err != nil {
		t.Fatalf("`logs -h` = %v, want success", err)
	}
	for _, want := range []string{"usage: vidra logs", "last 100 lines", "-C, --repo"} {
		if !strings.Contains(h.out.String(), want) {
			t.Errorf("help does not mention %q:\n%s", want, h.out.String())
		}
	}

	err := h.run("logs", "-C", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "compose.sh") {
		t.Errorf("err = %v, want a refusal naming the missing compose.sh", err)
	}
}
