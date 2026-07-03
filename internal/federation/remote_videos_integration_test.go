//go:build integration

// Integration test: proves remote-video ingestion (Create → upsert by
// object_url) and the instance-blocklist effects persist against a REAL
// PostgreSQL (migrations applied). Run via `make test-integration`.
package federation_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/federation"
	"github.com/vidra/vidra-core/internal/store"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// allowAllEdges satisfies federation.FollowEdgeChecker for the ingestion gate.
type allowAllEdges struct{}

func (allowAllEdges) HasAcceptedFollow(context.Context, string) (bool, error) { return true, nil }

func TestRemoteVideoIngestionPersists(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	st, err := store.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	// Unique per run so reruns never collide.
	suf := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	domain := "origin-" + suf + ".example"
	actorURL := "https://" + domain + "/video-channels/movies"
	objectURL := "https://" + domain + "/videos/watch/1"

	if err := q.UpsertRemoteActor(ctx, sqlcgen.UpsertRemoteActorParams{
		ActorUrl:          actorURL,
		ActorType:         "Group",
		PreferredUsername: "movies",
		Domain:            domain,
		InboxUrl:          "https://" + domain + "/inbox",
		PublicKeyPem:      "-----BEGIN PUBLIC KEY-----\nMA==\n-----END PUBLIC KEY-----",
		FollowersUrl:      actorURL + "/followers",
	}); err != nil {
		t.Fatalf("UpsertRemoteActor: %v", err)
	}

	svc := federation.NewService(q,
		federation.WithBaseURL("https://videos.example"),
		federation.WithFollowEdgeChecker(allowAllEdges{}),
	)

	create := `{"id":"https://` + domain + `/act/1","type":"Create","actor":"` + actorURL + `","object":{` +
		`"id":"` + objectURL + `","type":"Video","name":"Persisted clip","content":"body",` +
		`"attributedTo":"` + actorURL + `","published":"2026-06-30T12:00:00Z","duration":"PT90S",` +
		`"url":[{"type":"Link","mediaType":"application/x-mpegURL","href":"https://` + domain + `/hls/1.m3u8"}]}}`
	if err := svc.HandleInbox(ctx, actorURL, []byte(create)); err != nil {
		t.Fatalf("HandleInbox Create: %v", err)
	}

	// Upserting the same object_url returns the SAME row (idempotent ingestion)
	// and hands us its id for the read-model assertions.
	row1, err := q.UpsertRemoteVideo(ctx, sqlcgen.UpsertRemoteVideoParams{
		ObjectUrl: objectURL, RemoteActorUrl: actorURL, Title: "Updated title", WatchUrl: objectURL,
	})
	if err != nil {
		t.Fatalf("UpsertRemoteVideo: %v", err)
	}
	row2, err := q.UpsertRemoteVideo(ctx, sqlcgen.UpsertRemoteVideoParams{
		ObjectUrl: objectURL, RemoteActorUrl: actorURL, Title: "Updated title 2", WatchUrl: objectURL,
	})
	if err != nil {
		t.Fatalf("re-UpsertRemoteVideo: %v", err)
	}
	if row1.ID != row2.ID {
		t.Fatalf("upsert minted a new id (%s vs %s) for one object_url", row1.ID, row2.ID)
	}

	got, err := q.GetRemoteVideoByID(ctx, row1.ID)
	if err != nil {
		t.Fatalf("GetRemoteVideoByID: %v", err)
	}
	if got.Domain != domain || got.Title != "Updated title 2" {
		t.Errorf("read model = domain %q title %q", got.Domain, got.Title)
	}

	// Thumbnail key round-trip.
	key := "remote-thumbnails/" + row1.ID.String() + ".jpg"
	if err := q.SetRemoteVideoThumbnail(ctx, sqlcgen.SetRemoteVideoThumbnailParams{ID: row1.ID, ThumbnailKey: &key}); err != nil {
		t.Fatalf("SetRemoteVideoThumbnail: %v", err)
	}
	got, _ = q.GetRemoteVideoByID(ctx, row1.ID)
	if got.ThumbnailKey == nil || *got.ThumbnailKey != key {
		t.Errorf("thumbnail_key = %v, want %q", got.ThumbnailKey, key)
	}

	// Blocking the origin hides the video from the read model and drops new
	// inbound activities without marking them processed.
	if _, err := q.BlockInstance(ctx, sqlcgen.BlockInstanceParams{Domain: domain, Reason: "integration"}); err != nil {
		t.Fatalf("BlockInstance: %v", err)
	}
	t.Cleanup(func() { _, _ = q.UnblockInstance(context.Background(), domain) })

	if blocked, err := q.IsInstanceBlocked(ctx, domain); err != nil || !blocked {
		t.Fatalf("IsInstanceBlocked = %v, %v; want true", blocked, err)
	}
	if _, err := q.GetRemoteVideoByID(ctx, row1.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("blocked-domain video still readable (err = %v, want ErrNoRows)", err)
	}

	droppedID := "https://" + domain + "/act/2"
	dropped := `{"id":"` + droppedID + `","type":"Create","actor":"` + actorURL + `","object":{` +
		`"id":"https://` + domain + `/videos/watch/2","type":"Video","name":"Dropped","attributedTo":"` + actorURL + `"}}`
	if err := svc.HandleInbox(ctx, actorURL, []byte(dropped)); err != nil {
		t.Fatalf("HandleInbox (blocked): %v", err)
	}
	if seen, _ := q.IsActivityProcessed(ctx, droppedID); seen {
		t.Error("activity from a blocked domain was marked processed")
	}

	// Unblocking restores visibility.
	if _, err := q.UnblockInstance(ctx, domain); err != nil {
		t.Fatalf("UnblockInstance: %v", err)
	}
	if _, err := q.GetRemoteVideoByID(ctx, row1.ID); err != nil {
		t.Errorf("video not visible again after unblock: %v", err)
	}
}
