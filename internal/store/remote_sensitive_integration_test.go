//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// A remote video's origin `sensitive` flag on the discovery surfaces (T05).
// Every remote arm of the public feed and search used to hard-code
// `false AS is_sensitive` and skip the hide_sensitive predicate, so under the
// "hide" policy a remote NSFW video still surfaced — labelled safe. The
// remote arms now carry rv.is_sensitive and honour hide_sensitive exactly as
// the local arms do, and the hide-policy counts move by exactly this row.
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
	defer func() {
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM remote_actors WHERE actor_url = $1`, actor)
	}()
	row, err := q.UpsertRemoteVideo(ctx, sqlcgen.UpsertRemoteVideoParams{
		ObjectUrl: "https://" + domain + "/videos/1", RemoteActorUrl: actor, Title: title,
		WatchUrl: "https://" + domain + "/w/1", IsSensitive: true,
	})
	if err != nil {
		t.Fatalf("UpsertRemoteVideo: %v", err)
	}

	feed := func(hide bool) (found, flagged bool) {
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
		return found, flagged
	}
	search := func(hide bool) (found, flagged bool) {
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
		return found, flagged
	}

	for _, surface := range []struct {
		name string
		read func(bool) (bool, bool)
	}{{"public feed", feed}, {"search", search}} {
		shown, flagged := surface.read(false)
		if !shown || !flagged {
			t.Errorf("%s, warn/blur/display: found=%v is_sensitive=%v, want the video WITH its flag", surface.name, shown, flagged)
		}
		if hidden, _ := surface.read(true); hidden {
			t.Errorf("%s, hide: the sensitive remote video still surfaced", surface.name)
		}
	}

	// The counts must move with the list by EXACTLY this row. Measured inside
	// one REPEATABLE READ snapshot, toggling only this row's flag, so another
	// package's concurrent writes cannot satisfy (or break) the assertion.
	tx, err := st.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	tq := sqlcgen.New(tx)
	counts := func() (feedN, searchN int64) {
		t.Helper()
		if feedN, err = tq.CountPublicVideosSorted(ctx, sqlcgen.CountPublicVideosSortedParams{HideSensitive: true, IncludeRemote: true}); err != nil {
			t.Fatalf("CountPublicVideosSorted: %v", err)
		}
		if searchN, err = tq.CountSearchPublicVideos(ctx, sqlcgen.CountSearchPublicVideosParams{Query: title, HideSensitive: true}); err != nil {
			t.Fatalf("CountSearchPublicVideos: %v", err)
		}
		return feedN, searchN
	}
	feedFlagged, searchFlagged := counts()
	if _, err := tq.UpsertRemoteVideo(ctx, sqlcgen.UpsertRemoteVideoParams{
		ObjectUrl: "https://" + domain + "/videos/1", RemoteActorUrl: actor, Title: title,
		WatchUrl: "https://" + domain + "/w/1", IsSensitive: false,
	}); err != nil {
		t.Fatalf("UpsertRemoteVideo (unflag): %v", err)
	}
	feedClear, searchClear := counts()
	if feedClear-feedFlagged != 1 || searchClear-searchFlagged != 1 {
		t.Errorf("hide-policy counts after un-flagging this row: feed %d→%d, search %d→%d; want each +1 exactly",
			feedFlagged, feedClear, searchFlagged, searchClear)
	}
}
