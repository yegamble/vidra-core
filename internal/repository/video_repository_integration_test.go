package repository

import (
	"context"
	"testing"
	"time"

	"athena/internal/domain"
	"athena/internal/testutil"
	"github.com/google/uuid"
)

func seedVideo(t *testing.T, r *videoRepository, v *domain.Video) {
	t.Helper()
	if err := r.Create(context.Background(), v); err != nil {
		t.Fatalf("seed create video failed: %v", err)
	}
}

func TestVideoRepository_ListAndSearch_Integration(t *testing.T) {
	td := testutil.SetupTestDB(t)
	if td == nil {
		t.Skip("no test db")
	}
	// Clean tables
	td.DB.Exec("DELETE FROM videos")
	vr := NewVideoRepository(td.DB).(*videoRepository)

	now := time.Now()
	// Seed mixed videos
	seedVideo(t, vr, &domain.Video{ID: uuid.NewString(), ThumbnailID: uuid.NewString(), Title: "Alpha tutorial", Description: "learn", Privacy: domain.PrivacyPublic, Status: domain.StatusCompleted, UploadDate: now, UserID: uuid.NewString(), Views: 50, Tags: []string{"edu", "alpha"}, Category: "education", Language: "en", CreatedAt: now, UpdatedAt: now})
	seedVideo(t, vr, &domain.Video{ID: uuid.NewString(), ThumbnailID: uuid.NewString(), Title: "Beta vlog", Description: "daily", Privacy: domain.PrivacyPublic, Status: domain.StatusCompleted, UploadDate: now.Add(-time.Hour), UserID: uuid.NewString(), Views: 5, Tags: []string{"vlog"}, Category: "personal", Language: "en", CreatedAt: now, UpdatedAt: now})
	// Not listed: private or processing
	seedVideo(t, vr, &domain.Video{ID: uuid.NewString(), ThumbnailID: uuid.NewString(), Title: "Private thing", Privacy: domain.PrivacyPrivate, Status: domain.StatusCompleted, UploadDate: now, UserID: uuid.NewString(), Views: 100, Tags: []string{"secret"}, Category: "misc", Language: "en", CreatedAt: now, UpdatedAt: now})
	seedVideo(t, vr, &domain.Video{ID: uuid.NewString(), ThumbnailID: uuid.NewString(), Title: "Processing item", Privacy: domain.PrivacyPublic, Status: domain.StatusProcessing, UploadDate: now, UserID: uuid.NewString(), Views: 1000, Tags: []string{"proc"}, Category: "misc", Language: "en", CreatedAt: now, UpdatedAt: now})

	// List: filter by category/language, sort by views desc
	reqList := &domain.VideoSearchRequest{Category: "education", Language: "en", Sort: "views", Order: "desc", Limit: 10, Offset: 0}
	vids, total, err := vr.List(context.Background(), reqList)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total < 1 || len(vids) < 1 {
		t.Fatalf("expected at least 1 education video, got total=%d len=%d", total, len(vids))
	}
	if vids[0].Category != "education" || vids[0].Language != "en" {
		t.Fatalf("unexpected list filters: %+v", vids[0])
	}

	// Search: query "Alpha", tags include alpha
	reqSearch := &domain.VideoSearchRequest{Query: "Alpha", Tags: []string{"alpha"}, Limit: 10}
	sres, stotal, err := vr.Search(context.Background(), reqSearch)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if stotal < 1 || len(sres) < 1 {
		t.Fatalf("expected search hit for Alpha, got %d", stotal)
	}
}
