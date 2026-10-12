package peertubeimport

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// A remote follower's inbox and key come from resolving its actor through
// federation — the cached actor federation already fetched, else an
// SSRF-guarded fetch that caches it — never from the source database's copy.

// maxActorFetchesPerRun bounds one run's outbound fetches; the rest wait for
// the next run (the importer is re-run as a sync).
const maxActorFetchesPerRun = 500

const (
	followerSkipUnresolved = "the follower's actor could not be fetched (unreachable, gone, or not an actor); retried on the next run"
	followerSkipDeferred   = "this run's actor-fetch budget was spent; resolved on the next run"
)

// actorFetches is one run's fetch budget and the hosts that already failed (a
// dead instance costs one timeout, not one per follower).
type actorFetches struct {
	left int
	dead map[string]bool
}

func (im *Importer) newActorFetches() *actorFetches {
	return &actorFetches{left: im.actorFetchBudget, dead: map[string]bool{}}
}

// followerActor returns the follower's actor, or why it is not available this
// run (followerSkipUnresolved / followerSkipDeferred). Only a database error is
// an error: an unreachable actor is a skip, re-tried on the next run.
func (im *Importer) followerActor(ctx context.Context, actorURL string, budget *actorFetches) (sqlcgen.RemoteActor, string, error) {
	if ra, err := im.q.GetRemoteActor(ctx, actorURL); err == nil {
		return ra, "", nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return sqlcgen.RemoteActor{}, "", err
	}
	host := ""
	if u, err := url.Parse(actorURL); err == nil {
		host = strings.ToLower(u.Host)
	}
	switch {
	case host == "" || budget.dead[host]:
		return sqlcgen.RemoteActor{}, followerSkipUnresolved, nil
	case budget.left <= 0:
		return sqlcgen.RemoteActor{}, followerSkipDeferred, nil
	}
	budget.left--
	ra, err := im.resolveActor(ctx, actorURL)
	if err != nil {
		budget.dead[host] = true
		im.logger.InfoContext(ctx, "peertube import: remote follower unresolved", "host", host, "error", err)
		return sqlcgen.RemoteActor{}, followerSkipUnresolved, nil
	}
	return ra, "", nil
}
