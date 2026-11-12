package video

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// WithAnonymousCache shares the answers every caller without an account gets
// alike (public lists, search totals, the home rail) for ttl, and makes this
// service's own video writes invalidate them. Off unless wired: without it
// every call reads the database, as before.
func WithAnonymousCache(ttl time.Duration) Option {
	return func(s *Service) {
		s.anon = newAnonCache(ttl)
		s.repo = invalidatingRepo{Repository: s.repo, bump: s.anon.invalidate}
	}
}

// InvalidateAnonymous makes every later anonymous answer a new read. A write
// outside this service that takes something off the public lists (a moderator
// block or its lifting) calls it; this service's own writes already do.
func (s *Service) InvalidateAnonymous() {
	if s != nil && s.anon != nil {
		s.anon.invalidate()
	}
}

// AnonymousCached returns load's answer for key from the service's anonymous
// cache. The answer must be the same for every caller without an account and
// must not be modified by whoever receives it; key must name everything it
// depends on. Without WithAnonymousCache it simply calls load.
func AnonymousCached[T any](ctx context.Context, s *Service, key string, load func(context.Context) (T, error)) (T, error) {
	return anonCached(ctx, s.anon, key, load)
}

// invalidatingRepo invalidates the anonymous cache after every write that can
// put a video on, or take it off, a public list (publish and every other state
// change, an edit such as a privacy change, a delete), so no call site has to
// remember to. It fires after the statement returns, error or not: one
// needless reload is cheaper than one missed takedown.
type invalidatingRepo struct {
	Repository
	bump func()
}

func (r invalidatingRepo) SetVideoState(ctx context.Context, arg sqlcgen.SetVideoStateParams) (sqlcgen.Video, error) {
	defer r.bump()
	return r.Repository.SetVideoState(ctx, arg)
}

func (r invalidatingRepo) PublishTranscodingVideo(ctx context.Context, id uuid.UUID) (int64, error) {
	defer r.bump()
	return r.Repository.PublishTranscodingVideo(ctx, id)
}

func (r invalidatingRepo) UpdateVideo(ctx context.Context, arg sqlcgen.UpdateVideoParams) (sqlcgen.Video, error) {
	defer r.bump()
	return r.Repository.UpdateVideo(ctx, arg)
}

func (r invalidatingRepo) DeleteVideo(ctx context.Context, id uuid.UUID) error {
	defer r.bump()
	return r.Repository.DeleteVideo(ctx, id)
}

type feedPage struct {
	items []FeedItem
	total int64
}

// listPublicAnonymous is ListPublic for a caller without an account, through
// the cache. The key is the whole request; a signed-in caller never gets here.
func (s *Service) listPublicAnonymous(ctx context.Context, sort, scope string, filter FeedFilter, limit, offset int32) ([]FeedItem, int64, error) {
	key, err := json.Marshal(struct {
		Sort, Scope   string
		Filter        FeedFilter
		Limit, Offset int32
	}{NormalizeFeedSort(sort), NormalizeFeedScope(scope), filter, limit, offset})
	if err != nil {
		return nil, 0, err
	}
	page, err := anonCached(ctx, s.anon, "feed|"+string(key), func(ctx context.Context) (feedPage, error) {
		items, total, err := s.listPublic(ctx, sort, scope, filter, uuid.Nil, false, limit, offset)
		return feedPage{items: items, total: total}, err
	})
	// A copy of the slice: the cached page is shared by every caller.
	return slices.Clone(page.items), page.total, err
}

// countSearchPublicAnonymous is CountSearchPublic's cached form, keyed by the
// statement's whole argument (query, facets, hide policy).
func (s *Service) countSearchPublicAnonymous(ctx context.Context, query string, filter SearchFilter) (int64, error) {
	args := filter.searchParams(query, pgtype.UUID{})
	key, err := json.Marshal(args)
	if err != nil {
		return 0, err
	}
	return anonCached(ctx, s.anon, "search_count|"+string(key), func(ctx context.Context) (int64, error) {
		return s.repo.CountSearchPublicVideos(ctx, args)
	})
}
