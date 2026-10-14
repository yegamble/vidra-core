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
	"sync"
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
	stateDone      = "done"
	stateFailed    = "failed"
	skippedFed     = "skipped_federation"
	skippedInvalid = "skipped_invalid"
	statePending   = "pending"
	// MaxPerHost bounds the targets one import may aim at a single host, so an
	// archive cannot turn the worker into a fetch-flood against one server.
	MaxPerHost = 20
	// Claim shape: a batch of defaultBatch rows, at most perUserClaim per user,
	// run on workers goroutines with a per-target budget. The lease must outlast
	// the slowest batch: ceil(batch/workers) waves * perTargetBudget (= 90 s).
	defaultBatch    = 8
	perUserClaim    = 2
	workers         = 4
	defaultLease    = 5 * time.Minute
	perTargetBudget = 45 * time.Second
	// Retention for finished imports.
	Retention = 30 * 24 * time.Hour
)

type Repo interface {
	CreatePeerTubeFollowImport(ctx context.Context, userID uuid.UUID) (uuid.UUID, error)
	AddPeerTubeFollowImportTargets(ctx context.Context, arg sqlcgen.AddPeerTubeFollowImportTargetsParams) (int64, error)
	CountPendingPeerTubeFollowTargets(ctx context.Context, userID uuid.UUID) (int64, error)
	ListFollowedRemoteHandles(ctx context.Context, userID uuid.UUID) ([]string, error)
	PrunePeerTubeFollowImports(ctx context.Context, cutoff time.Time) (int64, error)
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

// Enqueue accepts name@host targets only (a URL or anything else is skipped),
// canonicalises them (lower-case, host normalised as the block list sees it),
// and queues up to MaxTargets for userID: at most MaxPerHost per host, none the
// user already follows or already has pending, and never more than MaxTargets
// pending in total for the user. The caller counts whatever was not queued.
// With federation disabled nothing is queued and every target is counted
// skipped_federation: no outbound fetch would be made anyway.
func (s *Service) Enqueue(ctx context.Context, userID uuid.UUID, targets []string) (Queued, error) {
	seen := map[string]bool{}
	perHost := map[string]int{}
	var list []string
	for _, t := range targets {
		t = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(t), "@"))
		name, host, ok := strings.Cut(t, "@")
		if !ok || name == "" || len(t) > 512 || strings.ContainsAny(name, "/:?#\\ ") || strings.Contains(host, "@") {
			continue
		}
		host, ok = federation.NormalizeFollowHost(host)
		if !ok {
			continue
		}
		t = name + "@" + host
		if seen[t] || perHost[host] >= MaxPerHost || len(list) >= MaxTargets {
			continue
		}
		seen[t] = true
		perHost[host]++
		list = append(list, t)
	}
	if len(list) == 0 {
		return Queued{}, nil
	}
	if !s.enabled() {
		return Queued{SkippedFederation: len(list)}, nil
	}
	followed, err := s.Repo.ListFollowedRemoteHandles(ctx, userID)
	if err != nil {
		return Queued{}, err
	}
	have := make(map[string]bool, len(followed))
	for _, h := range followed {
		have[h] = true
	}
	pending, err := s.Repo.CountPendingPeerTubeFollowTargets(ctx, userID)
	if err != nil {
		return Queued{}, err
	}
	room := int64(MaxTargets) - pending
	fresh := list[:0:0]
	for _, t := range list {
		if !have[t] && int64(len(fresh)) < room {
			fresh = append(fresh, t)
		}
	}
	if len(fresh) == 0 {
		return Queued{}, nil
	}
	id, err := s.Repo.CreatePeerTubeFollowImport(ctx, userID)
	if err != nil {
		return Queued{}, err
	}
	n, err := s.Repo.AddPeerTubeFollowImportTargets(ctx, sqlcgen.AddPeerTubeFollowImportTargetsParams{
		ImportID: id, UserID: userID, Column3: fresh,
	})
	if err != nil {
		return Queued{}, err
	}
	if n == 0 { // everything was already pending from an earlier import
		return Queued{}, nil
	}
	return Queued{JobID: &id, Queued: int(n)}, nil
}

// Prune deletes finished imports older than Retention.
func (s *Service) Prune(ctx context.Context) (int, error) {
	n, err := s.Repo.PrunePeerTubeFollowImports(ctx, s.now().Add(-Retention))
	return int(n), err
}

// Drain claims one batch of due targets and processes them. It returns how
// many targets reached a final state or were rescheduled.
func (s *Service) Drain(ctx context.Context) (int, error) {
	rows, err := s.Repo.ClaimDuePeerTubeFollowTargets(ctx, sqlcgen.ClaimDuePeerTubeFollowTargetsParams{
		LeaseSeconds: defaultLease.Seconds(), PerUserLimit: perUserClaim, ClaimLimit: defaultBatch,
	})
	if err != nil {
		return 0, err
	}
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
	)
	sem := make(chan struct{}, workers)
	for _, r := range rows {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			if err := s.process(ctx, r); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return len(rows), firstErr
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
		// Recorded exactly like an unresolvable target: the status must not
		// reveal which domains this instance blocks.
		return finish(stateFailed, "unresolvable")
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
		case skippedInvalid:
			st.SkippedInvalid += n
		}
	}
	return st, nil
}
