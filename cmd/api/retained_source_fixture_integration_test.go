//go:build integration

package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
	"github.com/vidra/vidra-core/internal/video"
)

func newRetainedSourceFixture(t *testing.T, st *store.Store) (sqlcgen.GetRetainedScanSourceParams, uuid.UUID, *storage.Local) {
	t.Helper()
	ctx, q := t.Context(), st.Queries()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	handle := "r" + uuid.NewString()[:8]
	u, err := q.CreateUser(ctx, sqlcgen.CreateUserParams{Username: handle, Email: handle + "@example.test", PasswordHash: "unused-test-hash", Role: "user"})
	must(err)
	c, err := q.CreateChannel(ctx, sqlcgen.CreateChannelParams{OwnerID: u.ID, Handle: handle + "-channel", DisplayName: "Fixture"})
	must(err)
	v, err := q.CreateVideo(ctx, sqlcgen.CreateVideoParams{ChannelID: c.ID, Title: "Synthetic", Privacy: "private", CommentsPolicy: "enabled"})
	must(err)
	_, err = q.SetVideoState(ctx, sqlcgen.SetVideoStateParams{ID: v.ID, State: "failed"})
	must(err)
	hash := sha256Hex("abc")
	blobs, err := storage.NewLocal(t.TempDir())
	must(err)
	_, err = blobs.Put(ctx, "web-videos/"+v.ID.String()+".mp4", strings.NewReader("abc"))
	must(err)
	_, err = q.CreateVideoFile(ctx, sqlcgen.CreateVideoFileParams{VideoID: v.ID, Kind: "original", StorageKey: "web-videos/" + v.ID.String() + ".mp4", SizeBytes: 3, Sha256: hash})
	must(err)
	s, err := q.CreateUploadSession(ctx, sqlcgen.CreateUploadSessionParams{VideoID: v.ID, ChannelID: c.ID, UserID: u.ID, Filename: "fixture.mp4", TotalSize: 3, ChunkSize: 8, ExpiresAt: time.Now().Add(time.Hour), Purpose: "upload"})
	must(err)
	old, err := q.EnqueueUploadFinalizeJob(ctx, sqlcgen.EnqueueUploadFinalizeJobParams{UploadID: s.ID, VideoID: v.ID, Purpose: "upload"})
	must(err)
	must(audit.NewService(q).Record(ctx, audit.Event{Actor: audit.ActorSnapshot{Kind: "system"}, Action: observability.ActionUploadMalwareRejected, Result: "failure", Reason: "video=" + v.ID.String() + " outcome=scan_error policy=fail-closed"}))
	must(q.FailUploadFinalizeJob(ctx, sqlcgen.FailUploadFinalizeJobParams{ID: old.ID, Error: video.SafetyScanRejectedMessage}))
	must(q.FailUploadSession(ctx, sqlcgen.FailUploadSessionParams{ID: s.ID, FailureReason: video.SafetyScanRejectedMessage}))
	p := sqlcgen.GetRetainedScanSourceParams{UploadID: s.ID, OwnerID: u.ID, Privacy: "private", Sha256: hash}
	return p, v.ID, blobs
}

func TestRetainedSourceRequiresCurrentOriginal(t *testing.T) {
	ctx := t.Context()
	st, err := store.New(ctx, verifyThrowawayDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	for _, change := range []string{"replaced file", "changed owner", "expired session"} {
		t.Run(change, func(t *testing.T) {
			p, id, _ := newRetainedSourceFixture(t, st)
			if _, err := st.Queries().GetRetainedScanSource(ctx, p); err != nil {
				t.Fatal(err)
			}
			switch change {
			case "replaced file":
				_, err = st.Pool.Exec(ctx, "UPDATE video_files SET sha256=$1 WHERE video_id=$2 AND kind='original'", sha256Hex("replacement"), id)
			case "changed owner":
				handle := "other-" + uuid.NewString()[:8]
				owner, createErr := st.Queries().CreateUser(ctx, sqlcgen.CreateUserParams{Username: handle, Email: handle + "@example.test", PasswordHash: "unused-test-hash", Role: "user"})
				if createErr != nil {
					t.Fatal(createErr)
				}
				_, err = st.Pool.Exec(ctx, "UPDATE channels SET owner_id=$1 WHERE id=(SELECT channel_id FROM videos WHERE id=$2)", owner.ID, id)
			case "expired session":
				_, err = st.Pool.Exec(ctx, "UPDATE upload_sessions SET expires_at=now()-interval '1 second' WHERE id=$1", p.UploadID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = st.Queries().GetRetainedScanSource(ctx, p); !errors.Is(err, pgx.ErrNoRows) {
				t.Fatalf("changed source accepted: %v", err)
			}
		})
	}
}
