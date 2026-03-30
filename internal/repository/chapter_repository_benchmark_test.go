package repository

import (
	"context"
	"fmt"
	"testing"

	"vidra-core/internal/domain"
	"vidra-core/internal/testutil"
)

func BenchmarkChapterRepository_ReplaceAll(b *testing.B) {
	testDB := testutil.SetupTestDB(b)
	if testDB == nil {
		b.Skip("Skipping benchmark as test DB could not be set up")
	}
	repo := NewChapterRepository(testDB.DB)

	// We need to create a test user and a test video to satisfy foreign key constraints
	user := createTestViewsUser(b, testDB)
	video := createTestViewsVideo(b, testDB, user.ID)

	batchSizes := []int{10, 50, 100}

	for _, size := range batchSizes {
		b.Run(fmt.Sprintf("Chapters_%d", size), func(b *testing.B) {
			ctx := context.Background()

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				chapters := make([]*domain.VideoChapter, size)
				for j := 0; j < size; j++ {
					chapters[j] = &domain.VideoChapter{
						VideoID:  video.ID,
						Timecode: j * 60,
						Title:    fmt.Sprintf("Chapter %d", j),
						Position: j,
					}
				}
				b.StartTimer()

				err := repo.ReplaceAll(ctx, video.ID, chapters)
				if err != nil {
					b.Fatalf("failed to replace all chapters: %v", err)
				}
			}
		})
	}
}
