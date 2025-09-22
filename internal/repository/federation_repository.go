package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"athena/internal/domain"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

type FederationRepository struct {
	db *sqlx.DB
}

func NewFederationRepository(db *sqlx.DB) *FederationRepository {
	return &FederationRepository{db: db}
}

// Jobs
func (r *FederationRepository) EnqueueJob(ctx context.Context, jobType string, payload any, runAt time.Time) (string, error) {
	b, _ := json.Marshal(payload)
	id := uuid.New().String()
	q := `INSERT INTO federation_jobs (id, job_type, payload, status, next_attempt_at)
          VALUES ($1, $2, $3, 'pending', $4)`
	if _, err := r.db.ExecContext(ctx, q, id, jobType, b, runAt); err != nil {
		return "", fmt.Errorf("enqueue job: %w", err)
	}
	return id, nil
}

func (r *FederationRepository) GetNextJob(ctx context.Context) (*domain.FederationJob, error) {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var job domain.FederationJob
	q := `SELECT id, job_type, payload, status, attempts, max_attempts, next_attempt_at, last_error, created_at, updated_at
          FROM federation_jobs
          WHERE status = 'pending' AND next_attempt_at <= CURRENT_TIMESTAMP
          ORDER BY created_at ASC
          LIMIT 1
          FOR UPDATE SKIP LOCKED`
	if err = tx.GetContext(ctx, &job, q); err != nil {
		if err == sql.ErrNoRows {
			_ = tx.Commit()
			return nil, nil
		}
		return nil, err
	}
	// Mark processing + increment attempts
	uq := `UPDATE federation_jobs SET status='processing', attempts = attempts + 1, updated_at = CURRENT_TIMESTAMP WHERE id = $1`
	if _, err = tx.ExecContext(ctx, uq, job.ID); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	job.Status = domain.FedJobProcessing
	job.Attempts++
	return &job, nil
}

func (r *FederationRepository) CompleteJob(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE federation_jobs SET status='completed', updated_at=CURRENT_TIMESTAMP WHERE id=$1`, id)
	return err
}

func (r *FederationRepository) RescheduleJob(ctx context.Context, id string, lastErr string, backoff time.Duration) error {
	// If attempts >= max_attempts, fail permanently
	q := `UPDATE federation_jobs
          SET status = CASE WHEN attempts >= max_attempts THEN 'failed' ELSE 'pending' END,
              last_error = $2,
              next_attempt_at = CASE WHEN attempts >= max_attempts THEN next_attempt_at ELSE CURRENT_TIMESTAMP + ($3::interval) END,
              updated_at = CURRENT_TIMESTAMP
          WHERE id = $1`
	_, err := r.db.ExecContext(ctx, q, id, lastErr, fmt.Sprintf("%fs", backoff.Seconds()))
	return err
}

// Posts
func (r *FederationRepository) UpsertPost(ctx context.Context, p *domain.FederatedPost) error {
	q := `INSERT INTO federated_posts (actor_did, actor_handle, uri, cid, text, created_at, indexed_at, embed_url, embed_title, embed_description, labels, raw)
          VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
          ON CONFLICT (uri) DO UPDATE SET
            actor_did=EXCLUDED.actor_did,
            actor_handle=EXCLUDED.actor_handle,
            cid=EXCLUDED.cid,
            text=EXCLUDED.text,
            created_at=EXCLUDED.created_at,
            indexed_at=EXCLUDED.indexed_at,
            embed_url=EXCLUDED.embed_url,
            embed_title=EXCLUDED.embed_title,
            embed_description=EXCLUDED.embed_description,
            labels=EXCLUDED.labels,
            raw=EXCLUDED.raw,
            updated_at=CURRENT_TIMESTAMP`
	_, err := r.db.ExecContext(ctx, q,
		p.ActorDID, p.ActorHandle, p.URI, p.CID, p.Text, p.CreatedAt, p.IndexedAt, p.EmbedURL, p.EmbedTitle, p.EmbedDescription, p.Labels, p.Raw,
	)
	return err
}

func (r *FederationRepository) ListTimeline(ctx context.Context, limit, offset int) ([]domain.FederatedPost, int, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	var total int
	if err := r.db.GetContext(ctx, &total, `SELECT COUNT(*) FROM federated_posts`); err != nil {
		return nil, 0, err
	}
	rows, err := r.db.QueryxContext(ctx, `SELECT id, actor_did, actor_handle, uri, cid, text, created_at, indexed_at, embed_url, embed_title, embed_description, labels, raw, inserted_at, updated_at
                                          FROM federated_posts
                                          ORDER BY COALESCE(indexed_at, inserted_at) DESC
                                          LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	var posts []domain.FederatedPost
	for rows.Next() {
		var p domain.FederatedPost
		if err := rows.StructScan(&p); err != nil {
			return nil, 0, err
		}
		posts = append(posts, p)
	}
	return posts, total, nil
}

// Admin/job views
func (r *FederationRepository) GetJob(ctx context.Context, id string) (*domain.FederationJob, error) {
	var j domain.FederationJob
	err := r.db.GetContext(ctx, &j, `SELECT id, job_type, payload, status, attempts, max_attempts, next_attempt_at, last_error, created_at, updated_at FROM federation_jobs WHERE id=$1`, id)
	if err == sql.ErrNoRows {
		return nil, domain.NewDomainError("NOT_FOUND", "Job not found")
	}
	return &j, err
}

func (r *FederationRepository) ListJobs(ctx context.Context, status string, limit, offset int) ([]domain.FederationJob, int, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	where := "1=1"
	args := []interface{}{}
	if status != "" {
		where = "status = $1"
		args = append(args, status)
	}
	var total int
	qCount := fmt.Sprintf("SELECT COUNT(*) FROM federation_jobs WHERE %s", where)
	if err := r.db.GetContext(ctx, &total, qCount, args...); err != nil {
		return nil, 0, err
	}
	q := fmt.Sprintf(`SELECT id, job_type, payload, status, attempts, max_attempts, next_attempt_at, last_error, created_at, updated_at
                      FROM federation_jobs WHERE %s ORDER BY created_at DESC LIMIT $%d OFFSET $%d`, where, len(args)+1, len(args)+2)
	args = append(args, limit, offset)
	rows, err := r.db.QueryxContext(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	var jobs []domain.FederationJob
	for rows.Next() {
		var j domain.FederationJob
		if err := rows.StructScan(&j); err != nil {
			return nil, 0, err
		}
		jobs = append(jobs, j)
	}
	return jobs, total, nil
}

func (r *FederationRepository) RetryJob(ctx context.Context, id string, when time.Time) error {
	_, err := r.db.ExecContext(ctx, `UPDATE federation_jobs SET status='pending', next_attempt_at=$2, updated_at=CURRENT_TIMESTAMP WHERE id=$1`, id, when)
	return err
}

func (r *FederationRepository) DeleteJob(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM federation_jobs WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.NewDomainError("NOT_FOUND", "Job not found")
	}
	return nil
}

// ---- Actors (ingestion sources) ----

type FederationActor struct {
	Actor            string         `db:"actor"`
	Enabled          bool           `db:"enabled"`
	Cursor           sql.NullString `db:"cursor"`
	NextAt           sql.NullTime   `db:"next_at"`
	Attempts         int            `db:"attempts"`
	RateLimitSeconds int            `db:"rate_limit_seconds"`
	LastError        sql.NullString `db:"last_error"`
	CreatedAt        time.Time      `db:"created_at"`
	UpdatedAt        time.Time      `db:"updated_at"`
}

func (r *FederationRepository) UpsertActor(ctx context.Context, actor string, enabled bool, rateLimitSeconds int) error {
	q := `INSERT INTO federation_actors (actor, enabled, rate_limit_seconds)
          VALUES ($1, $2, $3)
          ON CONFLICT (actor) DO UPDATE SET enabled=EXCLUDED.enabled, rate_limit_seconds=EXCLUDED.rate_limit_seconds`
	_, err := r.db.ExecContext(ctx, q, actor, enabled, rateLimitSeconds)
	return err
}

func (r *FederationRepository) ListActors(ctx context.Context, limit, offset int) ([]FederationActor, int, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	var total int
	if err := r.db.GetContext(ctx, &total, `SELECT COUNT(*) FROM federation_actors`); err != nil {
		return nil, 0, err
	}
	rows, err := r.db.QueryxContext(ctx, `SELECT actor, enabled, cursor, next_at, attempts, rate_limit_seconds, last_error, created_at, updated_at
                                           FROM federation_actors ORDER BY actor ASC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	var out []FederationActor
	for rows.Next() {
		var a FederationActor
		if err := rows.StructScan(&a); err != nil {
			return nil, 0, err
		}
		out = append(out, a)
	}
	return out, total, nil
}

func (r *FederationRepository) GetActor(ctx context.Context, actor string) (*FederationActor, error) {
	var a FederationActor
	err := r.db.GetContext(ctx, &a, `SELECT actor, enabled, cursor, next_at, attempts, rate_limit_seconds, last_error, created_at, updated_at FROM federation_actors WHERE actor=$1`, actor)
	if err == sql.ErrNoRows {
		return nil, domain.NewDomainError("NOT_FOUND", "Actor not found")
	}
	return &a, err
}

func (r *FederationRepository) UpdateActor(ctx context.Context, actor string, enabled *bool, rateLimitSeconds *int, cursor *string, nextAt *time.Time, attempts *int) error {
	set := []string{}
	args := []any{}
	idx := 1
	if enabled != nil {
		set = append(set, fmt.Sprintf("enabled=$%d", idx))
		args = append(args, *enabled)
		idx++
	}
	if rateLimitSeconds != nil {
		set = append(set, fmt.Sprintf("rate_limit_seconds=$%d", idx))
		args = append(args, *rateLimitSeconds)
		idx++
	}
	if cursor != nil {
		set = append(set, fmt.Sprintf("cursor=$%d", idx))
		args = append(args, sql.NullString{String: *cursor, Valid: *cursor != ""})
		idx++
	}
	if nextAt != nil {
		set = append(set, fmt.Sprintf("next_at=$%d", idx))
		args = append(args, sql.NullTime{Time: *nextAt, Valid: true})
		idx++
	}
	if attempts != nil {
		set = append(set, fmt.Sprintf("attempts=$%d", idx))
		args = append(args, *attempts)
		idx++
	}
	if len(set) == 0 {
		return nil
	}
	q := fmt.Sprintf("UPDATE federation_actors SET %s WHERE actor=$%d", strings.Join(set, ","), idx)
	args = append(args, actor)
	_, err := r.db.ExecContext(ctx, q, args...)
	return err
}

func (r *FederationRepository) DeleteActor(ctx context.Context, actor string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM federation_actors WHERE actor=$1`, actor)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.NewDomainError("NOT_FOUND", "Actor not found")
	}
	return nil
}

// Simplified helpers for use by services without exposing DB structs
func (r *FederationRepository) ListEnabledActors(ctx context.Context) ([]string, error) {
	rows, err := r.db.QueryxContext(ctx, `SELECT actor FROM federation_actors WHERE enabled = true ORDER BY actor ASC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

func (r *FederationRepository) GetActorState(ctx context.Context, actor string) (cursor string, nextAt sql.NullTime, attempts int, rateLimitSeconds int, err error) {
	var c sql.NullString
	err = r.db.QueryRowxContext(ctx, `SELECT cursor, next_at, attempts, rate_limit_seconds FROM federation_actors WHERE actor=$1`, actor).Scan(&c, &nextAt, &attempts, &rateLimitSeconds)
	if err == sql.ErrNoRows {
		return "", sql.NullTime{}, 0, 60, nil
	}
	if err != nil {
		return "", sql.NullTime{}, 0, 60, err
	}
	if c.Valid {
		cursor = c.String
	}
	return
}

func (r *FederationRepository) GetActorStateSimple(ctx context.Context, actor string) (cursor string, nextAt *time.Time, attempts int, rateLimitSeconds int, err error) {
	c, n, a, rl, err := r.GetActorState(ctx, actor)
	if err != nil {
		return "", nil, 0, 0, err
	}
	var t *time.Time
	if n.Valid {
		t = &n.Time
	}
	return c, t, a, rl, nil
}

func (r *FederationRepository) SetActorCursor(ctx context.Context, actor string, cursor string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE federation_actors SET cursor=$2 WHERE actor=$1`, actor, sql.NullString{String: cursor, Valid: cursor != ""})
	return err
}

func (r *FederationRepository) SetActorNextAt(ctx context.Context, actor string, t time.Time) error {
	_, err := r.db.ExecContext(ctx, `UPDATE federation_actors SET next_at=$2 WHERE actor=$1`, actor, sql.NullTime{Time: t, Valid: true})
	return err
}

func (r *FederationRepository) SetActorAttempts(ctx context.Context, actor string, n int) error {
	_, err := r.db.ExecContext(ctx, `UPDATE federation_actors SET attempts=$2 WHERE actor=$1`, actor, n)
	return err
}
