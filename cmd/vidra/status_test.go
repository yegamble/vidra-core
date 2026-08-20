package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// The status tests are hermetic: two httptest servers on the loopback stand in
// for the api and the frontend (which is exactly where the real ones are — this
// command only ever dials 127.0.0.1), and the fake runner answers for compose.
// A status command whose own suite needed a docker daemon would be a status
// command nobody could change.

type statusStage struct {
	dir      string
	runner   *fakeRunner
	api      *httptest.Server
	frontend *httptest.Server
}

// newStatusStage stands up the two servers and writes an env file pointing the
// command at them, so the ports are read the way a real run reads them.
func newStatusStage(t *testing.T, api, frontend http.Handler) *statusStage {
	t.Helper()
	st := &statusStage{}
	st.api = httptest.NewServer(api)
	st.frontend = httptest.NewServer(frontend)
	t.Cleanup(st.api.Close)
	t.Cleanup(st.frontend.Close)
	st.dir = fakeDeployment(t, fmt.Sprintf("HTTP_PORT=%s\nFRONTEND_PORT=%s\nVIDRA_COMPOSE_PROFILES=core frontend\n",
		port(t, st.api.URL), port(t, st.frontend.URL)))
	st.runner = swapRunner(t, &fakeRunner{})
	return st
}

func port(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %s: %v", raw, err)
	}
	return u.Port()
}

// healthyAPI answers both operations endpoints the way a good deployment does.
func healthyAPI() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, `{"status":"ok","components":{"postgres":{"status":"ok"},"redis":{"status":"ok"}}}`)
	})
	mux.HandleFunc("/schemaz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, `{"software":{"name":"vidra","version":"v0.2.0","commit":"abc1234"},"schema":{"version":104,"dirty":false,"applied":true}}`)
	})
	return mux
}

func healthyFrontend() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, `{"status":"ok"}`)
	})
	return mux
}

func writeJSON(w http.ResponseWriter, code int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write([]byte(body))
}

// runningStack is what `compose.sh ps` prints for a healthy deployment, and an
// exec into the search container that answers.
func runningStack(spec execSpec) (execResult, error) {
	if len(spec.Args) > 1 && spec.Args[1] == "ps" {
		return execResult{Stdout: "NAME              IMAGE                  SERVICE    STATUS\n" +
			"vidra-api-1       ghcr.io/x/core:v0.2.0  api        Up 2 hours (healthy)\n" +
			"vidra-frontend-1  ghcr.io/x/user:v0.2.0  frontend   Up 2 hours\n" +
			"vidra-search-1    ghcr.io/x/search:v0.2  search     Up 2 hours\n"}, nil
	}
	return execResult{Stdout: `{"status":"ok"}`}, nil
}

func TestStatusAllGreen(t *testing.T) {
	st := newStatusStage(t, healthyAPI(), healthyFrontend())
	st.runner.onCapture = runningStack

	h := newHarness(t)
	if err := h.run("status", "-C", st.dir); err != nil {
		t.Fatalf("status = %v, want success on a healthy deployment:\n%s", err, h.out.String())
	}
	out := h.out.String()
	for _, want := range []string{
		"vidra status —",
		"containers", "3 container(s)", "vidra-api-1",
		"api", "the api is ready — postgres ok, redis ok",
		"vidra v0.2.0 (abc1234) — schema version 104, clean",
		"search", "answers /readyz inside the compose network",
		"frontend", "answers /healthz",
		"Everything this command can reach is up and answering.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, glyphFail) && !strings.Contains(out, "0 "+glyphFail) {
		t.Errorf("a healthy deployment reported a failure:\n%s", out)
	}
	// The search probe is an exec into the container, because in production the
	// service publishes no host port at all.
	var probed bool
	for _, c := range st.runner.calls {
		if strings.Join(c.tail(), " ") == "exec -T search wget -qO- http://127.0.0.1:8080/readyz" {
			probed = true
		}
	}
	if !probed {
		t.Errorf("the search service was not probed from inside the network: %v", st.runner.calls)
	}
}

// A 503 from /readyz is the api saying it is up and cannot serve. That is a
// FAILURE — something that is running is broken — and it must set the exit code,
// because this command is meant to be usable as a gate.
func TestStatusDegradedReadyz(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusServiceUnavailable,
			`{"status":"degraded","components":{"postgres":{"status":"down","error":"dial tcp 172.18.0.2:5432: connect: connection refused"},"redis":{"status":"ok"}}}`)
	})
	mux.HandleFunc("/schemaz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, `{"software":{"version":"v0.2.0"},"schema":{"version":104,"applied":true}}`)
	})
	st := newStatusStage(t, mux, healthyFrontend())
	st.runner.onCapture = runningStack

	h := newHarness(t)
	err := h.run("status", "-C", st.dir)
	if err == nil {
		t.Fatalf("a degraded api exited 0:\n%s", h.out.String())
	}
	if !strings.Contains(err.Error(), "reported") {
		t.Errorf("err = %v, want the already-printed sentinel", err)
	}
	out := h.out.String()
	for _, want := range []string{glyphFail + " /readyz", "up but NOT ready", "postgres down", "redis ok", "Something that is running is not working"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report is missing %q:\n%s", want, out)
		}
	}
}

// An image older than /schemaz is not a fault. A status command that went red
// for a working deployment on last month's image is one people stop running.
func TestStatusSchemazMissingIsAWarning(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, `{"status":"ok","components":{"postgres":{"status":"ok"}}}`)
	})
	// No /schemaz route: ServeMux answers 404, which is exactly what an older
	// image does.
	st := newStatusStage(t, mux, healthyFrontend())
	st.runner.onCapture = runningStack

	h := newHarness(t)
	if err := h.run("status", "-C", st.dir); err != nil {
		t.Fatalf("status = %v, want success — an old image is not a failure:\n%s", err, h.out.String())
	}
	out := h.out.String()
	for _, want := range []string{glyphWarn + " /schemaz", "predates /schemaz", "vidra doctor"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report is missing %q:\n%s", want, out)
		}
	}
}

// A ledger left dirty by a half-applied migration is the one /schemaz finding
// that IS a failure: the schema is in a state nobody can reason about.
func TestStatusDirtyLedgerFails(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, `{"status":"ok","components":{}}`)
	})
	mux.HandleFunc("/schemaz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, `{"software":{"version":"v0.2.0"},"schema":{"version":103,"dirty":true,"applied":true}}`)
	})
	st := newStatusStage(t, mux, healthyFrontend())
	st.runner.onCapture = runningStack

	h := newHarness(t)
	if err := h.run("status", "-C", st.dir); err == nil {
		t.Fatalf("a dirty ledger exited 0:\n%s", h.out.String())
	}
	for _, want := range []string{"DIRTY at version 103", "migrate force"} {
		if !strings.Contains(h.out.String(), want) {
			t.Errorf("the report is missing %q:\n%s", want, h.out.String())
		}
	}
}

// The search container missing is a ⚠ with the reason, never a ✗: it is not
// running here, which is a fact about this host and not a fault in the service.
func TestStatusSearchContainerAbsent(t *testing.T) {
	st := newStatusStage(t, healthyAPI(), healthyFrontend())
	st.runner.onCapture = func(spec execSpec) (execResult, error) {
		if len(spec.Args) > 1 && spec.Args[1] == "ps" {
			return execResult{Stdout: "NAME         IMAGE   SERVICE   STATUS\nvidra-api-1  x       api       Up\n"}, nil
		}
		// What compose says when the service has no container. The [compose]
		// line is compose.sh's own note, on the same stream, and must not be
		// mistaken for the diagnostic.
		return execResult{
			Stderr:   "[compose] external datastores: postgres=1 redis=0\nservice \"search\" is not running\n",
			ExitCode: 1,
		}, nil
	}

	h := newHarness(t)
	if err := h.run("status", "-C", st.dir); err != nil {
		t.Fatalf("status = %v, want success — a service that is not running is not a failure:\n%s", err, h.out.String())
	}
	out := h.out.String()
	for _, want := range []string{glyphWarn, "search container is not running", "vidra logs search"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "external datastores") {
		t.Errorf("compose.sh's own log line was reported as the diagnostic:\n%s", out)
	}
}

// A search container that IS running and does not answer is the other half of
// that distinction, and it is a ✗.
func TestStatusSearchRunningButUnhealthy(t *testing.T) {
	st := newStatusStage(t, healthyAPI(), healthyFrontend())
	st.runner.onCapture = func(spec execSpec) (execResult, error) {
		if len(spec.Args) > 1 && spec.Args[1] == "ps" {
			return runningStack(spec)
		}
		// wget was given -q, so a silent non-zero exit is the service refusing.
		return execResult{ExitCode: 1}, nil
	}

	h := newHarness(t)
	if err := h.run("status", "-C", st.dir); err == nil {
		t.Fatalf("a search container that does not answer exited 0:\n%s", h.out.String())
	}
	for _, want := range []string{"running but does not answer /readyz", "falls back to database queries"} {
		if !strings.Contains(h.out.String(), want) {
			t.Errorf("the report is missing %q:\n%s", want, h.out.String())
		}
	}
}

// Frontend images from before this wave have no /healthz. Their 404 is the
// server answering, so / is the older way of asking the same question.
func TestStatusFrontendFallsBackToRoot(t *testing.T) {
	frontend := http.NewServeMux()
	frontend.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("<!doctype html>"))
	})
	st := newStatusStage(t, healthyAPI(), frontend)
	st.runner.onCapture = runningStack

	h := newHarness(t)
	if err := h.run("status", "-C", st.dir); err != nil {
		t.Fatalf("status = %v, want success:\n%s", err, h.out.String())
	}
	out := h.out.String()
	for _, want := range []string{glyphOK + " /:", "serves /", "predates /healthz"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report is missing %q:\n%s", want, out)
		}
	}
}

// A frontend that is up and serving errors is a ✗ — it is running, and it is
// wrong.
func TestStatusFrontendServingErrors(t *testing.T) {
	frontend := http.NewServeMux()
	frontend.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	st := newStatusStage(t, healthyAPI(), frontend)
	st.runner.onCapture = runningStack

	h := newHarness(t)
	if err := h.run("status", "-C", st.dir); err == nil {
		t.Fatalf("a 500 from the frontend exited 0:\n%s", h.out.String())
	}
	if !strings.Contains(h.out.String(), "answers /healthz with HTTP 500") {
		t.Errorf("the report does not say what happened:\n%s", h.out.String())
	}
}

// A stack that is simply DOWN is the most common thing this command is pointed
// at, and it is all warnings: nothing here is broken, there is just nothing
// here. It must exit 0, print no Go error, and say what to run next.
func TestStatusStackDown(t *testing.T) {
	st := newStatusStage(t, healthyAPI(), healthyFrontend())
	// Both servers gone: the ports are closed, so every probe is refused.
	st.api.Close()
	st.frontend.Close()
	st.runner.onCapture = func(spec execSpec) (execResult, error) {
		if len(spec.Args) > 1 && spec.Args[1] == "ps" {
			return execResult{Stdout: "NAME   IMAGE   SERVICE   STATUS\n"}, nil
		}
		return execResult{Stderr: "service \"search\" is not running", ExitCode: 1}, nil
	}

	h := newHarness(t)
	if err := h.run("status", "-C", st.dir); err != nil {
		t.Fatalf("status on a stopped stack = %v, want success (nothing is broken, nothing is there):\n%s", err, h.out.String())
	}
	out := h.out.String()
	for _, want := range []string{"nothing is running", "vidra deploy", "nothing is listening on that port", "Nothing is broken."} {
		if !strings.Contains(out, want) {
			t.Errorf("the report is missing %q:\n%s", want, out)
		}
	}
	// Rule 2 of the doctor package, which this command shares: an operator never
	// reads a Go error.
	for _, leak := range []string{"dial tcp", "connect: connection refused", "*net.OpError", "panic", "no such file or directory"} {
		if strings.Contains(out, leak) {
			t.Errorf("the report leaked %q:\n%s", leak, out)
		}
	}
}

// Pointed at a directory that is not a deployment, status REPORTS rather than
// erroring — its job is to describe what it finds, and the loopback probes still
// answer. (The passthrough commands refuse instead: they cannot do their job at
// all without the script.)
func TestStatusOnADirectoryThatIsNotADeployment(t *testing.T) {
	swapRunner(t, &fakeRunner{})
	h := newHarness(t)
	err := h.run("status", "-C", t.TempDir())
	out := h.out.String()
	if !strings.Contains(out, "not a Vidra deployment") {
		t.Errorf("the report does not say what is wrong:\n%s", out)
	}
	if !strings.Contains(out, "-C/--repo") {
		t.Errorf("the report does not say how to fix it:\n%s", out)
	}
	for _, leak := range []string{"no such file or directory", "panic", "*fs.PathError"} {
		if strings.Contains(out, leak) {
			t.Errorf("the report leaked %q:\n%s", leak, out)
		}
	}
	// Nothing is ✗, so it exits 0: this is a wrong invocation, not a broken
	// deployment.
	if err != nil {
		t.Errorf("err = %v, want success", err)
	}
}

// HTTP_PORT is read the way deploy/lib.sh reads it: the process environment
// first, then the env file, then the default.
func TestStatusPortPrecedence(t *testing.T) {
	values := map[string]string{"HTTP_PORT": "8081", "FRONTEND_PORT": `"3001"`}
	if got := envGet(map[string]string{"HTTP_PORT": "8088"}, values, "HTTP_PORT", "8080"); got != "8088" {
		t.Errorf("HTTP_PORT = %q, want the process environment to win", got)
	}
	if got := envGet(nil, values, "HTTP_PORT", "8080"); got != "8081" {
		t.Errorf("HTTP_PORT = %q, want the env file's value", got)
	}
	if got := envGet(nil, values, "FRONTEND_PORT", "3000"); got != "3001" {
		t.Errorf("FRONTEND_PORT = %q, want the quotes stripped", got)
	}
	if got := envGet(nil, nil, "HTTP_PORT", "8080"); got != "8080" {
		t.Errorf("HTTP_PORT = %q, want the default", got)
	}
	if got := envGet(map[string]string{"HTTP_PORT": "  "}, values, "HTTP_PORT", "8080"); got != "8081" {
		t.Errorf("HTTP_PORT = %q, want a blank process value to count as unset", got)
	}
}

func TestStatusUsage(t *testing.T) {
	swapRunner(t, &fakeRunner{})
	h := newHarness(t)
	if err := h.run("status", "-h"); err != nil {
		t.Fatalf("`status -h` = %v, want success", err)
	}
	for _, want := range []string{"usage: vidra status", "exits 0 unless", "vidra doctor", "127.0.0.1"} {
		if !strings.Contains(h.out.String(), want) {
			t.Errorf("help does not mention %q:\n%s", want, h.out.String())
		}
	}

	h2 := newHarness(t)
	if err := h2.run("status", "somewhere"); err == nil {
		t.Fatal("`status somewhere` succeeded, want a usage failure")
	}
	if !strings.Contains(h2.err.String(), "unexpected argument") {
		t.Errorf("stderr does not explain the problem:\n%s", h2.err.String())
	}
}
