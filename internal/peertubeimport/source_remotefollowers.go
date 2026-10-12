package peertubeimport

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// SourceRemoteFollower is a REMOTE (fediverse) actor's accepted follow of a
// LOCAL channel or account, with the follower's actor record as the source
// cached it. ChannelID is 0 when the target is an ACCOUNT.
type SourceRemoteFollower struct {
	ChannelID         int64
	TargetURL         string // the followed local actor's id on the source
	FollowURL         string // the Follow activity id ('' when the source kept none)
	ActorURL          string
	ActorType         string
	PreferredUsername string
	Host              string // the follower's server, as the source files it
	InboxURL          string
	SharedInboxURL    string
	FollowersURL      string
	PublicKeyPEM      string
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
		SELECT COALESCE(tvc.id, 0), COALESCE(ta.url, ''), COALESCE(af.url, ''),
		       fa.url, fa.type::text, fa."preferredUsername", srv.host,
		       COALESCE(fa."inboxUrl", ''), COALESCE(fa."sharedInboxUrl", ''),
		       COALESCE(fa."followersUrl", ''), COALESCE(fa."publicKey", '')
		FROM "actorFollow" af
		JOIN actor fa ON fa.id = af."actorId"
		JOIN server srv ON srv.id = fa."serverId"
		JOIN actor ta ON ta.id = af."targetActorId"
		LEFT JOIN "videoChannel" tvc ON `+channelJoin+`
		LEFT JOIN account tacc ON `+accountJoin+`
		WHERE af.state = 'accepted' AND ta."serverId" IS NULL AND fa.url IS NOT NULL
		  AND (tvc.id IS NOT NULL OR tacc."userId" IS NOT NULL)
		ORDER BY af.id`)
	if err != nil {
		return nil, fmt.Errorf("peertubeimport: read remote followers: %w", err)
	}
	defer rows.Close()
	var out []SourceRemoteFollower
	for rows.Next() {
		var f SourceRemoteFollower
		if err := rows.Scan(&f.ChannelID, &f.TargetURL, &f.FollowURL, &f.ActorURL, &f.ActorType,
			&f.PreferredUsername, &f.Host, &f.InboxURL, &f.SharedInboxURL, &f.FollowersURL, &f.PublicKeyPEM); err != nil {
			return nil, fmt.Errorf("peertubeimport: scan remote follower: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// CachedActor maps the follower's record onto the row federation caches when
// it fetches an actor itself (UpsertRemoteActor). It refuses a record that could
// be neither delivered to nor verified against: an id that is not http(s) on the
// server the source files it under, no inbox, or no public key. An absent
// shared inbox stays NULL — ListRemoteFollowerInboxes COALESCEs it, so ” would
// hide the inbox and the follower would never be delivered to.
func (f SourceRemoteFollower) CachedActor() (sqlcgen.UpsertRemoteActorParams, bool) {
	host := strings.ToLower(f.Host)
	if host == "" || !isHTTPURLOn(f.ActorURL, host) || !isHTTPURLOn(f.InboxURL, "") || strings.TrimSpace(f.PublicKeyPEM) == "" {
		return sqlcgen.UpsertRemoteActorParams{}, false
	}
	p := sqlcgen.UpsertRemoteActorParams{
		ActorUrl:          f.ActorURL,
		ActorType:         f.ActorType,
		PreferredUsername: f.PreferredUsername,
		Domain:            host,
		InboxUrl:          f.InboxURL,
		PublicKeyPem:      f.PublicKeyPEM,
		FollowersUrl:      f.FollowersURL,
	}
	if isHTTPURLOn(f.SharedInboxURL, "") {
		shared := f.SharedInboxURL
		p.SharedInboxUrl = &shared
	}
	return p, true
}

// isHTTPURLOn: raw is an absolute http(s) URL (on host, when host is set).
func isHTTPURLOn(raw, host string) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return false
	}
	return host == "" || strings.EqualFold(u.Host, host)
}
