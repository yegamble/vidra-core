package doctor

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/dbmigrate"
	"github.com/vidra/vidra-core/internal/preflight"
	"github.com/vidra/vidra-core/internal/storage"
)

// The fakes. Every check in this package reaches the outside world through Host
// or Prober and nothing else, which is what makes the suite below hermetic: no
// docker daemon, no nameserver, no bucket, no database, no systemd. That is not
// tidiness — this host's own Docker daemon has a corrupted content store, and a
// suite that needed it would be a suite nobody could run here.

const testRoot = "/srv/vidra"

// fakeHost is a whole machine described by a struct.
type fakeHost struct {
	files map[string]string
	usage map[string]DiskUsage
	paths map[string]string // what LookPath finds
	env   map[string]string
	now   time.Time

	// respond answers a command. The default answers the healthy production
	// case; a test replaces or wraps it to make one thing go wrong.
	respond func(name string, args []string) (Output, error)
	// ran records every command, so a test can assert doctor used the deploy's
	// own compose chain rather than one of its own devising.
	ran []string
}

func (h *fakeHost) Stat(path string) (os.FileInfo, error) {
	if _, ok := h.files[path]; !ok {
		return nil, &fs.PathError{Op: "stat", Path: path, Err: os.ErrNotExist}
	}
	return fakeInfo{name: filepath.Base(path), modTime: h.now}, nil
}

func (h *fakeHost) ReadFile(path string) ([]byte, error) {
	v, ok := h.files[path]
	if !ok {
		return nil, &fs.PathError{Op: "open", Path: path, Err: os.ErrNotExist}
	}
	return []byte(v), nil
}

func (h *fakeHost) DiskUsage(path string) (DiskUsage, error) {
	if u, ok := h.usage[path]; ok {
		return u, nil
	}
	return DiskUsage{}, errors.New("no such filesystem")
}

func (h *fakeHost) LookPath(file string) (string, error) {
	if p, ok := h.paths[file]; ok {
		return p, nil
	}
	return "", errors.New("executable file not found in $PATH")
}

func (h *fakeHost) Run(_ context.Context, _ string, name string, args ...string) (Output, error) {
	h.ran = append(h.ran, name+" "+strings.Join(args, " "))
	return h.respond(name, args)
}

func (h *fakeHost) Now() time.Time         { return h.now }
func (h *fakeHost) Getenv(k string) string { return h.env[k] }
func (h *fakeHost) commands() []string     { return h.ran }

type fakeInfo struct {
	name    string
	modTime time.Time
}

func (f fakeInfo) Name() string       { return f.name }
func (f fakeInfo) Size() int64        { return 0 }
func (f fakeInfo) Mode() os.FileMode  { return 0o644 }
func (f fakeInfo) ModTime() time.Time { return f.modTime }
func (f fakeInfo) IsDir() bool        { return false }
func (f fakeInfo) Sys() any           { return nil }

// fakeProber is the network and the database.
type fakeProber struct {
	domain preflight.DomainResult
	bucket bool
	//nolint:containedctx // fields, not a stored context.
	bucketErr error
	banner    string
	smtpErr   error
	ledgers   map[string]dbmigrate.Status // keyed by table ("" is core's)
	ledgerErr error

	sawBucket storage.S3Config
	sawSMTP   string
	sawDomain preflight.DomainRequest
}

func (p *fakeProber) CheckDomain(_ context.Context, req preflight.DomainRequest) preflight.DomainResult {
	p.sawDomain = req
	return p.domain
}

func (p *fakeProber) CheckBucket(_ context.Context, cfg storage.S3Config) (bool, error) {
	p.sawBucket = cfg
	return p.bucket, p.bucketErr
}

func (p *fakeProber) CheckSMTP(_ context.Context, addr string) (string, error) {
	p.sawSMTP = addr
	return p.banner, p.smtpErr
}

func (p *fakeProber) MigrationStatus(_ context.Context, _, table string) (dbmigrate.Status, error) {
	if p.ledgerErr != nil {
		return dbmigrate.Status{}, p.ledgerErr
	}
	return p.ledgers[table], nil
}

// ---------------------------------------------------------------------------
// The healthy baseline. Every test starts from a deployment with nothing wrong
// with it and breaks exactly one thing, so a finding in a test is attributable
// to the line that caused it.

// healthyEnv is a minimal production env file that passes setup.Check.
const healthyEnv = `VIDRA_ENV=production
PUBLIC_BASE_URL=https://tube.vidra.test
NEXT_PUBLIC_API_BASE_URL=https://tube.vidra.test
CORS_ALLOWED_ORIGINS=https://tube.vidra.test
JWT_SECRET=0123456789abcdef0123456789abcdef0123456789abcdef
POSTGRES_USER=vidra
POSTGRES_PASSWORD=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
POSTGRES_DB=vidra
REDIS_PASSWORD=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
VIDRA_COMPOSE_PROFILES=core frontend
VIDRA_EXTERNAL_POSTGRES=false
VIDRA_EXTERNAL_REDIS=false
VIDRA_TLS_MODE=acme
VIDRA_ACME_EMAIL=ops@vidra.test
STORAGE_BACKEND=local
STORAGE_LOCAL_ROOT=/app/data/media
MAIL_ENABLED=false
`

// healthyCaddyfile is what `vidra setup` renders: a site address that matches
// PUBLIC_BASE_URL, and example.com surviving only in the comments.
//
// The fixture's domain is a .test one rather than the usual example.org
// throughout this suite, and that is not cosmetic: deploy.sh refuses to deploy a
// Caddyfile.local naming example.com/org/net on a non-comment line, subdomains
// included, so `video.example.org` is not a healthy deployment — it is one the
// deploy script would stop. A fixture that used it would be asserting doctor
// passes a file the deploy refuses, which is the exact parity gap this suite
// exists to hold.
const healthyCaddyfile = `# Generated from deploy/Caddyfile — the comments still talk about example.com.
{
	email ops@vidra.test
}

tube.vidra.test {
	reverse_proxy /api/* api:8080
	reverse_proxy frontend:3000
}
`

// healthyModel is a rendered prod chain: caddy on 80/443, everything else on
// loopback or nothing, and every service capping its log.
const healthyModel = `{
  "name": "vidra",
  "services": {
    "api": {
      "image": "ghcr.io/yegamble/vidra-core:v0.1.1",
      "profiles": ["core"],
      "ports": [{"mode":"ingress","target":8080,"published":"8080","protocol":"tcp","host_ip":"127.0.0.1"}],
      "logging": {"driver":"json-file","options":{"max-size":"10m","max-file":"5"}}
    },
    "frontend": {
      "profiles": ["frontend"],
      "ports": [{"mode":"ingress","target":3000,"published":"3000","protocol":"tcp","host_ip":"127.0.0.1"}],
      "logging": {"driver":"json-file","options":{"max-size":"10m","max-file":"5"}}
    },
    "postgres": {"profiles":["core"],"logging":{"driver":"json-file","options":{"max-size":"10m"}}},
    "redis": {"profiles":["core"],"logging":{"driver":"json-file","options":{"max-size":"10m"}}},
    "search": {"profiles":["core"],"logging":{"driver":"json-file","options":{"max-size":"10m"}}},
    "caddy": {
      "ports": [
        {"mode":"ingress","target":80,"published":"80","protocol":"tcp"},
        {"mode":"ingress","target":443,"published":"443","protocol":"tcp"}
      ],
      "logging": {"driver":"json-file","options":{"max-size":"10m","max-file":"5"}}
    }
  }
}`

// healthyPS is the label dump doctor reads: the production chain, no override.
const healthyPS = "vidra-api-1\tvidra\tapi\t/srv/vidra/docker-compose.yml,/srv/vidra/docker-compose.prod.yml\trunning\tUp 2 hours (healthy)\n" +
	"vidra-search-1\tvidra\tsearch\t/srv/vidra/docker-compose.yml,/srv/vidra/docker-compose.prod.yml\trunning\tUp 2 hours (healthy)\n" +
	"vidra-caddy-1\tvidra\tcaddy\t/srv/vidra/docker-compose.yml,/srv/vidra/docker-compose.prod.yml\trunning\tUp 2 hours\n"

const healthyInfo = `{"DockerRootDir":"/var/lib/docker","LoggingDriver":"json-file"}`

var testNow = time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)

func newFakeHost() *fakeHost {
	h := &fakeHost{
		files: map[string]string{
			filepath.Join(testRoot, "env/production.env"):         healthyEnv,
			filepath.Join(testRoot, "env/production.env.example"): healthyEnv,
			filepath.Join(testRoot, "deploy/Caddyfile.local"):     healthyCaddyfile,
			filepath.Join(testRoot, "backups/last_success"):       testNow.Add(-3*time.Hour).Format(time.RFC3339) + " vidra-20260819T090000Z.dump.gz\n",
		},
		usage: map[string]DiskUsage{
			testRoot:          {TotalBytes: 100 * gib, FreeBytes: 60 * gib},
			"/var/lib/docker": {TotalBytes: 500 * gib, FreeBytes: 400 * gib},
		},
		paths: map[string]string{"systemctl": "/usr/bin/systemctl", "ffmpeg": "/usr/bin/ffmpeg"},
		env:   map[string]string{},
		now:   testNow,
	}
	h.respond = h.healthyRespond
	return h
}

// healthyRespond is the deployment where nothing is wrong.
func (h *fakeHost) healthyRespond(name string, args []string) (Output, error) {
	joined := strings.Join(args, " ")
	switch {
	case name == "docker" && joined == "compose version --short":
		return Output{Stdout: "2.29.7\n"}, nil
	case name == "docker" && strings.Contains(joined, "config --format json"):
		return Output{Stdout: healthyModel}, nil
	case name == "docker" && strings.HasPrefix(joined, "ps "):
		return Output{Stdout: healthyPS}, nil
	case name == "docker" && strings.HasPrefix(joined, "info "):
		return Output{Stdout: healthyInfo}, nil
	case name == "docker" && strings.Contains(joined, "exec -T api migrate version"):
		return Output{Stdout: "version=42 dirty=false\n"}, nil
	case name == "docker" && strings.Contains(joined, "exec -T api ffmpeg"):
		return Output{Stdout: "ffmpeg version 7.1 Copyright (c) 2000-2024\n"}, nil
	case name == "docker" && strings.Contains(joined, "exec -T search wget"):
		return Output{Stdout: "ok\n"}, nil
	case name == "systemctl" && strings.HasPrefix(joined, "is-enabled"):
		return Output{Stdout: "enabled\n"}, nil
	case name == "systemctl" && strings.HasPrefix(joined, "is-active"):
		return Output{Stdout: "active\n"}, nil
	}
	return Output{}, fmt.Errorf("fake host: unexpected command %s %s", name, joined)
}

func newFakeProber() *fakeProber {
	return &fakeProber{
		domain: preflight.DomainResult{Status: StatusOK, Domain: "tube.vidra.test", Message: "tube.vidra.test resolves to 203.0.113.10, which is this host"},
		bucket: true,
		banner: "220 smtp.example.net ESMTP ready",
		ledgers: map[string]dbmigrate.Status{
			"":                {Version: 42, Applied: true},
			searchLedgerTable: {Version: 7, Applied: true},
		},
	}
}

// run is the whole command, against fakes.
func run(t *testing.T, h *fakeHost, p *fakeProber) Report {
	t.Helper()
	if p == nil {
		p = newFakeProber()
	}
	rep, err := Run(context.Background(), Options{Root: testRoot, Host: h, Prober: p})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return rep
}

// only runs ONE check, which is how the table tests below stay attributable.
func only(t *testing.T, name string, h *fakeHost, p *fakeProber) []Finding {
	t.Helper()
	if p == nil {
		p = newFakeProber()
	}
	st, err := newState(Options{Root: testRoot, Host: h, Prober: p})
	if err != nil {
		t.Fatalf("newState: %v", err)
	}
	for _, c := range checks {
		if c.name == name {
			return c.run(context.Background(), st)
		}
	}
	t.Fatalf("no check named %q", name)
	return nil
}

// one is the single finding a check produced, failing the test when it produced
// a different number.
func one(t *testing.T, findings []Finding) Finding {
	t.Helper()
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want exactly 1: %+v", len(findings), findings)
	}
	return findings[0]
}

// wantFinding asserts a status, and that the detail and fix say the things an
// operator needs. Fix text is asserted deliberately: it is the half of this
// package that is easiest to break without noticing, because nothing else reads
// it.
func wantFinding(t *testing.T, f Finding, status Status, detailHas, fixHas string) {
	t.Helper()
	if f.Status != status {
		t.Errorf("status = %s, want %s (detail: %s)", f.Status, status, f.Detail)
	}
	if detailHas != "" && !strings.Contains(f.Detail, detailHas) {
		t.Errorf("detail = %q, want it to mention %q", f.Detail, detailHas)
	}
	if fixHas != "" && !strings.Contains(f.Fix, fixHas) {
		t.Errorf("fix = %q, want it to mention %q", f.Fix, fixHas)
	}
	if status == StatusFail && strings.TrimSpace(f.Fix) == "" {
		t.Errorf("a ✗ with no suggested fix: %q — every failure has to say what to do about it", f.Detail)
	}
}

// hasAny reports whether any finding mentions substr in its detail or fix.
func hasAny(findings []Finding, substr string) bool {
	for _, f := range findings {
		if strings.Contains(f.Detail, substr) || strings.Contains(f.Fix, substr) {
			return true
		}
	}
	return false
}

func statuses(findings []Finding) []Status {
	out := make([]Status, 0, len(findings))
	for _, f := range findings {
		out = append(out, f.Status)
	}
	return out
}

func hasStatus(findings []Finding, want Status) bool {
	for _, f := range findings {
		if f.Status == want {
			return true
		}
	}
	return false
}
