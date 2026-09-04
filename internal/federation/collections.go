package federation

import (
	"context"
	"errors"
	"strconv"

	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// outboxPageSize is how many activities an OrderedCollectionPage carries.
const outboxPageSize = 20

// OrderedCollection is a summary ActivityStreams OrderedCollection: it carries an
// accurate totalItems and, when non-empty and pageable (the outbox), a `first`
// link to the initial OrderedCollectionPage.
type OrderedCollection struct {
	Context    string `json:"@context"`
	ID         string `json:"id"`
	Type       string `json:"type"`
	TotalItems int64  `json:"totalItems"`
	First      string `json:"first,omitempty"`
}

// OrderedCollectionPage is one page of an OrderedCollection's items.
type OrderedCollectionPage struct {
	Context      string           `json:"@context"`
	ID           string           `json:"id"`
	Type         string           `json:"type"`
	PartOf       string           `json:"partOf"`
	OrderedItems []map[string]any `json:"orderedItems"`
	Next         string           `json:"next,omitempty"`
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
		col := orderedCollection(base+"/outbox", total)
		if total > 0 {
			col.First = base + "/outbox?page=1"
		}
		return col, nil
	case "following":
		total = 0 // channels don't follow anyone
	default:
		return nil, ErrNotFound
	}
	return orderedCollection(base+"/"+kind, total), nil
}

// ChannelOutboxPage returns one page of a channel's outbox: its public videos
// (newest first) as Create{Video} activities, with a `next` link when more pages
// remain. Unknown handle → ErrNotFound; page < 1 is treated as 1.
func (s *Service) ChannelOutboxPage(ctx context.Context, handle string, page int) (*OrderedCollectionPage, error) {
	if page < 1 {
		page = 1
	}
	ch, err := s.repo.GetChannelByHandle(ctx, handle)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	base := s.baseURL + "/video-channels/" + ch.Handle
	channelActor := base
	rows, err := s.repo.ListChannelOutboxVideos(ctx, sqlcgen.ListChannelOutboxVideosParams{
		ChannelID: ch.ID,
		Limit:     outboxPageSize,
		Offset:    int32((page - 1) * outboxPageSize),
	})
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		// Same split as buildVideoActivity: the object id is frozen at the uuid
		// form, only the human-facing url moves.
		objectID := s.baseURL + "/videos/" + r.ID.String()
		watchURL := objectID
		if r.ShortCode != "" {
			watchURL = s.baseURL + "/v/" + r.ShortCode
		}
		items = append(items, map[string]any{
			"id":     channelActor + "/activities/create/" + r.ID.String(),
			"type":   "Create",
			"actor":  channelActor,
			"to":     []string{publicAudience},
			"object": videoObject(channelActor, objectID, watchURL, r.Title, r.Description),
		})
	}
	pg := &OrderedCollectionPage{
		Context:      "https://www.w3.org/ns/activitystreams",
		ID:           base + "/outbox?page=" + strconv.Itoa(page),
		Type:         "OrderedCollectionPage",
		PartOf:       base + "/outbox",
		OrderedItems: items,
	}
	// A full page implies there may be more.
	if len(rows) == outboxPageSize {
		pg.Next = base + "/outbox?page=" + strconv.Itoa(page+1)
	}
	return pg, nil
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
