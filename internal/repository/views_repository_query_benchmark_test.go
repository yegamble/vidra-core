package repository

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"vidra-core/internal/domain"
)

func BenchmarkBuildAnalyticsQuery(b *testing.B) {
	repo := &ViewsRepository{}

	now := time.Now()
	isAnon := true
	filter := &domain.ViewAnalyticsFilter{
		VideoID:     "video-123",
		UserID:      "user-456",
		CountryCode: "US",
		DeviceType:  "mobile",
		StartDate:   &now,
		EndDate:     &now,
		IsAnonymous: &isAnon,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = repo.buildAnalyticsQuery(filter)
	}
}

func BenchmarkGetViewsByDateRange(b *testing.B) {
	now := time.Now()
	filter := &domain.ViewAnalyticsFilter{
		VideoID:     "video-123",
		StartDate:   &now,
		EndDate:     &now,
		Limit:       10,
		Offset:      20,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var builder strings.Builder
		builder.WriteString(`SELECT * FROM user_views WHERE 1=1`)

		args := make([]interface{}, 0, 5) // Preallocate for up to 5 parameters
		argIndex := 1

		if filter.VideoID != "" {
			builder.WriteString(" AND video_id = $")
			builder.WriteString(strconv.Itoa(argIndex))
			args = append(args, filter.VideoID)
			argIndex++
		}

		if filter.StartDate != nil {
			builder.WriteString(" AND created_at >= $")
			builder.WriteString(strconv.Itoa(argIndex))
			args = append(args, *filter.StartDate)
			argIndex++
		}

		if filter.EndDate != nil {
			builder.WriteString(" AND created_at <= $")
			builder.WriteString(strconv.Itoa(argIndex))
			args = append(args, *filter.EndDate)
			argIndex++
		}

		builder.WriteString(" ORDER BY created_at DESC")

		if filter.Limit > 0 {
			builder.WriteString(" LIMIT $")
			builder.WriteString(strconv.Itoa(argIndex))
			args = append(args, filter.Limit)
			argIndex++
		}

		if filter.Offset > 0 {
			builder.WriteString(" OFFSET $")
			builder.WriteString(strconv.Itoa(argIndex))
			args = append(args, filter.Offset)
		}

		_ = builder.String()
		_ = args
	}
}
