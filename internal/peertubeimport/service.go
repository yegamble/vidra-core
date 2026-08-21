package peertubeimport

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vidra/vidra-core/internal/observability"
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
	logger        *slog.Logger
	// buildImporter opens the configured source + storages and returns a ready
	// Importer for a given conflict policy, plus a cleanup func. Nil = not
	// configured (the admin API answers 503).
	buildImporter func(ctx context.Context, policy ConflictPolicy) (*Importer, func(), error)
	// recordAudit persists/emits a security-audit event. Nil = slog-only via the
	// service logger.
	recordAudit func(ctx context.Context, ev observability.AuditEvent)
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

// Option customises the Service.
type Option func(*Service)

// WithImporterFactory wires the source-connection factory (built from server
// config in cmd/api). Only when set is the import considered configured.
func WithImporterFactory(f func(ctx context.Context, policy ConflictPolicy) (*Importer, func(), error)) Option {
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

// WithDefaultPolicy sets the fallback conflict policy for runs that do not
// specify one.
func WithDefaultPolicy(p ConflictPolicy) Option {
	return func(s *Service) {
		if p != "" {
			s.defaultPolicy = p
		}
	}
}

// NewService builds the service. repo may be nil in wiring-only contexts (the
// OpenAPI contract test constructs the server without a database).
func NewService(repo Repository, opts ...Option) *Service {
	s := &Service{repo: repo, defaultPolicy: PolicySkip, logger: slog.Default()}
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
	ID             uuid.UUID  `json:"id"`
	Mode           string     `json:"mode"`
	State          string     `json:"state"`
	ConflictPolicy string     `json:"conflict_policy"`
	SourceVersion  *int       `json:"source_version"`
	Report         *Report    `json:"report,omitempty"`
	Error          string     `json:"error,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
}

// CreateRun launches a new run (dry_run or run) under the given policy. It
// returns ErrBusy when one is already active, ErrNotConfigured when no source is
// wired, and ErrInvalidMode for a bad mode. It emits a start audit event.
func (s *Service) CreateRun(ctx context.Context, mode string, policy ConflictPolicy, adminID uuid.UUID) (Run, error) {
	if !s.Configured() {
		return Run{}, ErrNotConfigured
	}
	if mode != "dry_run" && mode != "run" {
		return Run{}, ErrInvalidMode
	}
	if policy == "" {
		policy = s.defaultPolicy
	}
	row, err := s.repo.CreateImportRun(ctx, sqlcgen.CreateImportRunParams{
		Mode:           mode,
		ConflictPolicy: policy.String(),
		StartedBy:      optUUID(adminID),
	})
	if isUniqueViolation(err) {
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
	actor := ""
	if claim.StartedBy.Valid {
		actor = uuid.UUID(claim.StartedBy.Bytes).String()
	}
	policy, perr := ParseConflictPolicy(claim.ConflictPolicy)
	if perr != nil {
		policy = s.defaultPolicy
	}

	importer, cleanup, err := s.buildImporter(ctx, policy)
	if err != nil {
		s.logger.WarnContext(ctx, "peertube import: could not open source", "run_id", claim.ID.String(), "error", err)
		s.failRun(ctx, claim.ID, "could not connect to the configured source")
		s.emitAudit(ctx, observability.ActionPeerTubeImportFinish, observability.ResultFailure, actor, "source connection failed")
		return
	}
	defer cleanup()

	// The admin path NEVER self-passes --force: an unverified version is refused
	// here and the run fails with the safe version message for a human to act on.
	version, err := importer.Preflight(ctx)
	if err != nil {
		s.failRun(ctx, claim.ID, err.Error())
		s.emitAudit(ctx, observability.ActionPeerTubeImportFinish, observability.ResultFailure, actor, "preflight refused")
		return
	}
	v32 := int32(version)
	_ = s.repo.SetImportRunVersion(ctx, sqlcgen.SetImportRunVersionParams{ID: claim.ID, SourceVersion: &v32})

	var report *Report
	if claim.Mode == "dry_run" {
		report, err = importer.Plan(ctx, version)
	} else {
		report, err = importer.Run(ctx, version, func(r *Report) { s.persistProgress(ctx, claim.ID, r) })
	}
	if err != nil {
		s.failRun(ctx, claim.ID, safeRunError(err))
		s.emitAudit(ctx, observability.ActionPeerTubeImportFinish, observability.ResultFailure, actor, "import failed")
		return
	}
	data, merr := json.Marshal(report)
	if merr != nil {
		data = []byte("{}")
	}
	if err := s.repo.CompleteImportRun(ctx, sqlcgen.CompleteImportRunParams{ID: claim.ID, Progress: data}); err != nil {
		s.logger.WarnContext(ctx, "peertube import: complete run failed", "run_id", claim.ID.String(), "error", err)
	}
	s.emitAudit(ctx, observability.ActionPeerTubeImportFinish, observability.ResultSuccess, actor, report.Summary())
}

func (s *Service) persistProgress(ctx context.Context, id uuid.UUID, r *Report) {
	data, err := json.Marshal(r)
	if err != nil {
		return
	}
	_ = s.repo.UpdateImportRunProgress(ctx, sqlcgen.UpdateImportRunProgressParams{ID: id, Progress: data})
}

func (s *Service) failRun(ctx context.Context, id uuid.UUID, msg string) {
	if err := s.repo.FailImportRun(ctx, sqlcgen.FailImportRunParams{ID: id, Error: msg}); err != nil {
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
		ID:             row.ID,
		Mode:           row.Mode,
		State:          row.State,
		ConflictPolicy: row.ConflictPolicy,
		Error:          row.Error,
		CreatedAt:      row.CreatedAt,
		UpdatedAt:      row.UpdatedAt,
	}
	if row.SourceVersion != nil {
		v := int(*row.SourceVersion)
		r.SourceVersion = &v
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

// isUniqueViolation reports whether err is a PostgreSQL unique-constraint
// violation (SQLSTATE 23505) — the single-active-run guard.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
