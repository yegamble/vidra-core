package main

import (
	"strings"
	"testing"
)

// The dispatch table entry: `vidra doctor` has to be reachable and discoverable.
// A subcommand nobody can find is a subcommand nobody runs.
func TestDoctorIsInTheDispatchTable(t *testing.T) {
	h := newHarness(t)
	if err := h.run("help"); err != nil {
		t.Fatalf("`vidra help` = %v, want success", err)
	}
	out := h.out.String()
	if !strings.Contains(out, "doctor") {
		t.Errorf("`vidra help` does not list doctor:\n%s", out)
	}
	// The summary is what an operator reads to decide whether this is the command
	// they want, so it has to say what it looks at.
	for _, want := range []string{"compose", "backups"} {
		if !strings.Contains(out, want) {
			t.Errorf("the doctor summary does not mention %q:\n%s", want, out)
		}
	}
}

func TestDoctorHelpSucceedsOnStdout(t *testing.T) {
	for _, flagName := range []string{"-h", "--help"} {
		t.Run(flagName, func(t *testing.T) {
			h := newHarness(t)
			if err := h.run("doctor", flagName); err != nil {
				t.Fatalf("`doctor %s` = %v, want success", flagName, err)
			}
			out := h.out.String()
			if !strings.Contains(out, "usage: vidra doctor") {
				t.Errorf("help was not printed to stdout:\n%s", out)
			}
			// The exit-code contract and the two spellings of the repo flag are the
			// things a wrapper-script author needs from the help text.
			for _, want := range []string{"It exits 0 unless", "-repo", "-C", "-env", "-timeout", "-write-probe"} {
				if !strings.Contains(out, want) {
					t.Errorf("help does not document %q:\n%s", want, out)
				}
			}
			if h.err.Len() != 0 {
				t.Errorf("help wrote to stderr:\n%s", h.err.String())
			}
		})
	}
}

// The write probe is the one check that changes anything in the deployment it is
// diagnosing, so the help text has to say both that it exists — an operator who
// does not know it exists never learns their key is read-only — and that it
// writes. A flag that quietly PUTs to production storage is worse than no flag.
func TestDoctorHelpExplainsTheWriteProbeIsOptInBecauseItWrites(t *testing.T) {
	h := newHarness(t)
	if err := h.run("doctor", "--help"); err != nil {
		t.Fatalf("`doctor --help` = %v, want success", err)
	}
	out := h.out.String()
	for _, want := range []string{"--write-probe", "deletes it again", "opt-in"} {
		if !strings.Contains(out, want) {
			t.Errorf("help does not explain the write probe (%q):\n%s", want, out)
		}
	}
}

func TestDoctorRejectsBadArguments(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "a positional argument", args: []string{"doctor", "somewhere"}, want: "unexpected argument"},
		{name: "an unknown flag", args: []string{"doctor", "--nope"}, want: "not defined"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			if err := h.run(tc.args...); err == nil {
				t.Fatalf("`%s` succeeded, want a usage failure", strings.Join(tc.args, " "))
			}
			if !strings.Contains(h.err.String(), tc.want) {
				t.Errorf("stderr does not explain the problem (%q):\n%s", tc.want, h.err.String())
			}
			if !strings.Contains(h.err.String(), "usage: vidra doctor") {
				t.Errorf("usage was not printed after the error:\n%s", h.err.String())
			}
		})
	}

	// A zero timeout would remove the one thing stopping a hung probe from
	// costing the whole report, so it is refused rather than treated as "no
	// limit".
	h := newHarness(t)
	err := h.run("doctor", "--timeout", "0")
	if err == nil || !strings.Contains(err.Error(), "--timeout must be positive") {
		t.Errorf("err = %v, want a zero timeout refused", err)
	}
}

// A run against a directory that is not a deployment must still produce a
// REPORT — findings about what is missing — rather than an error, and it must
// exit non-zero because those findings are real.
func TestDoctorOnAnEmptyDirectoryReportsRatherThanErrors(t *testing.T) {
	h := newHarness(t)
	err := h.run("doctor", "-C", t.TempDir(), "--timeout", "2s")
	if err == nil {
		t.Fatalf("doctor on an empty directory succeeded; stdout:\n%s", h.out.String())
	}
	// errReported: the report has already been printed in the shape the operator
	// needs it, so main must not add a second error line under it.
	if !strings.Contains(err.Error(), "reported") {
		t.Errorf("err = %v, want the already-printed sentinel", err)
	}
	out := h.out.String()
	for _, want := range []string{"vidra doctor", "✗", "needs attention"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report is missing %q:\n%s", want, out)
		}
	}
	// Nothing raw ever reaches the operator, even for a directory with no
	// deployment in it at all.
	for _, leak := range []string{"no such file or directory", "*fs.PathError", "panic"} {
		if strings.Contains(out, leak) {
			t.Errorf("the report leaked %q:\n%s", leak, out)
		}
	}
}
