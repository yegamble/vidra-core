package federation

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// OrderedCollection is a summary ActivityStreams OrderedCollection: it carries an
// accurate totalItems but no paged orderedItems yet (paging is a later refinement).
type OrderedCollection struct {
	Context    string `json:"@context"`
	ID         string `json:"id"`
	Type       string `json:"type"`
	TotalItems int64  `json:"totalItems"`
}

func orderedCollection(id string, total int64) *OrderedCollection {
	return &OrderedCollection{
		Context:    "https://www.w3.org/ns/activitystreams",
		ID:         id,
		Type:       "OrderedCollection",
		TotalItems: total,
	}
}

// ChannelCollection returns the OrderedCollection for a channel's followers,
// following, or outbox. Unknown handle → ErrNotFound; unknown kind → ErrNotFound.
func (s *Service) ChannelCollection(ctx context.Context, handle, kind string) (*OrderedCollection, error) {
	ch, err := s.repo.GetChannelByHandle(ctx, handle)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	base := s.baseURL + "/video-channels/" + ch.Handle
	var total int64
	switch kind {
	case "followers":
		local, err := s.repo.CountChannelFollowers(ctx, ch.ID)
		if err != nil {
			return nil, err
		}
		remote, err := s.repo.CountRemoteFollowers(ctx, ch.ID)
		if err != nil {
			return nil, err
		}
		total = local + remote
	case "outbox":
		total, err = s.repo.CountPublicVideosByChannel(ctx, ch.ID)
		if err != nil {
			return nil, err
		}
	case "following":
		total = 0 // channels don't follow anyone
	default:
		return nil, ErrNotFound
	}
	return orderedCollection(base+"/"+kind, total), nil
}

// AccountCollection returns an (empty) OrderedCollection for an account actor.
// Accounts here are attribution actors — not followed, and videos belong to
// channels — so their followers/following/outbox are empty for now.
func (s *Service) AccountCollection(ctx context.Context, username, kind string) (*OrderedCollection, error) {
	u, err := s.repo.GetUserActorByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	switch kind {
	case "followers", "following", "outbox":
		return orderedCollection(s.baseURL+"/accounts/"+u.Username+"/"+kind, 0), nil
	default:
		return nil, ErrNotFound
	}
}
