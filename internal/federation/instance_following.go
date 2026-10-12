package federation

// Outbound instance follows (migration 0166): an admin follows a remote
// instance by host, as PeerTube's POST /server/following does. Our instance
// actor sends Follow to the remote instance actor, which is found the way
// PeerTube finds it (activitypub-follow.ts): NodeInfo's Application link
// first, then WebFinger acct:peertube@host. Every fetch goes through the same
// SSRF guard as every other remote fetch here. Once the remote Accepts, the
// ingestion gate admits what that instance announces (hasAcceptedFollowEdge).

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

var (
	// ErrInstanceActorUnavailable means this instance does not serve its
	// instance actor (an account kept the name, 0166), so it cannot follow as
	// it (409).
	ErrInstanceActorUnavailable = errors.New("federation: the instance actor is not served on this instance")
	// ErrInstanceBlocked means the target host, or its instance actor, is on
	// the admin blocklist (409).
	ErrInstanceBlocked = errors.New("federation: that instance is blocked")
)

// FollowInstance follows the instance at host as our instance actor. host is a
// bare host[:port]. Idempotent: following an instance already followed returns
// its row and sends nothing; following one that Rejected us re-arms it.
func (s *Service) FollowInstance(ctx context.Context, host string) (InstanceFollow, error) {
	host = strings.ToLower(strings.TrimSpace(host))
	if u, err := url.Parse("https://" + host); err != nil || host == "" || u.Host != host || strings.ContainsAny(host, "/@ ") {
		return InstanceFollow{}, ErrBadFollowTarget
	}
	if host == strings.ToLower(s.domain()) {
		return InstanceFollow{}, ErrLocalFollowTarget
	}
	self, ok := s.InstanceActorAvailable(ctx)
	if !ok {
		return InstanceFollow{}, ErrInstanceActorUnavailable
	}
	if blocked, err := s.repo.IsInstanceBlocked(ctx, host); err != nil {
		return InstanceFollow{}, err
	} else if blocked {
		return InstanceFollow{}, ErrInstanceBlocked
	}
	actorURL, err := s.discoverInstanceActor(ctx, host)
	if err != nil {
		return InstanceFollow{}, fmt.Errorf("%w: %w", ErrRemoteUnresolvable, err)
	}
	// The actor must live on the host the admin named: a host does not get to
	// point this instance's Follow at somebody else's server.
	if hostOf(actorURL) != host {
		return InstanceFollow{}, ErrRemoteUnresolvable
	}
	if blocked, err := s.repo.IsRemoteActorBlockedInstanceWide(ctx, actorURL); err != nil {
		return InstanceFollow{}, err
	} else if blocked {
		return InstanceFollow{}, ErrInstanceBlocked
	}
	ra, err := s.resolveRemoteActor(ctx, actorURL)
	if err != nil {
		return InstanceFollow{}, fmt.Errorf("%w: %w", ErrRemoteUnresolvable, err)
	}
	if ra.InboxUrl == "" {
		return InstanceFollow{}, ErrRemoteUnresolvable
	}
	if _, err := s.ensureInstanceKey(ctx); err != nil {
		return InstanceFollow{}, err
	}
	followID := self + "/follows/" + uuid.NewString()
	row, err := s.inst.UpsertInstanceFollowing(ctx, sqlcgen.UpsertInstanceFollowingParams{
		RemoteActorUrl: ra.ActorUrl, Host: host, FollowActivityUrl: followID,
	})
	if err != nil {
		return InstanceFollow{}, err
	}
	if row.FollowActivityUrl == followID {
		payload, err := buildFollow(followID, self, ra.ActorUrl)
		if err != nil {
			return InstanceFollow{}, err
		}
		// The actor's own inbox, as PeerTube's sendFollow unicasts.
		if err := s.enqueueInstanceDelivery(ctx, ra.InboxUrl, payload); err != nil {
			return InstanceFollow{}, err
		}
	}
	return InstanceFollow{ID: row.ID, ActorURL: row.RemoteActorUrl, Host: row.Host,
		Handle: ra.PreferredUsername + "@" + row.Host, State: row.State, CreatedAt: row.CreatedAt}, nil
}

// UnfollowInstance stops following: the row goes at once (local intent wins)
// and an Undo{Follow} is queued to the remote — a severing activity, which a
// block does not cancel (deliver.go).
func (s *Service) UnfollowInstance(ctx context.Context, id uuid.UUID) error {
	if s.inst == nil {
		return ErrInstanceFollowNotFound
	}
	row, err := s.inst.DeleteInstanceFollowingByID(ctx, id)
	if err != nil {
		return notFoundAs(err, ErrInstanceFollowNotFound)
	}
	ra, err := s.repo.GetRemoteActor(ctx, row.RemoteActorUrl)
	if row.State == instanceFollowRejected || err != nil || ra.InboxUrl == "" {
		return nil // nothing to undo, or nowhere to send it
	}
	payload, err := buildUndoFollow(s.InstanceActorURL(), row.FollowActivityUrl, row.RemoteActorUrl)
	if err != nil {
		return err
	}
	return s.enqueueInstanceDelivery(ctx, ra.InboxUrl, payload)
}

// ListInstanceFollowing returns the instances ours follows, newest first,
// optionally in one state only (""), with how many match.
func (s *Service) ListInstanceFollowing(ctx context.Context, state string, limit, offset int32) ([]InstanceFollow, int64, error) {
	if s.inst == nil {
		return nil, 0, nil
	}
	var filter *string
	if state != "" {
		filter = &state
	}
	rows, err := s.inst.ListInstanceFollowing(ctx, sqlcgen.ListInstanceFollowingParams{State: filter, ResultLimit: limit, ResultOffset: offset})
	if err != nil {
		return nil, 0, err
	}
	total, err := s.inst.CountInstanceFollowing(ctx, filter)
	if err != nil {
		return nil, 0, err
	}
	out := make([]InstanceFollow, 0, len(rows))
	for _, r := range rows {
		out = append(out, InstanceFollow{ID: r.ID, ActorURL: r.RemoteActorUrl, Host: r.Host,
			Handle: r.PreferredUsername + "@" + r.Host, State: r.State, CreatedAt: r.CreatedAt})
	}
	return out, total, nil
}

// discoverInstanceActor finds host's instance actor: NodeInfo's Application
// link, else WebFinger acct:peertube@host. https only, unless private fetches
// are allowed (dev/e2e), as for WebFinger.
func (s *Service) discoverInstanceActor(ctx context.Context, host string) (string, error) {
	schemes := []string{"https"}
	if s.allowPrivateFetch {
		schemes = append(schemes, "http")
	}
	for _, scheme := range schemes {
		doc, err := s.fetchJRD(ctx, scheme+"://"+host+"/.well-known/nodeinfo")
		if err != nil {
			continue
		}
		for _, l := range doc.Links {
			if l.Rel == "https://www.w3.org/ns/activitystreams#Application" && l.Href != "" {
				return l.Href, nil
			}
		}
		break // NodeInfo answered and names no instance actor
	}
	return s.webFingerRemote(ctx, InstanceActorName, host)
}

// answerInstanceFollowing applies an inbound Accept or Reject (already
// authority-checked by acceptedFollowID) to our instance follow, if it is one.
func (s *Service) answerInstanceFollowing(ctx context.Context, accept bool, followURL, signer string) error {
	if s.inst == nil {
		return nil
	}
	arg := sqlcgen.AcceptInstanceFollowingByActivityParams{FollowActivityUrl: followURL, RemoteActorUrl: signer}
	var err error
	if accept {
		_, err = s.inst.AcceptInstanceFollowingByActivity(ctx, arg)
	} else {
		_, err = s.inst.RejectInstanceFollowingByActivity(ctx, sqlcgen.RejectInstanceFollowingByActivityParams(arg))
	}
	return err
}
