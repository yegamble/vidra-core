//go:build integration

// The anonymous read path against REAL PostgreSQL, measured through the service
// calls the three public discovery endpoints make (GET /videos, the count
// behind GET /videos/search, and the hydration behind
// GET /recommendations/home). Run with:
//
//	DATABASE_URL=postgres://[REDACTED]@localhost:5432/vidra?sslmode=disable \
//	go test -tags=integration -run='^$' -bench=AnonymousReadPath -benchtime=20x ./internal/video/
package video_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/video"
)

func BenchmarkAnonymousReadPath(b *testing.B) {
	ctx := context.Background()
	st := openStore(b)
	sfx := seedReadPath(b, st, readPathShape{Owners: 5500, Videos: 15000})
	svc := video.NewService(st.Queries(), nil)

	for _, sort := range []string{"trending", "recent", "popular", "name"} {
		b.Run("list_"+sort, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				items, total, err := svc.ListPublic(ctx, sort, "local", video.FeedFilter{}, uuid.Nil, false, 20, 0)
				if err != nil || len(items) != 20 || total < 10000 {
					b.Fatalf("ListPublic(%s) = %d items, total %d, %v", sort, len(items), total, err)
				}
			}
		})
	}
	// The same calls with the anonymous cache wired as cmd/api wires it: every
	// call after the first inside the TTL. The runs above are the cache miss.
	cached := video.NewService(st.Queries(), nil, video.WithAnonymousCache(15*time.Second))
	b.Run("cached_list_trending", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if items, _, err := cached.ListPublic(ctx, "trending", "local", video.FeedFilter{}, uuid.Nil, false, 20, 0); err != nil || len(items) != 20 {
				b.Fatalf("cached ListPublic = %d items, %v", len(items), err)
			}
		}
	})
	b.Run("search_count", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if n, err := svc.CountSearchPublic(ctx, "coffee", video.SearchFilter{}, uuid.Nil, false); err != nil || n == 0 {
				b.Fatalf("CountSearchPublic = %d, %v", n, err)
			}
		}
	})
	b.Run("hydrate_50", func(b *testing.B) {
		rows, err := st.Pool.Query(ctx, `SELECT v.id FROM videos v JOIN channels c ON c.id = v.channel_id
			WHERE c.handle LIKE 'rpc' || $1 || '\_%' AND v.privacy = 'public' AND v.state = 'published'
			ORDER BY v.created_at DESC, v.id LIMIT 50`, sfx)
		if err != nil {
			b.Fatalf("pick ids: %v", err)
		}
		var ids []uuid.UUID
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				b.Fatalf("scan id: %v", err)
			}
			ids = append(ids, id)
		}
		rows.Close()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if items, err := svc.HydrateByIDs(ctx, ids, uuid.Nil, false, false); err != nil || len(items) == 0 {
				b.Fatalf("HydrateByIDs = %d items, %v", len(items), err)
			}
		}
	})
}
