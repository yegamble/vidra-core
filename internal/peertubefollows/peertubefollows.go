// Package peertubefollows follows, in the background, the accounts a PeerTube
// user-export lists in following.json. It owns only the queue and its
// retry/skip policy: every follow goes through federation.FollowRemoteChannel,
// the same path as POST /me/remote-follows, so WebFinger, the SSRF guard and
// the actor cache are not duplicated here.
package peertubefollows

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/federation"
	"github.com/vidra/vidra-core/internal/retry"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// MaxTargets bounds the follows one import may queue; the rest are counted by
// the caller as over-cap.
const MaxTargets = 1000

// ErrNotFound: no such import for this user (also for another user's id).
var ErrNotFound = errors.New("peertubefollows: import not found")

// Target states (the CHECK constraint of migration 0180).
const (
	stateDone       = "done"
	stateFailed     = "failed"
	skippedFed      = "skipped_federation"
	skippedBlocked  = "skipped_blocked"
	skippedInvalid  = "skipped_invalid"
	statePending    = "pending"
	defaultBatch    = 10
	defaultLease    = 5 * time.Minute
	perTargetBudget = 45 * time.Second
)

type Repo interface {
	CreatePeerTubeFollowImport(ctx context.Context, userID uuid.UUID) (uuid.UUID, error)
	AddPeerTubeFollowImportTargets(ctx context.Context, arg sqlcgen.AddPeerTubeFollowImportTargetsParams) error
	ClaimDuePeerTubeFollowTargets(ctx context.Context, arg sqlcgen.ClaimDuePeerTubeFollowTargetsParams) ([]sqlcgen.ClaimDuePeerTubeFollowTargetsRow, error)
	FinishPeerTubeFollowTarget(ctx context.Context, arg sqlcgen.FinishPeerTubeFollowTargetParams) error
	ReschedulePeerTubeFollowTarget(ctx context.Context, arg sqlcgen.ReschedulePeerTubeFollowTargetParams) error
	CountPeerTubeFollowImportTargets(ctx context.Context, arg sqlcgen.CountPeerTubeFollowImportTargetsParams) ([]sqlcgen.CountPeerTubeFollowImportTargetsRow, error)
	GetPeerTubeFollowImport(ctx context.Context, arg sqlcgen.GetPeerTubeFollowImportParams) (sqlcgen.GetPeerTubeFollowImportRow, error)
}

// Follower is federation.Service's outbound follow.
type Follower interface {
	FollowRemoteChannel(ctx context.Context, userID uuid.UUID, target string) (federation.RemoteFollow, error)
}

type Service struct {
	Repo     Repo
	Follower Follower
	// Enabled reports whether federation is on right now; nil means on.
	Enabled     func() bool
	MaxAttempts int           // default 5
	BackoffBase time.Duration // default 1m
	BackoffMax  time.Duration // default 1h
	Now         func() time.Time
}

func (s *Service) enabled() bool { return s.Enabled == nil || s.Enabled() }
func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Queued is what Enqueue did with the offered targets.
type Queued struct {
	JobID             *uuid.UUID // nil when nothing was queued
	Queued            int
	SkippedFederation int
}

// Enqueue normalises (trim, strip a leading @, lower-case, de-duplicate) and
// queues up to MaxTargets targets for userID. With federation disabled nothing
// is queued and every target is counted skipped_federation: no outbound fetch
// would be made anyway.
func (s *Service) Enqueue(ctx context.Context, userID uuid.UUID, targets []string) (Queued, error) {
	seen := map[string]bool{}
	var list []string
	for _, t := range targets {
		t = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(t), "@"))
		if t == "" || len(t) > 512 || seen[t] {
			continue
		}
		seen[t] = true
		if len(list) < MaxTargets {
			list = append(list, t)
		}
	}
	if len(list) == 0 {
		return Queued{}, nil
	}
	if !s.enabled() {
		return Queued{SkippedFederation: len(list)}, nil
	}
	id, err := s.Repo.CreatePeerTubeFollowImport(ctx, userID)
	if err != nil {
		return Queued{}, err
	}
	if err := s.Repo.AddPeerTubeFollowImportTargets(ctx, sqlcgen.AddPeerTubeFollowImportTargetsParams{
		ImportID: id, UserID: userID, Column3: list,
	}); err != nil {
		return Queued{}, err
	}
	return Queued{JobID: &id, Queued: len(list)}, nil
}

// Drain claims one batch of due targets and processes them. It returns how
// many targets reached a final state or were rescheduled.
func (s *Service) Drain(ctx context.Context) (int, error) {
	rows, err := s.Repo.ClaimDuePeerTubeFollowTargets(ctx, sqlcgen.ClaimDuePeerTubeFollowTargetsParams{
		LeaseSeconds: defaultLease.Seconds(), ClaimLimit: defaultBatch,
	})
	if err != nil {
		return 0, err
	}
	for _, r := range rows {
		if err := s.process(ctx, r); err != nil {
			return 0, err
		}
	}
	return len(rows), nil
}

func (s *Service) process(ctx context.Context, r sqlcgen.ClaimDuePeerTubeFollowTargetsRow) error {
	finish := func(state, reason string) error {
		return s.Repo.FinishPeerTubeFollowTarget(ctx, sqlcgen.FinishPeerTubeFollowTargetParams{ID: r.ID, State: state, LastError: reason})
	}
	if !s.enabled() {
		return finish(skippedFed, "")
	}
	fctx, cancel := context.WithTimeout(ctx, perTargetBudget)
	defer cancel()
	_, err := s.Follower.FollowRemoteChannel(fctx, r.UserID, r.Target)
	switch {
	case err == nil:
		return finish(stateDone, "")
	case errors.Is(err, federation.ErrBadFollowTarget), errors.Is(err, federation.ErrLocalFollowTarget):
		return finish(skippedInvalid, "")
	case errors.Is(err, federation.ErrBlockedFollowTarget):
		return finish(skippedBlocked, "")
	case errors.Is(err, federation.ErrNotFound):
		return finish(stateFailed, "user_missing")
	case ctx.Err() != nil:
		return ctx.Err() // shutting down: the lease returns the target
	}
	// last_error holds a fixed class, never the error text: it can echo the
	// remote host's response.
	class := "internal"
	if errors.Is(err, federation.ErrRemoteUnresolvable) {
		class = "unresolvable"
	}
	max := s.MaxAttempts
	if max <= 0 {
		max = 5
	}
	if int(r.Attempts)+1 >= max {
		return finish(stateFailed, class)
	}
	base, ceil := s.BackoffBase, s.BackoffMax
	if base <= 0 {
		base = time.Minute
	}
	if ceil <= 0 {
		ceil = time.Hour
	}
	return s.Repo.ReschedulePeerTubeFollowTarget(ctx, sqlcgen.ReschedulePeerTubeFollowTargetParams{
		ID: r.ID, NextAttemptAt: s.now().Add(retry.Backoff(int(r.Attempts)+1, base, ceil)), LastError: class,
	})
}

// Status is the per-state tally of one import. Total is their sum.
type Status struct {
	ID                uuid.UUID `json:"id"`
	CreatedAt         time.Time `json:"created_at"`
	Total             int       `json:"total"`
	Pending           int       `json:"pending"`
	Done              int       `json:"done"`
	Failed            int       `json:"failed"`
	SkippedFederation int       `json:"skipped_federation"`
	SkippedBlocked    int       `json:"skipped_blocked"`
	SkippedInvalid    int       `json:"skipped_invalid"`
}

// Status reports on one import owned by userID; anything else is ErrNotFound.
func (s *Service) Status(ctx context.Context, userID, id uuid.UUID) (Status, error) {
	j, err := s.Repo.GetPeerTubeFollowImport(ctx, sqlcgen.GetPeerTubeFollowImportParams{ID: id, UserID: userID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Status{}, ErrNotFound
		}
		return Status{}, err
	}
	counts, err := s.Repo.CountPeerTubeFollowImportTargets(ctx, sqlcgen.CountPeerTubeFollowImportTargetsParams{ID: id, UserID: userID})
	if err != nil {
		return Status{}, err
	}
	st := Status{ID: j.ID, CreatedAt: j.CreatedAt}
	for _, c := range counts {
		n := int(c.N)
		st.Total += n
		switch c.State {
		case statePending:
			st.Pending += n
		case stateDone:
			st.Done += n
		case stateFailed:
			st.Failed += n
		case skippedFed:
			st.SkippedFederation += n
		case skippedBlocked:
			st.SkippedBlocked += n
		case skippedInvalid:
			st.SkippedInvalid += n
		}
	}
	return st, nil
}
