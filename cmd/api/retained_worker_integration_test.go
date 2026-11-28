//go:build integration

package main

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/store"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
	"github.com/vidra/vidra-core/internal/video"
)

type retainedTestScan struct {
	verify func() (bool, error)
}

func (s retainedTestScan) Scan(context.Context, string) (bool, error) {
	return false, errors.New("unexpected unbound scan")
}

func (s retainedTestScan) ScanVerified(context.Context, string, int64, string) (bool, error) {
	return s.verify()
}

func (s retainedTestScan) Probe(context.Context, string) (media.Metadata, error) {
	return media.Metadata{DurationSeconds: 1}, nil
}

func TestRetainedScanHonorsPublicationHolds(t *testing.T) {
	ctx := t.Context()
	st, err := store.New(ctx, verifyThrowawayDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	q := st.Queries()
	for _, tc := range []struct {
		name, state     string
		encodes, events int
	}{
		{"live HLS job", "transcoding", 1, 0},
		{"no HLS job", "published", 1, 1},
		{"future schedule", "scheduled", 0, 0},
		{"quarantine", "quarantined", 0, 0},
		{"expired during scan", "failed", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			p, id, _ := newRetainedSourceFixture(t, st)
			src, err := q.GetRetainedScanSource(ctx, p)
			must(err)
			job, err := q.EnqueueUploadFinalizeJob(ctx, sqlcgen.EnqueueUploadFinalizeJobParams{UploadID: p.UploadID, VideoID: id, Purpose: "upload"})
			must(err)
			must(q.RecordRetainedScanRecovery(ctx, sqlcgen.RecordRetainedScanRecoveryParams{
				JobID: job.ID, UploadID: p.UploadID, FileID: src.FileID, OwnerID: src.OwnerID, ChannelID: src.ChannelID, Privacy: src.Privacy, Sha256: src.Sha256, SizeBytes: src.SizeBytes,
			}))
			_, err = q.MarkRetainedUploadQueued(ctx, p.UploadID)
			must(err)
			_, err = st.Pool.Exec(ctx, "UPDATE videos SET publish_after_transcode=true WHERE id=$1", id)
			must(err)
			if tc.name == "future schedule" {
				_, err = st.Pool.Exec(ctx, "UPDATE videos SET publish_at=now()+interval '1 hour' WHERE id=$1", id)
				must(err)
			}
			scanner := retainedTestScan{verify: func() (bool, error) {
				if tc.name == "expired during scan" {
					_, err := st.Pool.Exec(ctx, "UPDATE upload_sessions SET expires_at=now() WHERE id=$1", p.UploadID)
					must(err)
				}
				return true, nil
			}}
			encodes, events := 0, 0
			svc := video.NewService(q, nil, video.WithScanner(scanner), video.WithProber(scanner), video.WithScanMode("fail-closed"),
				video.WithQuarantineNewUploads(tc.name == "quarantine"), video.WithTranscodeReadinessFunc(func() bool { return true }),
				video.WithLiveTranscodeChecker(func(context.Context, uuid.UUID) bool { return tc.name == "live HLS job" }),
				video.WithTranscodeHook(func(context.Context, uuid.UUID, string) { encodes++ }), video.WithPublishHook(func(context.Context, uuid.UUID) { events++ }))
			err = svc.ProcessRetained(ctx, job.ID, id)
			if tc.state == "failed" {
				if !errors.Is(err, video.ErrRecoveryIneligible) {
					t.Fatalf("expired recovery: %v", err)
				}
			} else {
				must(err)
			}
			v, err := q.GetVideoByID(ctx, id)
			must(err)
			if v.State != tc.state || v.Privacy != "private" || encodes != tc.encodes || events != tc.events {
				t.Fatalf("state=%s privacy=%s encodes=%d publishes=%d", v.State, v.Privacy, encodes, events)
			}
		})
	}
}
