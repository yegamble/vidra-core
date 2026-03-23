package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"vidra-core/internal/domain"
	"vidra-core/internal/port"
)

type serverFollowingRepository struct {
	db *sqlx.DB
}

// NewServerFollowingRepository creates a new ServerFollowingRepository.
func NewServerFollowingRepository(db *sqlx.DB) port.ServerFollowingRepository {
	return &serverFollowingRepository{db: db}
}

func (r *serverFollowingRepository) ListFollowers(ctx context.Context) ([]*domain.ServerFollowing, error) {
	var rows []*domain.ServerFollowing
	err := r.db.SelectContext(ctx, &rows,
		`SELECT id, host, state, follower, created_at FROM server_following WHERE follower = true ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list followers: %w", err)
	}
	return rows, nil
}

func (r *serverFollowingRepository) ListFollowing(ctx context.Context) ([]*domain.ServerFollowing, error) {
	var rows []*domain.ServerFollowing
	err := r.db.SelectContext(ctx, &rows,
		`SELECT id, host, state, follower, created_at FROM server_following WHERE follower = false ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list following: %w", err)
	}
	return rows, nil
}

func (r *serverFollowingRepository) Follow(ctx context.Context, host string) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO server_following (id, host, state, follower, created_at)
		 VALUES ($1, $2, $3, false, $4)
		 ON CONFLICT (host, follower) DO UPDATE SET state = $3`,
		uuid.NewString(), host, domain.ServerFollowingStatePending, time.Now())
	if err != nil {
		return fmt.Errorf("follow instance: %w", err)
	}
	return nil
}

func (r *serverFollowingRepository) Unfollow(ctx context.Context, host string) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM server_following WHERE host = $1 AND follower = false`, host)
	if err != nil {
		return fmt.Errorf("unfollow instance: %w", err)
	}
	return nil
}

func (r *serverFollowingRepository) SetFollowerState(ctx context.Context, host string, state domain.ServerFollowingState) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE server_following SET state = $1 WHERE host = $2 AND follower = true`, state, host)
	if err != nil {
		return fmt.Errorf("set follower state: %w", err)
	}
	return nil
}

func (r *serverFollowingRepository) DeleteFollower(ctx context.Context, host string) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM server_following WHERE host = $1 AND follower = true`, host)
	if err != nil {
		return fmt.Errorf("delete follower: %w", err)
	}
	return nil
}
