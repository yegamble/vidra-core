package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"vidra-core/internal/domain"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

type RunnerRepository struct {
	db *sqlx.DB
}

func NewRunnerRepository(db *sqlx.DB) *RunnerRepository {
	return &RunnerRepository{db: db}
}

const runnerSelectColumns = `id, name, description, token, status, created_by, last_seen_at, created_at, updated_at, runner_version, ip_address, capabilities`

func (r *RunnerRepository) ListRunners(ctx context.Context) ([]*domain.RemoteRunner, error) {
	rows, err := r.db.QueryxContext(ctx, `
		SELECT `+runnerSelectColumns+`
		FROM remote_runners
		ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("list runners: %w", err)
	}
	defer func() { _ = rows.Close() }()

	runners := []*domain.RemoteRunner{}
	for rows.Next() {
		runner, scanErr := scanRunner(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		runners = append(runners, runner)
	}
	return runners, rows.Err()
}

func (r *RunnerRepository) GetRunner(ctx context.Context, runnerID uuid.UUID) (*domain.RemoteRunner, error) {
	return r.getRunner(ctx, `SELECT `+runnerSelectColumns+` FROM remote_runners WHERE id = $1`, runnerID)
}

func (r *RunnerRepository) GetRunnerByToken(ctx context.Context, token string) (*domain.RemoteRunner, error) {
	return r.getRunner(ctx, `SELECT `+runnerSelectColumns+` FROM remote_runners WHERE token = $1`, token)
}

func (r *RunnerRepository) getRunner(ctx context.Context, query string, args ...any) (*domain.RemoteRunner, error) {
	rows, err := r.db.QueryxContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("get runner: %w", err)
	}
	defer func() { _ = rows.Close() }()

	if !rows.Next() {
		return nil, domain.ErrNotFound
	}
	return scanRunner(rows)
}

func scanRunner(rows *sqlx.Rows) (*domain.RemoteRunner, error) {
	var runner domain.RemoteRunner
	var capabilitiesJSON []byte
	err := rows.Scan(
		&runner.ID,
		&runner.Name,
		&runner.Description,
		&runner.Token,
		&runner.Status,
		&runner.CreatedBy,
		&runner.LastSeenAt,
		&runner.CreatedAt,
		&runner.UpdatedAt,
		&runner.RunnerVersion,
		&runner.IPAddress,
		&capabilitiesJSON,
	)
	if err != nil {
		return nil, fmt.Errorf("scan runner: %w", err)
	}
	if len(capabilitiesJSON) > 0 {
		if unmarshalErr := json.Unmarshal(capabilitiesJSON, &runner.Capabilities); unmarshalErr != nil {
			return nil, fmt.Errorf("unmarshal runner capabilities: %w", unmarshalErr)
		}
	}
	if runner.Capabilities == nil {
		runner.Capabilities = map[string]any{}
	}
	return &runner, nil
}

func (r *RunnerRepository) TouchRunner(ctx context.Context, runnerID uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE remote_runners
		SET last_seen_at = NOW(), updated_at = NOW(), status = $2
		WHERE id = $1`, runnerID, domain.RemoteRunnerStatusRegistered)
	if err != nil {
		return fmt.Errorf("touch runner: %w", err)
	}
	return nil
}

func (r *RunnerRepository) CreateRegistrationToken(ctx context.Context, createdBy *uuid.UUID, expiresAt *time.Time) (*domain.RemoteRunnerRegistrationToken, error) {
	token := &domain.RemoteRunnerRegistrationToken{
		ID:        uuid.New(),
		Token:     uuid.NewString(),
		CreatedBy: createdBy,
		ExpiresAt: expiresAt,
		CreatedAt: time.Now().UTC(),
	}

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO remote_runner_registration_tokens (id, token, created_by, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5)`,
		token.ID, token.Token, token.CreatedBy, token.ExpiresAt, token.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create runner registration token: %w", err)
	}

	return token, nil
}

func (r *RunnerRepository) ListRegistrationTokens(ctx context.Context) ([]*domain.RemoteRunnerRegistrationToken, error) {
	var tokens []*domain.RemoteRunnerRegistrationToken
	err := r.db.SelectContext(ctx, &tokens, `
		SELECT id, token, created_by, expires_at, used_at, used_by_runner_id, created_at
		FROM remote_runner_registration_tokens
		ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("list runner registration tokens: %w", err)
	}
	return tokens, nil
}

func (r *RunnerRepository) DeleteRegistrationToken(ctx context.Context, tokenID uuid.UUID) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM remote_runner_registration_tokens WHERE id = $1`, tokenID)
	if err != nil {
		return fmt.Errorf("delete runner registration token: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete runner registration token rows: %w", err)
	}
	if rows == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *RunnerRepository) RegisterRunner(ctx context.Context, input domain.RegisterRunnerInput) (*domain.RemoteRunner, error) {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin runner registration transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var token domain.RemoteRunnerRegistrationToken
	err = tx.GetContext(ctx, &token, `
		SELECT id, token, created_by, expires_at, used_at, used_by_runner_id, created_at
		FROM remote_runner_registration_tokens
		WHERE token = $1
		FOR UPDATE`, input.RegistrationToken)
	if err == sql.ErrNoRows {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load runner registration token: %w", err)
	}

	if token.UsedAt != nil {
		return nil, domain.ErrConflict
	}
	if token.ExpiresAt != nil && token.ExpiresAt.Before(time.Now().UTC()) {
		return nil, domain.ErrForbidden
	}

	capabilities := input.Capabilities
	if capabilities == nil {
		capabilities = map[string]any{}
	}
	capabilitiesJSON, err := json.Marshal(capabilities)
	if err != nil {
		return nil, fmt.Errorf("marshal runner capabilities: %w", err)
	}

	runner := &domain.RemoteRunner{
		ID:            uuid.New(),
		Name:          input.Name,
		Description:   input.Description,
		Token:         uuid.NewString(),
		Status:        domain.RemoteRunnerStatusRegistered,
		CreatedBy:     token.CreatedBy,
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
		RunnerVersion: input.RunnerVersion,
		IPAddress:     input.IPAddress,
		Capabilities:  capabilities,
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO remote_runners (id, name, description, token, status, created_by, last_seen_at, created_at, updated_at, runner_version, ip_address, capabilities)
		VALUES ($1, $2, $3, $4, $5, $6, NOW(), $7, $8, $9, $10, $11)`,
		runner.ID, runner.Name, runner.Description, runner.Token, runner.Status, runner.CreatedBy, runner.CreatedAt, runner.UpdatedAt,
		runner.RunnerVersion, runner.IPAddress, capabilitiesJSON,
	)
	if err != nil {
		return nil, fmt.Errorf("create runner: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE remote_runner_registration_tokens
		SET used_at = NOW(), used_by_runner_id = $2
		WHERE id = $1`, token.ID, runner.ID)
	if err != nil {
		return nil, fmt.Errorf("mark runner registration token used: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit runner registration transaction: %w", err)
	}

	return runner, nil
}

func (r *RunnerRepository) UnregisterRunnerByToken(ctx context.Context, token string) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE remote_runners
		SET status = $2, updated_at = NOW()
		WHERE token = $1`, token, domain.RemoteRunnerStatusUnregistered)
	if err != nil {
		return fmt.Errorf("unregister runner: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("unregister runner rows: %w", err)
	}
	if rows == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *RunnerRepository) DeleteRunner(ctx context.Context, runnerID uuid.UUID) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM remote_runners WHERE id = $1`, runnerID)
	if err != nil {
		return fmt.Errorf("delete runner: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete runner rows: %w", err)
	}
	if rows == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *RunnerRepository) CreateAssignment(ctx context.Context, runnerID uuid.UUID, encodingJobID string) (*domain.RemoteRunnerJobAssignment, error) {
	assignment := &domain.RemoteRunnerJobAssignment{
		ID:          uuid.New(),
		RunnerID:    runnerID,
		EncodingJob: encodingJobID,
		State:       domain.RemoteRunnerJobStateAssigned,
		Progress:    0,
		Metadata:    map[string]any{},
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}

	metadataJSON, err := json.Marshal(assignment.Metadata)
	if err != nil {
		return nil, fmt.Errorf("marshal assignment metadata: %w", err)
	}

	_, err = r.db.ExecContext(ctx, `
		INSERT INTO remote_runner_job_assignments (
			id, runner_id, encoding_job_id, state, progress, last_error, metadata, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, '', $6, $7, $8)`,
		assignment.ID, assignment.RunnerID, assignment.EncodingJob, assignment.State, assignment.Progress, metadataJSON, assignment.CreatedAt, assignment.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create runner assignment: %w", err)
	}

	return assignment, nil
}

func (r *RunnerRepository) GetAssignmentByJob(ctx context.Context, jobID string) (*domain.RemoteRunnerJobAssignment, error) {
	return r.getAssignment(ctx, `SELECT id, runner_id, encoding_job_id, state, progress, last_error, metadata, accepted_at, completed_at, created_at, updated_at FROM remote_runner_job_assignments WHERE encoding_job_id = $1`, jobID)
}

func (r *RunnerRepository) GetAssignmentForRunnerAndJob(ctx context.Context, runnerID uuid.UUID, jobID string) (*domain.RemoteRunnerJobAssignment, error) {
	return r.getAssignment(ctx, `SELECT id, runner_id, encoding_job_id, state, progress, last_error, metadata, accepted_at, completed_at, created_at, updated_at FROM remote_runner_job_assignments WHERE runner_id = $1 AND encoding_job_id = $2`, runnerID, jobID)
}

// ListAssignments returns paginated/filtered job assignments along with the
// total count matching the same filter (without pagination). Zero opts returns
// everything ordered by created_at ASC.
func (r *RunnerRepository) ListAssignments(ctx context.Context, opts domain.ListAssignmentsOpts) ([]*domain.RemoteRunnerJobAssignment, int, error) {
	whereSQL, args := buildAssignmentFilter(opts)

	var total int
	if err := r.db.GetContext(ctx, &total,
		`SELECT COUNT(*) FROM remote_runner_job_assignments`+whereSQL, args...); err != nil {
		return nil, 0, fmt.Errorf("count runner assignments: %w", err)
	}

	query := `SELECT id, runner_id, encoding_job_id, state, progress, last_error, metadata, accepted_at, completed_at, created_at, updated_at
		FROM remote_runner_job_assignments` + whereSQL + ` ORDER BY created_at DESC`

	if opts.Count > 0 {
		query += fmt.Sprintf(` LIMIT $%d OFFSET $%d`, len(args)+1, len(args)+2)
		args = append(args, opts.Count, opts.Start)
	}

	rows, err := r.db.QueryxContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list runner assignments: %w", err)
	}
	defer func() { _ = rows.Close() }()

	assignments := []*domain.RemoteRunnerJobAssignment{}
	for rows.Next() {
		assignment, err := scanRunnerAssignment(rows)
		if err != nil {
			return nil, 0, err
		}
		assignments = append(assignments, assignment)
	}
	return assignments, total, rows.Err()
}

// buildAssignmentFilter returns the WHERE clause (with leading space) and the
// positional args for a ListAssignmentsOpts filter.
func buildAssignmentFilter(opts domain.ListAssignmentsOpts) (string, []any) {
	clauses := []string{}
	args := []any{}

	if opts.RunnerID != nil {
		args = append(args, *opts.RunnerID)
		clauses = append(clauses, fmt.Sprintf("runner_id = $%d", len(args)))
	}
	if len(opts.State) > 0 {
		placeholders := make([]string, 0, len(opts.State))
		for _, s := range opts.State {
			args = append(args, string(s))
			placeholders = append(placeholders, fmt.Sprintf("$%d", len(args)))
		}
		clauses = append(clauses, "state IN ("+strings.Join(placeholders, ",")+")")
	}
	if len(clauses) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

// HealthMetrics aggregates runner-subsystem signals for the admin dashboard.
// Online = last_seen_at within the last 5 minutes. Failed24h covers
// state IN (failed, aborted) within the last 24 hours. AvgCompletionMs is the
// mean (completed_at - accepted_at) in milliseconds for assignments completed
// in the last 24 hours; 0 when no completions in that window.
func (r *RunnerRepository) HealthMetrics(ctx context.Context) (*domain.RemoteRunnerHealth, error) {
	var h domain.RemoteRunnerHealth
	// avg_completion_ms is computed as a Postgres NUMERIC (AVG over EXTRACT
	// EPOCH * 1000) which lib/pq returns as a string-encoded []byte. We scan
	// into a float64 then truncate to int64 — sub-millisecond precision is
	// not interesting for an admin dashboard.
	var avgCompletionMs float64
	err := r.db.QueryRowxContext(ctx, `
		WITH runner_counts AS (
			SELECT
				COUNT(*) AS total,
				COUNT(*) FILTER (WHERE last_seen_at >= NOW() - INTERVAL '5 minutes') AS online
			FROM remote_runners
		),
		job_counts AS (
			SELECT
				COUNT(*) FILTER (WHERE state IN ('accepted','running')) AS in_flight,
				COUNT(*) FILTER (WHERE state IN ('failed','aborted') AND updated_at >= NOW() - INTERVAL '24 hours') AS failed_24h,
				COALESCE(AVG(EXTRACT(EPOCH FROM (completed_at - accepted_at)) * 1000) FILTER (WHERE state = 'completed' AND completed_at >= NOW() - INTERVAL '24 hours' AND accepted_at IS NOT NULL), 0)::float8 AS avg_completion_ms
			FROM remote_runner_job_assignments
		)
		SELECT
			runner_counts.total,
			runner_counts.online,
			runner_counts.total - runner_counts.online AS offline,
			job_counts.in_flight,
			job_counts.failed_24h,
			job_counts.avg_completion_ms
		FROM runner_counts, job_counts`).Scan(
		&h.TotalRunners,
		&h.OnlineRunners,
		&h.OfflineRunners,
		&h.JobsInFlight,
		&h.JobsFailed24h,
		&avgCompletionMs,
	)
	if err != nil {
		return nil, fmt.Errorf("runner health metrics: %w", err)
	}
	h.AvgCompletionMs = int64(avgCompletionMs)
	return &h, nil
}

func (r *RunnerRepository) UpdateAssignment(ctx context.Context, assignment *domain.RemoteRunnerJobAssignment) error {
	metadataJSON, err := json.Marshal(assignment.Metadata)
	if err != nil {
		return fmt.Errorf("marshal assignment metadata: %w", err)
	}

	result, err := r.db.ExecContext(ctx, `
		UPDATE remote_runner_job_assignments
		SET state = $2, progress = $3, last_error = $4, metadata = $5,
			accepted_at = $6, completed_at = $7, updated_at = NOW()
		WHERE id = $1`,
		assignment.ID,
		assignment.State,
		assignment.Progress,
		assignment.LastError,
		metadataJSON,
		assignment.AcceptedAt,
		assignment.CompletedAt,
	)
	if err != nil {
		return fmt.Errorf("update runner assignment: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("update runner assignment rows: %w", err)
	}
	if rows == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *RunnerRepository) RecordFileReceipt(ctx context.Context, assignment *domain.RemoteRunnerJobAssignment, fileKey string, details map[string]any) error {
	metadata := assignment.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}

	files, _ := metadata["files"].(map[string]any)
	if files == nil {
		files = map[string]any{}
	}
	files[fileKey] = details
	metadata["files"] = files
	assignment.Metadata = metadata

	return r.UpdateAssignment(ctx, assignment)
}

func (r *RunnerRepository) DeleteAssignment(ctx context.Context, jobID string) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM remote_runner_job_assignments WHERE encoding_job_id = $1`, jobID)
	if err != nil {
		return fmt.Errorf("delete runner assignment: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete runner assignment rows: %w", err)
	}
	if rows == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *RunnerRepository) getAssignment(ctx context.Context, query string, args ...any) (*domain.RemoteRunnerJobAssignment, error) {
	rows, err := r.db.QueryxContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("get runner assignment: %w", err)
	}
	defer func() { _ = rows.Close() }()

	if !rows.Next() {
		return nil, domain.ErrNotFound
	}

	return scanRunnerAssignment(rows)
}

func scanRunnerAssignment(rows *sqlx.Rows) (*domain.RemoteRunnerJobAssignment, error) {
	var assignment domain.RemoteRunnerJobAssignment
	var metadataJSON []byte
	err := rows.Scan(
		&assignment.ID,
		&assignment.RunnerID,
		&assignment.EncodingJob,
		&assignment.State,
		&assignment.Progress,
		&assignment.LastError,
		&metadataJSON,
		&assignment.AcceptedAt,
		&assignment.CompletedAt,
		&assignment.CreatedAt,
		&assignment.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan runner assignment: %w", err)
	}

	if len(metadataJSON) > 0 {
		if err := json.Unmarshal(metadataJSON, &assignment.Metadata); err != nil {
			return nil, fmt.Errorf("unmarshal runner assignment metadata: %w", err)
		}
	}
	if assignment.Metadata == nil {
		assignment.Metadata = map[string]any{}
	}

	return &assignment, nil
}
