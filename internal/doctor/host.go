package doctor

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/vidra/vidra-core/internal/dbmigrate"
	"github.com/vidra/vidra-core/internal/diskspace"
	"github.com/vidra/vidra-core/internal/preflight"
	"github.com/vidra/vidra-core/internal/storage"
)

// Host is everything doctor reads from the machine it runs on: the filesystem,
// the process table, the clock and the environment. It is an interface so the
// tests never touch a real one — a suite that needs a Docker daemon to test the
// Docker checks is a suite that only passes on the machine it was written on.
type Host interface {
	// Stat and ReadFile are the read-only filesystem. Paths are absolute.
	Stat(path string) (os.FileInfo, error)
	ReadFile(path string) ([]byte, error)
	// DiskUsage is statfs on the filesystem holding path.
	DiskUsage(path string) (DiskUsage, error)
	// LookPath answers "is this program installed", and nothing more.
	LookPath(file string) (string, error)
	// Run executes a command with ctx's deadline, in dir. It returns the
	// captured output EVEN WHEN the command failed — a compose error message is
	// the most useful thing a failing check can quote.
	Run(ctx context.Context, dir, name string, args ...string) (Output, error)
	// Now is the clock, injected so the backup-age check has a fixed today.
	Now() time.Time
	// Getenv reads the PROCESS environment (not the deployment's env file).
	Getenv(key string) string
}

// Output is a finished command.
type Output struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// DiskUsage is what statfs says about one filesystem.
type DiskUsage struct {
	// TotalBytes and FreeBytes are as seen by the CALLING user: free space is
	// the unprivileged figure, because that is what the containers get.
	TotalBytes uint64
	FreeBytes  uint64
}

// FreeFraction is free space as a fraction of the total, 0 when the total is
// unknown.
func (u DiskUsage) FreeFraction() float64 {
	if u.TotalBytes == 0 {
		return 0
	}
	return float64(u.FreeBytes) / float64(u.TotalBytes)
}

// Prober is everything doctor reaches for over a network or a database
// connection. It is separated from Host because the two fail for entirely
// different reasons and a test usually wants to fake exactly one of them.
type Prober interface {
	// CheckDomain is preflight's composed DNS check, wrapped so a test can hand
	// back a canned result instead of standing up a resolver.
	CheckDomain(ctx context.Context, req preflight.DomainRequest) preflight.DomainResult
	// CheckBucket makes ONE authenticated call against the object store and
	// reports whether the configured bucket is there. It must never create
	// anything.
	CheckBucket(ctx context.Context, cfg storage.S3Config) (bool, error)
	// CheckBucketRetention reports what the bucket does with overwritten and
	// deleted objects, so the report can warn about a bucket that never
	// reclaims. Read-only; creates and changes nothing.
	CheckBucketRetention(ctx context.Context, cfg storage.S3Config) (storage.BucketRetention, error)
	// CheckBucketMarker reads the media-GC ownership marker
	// (storage.OwnerMarkerKey) and reports whether it is there and what it says.
	// Read-only: doctor never writes the marker, because writing it is the
	// adoption decision and that belongs to an admin at the API, not to a
	// diagnostic that an operator may have run just to look around.
	CheckBucketMarker(ctx context.Context, cfg storage.S3Config) (found bool, content string, err error)
	// CheckSMTP dials the relay and reads its greeting. It never sends.
	CheckSMTP(ctx context.Context, addr string) (banner string, err error)
	// MigrationStatus reads a golang-migrate ledger. table is "" for the default
	// (schema_migrations, which is core's).
	MigrationStatus(ctx context.Context, dsn, table string) (dbmigrate.Status, error)
}

// ---------------------------------------------------------------------------
// The real implementations.

// RealHost is the production Host: the actual machine.
type RealHost struct{}

func (RealHost) Stat(path string) (os.FileInfo, error) { return os.Stat(path) }
func (RealHost) ReadFile(path string) ([]byte, error)  { return os.ReadFile(path) }
func (RealHost) LookPath(f string) (string, error)     { return exec.LookPath(f) }
func (RealHost) Now() time.Time                        { return time.Now() }
func (RealHost) Getenv(key string) string              { return os.Getenv(key) }
func (RealHost) DiskUsage(p string) (DiskUsage, error) { return statfs(p) }

func (RealHost) Run(ctx context.Context, dir, name string, args ...string) (Output, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	out := Output{Stdout: stdout.String(), Stderr: stderr.String()}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		// A non-zero exit is a RESULT, not a failure to run: `systemctl
		// is-enabled` says "disabled" that way, and so does `compose config` on a
		// bad env file. The caller decides what it means.
		out.ExitCode = exitErr.ExitCode()
		return out, nil
	}
	if err != nil {
		return out, err
	}
	return out, nil
}

// statfs is the free-space figure for the filesystem holding path. It delegates
// to internal/diskspace so the transcode worker's admission control and this
// diagnostic measure the same thing the same way — two answers about one disk is
// worse than one.
func statfs(path string) (DiskUsage, error) {
	u, err := diskspace.Measure(path)
	if err != nil {
		return DiskUsage{}, err
	}
	return DiskUsage{TotalBytes: u.TotalBytes, FreeBytes: u.FreeBytes}, nil
}

// RealProber is the production Prober.
type RealProber struct{}

func (RealProber) CheckDomain(ctx context.Context, req preflight.DomainRequest) preflight.DomainResult {
	return preflight.CheckDomain(ctx, req)
}

func (RealProber) CheckBucket(ctx context.Context, cfg storage.S3Config) (bool, error) {
	s3, err := storage.NewS3(cfg)
	if err != nil {
		return false, err
	}
	// BucketExists, never EnsureBucket: a diagnostic that creates the bucket it
	// could not find turns a typo into a new, empty, silently-wrong store.
	return s3.BucketExists(ctx)
}

func (RealProber) CheckBucketRetention(ctx context.Context, cfg storage.S3Config) (storage.BucketRetention, error) {
	s3, err := storage.NewS3(cfg)
	if err != nil {
		return storage.BucketRetention{}, err
	}
	return s3.Retention(ctx)
}

func (RealProber) CheckBucketMarker(ctx context.Context, cfg storage.S3Config) (bool, string, error) {
	s3, err := storage.NewS3(cfg)
	if err != nil {
		return false, "", err
	}
	content, found, err := storage.ReadOwnerMarker(ctx, s3)
	return found, content, err
}

// CheckSMTP delegates to preflight so the dial exists exactly once: `vidra
// doctor` and the api's own admin status page must ask the relay the same
// question in the same way, or an operator gets two different answers about one
// mail server.
func (RealProber) CheckSMTP(ctx context.Context, addr string) (string, error) {
	return preflight.CheckSMTP(ctx, addr)
}

// MigrationStatus reads a ledger through dbmigrate — the same library, the same
// driver and the same ledger contract the deploy's migration one-shot uses, so
// what doctor reports is what the migrator would see.
//
// The call is wrapped in a goroutine because dbmigrate.Version takes no context:
// its driver's only deadline is the DSN's own connect_timeout, which addDSNParams
// supplies, but a database that ACCEPTS the connection and then stops answering
// would still hang here. Abandoning the goroutine leaks one connection attempt
// for the few seconds until the process exits, which is the right trade against a
// diagnostic that never returns.
func (RealProber) MigrationStatus(ctx context.Context, dsn, table string) (dbmigrate.Status, error) {
	params := map[string]string{"connect_timeout": "5"}
	if table != "" {
		params["x-migrations-table"] = table
	}
	dsn, err := addDSNParams(dsn, params)
	if err != nil {
		return dbmigrate.Status{}, err
	}
	type result struct {
		st  dbmigrate.Status
		err error
	}
	done := make(chan result, 1)
	go func() {
		st, err := dbmigrate.Version(dsn)
		done <- result{st, err}
	}()
	select {
	case r := <-done:
		return r.st, r.err
	case <-ctx.Done():
		return dbmigrate.Status{}, fmt.Errorf("timed out after %s", timeoutOf(ctx))
	}
}

func timeoutOf(ctx context.Context) time.Duration {
	if deadline, ok := ctx.Deadline(); ok {
		if d := time.Until(deadline); d > 0 {
			return d.Round(time.Second)
		}
	}
	return 0
}

var _ Host = RealHost{}
var _ Prober = RealProber{}

// loopbackIP reports whether a compose publish binds to the loopback interface
// only. An empty host IP is NOT loopback: compose renders it as "publish on
// every interface", which is the whole point of the port audit.
func loopbackIP(hostIP string) bool {
	hostIP = strings.TrimSpace(hostIP)
	if hostIP == "" {
		return false
	}
	addr, err := netip.ParseAddr(strings.Trim(hostIP, "[]"))
	if err != nil {
		return false
	}
	return addr.IsLoopback()
}
