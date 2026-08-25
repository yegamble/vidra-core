package doctor

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/vidra/vidra-core/internal/dbmigrate"
	"github.com/vidra/vidra-core/internal/diskspace"
	"github.com/vidra/vidra-core/internal/preflight"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store"
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
	// CheckBucketWrite is the ONE call in this interface that writes: it stores a
	// tiny scratch object and removes it again, proving the credentials can
	// PutObject. It never touches the ownership marker — a probe object claims
	// nothing, where the marker IS the adoption decision — and it is only ever
	// called when the operator opted in (Options.WriteProbe).
	CheckBucketWrite(ctx context.Context, cfg storage.S3Config) (storage.WriteProbe, error)
	// CheckSMTP dials the relay and reads its greeting. It never sends.
	CheckSMTP(ctx context.Context, addr string) (banner string, err error)
	// MigrationStatus reads a golang-migrate ledger. table is "" for the default
	// (schema_migrations, which is core's).
	MigrationStatus(ctx context.Context, dsn, table string) (dbmigrate.Status, error)
	// ActiveStorageMigration reports whether a STORAGE migration campaign (media
	// moving between backends) is in flight. Read-only, and deliberately the
	// same question the media-GC interlock asks, so doctor and the sweep can
	// never disagree about whether a move is happening.
	ActiveStorageMigration(ctx context.Context, dsn string) (bool, error)
	// ServerMaxConnections reads the SERVER's max_connections. It is the other
	// half of a question DB_MAX_CONNS alone cannot answer: the pool is a
	// per-process budget and this is the total, so what an operator needs is the
	// arithmetic between them.
	ServerMaxConnections(ctx context.Context, dsn string) (int, error)
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

// CheckBucketWrite delegates to storage.ProbeWrite so the probe exists exactly
// once: `vidra doctor --write-probe` and a migration preflight must prove the
// destination the same way, at a key with the same guarantees, or the two would
// disagree about whether a destination is usable.
func (RealProber) CheckBucketWrite(ctx context.Context, cfg storage.S3Config) (storage.WriteProbe, error) {
	s3, err := storage.NewS3(cfg)
	if err != nil {
		return storage.WriteProbe{}, err
	}
	return storage.ProbeWrite(ctx, s3)
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

// ActiveStorageMigration asks the database the SAME question the media-GC
// interlock asks — the sqlc query HasActiveStorageMigration, through the same
// pool type the api uses — rather than a hand-written SELECT that would have to
// be kept in step with it. The query's semantics are deliberately wide (a
// 'failed' campaign still counts as in flight), and a doctor that quietly used
// a narrower definition would tell an operator the coast was clear while the
// sweep was still refusing to delete.
//
// A connect timeout is pushed into the DSN for the same reason MigrationStatus
// does it: a database that accepts the connection and then stops answering must
// not hang the whole report.
func (RealProber) ActiveStorageMigration(ctx context.Context, dsn string) (bool, error) {
	dsn, err := addDSNParams(dsn, map[string]string{"connect_timeout": "5"})
	if err != nil {
		return false, err
	}
	db, err := store.New(ctx, dsn)
	if err != nil {
		return false, err
	}
	defer db.Close()
	return db.Queries().HasActiveStorageMigration(ctx)
}

// ServerMaxConnections reads `SHOW max_connections` over a pool opened for the
// one query and closed again. A connect timeout is pushed into the DSN for the
// same reason the two probes above do it.
//
// The value comes back as TEXT (SHOW always does), so it is parsed rather than
// scanned into an int.
func (RealProber) ServerMaxConnections(ctx context.Context, dsn string) (int, error) {
	dsn, err := addDSNParams(dsn, map[string]string{"connect_timeout": "5"})
	if err != nil {
		return 0, err
	}
	db, err := store.New(ctx, dsn)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	var raw string
	if err := db.Pool.QueryRow(ctx, "SHOW max_connections").Scan(&raw); err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("max_connections is %q, which is not a number", raw)
	}
	return n, nil
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
