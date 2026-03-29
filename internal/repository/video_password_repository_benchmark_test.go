package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"

)

func BenchmarkReplaceAllVideoPasswords(b *testing.B) {
	db, mock, err := sqlmock.New()
	if err != nil {
		b.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
	}
	defer db.Close()

	sqlxDB := sqlx.NewDb(db, "sqlmock")
	repo := NewVideoPasswordRepository(sqlxDB)

	ctx := context.Background()
	videoID := "video-1"

	passwordCounts := []int{5, 20, 100}

	for _, count := range passwordCounts {
		b.Run(fmt.Sprintf("Count_%d", count), func(b *testing.B) {
			hashes := make([]string, count)
			for i := 0; i < count; i++ {
				hashes[i] = fmt.Sprintf("hash-%d", i)
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				mock.ExpectBegin()
				mock.ExpectExec("DELETE FROM video_passwords").WithArgs(videoID).WillReturnResult(sqlmock.NewResult(0, 0))

				// NEW implementation does bulk insert

				rows := sqlmock.NewRows([]string{"id", "video_id", "password_hash", "created_at"})
				for j := 0; j < count; j++ {
					rows.AddRow(int64(j+1), videoID, hashes[j], time.Now())
				}
				// We must use (?s) for multiline queries and match the new syntax
				mock.ExpectQuery("(?s)INSERT INTO video_passwords .* SELECT .* FROM UNNEST.*").WillReturnRows(rows)

				mock.ExpectCommit()

				_, err := repo.ReplaceAll(ctx, videoID, hashes)
				if err != nil {
					b.Fatalf("failed to replace passwords: %v", err)
				}
			}
		})
	}
}
