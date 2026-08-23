//go:build integration

// API startup smoke test (fix_plan P16): builds the real api binary and boots
// it against the live PostgreSQL and Redis from the environment, proving the
// full production wiring — config load, store/cache connections, service
// construction, route registration, and graceful shutdown — not just the parts
// unit tests exercise with fakes. Requires DATABASE_URL and REDIS_URL (each
// test self-skips otherwise), with migrations already applied. Run with:
//
//	docker compose --profile core up -d postgres redis migrate
//	DATABASE_URL=postgres://vidra:vidra@localhost:5432/vidra?sslmode=disable \
//	REDIS_URL=redis://localhost:6379/0 \
//	go test -tags=integration ./cmd/api/...
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// syncBuffer is a concurrency-safe log sink: exec's copier goroutines write to
// it while the test goroutine reads it for diagnostics.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// bootedAPI is a running api process under test: where to reach it, what it has
// logged, and how to find out that it has exited.
type bootedAPI struct {
	base   string
	logs   *syncBuffer
	cmd    *exec.Cmd
	exited chan error
	// done is set once the exit result has been consumed, so the cleanup never
	// kills a reaped process or blocks on an empty channel.
	done bool
}

// startAPI builds the real binary, boots it against the live PostgreSQL and
// Redis with extraEnv layered on, and waits until GET /readyz says it is ready.
//
// Exec'ing the artifact is the point: the test binary's own wiring proves
// nothing about main(), which is where config load, pool sizing, service
// construction and the shutdown sequence actually live.
func startAPI(t *testing.T, extraEnv ...string) *bootedAPI {
	t.Helper()

	bin := filepath.Join(t.TempDir(), "api")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	// Reserve a free loopback port for the server.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()

	api := &bootedAPI{
		base:   fmt.Sprintf("http://127.0.0.1:%d", port),
		logs:   &syncBuffer{},
		exited: make(chan error, 1),
	}
	api.cmd = exec.Command(bin)
	api.cmd.Env = append(os.Environ(), append([]string{
		"HTTP_HOST=127.0.0.1",
		fmt.Sprintf("HTTP_PORT=%d", port),
		"STORAGE_LOCAL_ROOT=" + t.TempDir(),
		"LOG_FORMAT=json",
	}, extraEnv...)...)
	api.cmd.Stdout = api.logs
	api.cmd.Stderr = api.logs
	if err := api.cmd.Start(); err != nil {
		t.Fatalf("start api: %v", err)
	}
	// Watch for early exit so a boot failure surfaces immediately (with logs)
	// instead of as a poll timeout.
	go func() { api.exited <- api.cmd.Wait() }()
	t.Cleanup(func() {
		if !api.done { // still running: don't leak it on test failure
			_ = api.cmd.Process.Kill()
			<-api.exited
		}
	})

	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(30 * time.Second)
	for {
		select {
		case err := <-api.exited:
			api.done = true
			t.Fatalf("api exited before becoming ready: %v\nlogs:\n%s", err, api.logs.String())
		default:
		}
		resp, err := client.Get(api.base + "/readyz")
		if err == nil {
			if resp.StatusCode == http.StatusOK {
				var ready struct {
					Status     string `json:"status"`
					Components map[string]struct {
						Status string `json:"status"`
					} `json:"components"`
				}
				if derr := json.NewDecoder(resp.Body).Decode(&ready); derr != nil {
					t.Fatalf("decode /readyz: %v", derr)
				}
				_ = resp.Body.Close()
				if ready.Status != "ok" ||
					ready.Components["postgres"].Status != "ok" ||
					ready.Components["redis"].Status != "ok" {
					t.Fatalf("/readyz = %+v, want ok with postgres+redis ok", ready)
				}
				return api
			}
			_ = resp.Body.Close()
		}
		if time.Now().After(deadline) {
			t.Fatalf("api not ready within 30s\nlogs:\n%s", api.logs.String())
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// TestAPIStartupSmoke boots the built binary, waits for readiness, asserts the
// operational endpoints respond, and shuts it down cleanly with SIGTERM.
func TestAPIStartupSmoke(t *testing.T) {
	if os.Getenv("DATABASE_URL") == "" || os.Getenv("REDIS_URL") == "" {
		t.Skip("DATABASE_URL/REDIS_URL not set; skipping startup smoke test")
	}

	api := startAPI(t)
	base, logs, cmd, exited := api.base, api.logs, api.cmd, api.exited

	client := &http.Client{Timeout: 2 * time.Second}

	// Liveness.
	resp, err := client.Get(base + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	var live struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&live); err != nil {
		t.Fatalf("decode /healthz: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || live.Status != "ok" {
		t.Errorf("/healthz = %d %+v, want 200 ok", resp.StatusCode, live)
	}

	// Build metadata.
	resp, err = client.Get(base + "/version")
	if err != nil {
		t.Fatalf("GET /version: %v", err)
	}
	var ver struct {
		Name    string `json:"name"`
		Version string `json:"version"`
		Go      string `json:"go"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ver); err != nil {
		t.Fatalf("decode /version: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || ver.Name != "vidra" || ver.Version == "" || ver.Go == "" {
		t.Errorf("/version = %d %+v, want 200 with name=vidra and non-empty version/go", resp.StatusCode, ver)
	}

	// Graceful shutdown: SIGTERM must end the process cleanly (exit 0).
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("SIGTERM: %v", err)
	}
	select {
	case err := <-exited:
		api.done = true
		if err != nil {
			t.Fatalf("api exited non-zero after SIGTERM: %v\nlogs:\n%s", err, logs.String())
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("api did not exit within 15s of SIGTERM\nlogs:\n%s", logs.String())
	}
	if !strings.Contains(logs.String(), "shutdown complete") {
		t.Errorf("logs missing \"shutdown complete\"; got:\n%s", logs.String())
	}
}

// TestAPIPoolSizingReachesPgx closes the loop on DB_MAX_CONNS: env → config →
// store.Option → pgxpool → the gauge an operator actually reads.
//
// Every link in that chain has its own unit test and none of them proves the
// chain. A pool option that silently did not apply would leave the config test
// green, the metrics test green, and the deployment opening ten connections per
// process while its operator believed it was opening three.
func TestAPIPoolSizingReachesPgx(t *testing.T) {
	if os.Getenv("DATABASE_URL") == "" || os.Getenv("REDIS_URL") == "" {
		t.Skip("DATABASE_URL/REDIS_URL not set; skipping pool sizing test")
	}

	// Deliberately not the default: a test that asserts the default cannot tell
	// "the option applied" from "the option was ignored".
	api := startAPI(t, "DB_MAX_CONNS=3", "DB_MIN_CONNS=2", "METRICS_ENABLED=true")
	defer func() {
		_ = api.cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-api.exited:
			api.done = true
		case <-time.After(15 * time.Second):
		}
	}()

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(api.base + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("read /metrics: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/metrics = %d, want 200", resp.StatusCode)
	}
	scrape := string(body)

	// The ceiling is the configured one, in the pool the process is really using.
	if !strings.Contains(scrape, "vidra_db_pool_max_conns 3") {
		t.Errorf("/metrics does not report vidra_db_pool_max_conns 3 — DB_MAX_CONNS did not reach pgx\nlogs:\n%s", api.logs.String())
	}
	// And the rest of the series is exported, so a dashboard built on it is not
	// built on names that do not exist.
	for _, name := range []string{
		"vidra_db_pool_total_conns",
		"vidra_db_pool_idle_conns",
		"vidra_db_pool_acquired_conns",
		"vidra_db_pool_empty_acquires_total",
		"vidra_db_pool_acquire_wait_seconds_total",
	} {
		if !strings.Contains(scrape, name) {
			t.Errorf("/metrics is missing %s", name)
		}
	}
	// DB_MIN_CONNS=2 means the pool keeps two connections warm, so the process
	// is holding at least that many before anything asks it for one.
	if !strings.Contains(scrape, "vidra_db_pool_total_conns 2") &&
		!strings.Contains(scrape, "vidra_db_pool_total_conns 3") {
		t.Logf("total_conns is not yet at DB_MIN_CONNS; the pool fills asynchronously. Scrape:\n%s",
			poolLines(scrape))
	}
}

// poolLines extracts just the vidra_db_pool_* samples from a scrape, so a
// failure message is readable.
func poolLines(scrape string) string {
	var out []string
	for _, line := range strings.Split(scrape, "\n") {
		if strings.HasPrefix(line, "vidra_db_pool_") {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

// TestAPIDrainPhase proves the two-phase shutdown against the real binary: after
// SIGTERM the process must go NOT READY while it is still SERVING.
//
// That gap is the whole point of HTTP_DRAIN_DELAY, and it cannot be tested from
// inside the process — the thing being proven is that a socket which is still
// accepting connections answers /readyz with a 503, which is a statement about
// the running artifact and its signal handling, not about a handler.
func TestAPIDrainPhase(t *testing.T) {
	if os.Getenv("DATABASE_URL") == "" || os.Getenv("REDIS_URL") == "" {
		t.Skip("DATABASE_URL/REDIS_URL not set; skipping drain test")
	}

	// Long enough to observe the drain without making the suite slow. The
	// assertions below all happen inside this window.
	const drain = 6 * time.Second
	api := startAPI(t, "HTTP_DRAIN_DELAY="+drain.String())
	client := &http.Client{Timeout: 2 * time.Second}

	if err := api.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("SIGTERM: %v", err)
	}

	// Poll for the flip rather than sleeping a fixed amount: the signal handler
	// runs asynchronously, and a fixed sleep is either flaky or slow.
	var (
		code   int
		status string
	)
	deadline := time.Now().Add(drain - 2*time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(api.base + "/readyz")
		if err != nil {
			t.Fatalf("GET /readyz during the drain window: %v — the listener must stay OPEN while draining\nlogs:\n%s", err, api.logs.String())
		}
		var body struct {
			Status string `json:"status"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&body)
		_ = resp.Body.Close()
		code, status = resp.StatusCode, body.Status
		if code == http.StatusServiceUnavailable {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if code != http.StatusServiceUnavailable || status != "draining" {
		t.Fatalf("/readyz after SIGTERM = %d %q, want 503 \"draining\"\nlogs:\n%s", code, status, api.logs.String())
	}

	// And the other half: it is still SERVING. A drain that stopped answering
	// would be an outage with extra steps.
	resp, err := client.Get(api.base + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz while draining: %v — the listener must stay open for the whole delay", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/healthz while draining = %d, want 200: liveness is not readiness", resp.StatusCode)
	}

	select {
	case err := <-api.exited:
		api.done = true
		if err != nil {
			t.Fatalf("api exited non-zero: %v\nlogs:\n%s", err, api.logs.String())
		}
	case <-time.After(drain + 15*time.Second):
		t.Fatalf("api did not exit within the drain delay plus 15s\nlogs:\n%s", api.logs.String())
	}
	for _, want := range []string{"draining", "shutdown complete"} {
		if !strings.Contains(api.logs.String(), want) {
			t.Errorf("logs missing %q; got:\n%s", want, api.logs.String())
		}
	}
}
