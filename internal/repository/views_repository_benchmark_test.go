package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"athena/internal/domain"
	"athena/internal/testutil"
)

func BenchmarkBatchCreateUserViews(b *testing.B) {
	testDB := testutil.SetupTestDB(b)
	if testDB == nil {
		b.Skip("Skipping benchmark as test DB could not be set up")
	}
	repo := NewViewsRepository(testDB.DB)

	// Create test user and video
	user := createTestViewsUser(b, testDB)
	video := createTestViewsVideo(b, testDB, user.ID)

	batchSizes := []int{100, 1000}

	for _, size := range batchSizes {
		b.Run(fmt.Sprintf("BatchSize_%d", size), func(b *testing.B) {
			ctx := context.Background()

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				views := make([]*domain.UserView, size)
				for j := 0; j < size; j++ {
					views[j] = &domain.UserView{
						VideoID:              video.ID,
						UserID:               &user.ID,
						SessionID:            uuid.New().String(),
						FingerprintHash:      fmt.Sprintf("bench_hash_%d_%d_%d", i, j, time.Now().UnixNano()),
						WatchDuration:        120,
						VideoDuration:        300,
						CompletionPercentage: 40.0,
						IsCompleted:          false,
						SeekCount:            0,
						PauseCount:           0,
						ReplayCount:          0,
						QualityChanges:       0,
						InitialLoadTime:      intPtr(100),
						BufferEvents:         0,
						ConnectionType:       stringPtrForViews("wifi"),
						VideoQuality:         stringPtrForViews("1080p"),
						DeviceType:           "desktop",
						OSName:               "Linux",
						BrowserName:          "Firefox",
						ScreenResolution:     "1920x1080",
						IsMobile:             false,
						CountryCode:          "US",
						RegionCode:           "CA",
						CityName:             "San Francisco",
						Timezone:             "America/Los_Angeles",
						ReferrerURL:          "direct",
						ReferrerType:         "direct",
						UTMSource:            "none",
						UTMMedium:            "none",
						UTMCampaign:          "none",
						IsAnonymous:          false,
						TrackingConsent:      true,
						GDPRConsent:          boolPtrForViews(true),
						ViewDate:             time.Now(),
						ViewHour:             12,
						Weekday:              1,
						CreatedAt:            time.Now(),
						UpdatedAt:            time.Now(),
					}
				}
				b.StartTimer()

				err := repo.BatchCreateUserViews(ctx, views)
				if err != nil {
					b.Fatalf("failed to batch create user views: %v", err)
				}
			}
		})
	}
}
