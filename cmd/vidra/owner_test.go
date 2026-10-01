package main

import (
	"reflect"
	"strings"
	"testing"
)

const ownerLink = "https://video.example.org/reset-password/confirm?token=SECRETtoken_-dddddddddddddddddddddddddddddddddddd"

// ownerOK is `api owner-recovery` succeeding: link on stdout, context on stderr.
func ownerOK() *fakeRunner {
	return &fakeRunner{onCapture: func(execSpec) (execResult, error) {
		return execResult{
			Stdout: ownerLink + "\n",
			Stderr: "Link expires 2026-10-01T12:00:00Z. Single-use; anyone with this link can set the owner's password.\n" +
				"Without --remove-mfa the owner still needs their authenticator to log in.\n",
		}, nil
	}}
}

func TestOwnerResetPrintsTheCapturedLinkAndContext(t *testing.T) {
	asTerminal(t, true)
	f := swapRunner(t, ownerOK())
	h := newHarness(t) // empty stdin: password-only must not prompt
	if err := h.run("owner", "reset", "-C", fakeDeployment(t, defaultEnv)); err != nil {
		t.Fatalf("owner reset = %v, want success", err)
	}
	spec := f.only(t)
	want := []string{"run", "--rm", "--no-deps", "-T", "api", "owner-recovery"}
	if got := spec.tail(); !reflect.DeepEqual(got, want) {
		t.Errorf("compose.sh was given %q, want %q", got, want)
	}
	out := h.out.String()
	if !strings.Contains(out, ownerLink+"\n") || !strings.Contains(out, "Link expires") {
		t.Errorf("output lacks the link or its context:\n%s", out)
	}
}

// Fail closed, and BEFORE anything runs: minting must not happen for a pipe.
func TestOwnerResetRefusesWhenStdoutIsNotATerminal(t *testing.T) {
	asTerminal(t, false)
	f := swapRunner(t, ownerOK())
	h := newHarness(t)
	err := h.run("owner", "reset", "--yes", "--remove-mfa", "-C", fakeDeployment(t, defaultEnv))
	if err == nil || !strings.Contains(err.Error(), "not a terminal") {
		t.Fatalf("err = %v, want a not-a-terminal refusal", err)
	}
	if len(f.calls) != 0 {
		t.Errorf("a link was minted (%d calls) for a non-terminal stdout", len(f.calls))
	}
}

func TestOwnerResetRemoveMFAConfirmation(t *testing.T) {
	wantArgv := []string{"run", "--rm", "--no-deps", "-T", "api", "owner-recovery", "--remove-mfa"}
	t.Run("typing remove runs it", func(t *testing.T) {
		asTerminal(t, true)
		f := swapRunner(t, ownerOK())
		h := newHarness(t)
		h.stdin = "remove\n"
		if err := h.run("owner", "reset", "--remove-mfa", "-C", fakeDeployment(t, defaultEnv)); err != nil {
			t.Fatalf("owner reset = %v", err)
		}
		if got := f.only(t).tail(); !reflect.DeepEqual(got, wantArgv) {
			t.Errorf("argv = %q, want %q", got, wantArgv)
		}
	})
	for _, answer := range []string{"yes\n", "", "REMOVE\n"} {
		t.Run("aborts on "+strings.TrimSpace(answer)+"|", func(t *testing.T) {
			asTerminal(t, true)
			f := swapRunner(t, ownerOK())
			h := newHarness(t)
			h.stdin = answer
			err := h.run("owner", "reset", "--remove-mfa", "-C", fakeDeployment(t, defaultEnv))
			if err == nil || !strings.Contains(err.Error(), "aborted") {
				t.Fatalf("err = %v, want an abort", err)
			}
			if len(f.calls) != 0 {
				t.Errorf("ran %d commands after a declined confirmation", len(f.calls))
			}
		})
	}
	t.Run("--yes skips the prompt", func(t *testing.T) {
		asTerminal(t, true)
		f := swapRunner(t, ownerOK())
		h := newHarness(t)
		if err := h.run("owner", "reset", "--remove-mfa", "--yes", "-C", fakeDeployment(t, defaultEnv)); err != nil {
			t.Fatalf("owner reset = %v", err)
		}
		if got := f.only(t).tail(); !reflect.DeepEqual(got, wantArgv) {
			t.Errorf("argv = %q, want %q", got, wantArgv)
		}
	})
}

func TestOwnerResetRelaysTheFailureLine(t *testing.T) {
	asTerminal(t, true)
	swapRunner(t, &fakeRunner{onCapture: func(execSpec) (execResult, error) {
		return execResult{ExitCode: 1, Stderr: " Container vidra-api-run Creating\n" +
			"owner-recovery: no instance owner exists yet — claim it with `vidra claim`\n"}, nil
	}})
	h := newHarness(t)
	err := h.run("owner", "reset", "-C", fakeDeployment(t, defaultEnv))
	if err == nil || !strings.Contains(err.Error(), "vidra claim") {
		t.Fatalf("err = %v, want the subcommand's own sentence", err)
	}
	if strings.Contains(h.out.String(), "reset-password") {
		t.Errorf("a link was printed on failure:\n%s", h.out.String())
	}
}

// Exit 0 with anything but exactly one link line breaks the contract.
func TestOwnerResetRejectsUnexpectedStdout(t *testing.T) {
	for name, stdout := range map[string]string{"empty": "", "two lines": ownerLink + "\nextra\n"} {
		t.Run(name, func(t *testing.T) {
			asTerminal(t, true)
			swapRunner(t, &fakeRunner{onCapture: func(execSpec) (execResult, error) {
				return execResult{Stdout: stdout}, nil
			}})
			h := newHarness(t)
			err := h.run("owner", "reset", "-C", fakeDeployment(t, defaultEnv))
			if err == nil {
				t.Fatal("success, want an error")
			}
			if strings.Contains(h.out.String(), "reset-password") {
				t.Error("unexpected stdout was echoed")
			}
		})
	}
}

func TestOwnerUsageAndHelp(t *testing.T) {
	swapRunner(t, &fakeRunner{})
	h := newHarness(t)
	if err := h.run("owner", "-h"); err != nil {
		t.Fatalf("owner -h = %v", err)
	}
	for _, w := range []string{"usage: vidra owner reset", "--remove-mfa", "--yes", "terminal"} {
		if !strings.Contains(h.out.String(), w) {
			t.Errorf("help lacks %q:\n%s", w, h.out.String())
		}
	}
	for _, args := range [][]string{{"owner"}, {"owner", "reset", "extra"}} {
		if err := h.run(args...); err == nil {
			t.Errorf("%v = success, want a usage error", args)
		}
	}
}
