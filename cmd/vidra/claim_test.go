package main

import (
	"io"
	"reflect"
	"strings"
	"testing"
)

// asTerminal makes stdout look like a terminal for one test; the real check
// needs a pty, which a hermetic `go test` does not have.
func asTerminal(t *testing.T, yes bool) {
	t.Helper()
	prev := stdoutIsTerminal
	stdoutIsTerminal = func(io.Writer) bool { return yes }
	t.Cleanup(func() { stdoutIsTerminal = prev })
}

const claimEnv = defaultEnv + "PUBLIC_BASE_URL=https://video.example.org\n"

// Two boots of an unclaimed api: the FIRST token is dead the moment the api
// restarted, so the command must print the SECOND. Both log shapes the api can
// emit (JSON in production, slog text in dev) and compose's prefix are covered.
const claimLogs = `api-1  | {"time":"t","level":"INFO","msg":"listening"}
api-1  | {"time":"t","level":"WARN","msg":"FIRST-RUN SETUP REQUIRED: no accounts exist yet — claim the owner (admin) account with the one-time setup token (sign-ups stay closed with 403 owner_claim_required until claimed): OLDtoken_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
api-1  | {"time":"t","level":"WARN","msg":"claim the owner account with: curl -X POST <public-base-url>/api/v1/setup/claim-owner ... — restarting mints a fresh token and invalidates this one"}
api-1  | time=t level=WARN msg="FIRST-RUN SETUP REQUIRED: no accounts exist yet — until claimed): NEWtoken_-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" extra=1
`

func TestClaimPrintsTheNewestTokenAsAFragmentURL(t *testing.T) {
	asTerminal(t, true)
	dir := fakeDeployment(t, claimEnv)
	f := swapRunner(t, &fakeRunner{onCapture: func(execSpec) (execResult, error) {
		return execResult{Stdout: claimLogs}, nil
	}})
	h := newHarness(t)
	if err := h.run("claim", "-C", dir); err != nil {
		t.Fatalf("claim = %v, want success", err)
	}
	spec := f.only(t)
	if got, want := spec.tail(), []string{"logs", "--no-color", "--no-log-prefix", "api"}; !reflect.DeepEqual(got, want) {
		t.Errorf("compose.sh was given %q, want %q", got, want)
	}
	out := h.out.String()
	want := "https://video.example.org/setup/claim#token=NEWtoken_-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\n"
	if !strings.Contains(out, want) {
		t.Errorf("output lacks the newest token's fragment URL %q:\n%s", want, out)
	}
	if strings.Contains(out, "OLDtoken") || strings.Contains(out, "?token=") {
		t.Errorf("output carries a revoked token or a query-string token:\n%s", out)
	}
	if !strings.Contains(out, "restart") || !strings.Contains(out, "invalidates") {
		t.Errorf("output does not say a restart invalidates the token:\n%s", out)
	}
}

// A restart on an instance that has users but never claimed its owner mints
// through the other log line, and it is newer than the first-run one.
// cmd/api/main.go logs a `vidra claim` hint on a line AFTER the token line. It
// must not become a token itself (it has no marker) and
// must not disturb the token before it; the hint line is the literal one main.go
// emits.
func TestClaimIgnoresTheHintLineThatFollowsTheToken(t *testing.T) {
	asTerminal(t, true)
	dir := fakeDeployment(t, claimEnv)
	logs := `api-1  | {"time":"t","level":"WARN","msg":"FIRST-RUN SETUP REQUIRED: no accounts exist yet — until claimed): HINTtoken_-cccccccccccccccccccccccccccccccccccccccc"}
api-1  | {"time":"t","level":"WARN","msg":"claim the owner account with: curl -X POST <public-base-url>/api/v1/setup/claim-owner ... — restarting mints a fresh token and invalidates this one"}
api-1  | {"time":"t","level":"WARN","msg":"on the host you can run ` + "`vidra claim`" + ` instead for a ready-to-open claim link"}
`
	if tok, ok := lastClaimToken(logs); !ok || tok != "HINTtoken_-cccccccccccccccccccccccccccccccccccccccc" {
		t.Fatalf("lastClaimToken = %q, %v; want the token on the marker line", tok, ok)
	}
	swapRunner(t, &fakeRunner{onCapture: func(execSpec) (execResult, error) {
		return execResult{Stdout: logs}, nil
	}})
	h := newHarness(t)
	if err := h.run("claim", "-C", dir); err != nil {
		t.Fatalf("claim = %v, want success", err)
	}
	if want := "/setup/claim#token=HINTtoken_-cccccccccccccccccccccccccccccccccccccccc\n"; !strings.Contains(h.out.String(), want) {
		t.Errorf("output lacks %q:\n%s", want, h.out.String())
	}
}

func TestClaimTakesTheStillUnclaimedLineToo(t *testing.T) {
	asTerminal(t, true)
	dir := fakeDeployment(t, claimEnv)
	swapRunner(t, &fakeRunner{onCapture: func(execSpec) (execResult, error) {
		return execResult{Stdout: `{"msg":"FIRST-RUN SETUP REQUIRED: x: AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}` + "\n" +
			`{"msg":"OWNER STILL UNCLAIMED: ... claim it now: BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"}` + "\n"}, nil
	}})
	h := newHarness(t)
	if err := h.run("claim", "-C", dir); err != nil {
		t.Fatalf("claim = %v", err)
	}
	if out := h.out.String(); !strings.Contains(out, "#token=BBBB") || strings.Contains(out, "AAAA") {
		t.Errorf("want the newer OWNER STILL UNCLAIMED token only:\n%s", out)
	}
}

// Fail closed: a token must never reach anything that is not provably a terminal.
func TestClaimRefusesWhenStdoutIsNotATerminal(t *testing.T) {
	asTerminal(t, false)
	dir := fakeDeployment(t, claimEnv)
	f := swapRunner(t, &fakeRunner{onCapture: func(execSpec) (execResult, error) {
		return execResult{Stdout: claimLogs}, nil
	}})
	h := newHarness(t)
	err := h.run("claim", "-C", dir)
	if err == nil || !strings.Contains(err.Error(), "interactively") {
		t.Fatalf("err = %v, want a refusal telling the operator to run it interactively", err)
	}
	if len(f.calls) != 0 {
		t.Errorf("logs were read (%d calls) before the terminal check", len(f.calls))
	}
	if strings.Contains(h.out.String()+h.err.String()+err.Error(), "token_") {
		t.Error("the refusal leaked a token")
	}
}

func TestClaimWithNoTokenPointsAtStatusAndLogs(t *testing.T) {
	asTerminal(t, true)
	dir := fakeDeployment(t, claimEnv)
	swapRunner(t, &fakeRunner{onCapture: func(execSpec) (execResult, error) {
		return execResult{Stdout: `{"msg":"listening"}` + "\n"}, nil
	}})
	h := newHarness(t)
	if err := h.run("claim", "-C", dir); err == nil {
		t.Fatal("claim with no token line = success, want a non-zero exit")
	}
	out := h.out.String()
	for _, want := range []string{"already claimed", "vidra status", "vidra logs api"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not mention %q:\n%s", want, out)
		}
	}
}

func TestClaimNeedsAnOriginAndReadableLogs(t *testing.T) {
	asTerminal(t, true)
	t.Run("no PUBLIC_BASE_URL", func(t *testing.T) {
		swapRunner(t, &fakeRunner{})
		h := newHarness(t)
		err := h.run("claim", "-C", fakeDeployment(t, defaultEnv))
		if err == nil || !strings.Contains(err.Error(), "PUBLIC_BASE_URL") {
			t.Errorf("err = %v, want a refusal naming PUBLIC_BASE_URL", err)
		}
	})
	t.Run("compose fails", func(t *testing.T) {
		swapRunner(t, &fakeRunner{onCapture: func(execSpec) (execResult, error) {
			return execResult{Stderr: "no such service: api\n", ExitCode: 1}, nil
		}})
		h := newHarness(t)
		err := h.run("claim", "-C", fakeDeployment(t, claimEnv))
		if err == nil || !strings.Contains(err.Error(), "no such service") {
			t.Errorf("err = %v, want compose's own sentence", err)
		}
	})
}

func TestClaimHelpAndUnexpectedArgument(t *testing.T) {
	swapRunner(t, &fakeRunner{})
	h := newHarness(t)
	if err := h.run("claim", "-h"); err != nil {
		t.Fatalf("claim -h = %v", err)
	}
	for _, want := range []string{"usage: vidra claim", "#token=", "terminal"} {
		if !strings.Contains(h.out.String(), want) {
			t.Errorf("help does not mention %q:\n%s", want, h.out.String())
		}
	}
	if err := h.run("claim", "extra"); err == nil {
		t.Error("claim with a stray argument = success, want a usage error")
	}
}
