//go:build integration

package main

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/vidra/vidra-core/internal/store"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
	"github.com/vidra/vidra-core/internal/upload"
	"github.com/vidra/vidra-core/internal/uploadfinalize"
	"github.com/vidra/vidra-core/internal/video"
)

func TestRetainedScanRecoveryUsesNormalFinalize(t *testing.T) {
	ctx := t.Context()
	st, err := store.New(ctx, verifyThrowawayDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	q := st.Queries()
	for _, scenario := range []string{"clean", "visibility changed", "blocked", "deleted", "infected", "scanner error"} {
		t.Run(scenario, func(t *testing.T) {
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			p, videoID, blobs := newRetainedSourceFixture(t, st)
			for _, mismatched := range []sqlcgen.GetRetainedScanSourceParams{
				{UploadID: p.UploadID, OwnerID: uuid.New(), Privacy: "private", Sha256: p.Sha256},
				{UploadID: p.UploadID, OwnerID: p.OwnerID, Privacy: "public", Sha256: p.Sha256},
				{UploadID: p.UploadID, OwnerID: p.OwnerID, Privacy: "private", Sha256: sha256Hex("other")},
			} {
				if _, err = queueRetainedUpload(ctx, st, blobs, mismatched, true, 100, uuid.Nil); err == nil {
					t.Fatal("admitted a mismatched source")
				}
			}
			_, err = queueRetainedUpload(ctx, st, blobs, p, false, 100, uuid.Nil)
			must(err)
			if scenario == "clean" {
				if _, sizeErr := queueRetainedUpload(ctx, st, blobs, p, true, 2, uuid.Nil); sizeErr == nil {
					t.Fatal("admitted an original exceeding the current upload limit")
				}
				tx, lockErr := st.Pool.Begin(ctx)
				must(lockErr)
				_, lockErr = q.WithTx(tx).LockRetainedUpload(ctx, p.UploadID)
				must(lockErr)
				_, secondErr := queueRetainedUpload(ctx, st, blobs, p, true, 100, uuid.Nil)
				must(tx.Rollback(ctx))
				if secondErr == nil {
					t.Fatal("concurrent operator acquired the same upload")
				}
			}
			_, err = queueRetainedUpload(ctx, st, blobs, p, true, 100, uuid.Nil)
			must(err)
			_, err = queueRetainedUpload(ctx, st, blobs, p, true, 100, uuid.Nil)
			must(err)
			if scenario == "blocked" {
				_, err = st.Pool.Exec(ctx, "INSERT INTO video_blocks(video_id) VALUES ($1)", videoID)
				must(err)
			}
			if scenario == "deleted" {
				_, err = st.Pool.Exec(ctx, "DELETE FROM videos WHERE id=$1", videoID)
				must(err)
			}
			scans, publishes := 0, 0
			scanner := retainedTestScan{verify: func() (bool, error) {
				scans++
				switch scenario {
				case "visibility changed":
					_, err := st.Pool.Exec(ctx, "UPDATE videos SET privacy='unlisted' WHERE id=$1", videoID)
					must(err)
				case "infected":
					return false, nil
				case "scanner error":
					return false, errors.New("offline")
				}
				return true, nil
			}}
			pipeline := video.NewService(q, nil, video.WithScanner(scanner), video.WithProber(scanner), video.WithScanMode("fail-closed"), video.WithPublishHook(func(context.Context, uuid.UUID) { publishes++ }))
			worker := uploadfinalize.NewService(q, upload.NewService(q, nil), pipeline)
			_, err = worker.DrainJobs(ctx, 1)
			must(err)
			got, err := q.GetRecordedScanRecovery(ctx, sqlcgen.GetRecordedScanRecoveryParams(p))
			if scenario == "deleted" {
				if !errors.Is(err, pgx.ErrNoRows) || scans != 0 || publishes != 0 {
					t.Fatal("recovery survived a creator deletion")
				}
				return
			}
			must(err)
			videoRow, err := q.GetVideoByID(ctx, videoID)
			must(err)
			if scenario == "blocked" && scans != 0 || scenario != "blocked" && scans != 1 {
				t.Fatalf("scans=%d", scans)
			}
			if scenario == "clean" {
				if got.State != "done" || videoRow.State != "published" || videoRow.Privacy != "private" || publishes != 1 {
					t.Fatalf("state=%s video=%s privacy=%s publishes=%d", got.State, videoRow.State, videoRow.Privacy, publishes)
				}
				must(pipeline.ProcessRetained(ctx, got.ID, videoID))
				if scans != 1 || publishes != 1 {
					t.Fatal("repeated delivery reprocessed the accepted upload")
				}
			} else if got.State != "failed" || videoRow.State != "failed" || publishes != 0 {
				t.Fatalf("unexpected recovery: job=%s video=%s publishes=%d", got.State, videoRow.State, publishes)
			}
		})
	}
}
