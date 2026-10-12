//go:build integration

// Integration test: the instance actor shares only what public discovery
// shows, on a REAL PostgreSQL — the outbox page, the new-follower backfill and
// the live Announce all read the instance_outbox_videos view (migration 0166).
package federation_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/federation"
	"github.com/vidra/vidra-core/internal/store"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func TestInstanceSharesNothingOfAnOwnerWhoOptedOutOfDiscovery(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := store.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()
	suf := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]

	// One owner who opted out of discovery, one who did not; a public,
	// published video each.
	video := func(name string, unlisted bool) uuid.UUID {
		t.Helper()
		u, err := q.CreateUser(ctx, sqlcgen.CreateUserParams{Username: name + suf, Email: name + suf + "@example.test", PasswordHash: "x", Role: "user"})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, u.ID) })
		if _, err := st.Pool.Exec(ctx, `UPDATE users SET unlisted = $2 WHERE id = $1`, u.ID, unlisted); err != nil {
			t.Fatal(err)
		}
		var ch, v uuid.UUID
		if err := st.Pool.QueryRow(ctx, `INSERT INTO channels (owner_id, handle, display_name) VALUES ($1, $2, 'c') RETURNING id`, u.ID, name+"_ch_"+suf).Scan(&ch); err != nil {
			t.Fatal(err)
		}
		if err := st.Pool.QueryRow(ctx, `INSERT INTO videos (channel_id, title, privacy, state) VALUES ($1, 't', 'public', 'published') RETURNING id`, ch).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	hidden, shown := video("optout", true), video("listed", false)

	// A PeerTube instance actor, cached as signature verification leaves it.
	host := "pt-" + suf + ".example"
	follower := "https://" + host + "/accounts/peertube"
	inbox := follower + "/inbox"
	if err := q.UpsertRemoteActor(ctx, sqlcgen.UpsertRemoteActorParams{ActorUrl: follower, ActorType: "Application",
		PreferredUsername: "peertube", Domain: host, InboxUrl: inbox, PublicKeyPem: "k"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = st.Pool.Exec(ctx, `DELETE FROM federation_deliveries WHERE inbox_url = $1`, inbox)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM remote_actors WHERE actor_url = $1`, follower)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM instance_follower_backfills WHERE host = $1`, host)
	})
	announced := func() map[string]bool {
		t.Helper()
		rows, err := st.Pool.Query(ctx, `SELECT payload FROM federation_deliveries WHERE inbox_url = $1`, inbox)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := map[string]bool{}
		for rows.Next() {
			var raw []byte
			if err := rows.Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var a struct{ Type, Object string }
			if json.Unmarshal(raw, &a) == nil && a.Type == "Announce" {
				out[a.Object] = true
			}
		}
		return out
	}
	const base = "https://videos.example"
	svc := federation.NewService(q, federation.WithBaseURL(base))

	// The backfill a new instance follower gets.
	follow := `{"id":"https://` + host + `/follows/1","type":"Follow","actor":"` + follower + `","object":"` + base + `/accounts/peertube"}`
	if err := svc.HandleInbox(ctx, follower, []byte(follow)); err != nil {
		t.Fatalf("HandleInbox(Follow): %v", err)
	}
	if got := announced(); got[base+"/videos/"+hidden.String()] {
		t.Error("the backfill announced an opted-out owner's video")
	}

	// The live Announce at publish time.
	for _, v := range []uuid.UUID{hidden, shown} {
		if err := svc.AnnounceVideo(ctx, v); err != nil {
			t.Fatal(err)
		}
	}
	got := announced()
	if got[base+"/videos/"+hidden.String()] {
		t.Error("the live Announce shared an opted-out owner's video")
	}
	if !got[base+"/videos/"+shown.String()] {
		t.Error("the listed owner's video was not announced (the test proves nothing)")
	}

	// The anonymous outbox.
	for page := 1; ; page++ {
		pg, err := svc.InstanceOutboxPage(ctx, page)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range pg.OrderedItems {
			if item["object"] == base+"/videos/"+hidden.String() {
				t.Fatal("the instance outbox lists an opted-out owner's video")
			}
		}
		if pg.Next == "" {
			break
		}
	}
}
