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

// TestAPIStartupSmoke boots the built binary, waits for readiness, asserts the
// operational endpoints respond, and shuts it down cleanly with SIGTERM.
func TestAPIStartupSmoke(t *testing.T) {
	if os.Getenv("DATABASE_URL") == "" || os.Getenv("REDIS_URL") == "" {
		t.Skip("DATABASE_URL/REDIS_URL not set; skipping startup smoke test")
	}

	// Build the real binary (this package). The test binary's own wiring is not
	// enough: exec'ing the artifact proves main() end to end.
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
	base := fmt.Sprintf("http://127.0.0.1:%d", port)

	var logs syncBuffer
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(),
		"HTTP_HOST=127.0.0.1",
		fmt.Sprintf("HTTP_PORT=%d", port),
		"STORAGE_LOCAL_ROOT="+t.TempDir(),
		"LOG_FORMAT=json",
	)
	cmd.Stdout = &logs
	cmd.Stderr = &logs
	if err := cmd.Start(); err != nil {
		t.Fatalf("start api: %v", err)
	}
	// Watch for early exit so a boot failure surfaces immediately (with logs)
	// instead of as a poll timeout. processDone tracks whether the exit result
	// was already consumed, so the cleanup defer never kills a reaped process
	// or blocks on an empty channel.
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	processDone := false
	defer func() {
		if !processDone { // still running: don't leak it on test failure
			_ = cmd.Process.Kill()
			<-exited
		}
	}()

	client := &http.Client{Timeout: 2 * time.Second}

	// Wait for readiness: /readyz 200 means postgres AND redis pinged OK.
	deadline := time.Now().Add(30 * time.Second)
	for {
		select {
		case err := <-exited:
			processDone = true
			t.Fatalf("api exited before becoming ready: %v\nlogs:\n%s", err, logs.String())
		default:
		}
		resp, err := client.Get(base + "/readyz")
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
				break
			}
			_ = resp.Body.Close()
		}
		if time.Now().After(deadline) {
			t.Fatalf("api not ready within 30s\nlogs:\n%s", logs.String())
		}
		time.Sleep(200 * time.Millisecond)
	}

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
		processDone = true
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
