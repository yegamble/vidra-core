package peertubeimport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/lease"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/pgconv"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Service is the admin-facing orchestration over the durable runs table
// (peertube_import_runs). It launches runs, reports their status, and — via
// DrainDueRuns — executes them on a worker. The SOURCE connection is built by an
// injected factory from SERVER CONFIG only; the service never accepts a DSN or
// credential from a request. When no factory is wired (import not configured)
// the launch endpoints report ErrNotConfigured.
type Service struct {
	repo          Repository
	defaultPolicy ConflictPolicy
	// defaultMediaMode is the SERVER's configured media mode
	// (PEERTUBE_IMPORT_MEDIA_MODE), used only when a launch names none. It is a
	// fallback and not a policy: a launch that names a mode overrides it for that
	// run, and the resolved value is written to the run row so the executing
	// worker never has to consult this again.
	defaultMediaMode MediaMode
	// leaseInterval is how often a running run's lease is renewed. It is a field
	// rather than lease.DefaultInterval spelled inline only so a test can drive
	// several renewals without waiting minutes; nothing configures it.
	leaseInterval time.Duration
	logger        *slog.Logger
	// buildImporter opens the configured source + storages and returns a ready
	// Importer for one claimed run, plus a cleanup func. Nil = not configured (the
	// admin API answers 503).
	//
	// The run's parameters are passed as a struct rather than assembled inside the
	// factory precisely because assembling them there is how this broke: Options
	// .Force was an OPTIONAL field, cmd/api's closure never mentioned it, and the
	// omission was invisible at the call site for as long as the field existed.
	// A parameter the factory is handed cannot be silently left out.
	buildImporter func(ctx context.Context, params RunParams) (*Importer, func(), error)
	// recordAudit persists/emits a security-audit event. Nil = slog-only via the
	// service logger.
	recordAudit func(ctx context.Context, ev observability.AuditEvent)
	// reconcileSearch sweeps the catalogue into the search index. Nil = unwired
	// (search events are optional), in which case a finished import behaves
	// exactly as it did before.
	reconcileSearch func(ctx context.Context) error
}

// Repository is the durable run-store the service needs. *sqlcgen.Queries
// satisfies it; tests can substitute a fake.
type Repository interface {
	CreateImportRun(ctx context.Context, arg sqlcgen.CreateImportRunParams) (sqlcgen.PeertubeImportRun, error)
	GetImportRun(ctx context.Context, id uuid.UUID) (sqlcgen.PeertubeImportRun, error)
	GetLatestImportRun(ctx context.Context) (sqlcgen.PeertubeImportRun, error)
	ListImportRuns(ctx context.Context, arg sqlcgen.ListImportRunsParams) ([]sqlcgen.PeertubeImportRun, error)
	ClaimDueImportRuns(ctx context.Context, limit int32) ([]sqlcgen.ClaimDueImportRunsRow, error)
	RenewImportRunLease(ctx context.Context, id uuid.UUID) error
	SetImportRunVersion(ctx context.Context, arg sqlcgen.SetImportRunVersionParams) error
	UpdateImportRunProgress(ctx context.Context, arg sqlcgen.UpdateImportRunProgressParams) error
	CompleteImportRun(ctx context.Context, arg sqlcgen.CompleteImportRunParams) error
	FailImportRun(ctx context.Context, arg sqlcgen.FailImportRunParams) error
}

// Sentinel errors mapped by the HTTP layer to status codes.
var (
	// ErrNotConfigured — no source configured (admin endpoints → 503).
	ErrNotConfigured = errors.New("peertubeimport: import is not configured")
	// ErrBusy — a run is already active (single-active constraint) → 409.
	ErrBusy = errors.New("peertubeimport: an import run is already in progress")
	// ErrInvalidMode — mode is not dry_run|run → 400.
	ErrInvalidMode = errors.New("peertubeimport: mode must be dry_run or run")
	// ErrRunNotFound — no such run → 404.
	ErrRunNotFound = errors.New("peertubeimport: run not found")
)

// The two run modes, as stored in peertube_import_runs.mode. Named because the
// mode now decides a side effect (the post-import search reconcile) and not just
// which importer entry point runs, so a typo would be silent rather than a 400.
const (
	modeDryRun = "dry_run"
	modeRun    = "run"
)

// Option customises the Service.
type Option func(*Service)

// WithImporterFactory wires the source-connection factory (built from server
// config in cmd/api). Only when set is the import considered configured.
func WithImporterFactory(f func(ctx context.Context, params RunParams) (*Importer, func(), error)) Option {
	return func(s *Service) { s.buildImporter = f }
}

// WithLogger overrides the service logger.
func WithLogger(l *slog.Logger) Option {
	return func(s *Service) {
		if l != nil {
			s.logger = l
		}
	}
}

// WithAudit wires a durable audit sink (in addition to the slog audit line).
func WithAudit(f func(ctx context.Context, ev observability.AuditEvent)) Option {
	return func(s *Service) { s.recordAudit = f }
}

// WithSearchReconcile wires the search-index reconciliation sweep a completed
// real run triggers (searchevents.Enqueuer.RunReconcile in cmd/api).
//
// The importer writes videos with direct SQL inside its own transactions and
// emits no index event, so without this the only path into vidra-search is the
// sweep worker: once at process start, then on a ticker that defaults to 24
// hours. An admin-UI import on a running stack — the documented flow, chosen
// precisely so cutover needs no restart — therefore left the whole migrated
// catalogue unsearchable for up to a day, with restarting core as the remedy.
func WithSearchReconcile(f func(ctx context.Context) error) Option {
	return func(s *Service) { s.reconcileSearch = f }
}

// WithDefaultPolicy sets the fallback conflict policy for runs that do not
// specify one.
func WithDefaultPolicy(p ConflictPolicy) Option {
	return func(s *Service) {
		if p != "" {
			s.defaultPolicy = p
		}
	}
}

// WithDefaultMediaMode sets the fallback media mode for launches that do not
// name one (the server's PEERTUBE_IMPORT_MEDIA_MODE).
func WithDefaultMediaMode(m MediaMode) Option {
	return func(s *Service) {
		if m != "" {
			s.defaultMediaMode = m
		}
	}
}

// NewService builds the service. repo may be nil in wiring-only contexts (the
// OpenAPI contract test constructs the server without a database).
func NewService(repo Repository, opts ...Option) *Service {
	s := &Service{repo: repo, defaultPolicy: PolicySkip, defaultMediaMode: MediaModeCopy, leaseInterval: lease.DefaultInterval, logger: slog.Default()}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Configured reports whether an import source is wired (the admin API mounts
// regardless — the handlers answer 503 when this is false, keeping the contract
// surface stable).
func (s *Service) Configured() bool { return s.buildImporter != nil }

// Run is the status view of one import run.
type Run struct {
	ID             uuid.UUID `json:"id"`
	Mode           string    `json:"mode"`
	State          string    `json:"state"`
	ConflictPolicy string    `json:"conflict_policy"`
	// SourceAuthoritative is the second, orthogonal axis: whether this run was
	// allowed to update rows the import already owns. Surfaced on the status view
	// because "why did that title change?" is asked long after the run.
	SourceAuthoritative bool `json:"source_authoritative"`
	// MediaMode is the media handling the run EXECUTED under, resolved at launch
	// so the row records a decision rather than deferring to whatever the
	// executing process was configured with. Empty on runs launched before it was
	// recorded (those took the server default of the day; inventing "copy" for
	// them would be a fabricated record). It is surfaced because the questions it
	// answers — "why is my object store 8 TB?", "why does nothing play?" — are
	// asked long after the run is off the screen.
	MediaMode     string `json:"media_mode"`
	SourceVersion *int   `json:"source_version"`
	// AcknowledgedSchemaVersion is the unverified schema version the launching
	// admin explicitly accepted for this run, or nil (the norm). It is reported
	// back so the admin history shows what was signed off on, not only that
	// something was.
	AcknowledgedSchemaVersion *int    `json:"acknowledged_schema_version"`
	Report                    *Report `json:"report,omitempty"`
	Error                     string  `json:"error,omitempty"`
	// ErrorCode is the stable snake_case class of a failure, empty when the run
	// has not failed or the failure has no class of its own. Clients branch on
	// this; Error is for a person to read.
	ErrorCode  string     `json:"error_code,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// Launch is one admin launch request. It is a struct so that adding a per-run
// decision cannot quietly default: every caller names every field.
type Launch struct {
	// Mode is "dry_run" or "run".
	Mode string
	// Policy resolves naming collisions; empty takes the server default.
	Policy ConflictPolicy
	// SourceAuthoritative is the write policy for THIS run: whether a re-run may
	// update the rows the import already owns when the source has moved. It is
	// orthogonal to Policy (which resolves natural-key collisions at insert time)
	// and to AcknowledgedSchemaVersion (which is a version gate, not a write
	// policy). False — gap-filling only — is the default everywhere.
	SourceAuthoritative bool
	// AcknowledgedSchemaVersion is the unverified source schema version the
	// administrator making THIS request explicitly accepted, or 0 for the normal
	// case of no acknowledgement.
	//
	// It is never inferred, never defaulted, never read from configuration and
	// never carried over from an earlier run: it exists on one request, is stored
	// against one run row, and has to be stated again the next time. The server
	// has no code path that can produce a non-zero value for it.
	AcknowledgedSchemaVersion int
	// MediaMode is what THIS run does with the source's media objects: copy the
	// bytes into Vidra's layout, reference the source's existing object keys in
	// place, or carry no media at all. Empty takes the server default.
	//
	// It is a FOURTH axis, orthogonal to the other three — none of which says
	// anything about bytes — and it is per-run rather than server configuration
	// because the answer changes DURING a migration, when restarting the API to
	// change it is exactly what an operator cannot afford.
	MediaMode MediaMode
}

// RunParams is what the importer factory needs to build the Importer for one
// claimed run: the policy the run was launched under, the write policy it was
// launched under, and the acknowledgement it carries (0 = none).
type RunParams struct {
	Policy                    ConflictPolicy
	SourceAuthoritative       bool
	AcknowledgedSchemaVersion int
	// MediaMode is the media handling the run was launched under. Empty only for
	// a run row written before 0125 recorded it; the factory falls back to the
	// server default there, which is what such a run already did.
	MediaMode MediaMode
}

// CreateRun launches a new run from an admin request. It returns ErrBusy when one
// is already active, ErrNotConfigured when no source is wired, and ErrInvalidMode
// for a bad mode. It emits a start audit event.
func (s *Service) CreateRun(ctx context.Context, in Launch, adminID uuid.UUID) (Run, error) {
	if !s.Configured() {
		return Run{}, ErrNotConfigured
	}
	if in.Mode != modeDryRun && in.Mode != modeRun {
		return Run{}, ErrInvalidMode
	}
	policy := in.Policy
	if policy == "" {
		policy = s.defaultPolicy
	}
	// The media mode is resolved HERE, at launch, and not at execution: the row is
	// then the record of what was decided, and a worker restarted with a different
	// PEERTUBE_IMPORT_MEDIA_MODE cannot change a run's meaning after the fact.
	mediaMode := in.MediaMode
	if mediaMode == "" {
		mediaMode = s.defaultMediaMode
	}
	row, err := s.repo.CreateImportRun(ctx, sqlcgen.CreateImportRunParams{
		Mode:                      in.Mode,
		ConflictPolicy:            policy.String(),
		SourceAuthoritative:       in.SourceAuthoritative,
		StartedBy:                 optUUID(adminID),
		AcknowledgedSchemaVersion: optSchemaVersion(in.AcknowledgedSchemaVersion),
		MediaMode:                 mediaMode.String(),
	})
	if pgconv.IsUniqueViolation(err) {
		return Run{}, ErrBusy
	}
	if err != nil {
		return Run{}, err
	}
	// The HTTP handler audits the launch (it has the request/correlation id); the
	// worker audits the finish via executeRun.
	return runFromRow(row), nil
}

// GetRun returns one run by id (ErrRunNotFound when absent).
func (s *Service) GetRun(ctx context.Context, id uuid.UUID) (Run, error) {
	row, err := s.repo.GetImportRun(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, ErrRunNotFound
	}
	if err != nil {
		return Run{}, err
	}
	return runFromRow(row), nil
}

// LatestRun returns the most recent run (ErrRunNotFound when none exist).
func (s *Service) LatestRun(ctx context.Context) (Run, error) {
	row, err := s.repo.GetLatestImportRun(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, ErrRunNotFound
	}
	if err != nil {
		return Run{}, err
	}
	return runFromRow(row), nil
}

// ListRuns returns runs newest-first.
func (s *Service) ListRuns(ctx context.Context, limit, offset int32) ([]Run, error) {
	rows, err := s.repo.ListImportRuns(ctx, sqlcgen.ListImportRunsParams{Limit: limit, Offset: offset})
	if err != nil {
		return nil, err
	}
	out := make([]Run, 0, len(rows))
	for _, r := range rows {
		out = append(out, runFromRow(r))
	}
	return out, nil
}

// DrainDueRuns claims up to limit due runs and executes each. Only the claim
// error is returned; per-run outcomes are persisted to the run row. Intended to
// be called on a ticker by a single worker.
func (s *Service) DrainDueRuns(ctx context.Context, limit int) (int, error) {
	if !s.Configured() {
		return 0, nil
	}
	claims, err := s.repo.ClaimDueImportRuns(ctx, int32(limit))
	if err != nil {
		return 0, err
	}
	for _, claim := range claims {
		s.executeRun(ctx, claim)
	}
	return len(claims), nil
}

// executeRun performs one claimed run: preflight, then plan (dry-run) or run,
// persisting progress + the terminal state. All persisted messages are SAFE
// (no DSN/credential/PII).
func (s *Service) executeRun(ctx context.Context, claim sqlcgen.ClaimDueImportRunsRow) {
	// RENEW THE LEASE. The claim pushed next_attempt_at 30 minutes out and
	// jobrecovery sweeps every 2; a real migration runs for hours. Every sibling
	// durable queue renews while it works and this one — the longest-running of
	// them — was the only one that never did, so from T+30min the row read
	// 'pending' with started_at NULL and an inflated attempts count while the
	// import was actively writing.
	//
	// On ONE instance that is cosmetic: the terminal writes are unguarded on
	// state, and this goroutine is blocked here and cannot re-claim its own row.
	// On TWO it is data corruption — the other instance claims the requeued row
	// and executes concurrently. Entity kinds survive that (the ledger upsert
	// shares the insert's transaction and UNIQUE (entity_kind, source_id)
	// serialises them); counters do not, because importOneViewCount reads the
	// applied total OUTSIDE its transaction and applies a delta, so both
	// executors add the same delta and the view counts double.
	//
	// stop is deferred rather than called before the terminal write (the
	// siblings' shape) because this function has six exits. A renewal racing a
	// completion is harmless: RenewImportRunLease is guarded on state='running'
	// and cannot revive a finished run.
	stopLease := lease.Keep(ctx, s.leaseInterval, "peertube_import_run", func(c context.Context) error {
		return s.repo.RenewImportRunLease(c, claim.ID)
	})
	defer stopLease()

	actor := ""
	if claim.StartedBy.Valid {
		actor = uuid.UUID(claim.StartedBy.Bytes).String()
	}
	policy, perr := ParseConflictPolicy(claim.ConflictPolicy)
	if perr != nil {
		policy = s.defaultPolicy
	}
	// The acknowledgement comes off the RUN ROW, which is the only place it has
	// ever existed: it was written from the launch request and nothing since has
	// been able to add one. A run launched without it stays at 0 here.
	ack := 0
	if claim.AcknowledgedSchemaVersion != nil {
		ack = int(*claim.AcknowledgedSchemaVersion)
	}

	// The write policy comes off the RUN ROW for the same reason the
	// acknowledgement does: an admin launches through the API and a worker in
	// another process claims the run, so the run row is the whole of what survives
	// the hop.
	//
	// The media mode rides the same row. An unparseable value (only reachable if
	// something wrote the column behind the CHECK) falls back to the server
	// default rather than aborting: the same treatment the policy above gets.
	mediaMode, mmerr := ParseMediaMode(claim.MediaMode)
	if mmerr != nil || claim.MediaMode == "" {
		mediaMode = s.defaultMediaMode
	}
	importer, cleanup, err := s.buildImporter(ctx, RunParams{
		Policy:                    policy,
		SourceAuthoritative:       claim.SourceAuthoritative,
		AcknowledgedSchemaVersion: ack,
		MediaMode:                 mediaMode,
	})
	if err != nil {
		s.logger.WarnContext(ctx, "peertube import: could not open source", "run_id", claim.ID.String(), "error", err)
		s.failRun(ctx, claim.ID, "could not connect to the configured source", "")
		s.emitAudit(ctx, observability.ActionPeerTubeImportFinish, observability.ResultFailure, actor, "source connection failed")
		return
	}
	defer cleanup()

	// The admin path NEVER self-passes --force. An unverified version is refused
	// here unless the launching administrator acknowledged THIS version on the
	// request; without that the run fails with the safe version message and the
	// machine-readable class, so the UI can offer the acknowledgement instead of
	// leaving the operator to reach for the CLI.
	version, err := importer.Preflight(ctx)
	// Preflight reports the version it detected even when it refuses. Record it
	// first: the number the operator is being asked to accept is the whole of what
	// makes the refusal actionable, and a failed run used to carry no version at all.
	s.recordSourceVersion(ctx, claim.ID, version)
	if err != nil {
		var refusal *UnverifiedSchemaError
		code := ""
		reason := "preflight failed"
		if errors.As(err, &refusal) {
			code, reason = refusal.Code(), "preflight refused: "+refusal.Code()
		}
		s.failRun(ctx, claim.ID, err.Error(), code)
		s.emitAudit(ctx, observability.ActionPeerTubeImportFinish, observability.ResultFailure, actor, reason)
		return
	}
	// The launch audit recorded what the admin ASKED for; this records whether the
	// gate was actually opened on it, which is a different fact — an acknowledgement
	// of a version the source turns out not to be running overrules nothing. It
	// rides the run's own finish event rather than a second start, so a run is still
	// exactly one start and one finish in the log, and it is on the record
	// independently of the run row, which is prunable.
	overruled := ""
	if !IsSupported(version) {
		overruled = fmt.Sprintf("ran on acknowledged unverified schema version %d; ", version)
	}

	var report *Report
	if claim.Mode == modeDryRun {
		report, err = importer.Plan(ctx, version)
	} else {
		report, err = importer.Run(ctx, version, func(r *Report) { s.persistProgress(ctx, claim.ID, r) })
	}
	if err != nil {
		s.abandonRun(ctx, claim, actor, overruled, report, err)
		return
	}
	s.finishRun(ctx, claim, actor, overruled, report)
}

// abandonRun records the terminal outcome of a run whose passes did NOT all run:
// the run row is failed with a safe message and the finish audit says so.
//
// It sweeps the search index for the same reason finishRun does. A run that died
// partway — a dropped source connection, a context deadline, an abort on
// --conflict-policy fail — has already WRITTEN everything it imported before it
// failed, and the report it returns alongside the error is a real tally of those
// real rows. Leaving that partial catalogue out of the index because the run
// ended badly puts the operator back on the 24h ticker or a container restart,
// which is exactly what the post-import sweep exists to remove.
//
// The report is NOT persisted here: FailImportRun owns the run row's terminal
// write and carries the operator-facing error, and the last progress snapshot
// persistProgress wrote is already on the row.
func (s *Service) abandonRun(ctx context.Context, claim sqlcgen.ClaimDueImportRunsRow, actor, overruled string, report *Report, err error) {
	s.failRun(ctx, claim.ID, safeRunError(err), "")
	s.emitAudit(ctx, observability.ActionPeerTubeImportFinish, observability.ResultFailure, actor, overruled+"import failed")
	s.reconcileSearchIndex(ctx, claim, report)
}

// finishRun records the terminal outcome of a run whose passes all ran: the
// report is persisted, the run row is completed, and the finish audit is emitted
// with the result THE REPORT gives — not the one the absence of a run-level
// error implies.
//
// Those are different facts. Importer.Run returns nil when every individual
// entity failed, because failures are recorded per row and the loop continues;
// branching on that error alone stamped ResultSuccess on
// "imported=0 failed=13528". The run row is prunable and the audit log is not,
// so months later the audit line is the only surviving record of the migration
// and it has to be the honest one.
//
// The run STATE is deliberately untouched. The run did finish, 0067's CHECK
// admits only pending/running/done/failed, and a fifth state is not what needs
// to change.
//
// It takes the whole claim rather than just the id because the MODE decides the
// last step: this is the one place a run is known to have completed, so it is
// where the freshly written catalogue is swept into the search index — and a dry
// run wrote nothing, so it sweeps nothing.
func (s *Service) finishRun(ctx context.Context, claim sqlcgen.ClaimDueImportRunsRow, actor, overruled string, report *Report) {
	id := claim.ID
	data, merr := json.Marshal(report)
	if merr != nil {
		data = []byte("{}")
	}
	if err := s.repo.CompleteImportRun(ctx, sqlcgen.CompleteImportRunParams{ID: id, Progress: data}); err != nil {
		s.logger.WarnContext(ctx, "peertube import: complete run failed", "run_id", id.String(), "error", err)
	}
	// Only Failed counts. Skipped is an already-imported row and Unsupported is
	// a family this version defers on purpose — a branch wide enough to catch
	// those would mark every healthy run failed, which destroys the signal.
	result, note := observability.ResultSuccess, ""
	if failed := report.totals().Failed; failed > 0 {
		result = observability.ResultFailure
		note = fmt.Sprintf("finished with %d failed rows; ", failed)
	}
	s.emitAudit(ctx, observability.ActionPeerTubeImportFinish, result, actor, overruled+note+report.Summary())
	s.reconcileSearchIndex(ctx, claim, report)
}

// reconcileSearchIndex sweeps the catalogue into the search index after a run
// that WROTE something. The importer writes videos with direct SQL inside its own
// transactions and emits no index event, so this sweep is the only path into
// vidra-search short of the 24h ticker or a process restart.
//
// The condition is what this run wrote, not how it ended. A dry run planned and
// wrote nothing, so it sweeps nothing; a run that imported or updated nothing has
// nothing new to state and paging the whole catalogue through the outbox to
// restate it is pure cost. Updated counts alongside Imported because a
// source-authoritative re-run can change every title in the catalogue while
// importing zero rows, and a stale index is a stale index either way.
//
// It is safe to run twice: RunReconcile stamps every begin/page/end with a fresh
// run id, so a second sweep simply restates the same documents under a newer
// stamp and the receiver suppresses whatever the newest stamp did not name.
// Re-running is therefore a no-op for the index, not a duplicate — which is what
// makes it safe to fire from both terminal paths.
//
// Best-effort, like every other emission here: the run's terminal record is
// already written, and a failed index refresh must not rewrite it. The 24h sweep
// worker is still the backstop if this drops.
func (s *Service) reconcileSearchIndex(ctx context.Context, claim sqlcgen.ClaimDueImportRunsRow, report *Report) {
	if s.reconcileSearch == nil || claim.Mode == modeDryRun || report == nil {
		return
	}
	if t := report.totals(); t.Imported+t.Updated == 0 {
		return
	}
	if err := s.reconcileSearch(ctx); err != nil {
		s.logger.WarnContext(ctx, "peertube import: search reconcile after import failed; "+
			"the imported catalogue stays unsearchable until the next sweep",
			"run_id", claim.ID.String(), "error", err)
	}
}

func (s *Service) persistProgress(ctx context.Context, id uuid.UUID, r *Report) {
	data, err := json.Marshal(r)
	if err != nil {
		return
	}
	_ = s.repo.UpdateImportRunProgress(ctx, sqlcgen.UpdateImportRunProgressParams{ID: id, Progress: data})
}

// recordSourceVersion stamps the detected schema version on the run. It is called
// whether or not preflight went on to accept that version: a run refused for an
// unverified schema is exactly the run whose version the operator most needs to
// see, since acknowledging it means naming it.
func (s *Service) recordSourceVersion(ctx context.Context, id uuid.UUID, version int) {
	if version <= 0 {
		return
	}
	v32 := int32(version)
	_ = s.repo.SetImportRunVersion(ctx, sqlcgen.SetImportRunVersionParams{ID: id, SourceVersion: &v32})
}

// failRun marks a run failed with SAFE operator prose and a stable snake_case
// class (empty when the failure has none).
func (s *Service) failRun(ctx context.Context, id uuid.UUID, msg, code string) {
	if err := s.repo.FailImportRun(ctx, sqlcgen.FailImportRunParams{ID: id, Error: msg, ErrorCode: code}); err != nil {
		s.logger.WarnContext(ctx, "peertube import: mark run failed", "run_id", id.String(), "error", err)
	}
}

// emitAudit records a security-audit event. It always emits the slog audit line
// and, when a durable sink is wired, persists it too. Reason is caller-provided
// and MUST be secret-free.
func (s *Service) emitAudit(ctx context.Context, action, result, actorID, reason string) {
	ev := observability.AuditEvent{Action: action, Result: result, ActorID: actorID, Reason: reason}
	observability.Audit(ctx, s.logger, ev)
	if s.recordAudit != nil {
		s.recordAudit(ctx, ev)
	}
}

// runFromRow maps a durable run row to the status DTO, parsing the progress JSON
// into a Report.
func runFromRow(row sqlcgen.PeertubeImportRun) Run {
	r := Run{
		ID:                  row.ID,
		Mode:                row.Mode,
		State:               row.State,
		ConflictPolicy:      row.ConflictPolicy,
		SourceAuthoritative: row.SourceAuthoritative,
		MediaMode:           row.MediaMode,
		Error:               row.Error,
		ErrorCode:           row.ErrorCode,
		CreatedAt:           row.CreatedAt,
		UpdatedAt:           row.UpdatedAt,
	}
	if row.SourceVersion != nil {
		v := int(*row.SourceVersion)
		r.SourceVersion = &v
	}
	if row.AcknowledgedSchemaVersion != nil {
		v := int(*row.AcknowledgedSchemaVersion)
		r.AcknowledgedSchemaVersion = &v
	}
	if len(row.Progress) > 0 && string(row.Progress) != "{}" {
		var rep Report
		if err := json.Unmarshal(row.Progress, &rep); err == nil {
			r.Report = &rep
		}
	}
	if row.StartedAt.Valid {
		t := row.StartedAt.Time
		r.StartedAt = &t
	}
	if row.FinishedAt.Valid {
		t := row.FinishedAt.Time
		r.FinishedAt = &t
	}
	return r
}

// safeRunError renders a run-level error as a SAFE operator message. ErrConflictFail
// gets a clear explanation; everything else is bounded and generic enough not to
// leak internals.
func safeRunError(err error) string {
	if errors.Is(err, ErrConflictFail) {
		return "aborted: a naming conflict was hit and the conflict policy is 'fail'"
	}
	return safeErr(err)
}
