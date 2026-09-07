// Package processheartbeat makes a vidra-core process visible to the rest of
// the fleet, and to the admin status page in particular.
//
// THE FAILURE MODE IT CLOSES. /admin/system reports on the process that served
// the request. That is correct for postgres, redis, s3 and the rest — they are
// this process's own dependencies — but it is wrong for settings_sync, whose
// whole subject is "does every replica agree with the database". On a split
// topology (VIDRA_ROLE=api plus VIDRA_ROLE=worker) the api answers only for
// itself, so a worker whose every settings poll fails — or a worker that is not
// running at all — leaves the page reading `ok` with every component healthy.
// The worker is the half that transcodes, imports, sweeps and expires while
// consulting the same instance-settings overlay, so "the worker is gone" is
// exactly the outage an operator must be able to see and today the single
// surface built to show it says nothing.
//
// THE MECHANISM. One row per process (migration 0133), upserted on every tick
// of the settings-version poller — the one loop that already runs in EVERY role
// and already keeps the per-process health record the page reads. The api then
// answers for the FLEET: healthy only when every process it can see polled
// successfully inside the window.
//
// WHAT A HEARTBEAT CANNOT DO. It reports a process alive enough to write one. A
// process killed with SIGKILL, or one whose host vanished, shows up as ABSENCE —
// a last_seen_at that stopped moving — never as a self-reported failure. That is
// why staleness, not a status column, is the primary signal, and why a clean
// shutdown writes 'stopped' explicitly: without it an operator cannot tell a
// replica they scaled down from one that died.
//
// WHY A TABLE AND NOT A REDIS KEY. internal/settingsversion argues at length
// against putting the fleet's knowledge of its own configuration on Redis: every
// other use of Redis here fails open, and a Redis outage must not be able to
// fork the fleet's view of itself. The same argument applies to the surface that
// REPORTS that view. A row costs a migration; it is the house idiom, it survives
// a Redis flush, and it is queryable by an operator with psql when the admin page
// is the thing that is broken.
package processheartbeat

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

const (
	// DefaultForgetAfter is how long a silent row stays in the fleet answer. It
	// is deliberately far longer than the staleness window: a worker killed a
	// minute ago must degrade the instance, but a replica decommissioned last
	// week must not degrade it forever, and an operator staring at a machine
	// they scrapped learns nothing from it. Between those two the row is a
	// visible, named fault.
	DefaultForgetAfter = 24 * time.Hour

	// StaleMultiplier is how many poll intervals a process may miss before the
	// fleet calls it stale. Three is one lost tick plus jitter plus a slow
	// database: a single missed write is noise, three in a row is an absence.
	StaleMultiplier = 3

	// maxPollError bounds the stored poll error to the column's CHECK.
	maxPollError = 1024

	// forgetSweepEvery is how often a process sweeps rows the fleet answer
	// already hides. It runs from the heartbeat tick rather than from a loop of
	// its own because there is nothing else to schedule and the work is one
	// indexed DELETE that normally matches nothing; hourly keeps that cost
	// invisible while still bounding the table.
	forgetSweepEvery = time.Hour
)

// Repository is the persistence this package needs. *sqlcgen.Queries satisfies
// it directly; tests substitute an in-memory fake.
type Repository interface {
	UpsertProcessHeartbeat(ctx context.Context, arg sqlcgen.UpsertProcessHeartbeatParams) error
	MarkProcessStopped(ctx context.Context, processID string) error
	ListProcessHeartbeats(ctx context.Context, forgetAfter pgtype.Interval) ([]sqlcgen.ProcessHeartbeat, error)
	ForgetStaleProcessHeartbeats(ctx context.Context, forgetAfter pgtype.Interval) (int64, error)
}

// PollHealth is the settings poller's health record: when this process last
// agreed with the shared counter, and the error keeping it stale now (nil when
// the last attempt succeeded). *settingsversion.Poller satisfies it.
type PollHealth interface {
	Health() (lastSuccess time.Time, lastErr error)
}

// ProcessID is the stable key for one running process: hostname:pid.
//
// Bounded on purpose. A restarted container or pod comes back with the same
// hostname and (in a container) the same pid 1, so it UPSERTS over its own row
// instead of leaving a corpse behind on every deploy; started_at is what tells
// one run from the next. A host that is genuinely gone ages out through
// ForgetAfter rather than being remembered forever.
func ProcessID(hostname string, pid int) string {
	if hostname == "" {
		hostname = "unknown-host"
	}
	return fmt.Sprintf("%s:%d", hostname, pid)
}

// SelfID is this process's key, computed the same way NewWriter computes it. It
// exists because the JOB recorder (internal/jobtrace) is built long before the
// heartbeat writer in cmd/api — services are constructed first — and both must
// stamp the SAME string, or a run's worker_id would name a process the status
// page's list does not contain.
func SelfID() string {
	host, _ := os.Hostname()
	return ProcessID(host, os.Getpid())
}

// Writer keeps one process's row current.
type Writer struct {
	repo        Repository
	health      PollHealth
	processID   string
	role        string
	hostname    string
	pid         int32
	version     string
	commit      string
	startedAt   time.Time
	forgetAfter time.Duration

	mu         sync.Mutex
	lastForget time.Time
}

// Config describes the process being announced.
type Config struct {
	Role        string
	Version     string
	Commit      string
	StartedAt   time.Time
	ForgetAfter time.Duration
	// Hostname and PID are overridable for tests; zero values are read from the
	// host.
	Hostname string
	PID      int
}

// NewWriter builds the writer for THIS process. A nil repo yields a nil writer,
// which every method tolerates: single-process installs without a database and
// the existing unit servers are unaffected.
func NewWriter(repo Repository, health PollHealth, cfg Config) *Writer {
	if repo == nil {
		return nil
	}
	host := cfg.Hostname
	if host == "" {
		host, _ = os.Hostname()
	}
	pid := cfg.PID
	if pid == 0 {
		pid = os.Getpid()
	}
	started := cfg.StartedAt
	if started.IsZero() {
		started = time.Now().UTC()
	}
	forget := cfg.ForgetAfter
	if forget <= 0 {
		forget = DefaultForgetAfter
	}
	return &Writer{
		repo:        repo,
		health:      health,
		processID:   ProcessID(host, pid),
		role:        cfg.Role,
		hostname:    host,
		pid:         int32(pid),
		version:     cfg.Version,
		commit:      cfg.Commit,
		startedAt:   started,
		forgetAfter: forget,
	}
}

// ProcessID reports this process's key, which is also the worker id stamped on
// the job runs it claims — one identifier, so a run's WORKER column and the
// status page's process list name the same thing.
func (w *Writer) ProcessID() string {
	if w == nil {
		return ""
	}
	return w.processID
}

// Beat writes this process's row, carrying the settings poller's current health.
func (w *Writer) Beat(ctx context.Context) error {
	if w == nil {
		return nil
	}
	var (
		lastSuccess time.Time
		lastErr     error
	)
	if w.health != nil {
		lastSuccess, lastErr = w.health.Health()
	}
	msg := ""
	if lastErr != nil {
		msg = truncate(lastErr.Error(), maxPollError)
	}
	return w.repo.UpsertProcessHeartbeat(ctx, sqlcgen.UpsertProcessHeartbeatParams{
		ProcessID:                 w.processID,
		Role:                      w.role,
		Hostname:                  w.hostname,
		Pid:                       w.pid,
		Version:                   w.version,
		BuildCommit:               w.commit,
		StartedAt:                 w.startedAt,
		LastSettingsPollSuccessAt: timestamptz(lastSuccess),
		LastSettingsPollError:     msg,
	})
}

// Stop marks this process stopped. Best effort and deliberately short-lived:
// shutdown must not block on it, and a process that never gets here shows as a
// stale 'running' row, which is the honest report of a crash.
func (w *Writer) Stop(ctx context.Context) error {
	if w == nil {
		return nil
	}
	return w.repo.MarkProcessStopped(ctx, w.processID)
}

// Forget sweeps rows the fleet answer already hides, so the table stays the size
// of the fleet rather than the size of its history.
func (w *Writer) Forget(ctx context.Context) (int64, error) {
	if w == nil {
		return 0, nil
	}
	return w.repo.ForgetStaleProcessHeartbeats(ctx, interval(w.forgetAfter))
}

// AfterTick is the hook the settings poller calls once per tick. It never
// returns an error to the poller: a heartbeat that cannot be written must not
// stop the poll it reports on, because the poll is the thing that keeps this
// replica's settings correct and the heartbeat is only the thing that says so.
func (w *Writer) AfterTick(ctx context.Context, logger *slog.Logger) {
	if w == nil {
		return
	}
	if err := w.Beat(ctx); err != nil && logger != nil {
		logger.Warn("process heartbeat write failed; this process may read as stale on the admin status page",
			"process_id", w.processID, "role", w.role, "error", err.Error())
	}
	if !w.dueForForget(time.Now()) {
		return
	}
	if n, err := w.Forget(ctx); err != nil {
		if logger != nil {
			logger.Warn("could not sweep forgotten process heartbeats", "error", err.Error())
		}
	} else if n > 0 && logger != nil {
		logger.Info("forgot process heartbeats not seen inside the window",
			"count", n, "forget_after", w.forgetAfter.String())
	}
}

// dueForForget reports whether this tick should also sweep, and claims the slot
// if so. Every process sweeps, which is correct: the work is idempotent and a
// deployment whose only surviving process is a worker must still be able to
// forget the api that went away.
func (w *Writer) dueForForget(now time.Time) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.lastForget.IsZero() && now.Sub(w.lastForget) < forgetSweepEvery {
		return false
	}
	w.lastForget = now
	return true
}

// Fleet reads every process the instance can currently see.
type Fleet struct {
	repo        Repository
	self        string
	interval    time.Duration
	forgetAfter time.Duration
}

// NewFleet builds the read side. A nil repo yields nil, and the status page then
// omits the process list entirely rather than claiming an empty fleet.
func NewFleet(repo Repository, selfID string, pollInterval, forgetAfter time.Duration) *Fleet {
	if repo == nil {
		return nil
	}
	if pollInterval <= 0 {
		pollInterval = 10 * time.Second
	}
	if forgetAfter <= 0 {
		forgetAfter = DefaultForgetAfter
	}
	return &Fleet{repo: repo, self: selfID, interval: pollInterval, forgetAfter: forgetAfter}
}

// Process is one row of the fleet, already judged.
type Process struct {
	ProcessID    string
	Role         string
	Hostname     string
	PID          int32
	Version      string
	Commit       string
	State        string // "running" | "stale" | "stopped"
	StartedAt    time.Time
	LastSeenAt   time.Time
	LastSeenAgo  time.Duration
	SettingsPoll string // "ok" | "failing" | "never"
	PollError    string
	PollSuccess  time.Time
	Self         bool
}

// StaleWindow is how long a process may stay silent before the fleet stops
// believing it is there.
func (f *Fleet) StaleWindow() time.Duration {
	if f == nil {
		return 0
	}
	return time.Duration(StaleMultiplier) * f.interval
}

// List returns the fleet, freshest first, with each row's state decided.
func (f *Fleet) List(ctx context.Context, now time.Time) ([]Process, error) {
	if f == nil {
		return nil, nil
	}
	rows, err := f.repo.ListProcessHeartbeats(ctx, interval(f.forgetAfter))
	if err != nil {
		return nil, err
	}
	stale := f.StaleWindow()
	out := make([]Process, 0, len(rows))
	for _, r := range rows {
		p := Process{
			ProcessID:   r.ProcessID,
			Role:        r.Role,
			Hostname:    r.Hostname,
			PID:         r.Pid,
			Version:     r.Version,
			Commit:      r.BuildCommit,
			StartedAt:   r.StartedAt,
			LastSeenAt:  r.LastSeenAt,
			LastSeenAgo: now.Sub(r.LastSeenAt).Round(time.Second),
			PollError:   r.LastSettingsPollError,
			Self:        r.ProcessID == f.self,
		}
		if p.LastSeenAgo < 0 {
			p.LastSeenAgo = 0
		}
		if r.LastSettingsPollSuccessAt.Valid {
			p.PollSuccess = r.LastSettingsPollSuccessAt.Time
		}
		switch {
		case r.State == "stopped":
			p.State = "stopped"
		case p.LastSeenAgo > stale:
			p.State = "stale"
		default:
			p.State = "running"
		}
		switch {
		case r.LastSettingsPollError != "":
			p.SettingsPoll = "failing"
		case r.LastSettingsPollSuccessAt.Valid:
			p.SettingsPoll = "ok"
		default:
			p.SettingsPoll = "never"
		}
		out = append(out, p)
	}
	return out, nil
}

// FleetFault is the first fleet-wide problem worth degrading the instance for,
// or the zero value when every visible process is healthy.
type FleetFault struct {
	Process Process
	Reason  string
}

// Judge picks the one process an operator should be told about. Order matters:
// a process that stopped reporting is a worse fact than one that is reporting a
// failure, because the second one is at least still there to fix itself.
//
// A 'stopped' row NEVER degrades: it is a replica that said goodbye, which is
// what a scale-down looks like.
func Judge(processes []Process) (FleetFault, bool) {
	var failing *Process
	for i := range processes {
		p := processes[i]
		if p.State == "stopped" {
			continue
		}
		if p.State == "stale" {
			return FleetFault{
				Process: p,
				Reason: fmt.Sprintf(
					"the %s process %s has not checked in for %s, so it is stopped, wedged or unable to reach the database; work it owns is not running and it may be serving stale instance settings/documents/branding",
					p.Role, p.ProcessID, p.LastSeenAgo),
			}, true
		}
		if p.SettingsPoll == "failing" && failing == nil {
			q := p
			failing = &q
		}
	}
	if failing != nil {
		last := "never"
		if !failing.PollSuccess.IsZero() {
			last = time.Since(failing.PollSuccess).Round(time.Second).String() + " ago"
		}
		return FleetFault{
			Process: *failing,
			Reason: fmt.Sprintf(
				"the %s process %s cannot read the settings version (last success %s), so that replica may be serving stale instance settings/documents/branding: %s",
				failing.Role, failing.ProcessID, last, failing.PollError),
		}, true
	}
	return FleetFault{}, false
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func timestamptz(t time.Time) pgtype.Timestamptz {
	if t.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: t, Valid: true}
}

func interval(d time.Duration) pgtype.Interval {
	return pgtype.Interval{Microseconds: int64(d / time.Microsecond), Valid: true}
}
