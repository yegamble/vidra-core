package setupweb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// sseEvent is one parsed frame off the install stream.
type sseEvent struct {
	name    string
	payload InstallEvent
}

// readSSE opens the install stream and reads it to the end. It parses the wire
// format rather than trusting a helper, because the wire format IS the contract
// with the page — a change from "event: line" to anything else is a change the
// browser has to be told about.
func (w *wizard) readSSE(t *testing.T, path string) (int, []sseEvent) {
	t.Helper()
	req, err := http.NewRequest("POST", w.ts.URL+path, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set(tokenHeader, w.Token())
	resp, err := w.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, nil
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read stream: %v", err)
	}
	var out []sseEvent
	for _, frame := range strings.Split(string(body), "\n\n") {
		frame = strings.TrimSpace(frame)
		if frame == "" {
			continue
		}
		var ev sseEvent
		for _, line := range strings.Split(frame, "\n") {
			switch {
			case strings.HasPrefix(line, "event: "):
				ev.name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev.payload); err != nil {
					t.Fatalf("frame payload is not JSON (%v): %s", err, line)
				}
			}
		}
		out = append(out, ev)
	}
	return resp.StatusCode, out
}

func lines(events []sseEvent) []string {
	var out []string
	for _, e := range events {
		if e.name == "line" {
			out = append(out, e.payload.Text)
		}
	}
	return out
}

func done(t *testing.T, events []sseEvent) InstallEvent {
	t.Helper()
	var got *InstallEvent
	for i, e := range events {
		if e.name == "done" {
			if got != nil {
				t.Fatal("more than one done event")
			}
			got = &events[i].payload
		}
	}
	if got == nil {
		t.Fatal("the stream ended with no done event — a page waiting for one would spin for ever")
	}
	return *got
}

func TestInstallStreamsOneEventPerLine(t *testing.T) {
	t.Parallel()
	w := newWizard(t, "", func(o *Options) {
		o.Install = func(_ context.Context, out io.Writer) (int, error) {
			// Deliberately awkward chunking: a real pipe fills wherever it fills,
			// and half a sentence arriving as its own event is the thing the line
			// buffering exists to prevent.
			_, _ = io.WriteString(out, "[deploy] pre-deploy dump")
			_, _ = io.WriteString(out, "\n[deploy] pulling images\n[deploy] migrating core\n")
			_, _ = io.WriteString(out, "[deploy] done, no trailing newline")
			return 0, nil
		}
	})
	code, events := w.readSSE(t, "/api/install")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	want := []string{
		"[deploy] pre-deploy dump",
		"[deploy] pulling images",
		"[deploy] migrating core",
		// The final line has no newline after it — which is what a script that
		// died mid-sentence leaves behind, and exactly the line worth reading.
		"[deploy] done, no trailing newline",
	}
	got := lines(events)
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("lines = %q, want %q", got, want)
	}
	if fin := done(t, events); fin.ExitCode != 0 || fin.Hint != "" {
		t.Errorf("done = %+v, want a clean exit with no hint", fin)
	}
}

func TestInstallReportsTheExitCodeAndWhatToDoAboutIt(t *testing.T) {
	t.Parallel()
	w := newWizard(t, "", func(o *Options) {
		o.Install = func(_ context.Context, out io.Writer) (int, error) {
			_, _ = io.WriteString(out, "[deploy] api did not become ready within 120s\n")
			return 3, nil
		}
	})
	_, events := w.readSSE(t, "/api/install")
	fin := done(t, events)
	if fin.ExitCode != 3 {
		t.Errorf("exit_code = %d, want the script's own 3", fin.ExitCode)
	}
	if !strings.Contains(fin.Hint, "vidra doctor") {
		t.Errorf("hint = %q, want it to name `vidra doctor`", fin.Hint)
	}
	// The tail is NOT repeated in the done event: the page already holds every
	// line it was sent.
	if strings.Contains(fin.Hint, "did not become ready") {
		t.Errorf("the hint repeats the log back: %q", fin.Hint)
	}
	if fin.Error != "" {
		t.Errorf("error = %q; the script RAN and failed, which is a different thing from one that could not start", fin.Error)
	}
}

func TestInstallDistinguishesAScriptThatCouldNotStart(t *testing.T) {
	t.Parallel()
	w := newWizard(t, "", func(o *Options) {
		o.Install = func(context.Context, io.Writer) (int, error) {
			return 0, errors.New("deploy/deploy.sh could not be run: exec: \"bash\": executable file not found in $PATH")
		}
	})
	_, events := w.readSSE(t, "/api/install")
	fin := done(t, events)
	if fin.Error == "" {
		t.Error("a deploy that never started was reported as one that ran")
	}
	if fin.ExitCode == 0 {
		t.Error("exit_code = 0 for a deploy that never started")
	}
	if !strings.Contains(fin.Hint, "Nothing was deployed") {
		t.Errorf("hint = %q", fin.Hint)
	}
}

func TestOnlyOneInstallRunsAtATime(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	w := newWizard(t, "", func(o *Options) {
		o.Install = func(_ context.Context, out io.Writer) (int, error) {
			once.Do(func() { close(started) })
			_, _ = io.WriteString(out, "[deploy] working\n")
			<-release
			return 0, nil
		}
	})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if code, _ := w.readSSE(t, "/api/install"); code != http.StatusOK {
			t.Errorf("first install = %d", code)
		}
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the first install never started")
	}
	// A stray double-click must not become two `docker compose up` runs against
	// the same project.
	code, _ := w.readSSE(t, "/api/install")
	if code != http.StatusConflict {
		t.Errorf("second install = %d, want 409", code)
	}
	close(release)
	wg.Wait()

	// And once it is over, the next one is allowed: the lock is a concurrency
	// guard, not a one-shot.
	if code, _ := w.readSSE(t, "/api/install"); code != http.StatusOK {
		t.Errorf("install after the first finished = %d, want 200", code)
	}
}

func TestInstallStreamNeedsTheToken(t *testing.T) {
	t.Parallel()
	ran := false
	w := newWizard(t, "", func(o *Options) {
		o.Install = func(context.Context, io.Writer) (int, error) { ran = true; return 0, nil }
	})
	// EventSource cannot set a header, which is exactly why the install stream is
	// a POST the page reads with fetch: an <script>-reachable stream would be a
	// deploy anyone's page could start.
	req, err := http.NewRequest("POST", w.ts.URL+"/api/install?t="+w.Token(), nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := w.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("install with the token in the query = %d, want 403", resp.StatusCode)
	}
	if ran {
		t.Error("a deploy was started by an unauthenticated request")
	}
}

func TestInstallWithNothingWiredSaysSoRatherThanHanging(t *testing.T) {
	t.Parallel()
	w := newWizard(t, "", nil)
	code, raw := w.call(t, "POST", "/api/install", nil)
	if code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", code)
	}
	if !strings.Contains(string(raw), "vidra deploy") {
		t.Errorf("the refusal does not name the command to run instead: %s", raw)
	}
}

// ---------------------------------------------------------------------------
// Status.

func TestStatusCarriesTheOwnerClaimHandoff(t *testing.T) {
	t.Parallel()
	w := newWizard(t, reRunEnv, func(o *Options) {
		o.Status = func(context.Context) []StatusLine {
			return []StatusLine{
				{Source: "containers", Check: "compose ps", Status: "ok", Detail: "7 container(s)"},
				{Source: "api", Check: "/readyz", Status: "fail", Detail: "the api is up but NOT ready — postgres down", Fix: "vidra logs postgres"},
			}
		}
	})
	var out StatusResponse
	if code := w.callJSON(t, "POST", "/api/status", nil, &out); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if len(out.Lines) != 2 || out.Lines[1].Status != "fail" {
		t.Errorf("lines = %+v", out.Lines)
	}
	// The origin comes from the file on disk, not from anything the page said:
	// this step is about the deployment that EXISTS.
	if out.ClaimURL != "https://video.example.org/setup/claim" {
		t.Errorf("claim_url = %q", out.ClaimURL)
	}
	// compose.sh and never a bare `docker compose`: on a deployment host the bare
	// form picks up docker-compose.override.yml's dev defaults and addresses a
	// different project than the deploy scripts do.
	if out.LogsCommand != "./deploy/compose.sh logs api" {
		t.Errorf("logs_command = %q", out.LogsCommand)
	}
}

func TestStatusBeforeAnyFileExistsStillAnswers(t *testing.T) {
	t.Parallel()
	w := newWizard(t, "", nil)
	var out StatusResponse
	if code := w.callJSON(t, "POST", "/api/status", nil, &out); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	// A placeholder origin rather than a broken link or an error: the Success
	// step must render even when it is reached out of order.
	if !strings.Contains(out.ClaimURL, "/setup/claim") {
		t.Errorf("claim_url = %q", out.ClaimURL)
	}
}

func TestInstallStreamNeverEchoesASecret(t *testing.T) {
	t.Parallel()
	w := newWizard(t, reRunEnv, func(o *Options) {
		o.Install = func(_ context.Context, out io.Writer) (int, error) {
			// The deploy scripts do not print secrets, but the stream is a pipe out
			// of a process this package does not own, so the sweep covers it too.
			_, _ = fmt.Fprintf(out, "[deploy] using %s\n", "env/production.env")
			return 0, nil
		}
		o.Status = func(context.Context) []StatusLine { return nil }
	})
	for _, path := range []string{"/api/status"} {
		_, raw := w.call(t, "POST", path, nil)
		assertNoSentinel(t, "POST "+path, raw)
	}
	_, events := w.readSSE(t, "/api/install")
	for _, e := range events {
		assertNoSentinel(t, "install stream", []byte(e.payload.Text))
	}
}
