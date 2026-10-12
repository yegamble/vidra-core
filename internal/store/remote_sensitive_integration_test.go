//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// A remote video's origin `sensitive` flag on the discovery surfaces (T05).
// Every remote arm of the public feed and search used to hard-code
// `false AS is_sensitive` and skip the hide_sensitive predicate, so under the
// "hide" policy a remote NSFW video still surfaced — labelled safe. The
// remote arms now carry rv.is_sensitive and honour hide_sensitive exactly as
// the local arms do; list and count agree.
func TestRemoteSensitiveVideoFollowsTheHidePolicy(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	suffix := uuid.NewString()[:8]
	domain := "nsfw-" + suffix + ".test"
	actor := "https://" + domain + "/video-channels/late"
	title := "Late show " + suffix
	if err := q.UpsertRemoteActor(ctx, sqlcgen.UpsertRemoteActorParams{
		ActorUrl: actor, ActorType: "Group", PreferredUsername: "late", Domain: domain,
		InboxUrl: actor + "/inbox", PublicKeyPem: "pem", FollowersUrl: actor + "/followers",
	}); err != nil {
		t.Fatalf("UpsertRemoteActor: %v", err)
	}
	defer func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM remote_actors WHERE actor_url = $1`, actor) }()
	row, err := q.UpsertRemoteVideo(ctx, sqlcgen.UpsertRemoteVideoParams{
		ObjectUrl: "https://" + domain + "/videos/1", RemoteActorUrl: actor, Title: title,
		WatchUrl: "https://" + domain + "/w/1", IsSensitive: true,
	})
	if err != nil {
		t.Fatalf("UpsertRemoteVideo: %v", err)
	}

	feed := func(hide bool) (found, flagged bool, total int64) {
		t.Helper()
		rows, err := q.ListPublicVideosSorted(ctx, sqlcgen.ListPublicVideosSortedParams{
			HideSensitive: hide, IncludeRemote: true, Sort: "recent", ResultLimit: 10000,
		})
		if err != nil {
			t.Fatalf("ListPublicVideosSorted: %v", err)
		}
		for _, r := range rows {
			if r.ID == row.ID {
				found, flagged = true, r.IsSensitive
			}
		}
		total, err = q.CountPublicVideosSorted(ctx, sqlcgen.CountPublicVideosSortedParams{HideSensitive: hide, IncludeRemote: true})
		if err != nil {
			t.Fatalf("CountPublicVideosSorted: %v", err)
		}
		return found, flagged, total
	}
	search := func(hide bool) (found, flagged bool, total int64) {
		t.Helper()
		rows, err := q.SearchPublicVideos(ctx, sqlcgen.SearchPublicVideosParams{
			Query: title, HideSensitive: hide, Sort: "relevance", ResultLimit: 50,
		})
		if err != nil {
			t.Fatalf("SearchPublicVideos: %v", err)
		}
		for _, r := range rows {
			if r.ID == row.ID {
				found, flagged = true, r.IsSensitive
			}
		}
		total, err = q.CountSearchPublicVideos(ctx, sqlcgen.CountSearchPublicVideosParams{Query: title, HideSensitive: hide})
		if err != nil {
			t.Fatalf("CountSearchPublicVideos: %v", err)
		}
		return found, flagged, total
	}

	for _, surface := range []struct {
		name string
		read func(bool) (bool, bool, int64)
	}{{"public feed", feed}, {"search", search}} {
		shown, flagged, totalShown := surface.read(false)
		if !shown || !flagged {
			t.Errorf("%s, warn/blur/display: found=%v is_sensitive=%v, want the video WITH its flag", surface.name, shown, flagged)
		}
		hidden, _, totalHidden := surface.read(true)
		if hidden {
			t.Errorf("%s, hide: the sensitive remote video still surfaced", surface.name)
		}
		if totalShown-totalHidden < 1 {
			t.Errorf("%s: count %d (shown) vs %d (hidden) — the count must drop with the list", surface.name, totalShown, totalHidden)
		}
	}
}
