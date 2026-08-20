package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/config"
)

// ffmpegFound / ffmpegMissing pin the ffmpeg component so the suite asserts the
// probe's behaviour rather than whether the machine running it happens to have
// ffmpeg installed.
func ffmpegFound(string) (string, error) { return "/usr/bin/ffmpeg", nil }

func ffmpegMissing(string) (string, error) {
	return "", errors.New("executable file not found in $PATH")
}

// authServerWithConfig is authServer with the config under the test's control —
// the SMTP probe reads SMTP_HOST/SMTP_PORT off it.
func authServerWithConfig(t *testing.T, cfg *config.Config) *Server {
	t.Helper()
	issuer := auth.NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", 15*time.Minute)
	svc := auth.NewService(newAuthFakeRepo(), issuer, 720*time.Hour)
	return New(cfg, nil, nil, WithAuthService(svc, 15*time.Minute))
}

// systemStatus reads the admin snapshot as the instance's first (admin) account.
func systemStatus(t *testing.T, srv *Server) systemStatusResponse {
	t.Helper()
	admin := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	rec := getWithAuth(srv, "/api/v1/admin/system", admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("system status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var body systemStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return body
}

func TestSystemStatus(t *testing.T) {
	srv := authServer(t)
	srv.lookPath = ffmpegFound
	// The first registered account becomes admin.
	admin := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	rec := getWithAuth(srv, "/api/v1/admin/system", admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("system status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var body systemStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Software.Name != "vidra" || body.Software.GoVersion == "" {
		t.Errorf("software = %+v, want vidra with a go_version", body.Software)
	}
	if body.Environment != "test" {
		t.Errorf("environment = %q, want test", body.Environment)
	}
	if body.Status != "ok" {
		t.Errorf("status = %q, want ok (nil deps report not_configured, not down)", body.Status)
	}
	// The component map is always present (postgres/redis "not_configured" here,
	// since authServer wires no db/redis).
	if c, ok := body.Components["postgres"]; !ok || c.Status != "not_configured" {
		t.Errorf("postgres component = %+v (present=%v), want not_configured", c, ok)
	}
	if _, ok := body.Components["redis"]; !ok {
		t.Error("redis component missing")
	}

	// The effective (non-secret) rate-limit config is surfaced read-only so an
	// operator can confirm what is in force (product-decisions §3 — config-only,
	// no runtime mutation endpoint). testConfig mirrors the production defaults.
	rl := body.RateLimits
	if !rl.Enabled || rl.Requests != 120 || rl.AuthRequests != 10 || rl.WindowSeconds != 60 {
		t.Errorf("rate_limits = %+v, want {Enabled:true Requests:120 AuthRequests:10 WindowSeconds:60}", rl)
	}

	// A regular user cannot read it; anon is unauthorized.
	bob := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	if rec := getWithAuth(srv, "/api/v1/admin/system", bob); rec.Code != http.StatusForbidden {
		t.Errorf("non-admin system status = %d, want 403", rec.Code)
	}
	if rec := getWithAuth(srv, "/api/v1/admin/system", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anon system status = %d, want 401", rec.Code)
	}
}

// A deployment on local storage, with mail off and no search service, is a
// SUPPORTED configuration: each of those components reports not_configured and
// the instance is still "ok". None of them may read as a fault.
func TestSystemStatusUnconfiguredComponentsDoNotDegrade(t *testing.T) {
	srv := authServer(t)
	srv.lookPath = ffmpegFound

	body := systemStatus(t, srv)

	for _, name := range []string{"s3", "smtp", "search", "ffmpeg"} {
		if _, ok := body.Components[name]; !ok {
			t.Fatalf("component %q missing from %+v", name, body.Components)
		}
	}
	for _, name := range []string{"s3", "smtp", "search"} {
		if got := body.Components[name].Status; got != "not_configured" {
			t.Errorf("%s status = %q, want not_configured", name, got)
		}
	}
	if got := body.Components["ffmpeg"].Status; got != "ok" {
		t.Errorf("ffmpeg status = %q, want ok", got)
	}
	if body.Status != "ok" {
		t.Errorf("status = %q; not_configured must never degrade the instance", body.Status)
	}
}

// ffmpeg is baked into the api image, so a missing one is a broken image — and
// the endpoint still answers 200, because an admin cannot fix what the page
// refuses to show.
func TestSystemStatusMissingFFmpegDegrades(t *testing.T) {
	srv := authServer(t)
	srv.lookPath = ffmpegMissing

	body := systemStatus(t, srv)

	c := body.Components["ffmpeg"]
	if c.Status != "down" || c.Error == "" {
		t.Errorf("ffmpeg component = %+v, want down with an error", c)
	}
	if body.Status != "degraded" {
		t.Errorf("status = %q, want degraded", body.Status)
	}
}

func TestSystemStatusSearchProbe(t *testing.T) {
	for name, tc := range map[string]struct {
		readyErr    error
		wantStatus  string
		wantOverall string
	}{
		"ready":     {nil, "ok", "ok"},
		"not ready": {errors.New("searchclient: readyz answered 503"), "down", "degraded"},
	} {
		t.Run(name, func(t *testing.T) {
			gateway := &fakeSearchGateway{healthy: true, readyErr: tc.readyErr}
			srv := authServer(t)
			srv.lookPath = ffmpegFound
			WithSearchClient(gateway)(srv)

			body := systemStatus(t, srv)

			if got := body.Components["search"].Status; got != tc.wantStatus {
				t.Errorf("search status = %q, want %q", got, tc.wantStatus)
			}
			if body.Status != tc.wantOverall {
				t.Errorf("status = %q, want %q", body.Status, tc.wantOverall)
			}
			// The component reflects the service's answer NOW, not the background
			// prober's cached flag — so the page must have asked.
			if gateway.readyCalls != 1 {
				t.Errorf("readyCalls = %d, want exactly one probe per request", gateway.readyCalls)
			}
		})
	}
}

// fakeBucket is a media backend that can be asked about its bucket — the S3
// backend's shape. A backend that cannot answer that question (the local one) is
// what makes the component report not_configured, so this fake is the only way
// to reach the probe in a unit test.
type fakeBucket struct {
	exists bool
	err    error
	// hook runs inside BucketExists so a test can hold the probe open.
	hook func()
}

func (fakeBucket) Put(context.Context, string, io.Reader) (int64, error) { return 0, nil }
func (fakeBucket) Open(context.Context, string) (io.ReadCloser, error)   { return nil, nil }
func (fakeBucket) Delete(context.Context, string) error                  { return nil }
func (fakeBucket) Exists(context.Context, string) (bool, error)          { return false, nil }

func (f fakeBucket) BucketExists(context.Context) (bool, error) {
	if f.hook != nil {
		f.hook()
	}
	return f.exists, f.err
}

func TestSystemStatusObjectStoreProbe(t *testing.T) {
	for name, tc := range map[string]struct {
		backend     fakeBucket
		wantStatus  string
		wantOverall string
	}{
		"bucket there":   {fakeBucket{exists: true}, "ok", "ok"},
		"bucket missing": {fakeBucket{}, "down", "degraded"},
		"store unusable": {fakeBucket{err: errors.New("dial tcp: connection refused")}, "down", "degraded"},
	} {
		t.Run(name, func(t *testing.T) {
			srv := authServer(t)
			srv.lookPath = ffmpegFound
			WithMediaStorage(tc.backend)(srv)

			body := systemStatus(t, srv)

			if got := body.Components["s3"].Status; got != tc.wantStatus {
				t.Errorf("s3 status = %q, want %q", got, tc.wantStatus)
			}
			if body.Status != tc.wantOverall {
				t.Errorf("status = %q, want %q", body.Status, tc.wantOverall)
			}
		})
	}
}

// smtpGreeter is a loopback server that answers one greeting per connection. An
// empty greeting means "accept and never speak", which is what an unreachable
// relay behind a proxy looks like.
func smtpGreeter(t *testing.T, greeting string) (host string, port int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			if greeting != "" {
				_, _ = conn.Write([]byte(greeting))
			}
			_ = conn.Close()
		}
	}()
	addr := ln.Addr().(*net.TCPAddr)
	return addr.IP.String(), addr.Port
}

func TestSystemStatusSMTPProbe(t *testing.T) {
	host, port := smtpGreeter(t, "220 mail.example.test ESMTP ready\r\n")

	cfg := testConfig()
	cfg.MailEnabled = true
	cfg.SMTPHost = host
	cfg.SMTPPort = port

	srv := authServerWithConfig(t, cfg)
	srv.lookPath = ffmpegFound

	body := systemStatus(t, srv)

	if got := body.Components["smtp"].Status; got != "ok" {
		t.Errorf("smtp status = %q, want ok (a relay answered 220)", got)
	}
	if body.Status != "ok" {
		t.Errorf("status = %q, want ok", body.Status)
	}
}

// Something answered, but it is not a mail relay: password resets fail exactly
// as if nothing were listening, so the component is down.
func TestSystemStatusSMTPNonRelayDegrades(t *testing.T) {
	host, port := smtpGreeter(t, "HTTP/1.1 400 Bad Request\r\n")

	cfg := testConfig()
	cfg.MailEnabled = true
	cfg.SMTPHost = host
	cfg.SMTPPort = port

	srv := authServerWithConfig(t, cfg)
	srv.lookPath = ffmpegFound

	body := systemStatus(t, srv)

	c := body.Components["smtp"]
	if c.Status != "down" || c.Error == "" {
		t.Errorf("smtp component = %+v, want down with an error", c)
	}
	if body.Status != "degraded" {
		t.Errorf("status = %q, want degraded", body.Status)
	}
}

// The probes must run concurrently: one slow relay serialising the page is the
// whole reason they are not a loop. Both probes below block until the other has
// arrived, so a serial implementation cannot get past the first one — the
// timeout in rendezvous is what turns that deadlock into a failed assertion
// rather than a hung test.
func TestSystemStatusProbesRunInParallel(t *testing.T) {
	arrive, serialized := rendezvous(2, 2*time.Second)

	gateway := &fakeSearchGateway{healthy: true, readyHook: arrive}
	srv := authServer(t)
	srv.lookPath = ffmpegFound
	WithSearchClient(gateway)(srv)
	WithMediaStorage(fakeBucket{exists: true, hook: arrive})(srv)

	body := systemStatus(t, srv)

	if serialized.Load() {
		t.Error("a probe waited out the rendezvous; the probes ran one after another")
	}
	if body.Components["search"].Status != "ok" || body.Components["s3"].Status != "ok" {
		t.Errorf("components = %+v, want search and s3 ok", body.Components)
	}
}

// rendezvous returns a barrier that blocks each caller until n of them have
// arrived, plus a flag set when a caller gave up waiting.
func rendezvous(n int, wait time.Duration) (arrive func(), serialized *atomic.Bool) {
	var (
		mu       sync.Mutex
		arrived  int
		released = make(chan struct{})
		flag     atomic.Bool
	)
	return func() {
		mu.Lock()
		arrived++
		if arrived == n {
			close(released)
		}
		mu.Unlock()
		select {
		case <-released:
		case <-time.After(wait):
			flag.Store(true)
		}
	}, &flag
}
