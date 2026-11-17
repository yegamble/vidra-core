//go:build integration

package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// UpdateRemoteVideo can only UPDATE: a video its origin deleted is not put
// back by an edit that was already in flight, which is what an upsert did.
func TestUpdateRemoteVideoNeverInserts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	domain := "edit-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12] + ".example"
	actorURL, objectURL := "https://"+domain+"/video-channels/films", "https://"+domain+"/videos/watch/1"
	if err := q.UpsertRemoteActor(ctx, sqlcgen.UpsertRemoteActorParams{
		ActorUrl: actorURL, ActorType: "Group", PreferredUsername: "films", Domain: domain,
		InboxUrl: actorURL + "/inbox", PublicKeyPem: "k",
	}); err != nil {
		t.Fatalf("UpsertRemoteActor: %v", err)
	}
	t.Cleanup(func() { _, _ = q.DeleteRemoteActor(context.Background(), actorURL) })
	edit := sqlcgen.UpdateRemoteVideoParams{ObjectUrl: objectURL, RemoteActorUrl: actorURL, Title: "edited", WatchUrl: objectURL}
	held, err := q.UpsertRemoteVideo(ctx, sqlcgen.UpsertRemoteVideoParams{
		ObjectUrl: objectURL, RemoteActorUrl: actorURL, Title: "original", WatchUrl: objectURL,
	})
	if err != nil {
		t.Fatalf("UpsertRemoteVideo: %v", err)
	}
	edited, err := q.UpdateRemoteVideo(ctx, edit)
	if err != nil || edited.ID != held.ID {
		t.Fatalf("UpdateRemoteVideo = %+v, %v; want the held row %s", edited, err, held.ID)
	}
	if got, err := q.GetRemoteVideoByID(ctx, held.ID); err != nil || got.Title != "edited" || got.SensitiveFlags == nil {
		t.Fatalf("after the edit: title %q, flags %v, err %v; want the edit and '{}' flags", got.Title, got.SensitiveFlags, err)
	}
	// The origin's Delete lands; the edit that was already in flight follows.
	if _, err := q.DeleteRemoteVideoByObjectURL(ctx, objectURL); err != nil {
		t.Fatalf("DeleteRemoteVideoByObjectURL: %v", err)
	}
	if _, err := q.UpdateRemoteVideo(ctx, edit); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("UpdateRemoteVideo of a deleted video: err = %v, want ErrNoRows", err)
	}
	if _, err := q.GetRemoteVideoByObjectURL(ctx, objectURL); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("the edit put the deleted video back (err = %v)", err)
	}
}
