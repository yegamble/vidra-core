package peertubeimport

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/federation"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// ── remote (fediverse) followers ──
//
// Without these a migrated instance publishes into silence: keys and actor ids
// survive, but nobody is on the delivery list. Each is carried as the accepted
// remote_follows row every publish fans out over, plus a cached actor to
// deliver to. A skip writes no ledger row, so it is re-decided on every run.
const (
	followerSkipActorID    = "the channel's actor id here differs from the one they follow on the source (another domain or a renamed handle), so they would receive posts from an actor they never followed"
	followerSkipNoFederate = "the channel has ActivityPub turned off here"
	followerSkipRefused    = "the follower is blocked here (instance blocklist, an admin block, or the channel owner's block)"
	followerSkipUnusable   = "the source holds no usable actor record for the follower (no inbox, public key or valid id)"
)

func (im *Importer) importRemoteFollowers(ctx context.Context, r *Report) error {
	followers, err := im.src.RemoteFollowers(ctx)
	if err != nil || len(followers) == 0 {
		return err
	}
	if im.publicBaseURL == "" {
		r.Deferred = append(r.Deferred, "remote (fediverse) followers: this instance's public base URL is not configured, so it cannot tell whether it answers for the actor ids they follow")
		return nil
	}
	c := r.count(KindRemoteFollower)
	skipped := map[string]int{}
	accounts := 0
	for _, f := range followers {
		if f.ChannelID == 0 {
			accounts++
			c.Unsupported++
			continue
		}
		sid := strconv.FormatInt(f.ChannelID, 10) + ":" + f.ActorURL
		if _, _, done, err := im.alreadyProcessed(ctx, KindRemoteFollower, sid); err != nil {
			return err
		} else if done {
			c.Skipped++
			continue
		}
		reason, err := im.importOneRemoteFollower(ctx, f, sid, c)
		if err != nil {
			im.markFailed(ctx, KindRemoteFollower, sid, safeErr(err))
			c.Failed++
			im.logger.WarnContext(ctx, "peertube import: remote follower failed", "channel_source_id", f.ChannelID, "error", err)
		} else if reason != "" {
			skipped[reason]++
			c.Skipped++
		}
	}
	for _, reason := range []string{followerSkipActorID, followerSkipNoFederate, followerSkipRefused, followerSkipUnusable} {
		if n := skipped[reason]; n > 0 {
			r.addConflict(fmt.Sprintf("%d remote follower(s) not carried: %s", n, reason))
		}
	}
	if accounts > 0 {
		r.addConflict(fmt.Sprintf("%d remote follower(s) of ACCOUNTS not carried: followers are recorded per channel here, and a live Follow of an account is rejected too", accounts))
	}
	return nil
}

// importOneRemoteFollower returns why a follower was not carried, if it was not.
func (im *Importer) importOneRemoteFollower(ctx context.Context, f SourceRemoteFollower, sid string, c *Counts) (string, error) {
	channelID, ok, err := im.resolveParent(ctx, KindChannel, strconv.FormatInt(f.ChannelID, 10))
	if err != nil {
		return "", err
	}
	if !ok {
		return "", awaitParent(c)
	}
	ch, err := im.q.GetChannelByID(ctx, channelID)
	if err != nil {
		return "", err
	}
	// A follow is of an actor ID: from any other id a post is a stranger's, and
	// Mastodon drops it.
	if f.TargetURL != im.publicBaseURL+"/video-channels/"+ch.Handle {
		return followerSkipActorID, nil
	}
	if !ch.ActivitypubEnabled {
		return followerSkipNoFederate, nil
	}
	actor, ok := f.CachedActor()
	if !ok {
		return followerSkipUnusable, nil
	}
	if refused, err := federation.RemoteFollowerRefused(ctx, im.q, ch.OwnerID, f.ActorURL); err != nil {
		return "", err
	} else if refused {
		return followerSkipRefused, nil
	}
	err = im.withTx(ctx, func(q *sqlcgen.Queries) error {
		// An actor cached here was fetched, so it is newer (maybe a rotated key).
		if _, err := q.GetRemoteActor(ctx, f.ActorURL); errors.Is(err, pgx.ErrNoRows) {
			if err := q.UpsertRemoteActor(ctx, actor); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		if err := q.ImportInsertRemoteFollow(ctx, sqlcgen.ImportInsertRemoteFollowParams{
			ChannelID: channelID, RemoteActorUrl: f.ActorURL, FollowActivityUrl: f.FollowURL,
		}); err != nil {
			return err
		}
		return recordLedger(ctx, q, KindRemoteFollower, sid, channelID, "done", "")
	})
	if err != nil {
		return "", err
	}
	c.Imported++
	return "", nil
}
