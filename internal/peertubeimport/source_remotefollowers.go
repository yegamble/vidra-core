package peertubeimport

import (
	"context"
	"fmt"
)

// SourceRemoteFollower is a REMOTE (fediverse) actor's accepted follow of a
// LOCAL channel or account. ChannelID is 0 when the target is an ACCOUNT.
//
// It carries the follower's actor ID and nothing else about it. The source's
// cached copy of a remote actor — inbox, shared inbox, public key — is never
// read: the importer resolves the actor itself, through the same fetch
// federation uses, so no endpoint or key this instance acts on comes from
// another database.
type SourceRemoteFollower struct {
	ChannelID int64
	TargetURL string // the followed local actor's id on the source
	FollowURL string // the Follow activity id ('' when the source kept none)
	ActorURL  string
}

// RemoteFollowers returns every accepted follow by a REMOTE actor of a LOCAL
// user's channel or account — the instance's fediverse audience, which Follows
// deliberately leaves out. The source server's own actor (an Application other
// instances follow) belongs to no user and is never a target here.
func (s *Source) RemoteFollowers(ctx context.Context) ([]SourceRemoteFollower, error) {
	onActor, err := s.actorLinksLiveOnActor(ctx)
	if err != nil {
		return nil, err
	}
	channelJoin, accountJoin := `tvc."actorId" = ta.id`, `tacc."actorId" = ta.id`
	if onActor {
		channelJoin, accountJoin = `tvc.id = ta."videoChannelId"`, `tacc.id = ta."accountId"`
	}
	rows, err := s.pool.Query(ctx, `
		SELECT COALESCE(tvc.id, 0), COALESCE(ta.url, ''), COALESCE(af.url, ''), fa.url
		FROM "actorFollow" af
		JOIN actor fa ON fa.id = af."actorId"
		JOIN actor ta ON ta.id = af."targetActorId"
		LEFT JOIN "videoChannel" tvc ON `+channelJoin+`
		LEFT JOIN account tacc ON `+accountJoin+`
		WHERE af.state = 'accepted' AND fa."serverId" IS NOT NULL AND fa.url IS NOT NULL
		  AND ta."serverId" IS NULL AND (tvc.id IS NOT NULL OR tacc."userId" IS NOT NULL)
		ORDER BY af.id`)
	if err != nil {
		return nil, fmt.Errorf("peertubeimport: read remote followers: %w", err)
	}
	defer rows.Close()
	var out []SourceRemoteFollower
	for rows.Next() {
		var f SourceRemoteFollower
		if err := rows.Scan(&f.ChannelID, &f.TargetURL, &f.FollowURL, &f.ActorURL); err != nil {
			return nil, fmt.Errorf("peertubeimport: scan remote follower: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
