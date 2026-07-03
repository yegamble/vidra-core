//go:build integration

// Integration tests: prove the outbound remote-channel follow model persists
// against a REAL PostgreSQL (migrations applied), that the UNION feed queries
// surface remote videos with their moderation filters, and — the §9 bar — the
// FULL federation loop against an httptest "remote instance": WebFinger →
// actor → signed Follow delivery → Accept → Create ingests → the video appears
// in that user's subscriptions feed. Run via `make test-integration`.
package federation_test

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/federation"
	"github.com/vidra/vidra-core/internal/httpsig"
	"github.com/vidra/vidra-core/internal/store"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// integrationQueries connects to the integration database, skipping when it is
// not configured.
func integrationQueries(t *testing.T) (context.Context, *sqlcgen.Queries) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	st, err := store.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(st.Close)
	return ctx, st.Queries()
}

// seedUser creates a unique local user.
func seedUser(t *testing.T, ctx context.Context, q *sqlcgen.Queries, suf string) sqlcgen.User {
	t.Helper()
	u, err := q.CreateUser(ctx, sqlcgen.CreateUserParams{
		Username:     "rcf_" + suf,
		Email:        "rcf_" + suf + "@example.test",
		PasswordHash: "x",
		Role:         "user",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	return u
}

// seedRemoteActor caches a remote channel actor row.
func seedRemoteActor(t *testing.T, ctx context.Context, q *sqlcgen.Queries, actorURL, domain string) {
	t.Helper()
	if err := q.UpsertRemoteActor(ctx, sqlcgen.UpsertRemoteActorParams{
		ActorUrl: actorURL, ActorType: "Group", PreferredUsername: "movies",
		Domain: domain, InboxUrl: "https://" + domain + "/inbox",
		PublicKeyPem: "PEM", FollowersUrl: actorURL + "/followers",
	}); err != nil {
		t.Fatalf("UpsertRemoteActor: %v", err)
	}
}

func TestRemoteChannelFollowPersists(t *testing.T) {
	ctx, q := integrationQueries(t)
	suf := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	u := seedUser(t, ctx, q, suf)
	domain := "follow-" + suf + ".example"
	actorURL := "https://" + domain + "/video-channels/movies"
	seedRemoteActor(t, ctx, q, actorURL, domain)

	followURL := "https://videos.example/accounts/" + u.Username + "/activities/follow/" + uuid.NewString()
	row, err := q.UpsertRemoteChannelFollow(ctx, sqlcgen.UpsertRemoteChannelFollowParams{
		UserID: u.ID, RemoteActorUrl: actorURL, FollowActivityUrl: followURL,
	})
	if err != nil {
		t.Fatalf("UpsertRemoteChannelFollow: %v", err)
	}
	if row.State != "pending" || row.FollowActivityUrl != followURL {
		t.Fatalf("inserted row = %+v", row)
	}

	// Re-upsert (idempotent re-follow) keeps the existing row + activity URL.
	row2, err := q.UpsertRemoteChannelFollow(ctx, sqlcgen.UpsertRemoteChannelFollowParams{
		UserID: u.ID, RemoteActorUrl: actorURL, FollowActivityUrl: "https://videos.example/other",
	})
	if err != nil {
		t.Fatalf("re-UpsertRemoteChannelFollow: %v", err)
	}
	if row2.ID != row.ID || row2.FollowActivityUrl != followURL {
		t.Errorf("re-upsert changed the row: %+v", row2)
	}

	// An Accept "signed" by a different actor matches nothing.
	if n, _ := q.AcceptRemoteChannelFollowByActivity(ctx, sqlcgen.AcceptRemoteChannelFollowByActivityParams{
		FollowActivityUrl: followURL, RemoteActorUrl: "https://evil.example/actor",
	}); n != 0 {
		t.Fatalf("foreign Accept matched %d rows, want 0", n)
	}
	if ok, _ := q.HasAcceptedRemoteChannelFollow(ctx, actorURL); ok {
		t.Fatal("edge accepted before the real Accept")
	}
	// The real Accept flips it.
	if n, err := q.AcceptRemoteChannelFollowByActivity(ctx, sqlcgen.AcceptRemoteChannelFollowByActivityParams{
		FollowActivityUrl: followURL, RemoteActorUrl: actorURL,
	}); err != nil || n != 1 {
		t.Fatalf("Accept matched %d rows (err=%v), want 1", n, err)
	}
	if ok, _ := q.HasAcceptedRemoteChannelFollow(ctx, actorURL); !ok {
		t.Error("edge not accepted after Accept")
	}

	// The list carries the actor identity.
	list, err := q.ListRemoteChannelFollows(ctx, sqlcgen.ListRemoteChannelFollowsParams{
		UserID: u.ID, ResultLimit: 10,
	})
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %d rows (err=%v), want 1", len(list), err)
	}
	if list[0].PreferredUsername != "movies" || list[0].Domain != domain || list[0].State != "accepted" {
		t.Errorf("list row = %+v", list[0])
	}

	// Deleting is scoped to the owner.
	if n, _ := q.DeleteRemoteChannelFollowByID(ctx, sqlcgen.DeleteRemoteChannelFollowByIDParams{
		ID: row.ID, UserID: uuid.New(),
	}); n != 0 {
		t.Error("another user could delete the follow")
	}
	if n, _ := q.DeleteRemoteChannelFollowByID(ctx, sqlcgen.DeleteRemoteChannelFollowByIDParams{
		ID: row.ID, UserID: u.ID,
	}); n != 1 {
		t.Error("owner delete did not remove the row")
	}
}

func TestSubscriptionAndDiscoveryUnionsPersist(t *testing.T) {
	ctx, q := integrationQueries(t)
	suf := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	u := seedUser(t, ctx, q, suf)
	domain := "union-" + suf + ".example"
	actorURL := "https://" + domain + "/video-channels/movies"
	seedRemoteActor(t, ctx, q, actorURL, domain)

	title := "Union premiere " + suf
	if _, err := q.UpsertRemoteVideo(ctx, sqlcgen.UpsertRemoteVideoParams{
		ObjectUrl: "https://" + domain + "/videos/watch/1", RemoteActorUrl: actorURL,
		Title: title, WatchUrl: "https://" + domain + "/videos/watch/1",
	}); err != nil {
		t.Fatalf("UpsertRemoteVideo: %v", err)
	}

	subs := func() []sqlcgen.ListSubscriptionVideosRow {
		rows, err := q.ListSubscriptionVideos(ctx, sqlcgen.ListSubscriptionVideosParams{
			FollowerID: u.ID, ResultLimit: 50,
		})
		if err != nil {
			t.Fatalf("ListSubscriptionVideos: %v", err)
		}
		var remote []sqlcgen.ListSubscriptionVideosRow
		for _, r := range rows {
			if r.Remote {
				remote = append(remote, r)
			}
		}
		return remote
	}

	// No follow yet → no remote entries.
	if got := subs(); len(got) != 0 {
		t.Fatalf("subscriptions before follow carry %d remote rows", len(got))
	}

	// A pending follow is not enough; an ACCEPTED one surfaces the video.
	followURL := "https://videos.example/accounts/" + u.Username + "/activities/follow/x"
	if _, err := q.UpsertRemoteChannelFollow(ctx, sqlcgen.UpsertRemoteChannelFollowParams{
		UserID: u.ID, RemoteActorUrl: actorURL, FollowActivityUrl: followURL,
	}); err != nil {
		t.Fatalf("UpsertRemoteChannelFollow: %v", err)
	}
	if got := subs(); len(got) != 0 {
		t.Fatalf("a PENDING follow surfaced %d remote rows", len(got))
	}
	if _, err := q.AcceptRemoteChannelFollowByActivity(ctx, sqlcgen.AcceptRemoteChannelFollowByActivityParams{
		FollowActivityUrl: followURL, RemoteActorUrl: actorURL,
	}); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	got := subs()
	if len(got) != 1 {
		t.Fatalf("subscriptions carry %d remote rows, want 1", len(got))
	}
	if got[0].Title != title || got[0].Domain != domain || got[0].ChannelHandle != "movies@"+domain {
		t.Errorf("remote card = %+v", got[0])
	}

	// The user's instance mute hides it; unmuting restores it.
	if _, err := q.MuteInstance(ctx, sqlcgen.MuteInstanceParams{MuterID: u.ID, Domain: domain}); err != nil {
		t.Fatalf("MuteInstance: %v", err)
	}
	if got := subs(); len(got) != 0 {
		t.Error("muted instance still surfaced in subscriptions")
	}
	if _, err := q.UnmuteInstance(ctx, sqlcgen.UnmuteInstanceParams{MuterID: u.ID, Domain: domain}); err != nil {
		t.Fatalf("UnmuteInstance: %v", err)
	}

	// Search UNION: the remote video matches by title, flagged remote.
	search := func() int {
		rows, err := q.SearchPublicVideos(ctx, sqlcgen.SearchPublicVideosParams{
			Query: "Union premiere " + suf, ResultLimit: 50,
		})
		if err != nil {
			t.Fatalf("SearchPublicVideos: %v", err)
		}
		n := 0
		for _, r := range rows {
			if r.Remote && r.Domain == domain {
				n++
			}
		}
		return n
	}
	if n := search(); n != 1 {
		t.Errorf("search finds %d remote rows, want 1", n)
	}

	// Feed UNION: only scope=all (include_remote) shows it.
	feed := func(includeRemote bool) int {
		rows, err := q.ListPublicVideosSorted(ctx, sqlcgen.ListPublicVideosSortedParams{
			Sort: "recent", IncludeRemote: includeRemote, ResultLimit: 100,
		})
		if err != nil {
			t.Fatalf("ListPublicVideosSorted: %v", err)
		}
		n := 0
		for _, r := range rows {
			if r.Remote && r.Domain == domain {
				n++
			}
		}
		return n
	}
	if n := feed(false); n != 0 {
		t.Errorf("local-scope feed leaked %d remote rows", n)
	}
	if n := feed(true); n != 1 {
		t.Errorf("scope=all feed shows %d remote rows, want 1", n)
	}

	// The admin blocklist hides it everywhere.
	if _, err := q.BlockInstance(ctx, sqlcgen.BlockInstanceParams{Domain: domain, Reason: "integration"}); err != nil {
		t.Fatalf("BlockInstance: %v", err)
	}
	t.Cleanup(func() { _, _ = q.UnblockInstance(context.Background(), domain) })
	if len(subs()) != 0 || search() != 0 || feed(true) != 0 {
		t.Error("blocked instance still surfaced in a feed")
	}
}

// TestRemoteFollowFullLoop is the §9 acceptance bar: an httptest "remote
// instance" serves WebFinger + its channel actor and captures the signed
// Follow its inbox receives; the test answers with Accept, then delivers a
// Create{Video} — which must ingest (the edge gate now passes) and appear in
// the following user's subscriptions feed.
func TestRemoteFollowFullLoop(t *testing.T) {
	ctx, q := integrationQueries(t)
	suf := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	u := seedUser(t, ctx, q, suf)

	svc := federation.NewService(q,
		federation.WithBaseURL("https://videos.example"),
		// Dev/e2e posture so the loopback httptest origin is reachable (and the
		// https-first WebFinger can fall back to plain http).
		federation.WithAllowPrivateFetch(true),
	)

	// --- the remote instance ------------------------------------------------
	type inboxHit struct {
		keyID string
		body  []byte
		err   error
	}
	hits := make(chan inboxHit, 1)
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/.well-known/webfinger", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"subject": r.URL.Query().Get("resource"),
			"links": []map[string]string{{
				"rel": "self", "type": "application/activity+json",
				"href": srv.URL + "/video-channels/movies",
			}},
		})
	})
	mux.HandleFunc("/video-channels/movies", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": srv.URL + "/video-channels/movies", "type": "Group",
			"preferredUsername": "movies",
			"inbox":             srv.URL + "/inbox",
			"publicKey":         map[string]string{"publicKeyPem": "PEM-remote"},
		})
	})
	mux.HandleFunc("/inbox", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		// Verify the Follow's HTTP signature against OUR account actor key,
		// resolved from the database like a real remote would resolve it from
		// the actor document.
		verifier := httpsig.Verifier{ResolveKey: func(_ context.Context, keyID string) (*rsa.PublicKey, error) {
			row, err := q.GetAccountActorKey(context.Background(), u.ID)
			if err != nil {
				return nil, err
			}
			block, _ := pem.Decode([]byte(row.PublicKeyPem))
			if block == nil {
				return nil, errors.New("bad PEM")
			}
			pub, err := x509.ParsePKIXPublicKey(block.Bytes)
			if err != nil {
				return nil, err
			}
			return pub.(*rsa.PublicKey), nil
		}}
		keyID, err := verifier.Verify(r.Context(), r, body)
		hits <- inboxHit{keyID: keyID, body: body, err: err}
		w.WriteHeader(http.StatusAccepted)
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	remoteActor := srv.URL + "/video-channels/movies"

	// --- follow: WebFinger resolve → pending row → queued signed Follow ------
	follow, err := svc.FollowRemoteChannel(ctx, u.ID, "movies@"+host)
	if err != nil {
		t.Fatalf("FollowRemoteChannel: %v", err)
	}
	if follow.State != "pending" || follow.ActorURL != remoteActor {
		t.Fatalf("follow = %+v", follow)
	}
	if _, err := svc.DrainDeliveries(ctx, 100); err != nil {
		t.Fatalf("DrainDeliveries: %v", err)
	}
	var hit inboxHit
	select {
	case hit = <-hits:
	default:
		t.Fatal("the remote inbox never received the Follow")
	}
	if hit.err != nil {
		t.Fatalf("remote could not verify the Follow signature: %v", hit.err)
	}
	if want := "https://videos.example/accounts/" + u.Username + "#main-key"; hit.keyID != want {
		t.Errorf("Follow signed by %q, want %q (the ACCOUNT actor)", hit.keyID, want)
	}
	var followAct struct {
		ID     string `json:"id"`
		Type   string `json:"type"`
		Actor  string `json:"actor"`
		Object string `json:"object"`
	}
	if err := json.Unmarshal(hit.body, &followAct); err != nil {
		t.Fatalf("unmarshal Follow: %v", err)
	}
	if followAct.Type != "Follow" || followAct.Object != remoteActor ||
		followAct.Actor != "https://videos.example/accounts/"+u.Username {
		t.Fatalf("follow activity = %+v", followAct)
	}

	// --- the remote answers Accept → the follow flips to accepted ------------
	accept := `{"id":"` + remoteActor + `/act/accept-1","type":"Accept","actor":"` + remoteActor + `",` +
		`"object":{"id":"` + followAct.ID + `","type":"Follow","actor":"` + followAct.Actor + `","object":"` + remoteActor + `"}}`
	if err := svc.HandleInbox(ctx, remoteActor, []byte(accept)); err != nil {
		t.Fatalf("HandleInbox Accept: %v", err)
	}
	list, err := q.ListRemoteChannelFollows(ctx, sqlcgen.ListRemoteChannelFollowsParams{UserID: u.ID, ResultLimit: 10})
	if err != nil || len(list) != 1 {
		t.Fatalf("follows = %d (err=%v), want 1", len(list), err)
	}
	if list[0].State != "accepted" {
		t.Fatalf("state after Accept = %q, want accepted", list[0].State)
	}

	// --- the remote publishes: Create{Video} ingests through the edge gate ---
	objectURL := srv.URL + "/videos/watch/loop-1"
	title := "Full loop premiere " + suf
	create := `{"id":"` + remoteActor + `/act/create-1","type":"Create","actor":"` + remoteActor + `","object":{` +
		`"id":"` + objectURL + `","type":"Video","name":"` + title + `","content":"body",` +
		`"attributedTo":"` + remoteActor + `","published":"2026-07-01T12:00:00Z","duration":"PT120S",` +
		`"url":[{"type":"Link","mediaType":"application/x-mpegURL","href":"` + srv.URL + `/hls/loop-1.m3u8"}]}}`
	if err := svc.HandleInbox(ctx, remoteActor, []byte(create)); err != nil {
		t.Fatalf("HandleInbox Create: %v", err)
	}

	// --- and appears in that user's subscriptions feed -----------------------
	rows, err := q.ListSubscriptionVideos(ctx, sqlcgen.ListSubscriptionVideosParams{FollowerID: u.ID, ResultLimit: 50})
	if err != nil {
		t.Fatalf("ListSubscriptionVideos: %v", err)
	}
	found := false
	for _, r := range rows {
		if r.Remote && r.Title == title {
			found = true
			if r.Domain != strings.ToLower(host) {
				t.Errorf("card domain = %q, want %q", r.Domain, host)
			}
			if r.StreamUrl == nil || *r.StreamUrl != srv.URL+"/hls/loop-1.m3u8" {
				t.Errorf("card stream_url = %v", r.StreamUrl)
			}
			if r.WatchUrl != objectURL {
				t.Errorf("card watch_url = %q", r.WatchUrl)
			}
		}
	}
	if !found {
		t.Fatalf("the ingested remote video is not in the subscriptions feed (%d rows)", len(rows))
	}

	// --- unfollow: the row goes and an account-signed Undo reaches the remote
	if err := svc.UnfollowRemoteChannel(ctx, u.ID, follow.ID); err != nil {
		t.Fatalf("UnfollowRemoteChannel: %v", err)
	}
	if _, err := svc.DrainDeliveries(ctx, 100); err != nil {
		t.Fatalf("DrainDeliveries (undo): %v", err)
	}
	select {
	case hit = <-hits:
	default:
		t.Fatal("the remote inbox never received the Undo")
	}
	if hit.err != nil {
		t.Fatalf("remote could not verify the Undo signature: %v", hit.err)
	}
	var undo struct {
		Type   string `json:"type"`
		Object struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		} `json:"object"`
	}
	_ = json.Unmarshal(hit.body, &undo)
	if undo.Type != "Undo" || undo.Object.Type != "Follow" || undo.Object.ID != followAct.ID {
		t.Errorf("undo = %+v, want Undo of %q", undo, followAct.ID)
	}
}
