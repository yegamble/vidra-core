//go:build integration

package main

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/store"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
	"github.com/vidra/vidra-core/internal/upload"
	"github.com/vidra/vidra-core/internal/uploadfinalize"
	"github.com/vidra/vidra-core/internal/video"
)

func TestRetryDeclinedRetainedScan(t *testing.T) {
	ctx := t.Context()
	db, err := store.New(ctx, verifyThrowawayDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	q := db.Queries()
	for _, scenario := range []string{"retry", "replaced file", "blocked video", "new scanner verdict", "expired", "prior second attempt", "prior scanner failure", "already accepted", "missing stored object"} {
		t.Run(scenario, func(t *testing.T) {
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			p, id, blobs := newRetainedSourceFixture(t, db)
			_, err := queueRetainedUpload(ctx, db, blobs, p, true, 100, uuid.Nil)
			must(err)
			rec, err := q.GetRecordedScanRecovery(ctx, sqlcgen.GetRecordedScanRecoveryParams(p))
			must(err)
			// This worker lacks the verified scanner capability. It refuses
			// before scanning, exactly as the cached v0.7.37 worker did.
			uploads := upload.NewService(q, blobs)
			worker := uploadfinalize.NewService(q, uploads, video.NewService(q, blobs))
			n, err := worker.DrainJobs(ctx, 1)
			must(err)
			if n != 0 {
				t.Fatalf("previous worker completed %d jobs", n)
			}
			latest, err := q.GetLatestUploadFinalizeJob(ctx, p.UploadID)
			must(err)
			if latest.ID != rec.ID || latest.Error != video.RetainedRecoveryDeclined || latest.Attempts != 1 {
				t.Fatalf("previous terminal job: %+v", latest)
			}
			exec := func(sql string, args ...any) { t.Helper(); _, err := db.Pool.Exec(ctx, sql, args...); must(err) }
			switch scenario {
			case "replaced file":
				exec("UPDATE video_files SET id=$1 WHERE video_id=$2 AND kind='original'", uuid.New(), id)
			case "blocked video":
				exec("INSERT INTO video_blocks(video_id) VALUES ($1)", id)
			case "expired":
				exec("UPDATE upload_sessions SET expires_at=now() WHERE id=$1", p.UploadID)
			case "new scanner verdict":
				must(audit.NewService(q).Record(ctx, audit.Event{Actor: audit.ActorSnapshot{Kind: "system"}, Action: observability.ActionUploadMalwareRejected, Result: observability.ResultFailure, Reason: "video=" + id.String() + " outcome=scan_limit policy=fail-closed"}))
			case "prior second attempt":
				exec("UPDATE upload_finalize_jobs SET attempts=2 WHERE id=$1", rec.ID)
			case "prior scanner failure":
				exec("UPDATE upload_finalize_jobs SET error=$2 WHERE id=$1", rec.ID, video.SafetyScanRejectedMessage)
			case "already accepted":
				exec("UPDATE retained_upload_recoveries SET accepted=true WHERE job_id=$1", rec.ID)
			case "missing stored object":
				src, err := q.GetRetainedScanSource(ctx, p)
				must(err)
				must(blobs.Delete(ctx, src.StorageKey))
			}
			if scenario != "retry" {
				if _, err := queueRetainedUpload(ctx, db, blobs, p, true, 100, rec.ID); err == nil {
					t.Fatal("ineligible retry accepted")
				}
				return
			}
			for _, attempt := range []struct {
				job    uuid.UUID
				max    int64
				params sqlcgen.GetRetainedScanSourceParams
			}{
				{uuid.New(), 100, p}, {rec.ID, 2, p}, {rec.ID, 100, sqlcgen.GetRetainedScanSourceParams{UploadID: p.UploadID, OwnerID: uuid.New(), Privacy: p.Privacy, Sha256: p.Sha256}},
			} {
				if _, err := queueRetainedUpload(ctx, db, blobs, attempt.params, true, attempt.max, attempt.job); err == nil {
					t.Fatal("mismatched identity or excessive size admitted")
				}
			}
			before, err := queueRetainedUpload(ctx, db, blobs, p, true, 100, uuid.Nil)
			must(err)
			if before != "previously queued, current job state failed" {
				t.Fatalf("ordinary replay changed terminal job: %s", before)
			}
			dry, err := queueRetainedUpload(ctx, db, blobs, p, false, 100, rec.ID)
			must(err)
			if dry != "eligible previously declined job, 3 bytes, dry run" {
				t.Fatalf("dry run: %s", dry)
			}
			result, err := queueRetainedUpload(ctx, db, blobs, p, true, 100, rec.ID)
			must(err)
			if result != "queued for full processing" {
				t.Fatalf("apply: %s", result)
			}
			duplicate, err := queueRetainedUpload(ctx, db, blobs, p, true, 100, rec.ID)
			must(err)
			if duplicate != "previously queued, current job state pending" {
				t.Fatalf("duplicate: %s", duplicate)
			}
			scanned, published := 0, 0
			scanner := retainedTestScan{verify: func() (bool, error) { scanned++; return true, nil }}
			worker = uploadfinalize.NewService(q, uploads, video.NewService(q, blobs, video.WithScanner(scanner), video.WithProber(scanner), video.WithScanMode("fail-closed"), video.WithPublishHook(func(context.Context, uuid.UUID) { published++ })))
			n, err = worker.DrainJobs(ctx, 1)
			must(err)
			if n != 1 || scanned != 1 || published != 1 {
				t.Fatalf("done=%d scans=%d published=%d", n, scanned, published)
			}
			latest, err = q.GetLatestUploadFinalizeJob(ctx, p.UploadID)
			must(err)
			if latest.ID != rec.ID || latest.State != "done" || latest.Attempts != 1 {
				t.Fatalf("retried job: %+v", latest)
			}
			v, err := q.GetVideoByID(ctx, id)
			must(err)
			if v.State != "published" || v.Privacy != "private" {
				t.Fatalf("video=%s/%s", v.State, v.Privacy)
			}
			var terminal, retry, success, operatorEvents int
			err = db.Pool.QueryRow(ctx, "SELECT count(*) FILTER(WHERE e.kind='dead_lettered'), count(*) FILTER(WHERE e.kind='retry_scheduled'), count(*) FILTER(WHERE e.kind='succeeded') FROM job_events e JOIN job_runs j ON j.id=e.job_id WHERE j.queue='upload_finalize_jobs' AND j.source_id=$1", rec.ID.String()).Scan(&terminal, &retry, &success)
			must(err)
			err = db.Pool.QueryRow(ctx, "SELECT count(*) FROM audit_log WHERE action=$1 AND job_id=$2", observability.ActionUploadScanRecovery, rec.ID).Scan(&operatorEvents)
			must(err)
			if terminal != 1 || retry != 1 || success != 1 || operatorEvents != 2 {
				t.Fatalf("terminal=%d retry=%d success=%d audits=%d", terminal, retry, success, operatorEvents)
			}
			again, err := queueRetainedUpload(ctx, db, blobs, p, true, 100, rec.ID)
			must(err)
			if again != "previously queued, current job state done" {
				t.Fatalf("finished replay: %s", again)
			}
			if _, err := q.GetCurrentRetainedScanRecovery(ctx, rec.ID); !errors.Is(err, pgx.ErrNoRows) {
				t.Fatal("completed job still eligible")
			}
		})
	}
}
