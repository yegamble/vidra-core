package federation

// Golden-fixture contract tests (fix_plan P10 "federation contract tests using
// fixtures", remote-content §9): a testdata/ corpus of real-SHAPED — sanitized,
// hand-built, never copied verbatim — PeerTube and Mastodon payloads is driven
// through HandleInbox (and the WebFinger/actor resolution path) so an interop
// regression in the parser or dispatcher flips a test here before a live remote
// instance ever sees it. The outbound counterpart lives in
// contract_golden_test.go; Digest/Signature interop in contract_httpsig_test.go.

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// The fixture corpus is pinned to these identities. Local = videos.example
// (matching the rest of the federation tests); remotes are a PeerTube-shaped
// instance at peertube.example and a Mastodon-shaped one at mastodon.example.
const (
	contractBase = "https://videos.example"

	ctPTAccount    = "https://peertube.example/accounts/marta"
	ctPTChannel    = "https://peertube.example/video-channels/kayak_diaries"
	ctPTVideo      = "https://peertube.example/videos/watch/e2b7c9d4-1f3a-4a6b-8c5d-0e9f8a7b6c5d"
	ctPTFollowID   = "https://peertube.example/accounts/marta/follows/41"
	ctPTHLSLink    = "https://peertube.example/static/streaming-playlists/hls/e2b7c9d4/master.m3u8"
	ctMastoUser    = "https://mastodon.example/users/kaisa"
	ctMastoUser2   = "https://mastodon.example/users/pekka"
	ctMastoFollow  = "https://mastodon.example/2f6c1a9e-90b1-47a4-b2c8-3d5e7f9a1b2c"
	ctMastoNote    = "https://mastodon.example/users/kaisa/statuses/114522110099887766"
	ctMastoReply   = "https://mastodon.example/users/pekka/statuses/114522110099887799"
	ctOurFollowURL = contractBase + "/accounts/ada/activities/follow/1e2d3c4b-5a69-4788-9b0a-6f5e4d3c2b1a"
)

// Deterministic local ids shared with the golden tests.
var (
	ctChannelID = uuid.MustParse("3f1c2b6e-8d4a-4e9b-9c7d-1a2b3c4d5e6f")
	ctUserID    = uuid.MustParse("b2c3d4e5-f6a7-4b8c-9d0e-1f2a3b4c5d6e")
	ctVideoID   = uuid.MustParse("7a1d9e42-5b3c-4f8e-a6d0-9c8b7a6f5e4d")
	ctVideo2ID  = uuid.MustParse("8b2e0f53-6c4d-4a9f-b7e1-0d9c8b7a6f5e")
	ctCommentID = uuid.MustParse("c4d5e6f7-a8b9-4c0d-8e1f-2a3b4c5d6e7f")
	ctReplyID   = uuid.MustParse("d5e6f7a8-b9c0-4d1e-9f2a-3b4c5d6e7f80")
)

// readFixture loads one corpus file (path relative to testdata/).
func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

// newContractRepo builds a fully-initialized fakeRepo seeded with the local
// actors the corpus references: user ada, channel films, and one public,
// published video on that channel.
func newContractRepo() fakeRepo {
	r := fakeRepo{
		usersByName:     map[string]sqlcgen.GetUserActorByUsernameRow{},
		usersByID:       map[uuid.UUID]sqlcgen.GetUserActorByIDRow{},
		channels:        map[string]sqlcgen.Channel{},
		channelsByID:    map[uuid.UUID]sqlcgen.Channel{},
		acctKeys:        map[uuid.UUID]sqlcgen.GetAccountActorKeyRow{},
		chanKeys:        map[uuid.UUID]sqlcgen.GetChannelActorKeyRow{},
		remoteActors:    map[string]sqlcgen.RemoteActor{},
		processed:       map[string]bool{},
		remoteFollows:   map[string]sqlcgen.InsertRemoteFollowParams{},
		deliveries:      map[uuid.UUID]*fakeDelivery{},
		videosByID:      map[uuid.UUID]sqlcgen.GetVideoByIDRow{},
		followerInboxes: map[uuid.UUID][]string{},
		localFollowers:  map[uuid.UUID]int64{},
		remoteFollowerN: map[uuid.UUID]int64{},
		channelVideoN:   map[uuid.UUID]int64{},
		outboxVideos:    map[uuid.UUID][]sqlcgen.ListChannelOutboxVideosRow{},
		remoteVideos:    map[string]*fakeRemoteVideo{},
		blockedDomains:  map[string]bool{},
		rcFollows:       map[uuid.UUID]*sqlcgen.RemoteChannelFollow{},
		commentsByID:    map[uuid.UUID]sqlcgen.Comment{},
	}
	r.usersByName["ada"] = sqlcgen.GetUserActorByUsernameRow{ID: ctUserID, Username: "ada", DisplayName: "Ada", Bio: "Field recordings."}
	r.usersByID[ctUserID] = sqlcgen.GetUserActorByIDRow{ID: ctUserID, Username: "ada", DisplayName: "Ada", Bio: "Field recordings."}
	films := sqlcgen.Channel{ID: ctChannelID, OwnerID: ctUserID, Handle: "films", DisplayName: "Films", Description: "Hand-picked documentaries."}
	r.channels["films"] = films
	r.channelsByID[ctChannelID] = films
	r.videosByID[ctVideoID] = sqlcgen.GetVideoByIDRow{
		ID: ctVideoID, ChannelID: ctChannelID,
		Title: "Dawn over the fjord", Description: "A quiet opening.",
		Privacy: "public", State: "published",
	}
	return r
}

// cacheContractActor seeds the remote-actor cache the way inbound signature
// verification would have (resolve → cache) before HandleInbox ever runs.
func cacheContractActor(repo fakeRepo, actorURL, actorType, preferredUsername, domain string) {
	shared := "https://" + domain + "/inbox"
	repo.remoteActors[actorURL] = sqlcgen.RemoteActor{
		ActorUrl:          actorURL,
		ActorType:         actorType,
		PreferredUsername: preferredUsername,
		Domain:            domain,
		InboxUrl:          actorURL + "/inbox",
		SharedInboxUrl:    &shared,
		PublicKeyPem:      "-----BEGIN PUBLIC KEY-----\nplaceholder\n-----END PUBLIC KEY-----",
	}
}

// seedPendingContractFollow inserts ada's pending outbound follow of actorURL,
// keyed by the corpus follow-activity URL that the Accept/Reject fixtures echo.
func seedPendingContractFollow(repo fakeRepo, actorURL string) uuid.UUID {
	id := uuid.New()
	repo.rcFollows[id] = &sqlcgen.RemoteChannelFollow{
		ID:                id,
		UserID:            ctUserID,
		RemoteActorUrl:    actorURL,
		State:             "pending",
		FollowActivityUrl: ctOurFollowURL,
		CreatedAt:         time.Now(),
	}
	return id
}

// seedKaisaNote stores kaisa's federated comment on the local video (as the
// mastodon_create_note fixture would have) so Update/Delete/reply fixtures have
// their target.
func seedKaisaNote(repo fakeRepo) uuid.UUID {
	id := uuid.New()
	actor, note := ctMastoUser, ctMastoNote
	name := "kaisa"
	repo.commentsByID[id] = sqlcgen.Comment{
		ID: id, VideoID: ctVideoID, Body: "The drone shots are stunning!",
		RemoteActorUrl: &actor, RemoteAuthorName: &name, RemoteObjectUrl: &note,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	return id
}

// TestContractFixturesDispatch drives every inbound corpus fixture through
// HandleInbox and asserts the dispatch outcome — parse tolerance for the extra
// fields real PeerTube/Mastodon payloads carry, authority checks, and effects.
func TestContractFixturesDispatch(t *testing.T) {
	cases := []struct {
		name    string
		fixture string
		signer  string
		edges   []string         // accepted-follow edges the ingestion gate sees
		seed    func(r fakeRepo) // extra state beyond newContractRepo
		wantErr error            // sentinel expected from HandleInbox
		check   func(t *testing.T, r fakeRepo)
	}{
		{
			name:    "peertube Follow of a local channel records + queues Accept",
			fixture: "inbound/peertube_follow.json",
			signer:  ctPTAccount,
			seed:    func(r fakeRepo) { cacheContractActor(r, ctPTAccount, "Person", "marta", "peertube.example") },
			check: func(t *testing.T, r fakeRepo) {
				rf, ok := r.remoteFollows[ctChannelID.String()+"|"+ctPTAccount]
				if !ok {
					t.Fatalf("remote follow not recorded: %+v", r.remoteFollows)
				}
				if rf.FollowActivityUrl != ctPTFollowID {
					t.Errorf("follow_activity_url = %q, want %q", rf.FollowActivityUrl, ctPTFollowID)
				}
				if len(r.deliveries) != 1 {
					t.Errorf("deliveries = %d, want 1 (the Accept)", len(r.deliveries))
				}
				if !r.processed[ctPTFollowID] {
					t.Error("activity not marked processed")
				}
			},
		},
		{
			name:    "mastodon Follow of a local channel records + queues Accept",
			fixture: "inbound/mastodon_follow.json",
			signer:  ctMastoUser,
			seed:    func(r fakeRepo) { cacheContractActor(r, ctMastoUser, "Person", "kaisa", "mastodon.example") },
			check: func(t *testing.T, r fakeRepo) {
				if _, ok := r.remoteFollows[ctChannelID.String()+"|"+ctMastoUser]; !ok {
					t.Fatalf("remote follow not recorded: %+v", r.remoteFollows)
				}
				if len(r.deliveries) != 1 {
					t.Errorf("deliveries = %d, want 1 (the Accept)", len(r.deliveries))
				}
			},
		},
		{
			name:    "mastodon Undo{Follow} removes the follow",
			fixture: "inbound/mastodon_undo_follow.json",
			signer:  ctMastoUser,
			seed: func(r fakeRepo) {
				r.remoteFollows[ctChannelID.String()+"|"+ctMastoUser] = sqlcgen.InsertRemoteFollowParams{
					ChannelID: ctChannelID, RemoteActorUrl: ctMastoUser, FollowActivityUrl: ctMastoFollow,
				}
			},
			check: func(t *testing.T, r fakeRepo) {
				if len(r.remoteFollows) != 0 {
					t.Errorf("follow survived the Undo: %+v", r.remoteFollows)
				}
			},
		},
		{
			name:    "peertube Accept{Follow} flips our pending follow to accepted",
			fixture: "inbound/peertube_accept_follow.json",
			signer:  ctPTChannel,
			seed:    func(r fakeRepo) { seedPendingContractFollow(r, ctPTChannel) },
			check: func(t *testing.T, r fakeRepo) {
				for _, row := range r.rcFollows {
					if row.State != "accepted" {
						t.Errorf("state = %q, want accepted", row.State)
					}
				}
			},
		},
		{
			name:    "peertube Accept{Follow} from the wrong signer is ignored",
			fixture: "inbound/peertube_accept_follow.json",
			signer:  ctPTAccount, // marta, not the followed channel
			seed:    func(r fakeRepo) { seedPendingContractFollow(r, ctPTChannel) },
			check: func(t *testing.T, r fakeRepo) {
				for _, row := range r.rcFollows {
					if row.State != "pending" {
						t.Errorf("state = %q, want pending (foreign Accept must not apply)", row.State)
					}
				}
			},
		},
		{
			name:    "mastodon Reject{Follow} deletes our pending follow",
			fixture: "inbound/mastodon_reject_follow.json",
			signer:  ctMastoUser,
			seed:    func(r fakeRepo) { seedPendingContractFollow(r, ctMastoUser) },
			check: func(t *testing.T, r fakeRepo) {
				if len(r.rcFollows) != 0 {
					t.Errorf("pending follow survived the Reject: %+v", r.rcFollows)
				}
			},
		},
		{
			name:    "peertube Create{Video} ingests the full-shaped object",
			fixture: "inbound/peertube_create_video.json",
			signer:  ctPTChannel,
			edges:   []string{ctPTChannel},
			check: func(t *testing.T, r fakeRepo) {
				rv, ok := r.remoteVideos[ctPTVideo]
				if !ok {
					t.Fatalf("remote video not stored: %+v", r.remoteVideos)
				}
				p := rv.params
				if p.RemoteActorUrl != ctPTChannel {
					t.Errorf("owner = %q, want the signing channel %q", p.RemoteActorUrl, ctPTChannel)
				}
				if p.Title != "Dawn crossing at Whitegull Sound" {
					t.Errorf("title = %q", p.Title)
				}
				if p.DurationSeconds == nil || *p.DurationSeconds != 212 {
					t.Errorf("duration = %v, want 212 (PT212S)", p.DurationSeconds)
				}
				if !p.PublishedAt.Valid || !p.PublishedAt.Time.Equal(time.Date(2026, 5, 12, 8, 30, 0, 0, time.UTC)) {
					t.Errorf("published_at = %+v, want the fixture timestamp", p.PublishedAt)
				}
				if p.WatchUrl != ctPTVideo {
					t.Errorf("watch_url = %q, want the text/html link %q", p.WatchUrl, ctPTVideo)
				}
				if p.StreamUrl == nil || *p.StreamUrl != ctPTHLSLink {
					t.Errorf("stream_url = %v, want the HLS playlist preferred over mp4/magnet", p.StreamUrl)
				}
			},
		},
		{
			name:    "peertube Create{Video} without an accepted follow edge is ignored",
			fixture: "inbound/peertube_create_video.json",
			signer:  ctPTChannel,
			check: func(t *testing.T, r fakeRepo) {
				if len(r.remoteVideos) != 0 {
					t.Error("video ingested without an accepted local follow edge")
				}
			},
		},
		{
			name:    "peertube Create{Video} signed by a foreign actor is rejected",
			fixture: "inbound/peertube_create_video.json",
			signer:  ctMastoUser,
			edges:   []string{ctPTChannel, ctMastoUser},
			wantErr: ErrActorMismatch,
			check: func(t *testing.T, r fakeRepo) {
				if len(r.remoteVideos) != 0 {
					t.Error("video stored despite the actor mismatch")
				}
			},
		},
		{
			name:    "peertube Update{Video} upserts the changed metadata in place",
			fixture: "inbound/peertube_update_video.json",
			signer:  ctPTChannel,
			edges:   []string{ctPTChannel},
			seed: func(r fakeRepo) {
				r.remoteVideos[ctPTVideo] = &fakeRemoteVideo{id: uuid.New(), params: sqlcgen.UpsertRemoteVideoParams{
					ObjectUrl: ctPTVideo, RemoteActorUrl: ctPTChannel, Title: "Dawn crossing at Whitegull Sound",
				}}
			},
			check: func(t *testing.T, r fakeRepo) {
				if len(r.remoteVideos) != 1 {
					t.Fatalf("videos = %d, want 1 (upsert)", len(r.remoteVideos))
				}
				if got := r.remoteVideos[ctPTVideo].params.Title; got != "Dawn crossing at Whitegull Sound (remastered)" {
					t.Errorf("title = %q, want the updated one", got)
				}
			},
		},
		{
			name:    "peertube Delete of its video removes the cached row",
			fixture: "inbound/peertube_delete_video.json",
			signer:  ctPTChannel,
			seed: func(r fakeRepo) {
				r.remoteVideos[ctPTVideo] = &fakeRemoteVideo{id: uuid.New(), params: sqlcgen.UpsertRemoteVideoParams{
					ObjectUrl: ctPTVideo, RemoteActorUrl: ctPTChannel,
				}}
			},
			check: func(t *testing.T, r fakeRepo) {
				if len(r.remoteVideos) != 0 {
					t.Error("remote video survived its origin Delete")
				}
			},
		},
		{
			name:    "mastodon Create{Note} on a local video stores a federated comment",
			fixture: "inbound/mastodon_create_note.json",
			signer:  ctMastoUser,
			seed:    func(r fakeRepo) { cacheContractActor(r, ctMastoUser, "Person", "kaisa", "mastodon.example") },
			check: func(t *testing.T, r fakeRepo) {
				c := onlyComment(t, r)
				if c.VideoID != ctVideoID || c.ParentID.Valid {
					t.Errorf("comment target = %+v, want top-level on %s", c, ctVideoID)
				}
				want := "The drone shots at #whitegull are stunning & the route notes helped a lot!"
				if c.Body != want {
					t.Errorf("body = %q, want HTML-stripped + entity-unescaped %q", c.Body, want)
				}
				if c.RemoteAuthorName == nil || *c.RemoteAuthorName != "kaisa" {
					t.Errorf("author snapshot = %v, want kaisa", c.RemoteAuthorName)
				}
				if c.RemoteObjectUrl == nil || *c.RemoteObjectUrl != ctMastoNote {
					t.Errorf("remote_object_url = %v, want %q", c.RemoteObjectUrl, ctMastoNote)
				}
			},
		},
		{
			name:    "mastodon Create{Note} replying to a remote sibling threads onto the video",
			fixture: "inbound/mastodon_create_note_reply.json",
			signer:  ctMastoUser2,
			seed: func(r fakeRepo) {
				cacheContractActor(r, ctMastoUser2, "Person", "pekka", "mastodon.example")
				seedKaisaNote(r)
			},
			check: func(t *testing.T, r fakeRepo) {
				var reply *sqlcgen.Comment
				for _, c := range r.commentsByID {
					if c.RemoteObjectUrl != nil && *c.RemoteObjectUrl == ctMastoReply {
						cc := c
						reply = &cc
					}
				}
				if reply == nil {
					t.Fatalf("reply not stored: %+v", r.commentsByID)
				}
				if reply.VideoID != ctVideoID || !reply.ParentID.Valid {
					t.Errorf("reply threading = %+v, want a child comment on %s", reply, ctVideoID)
				}
				if !strings.Contains(reply.Body, "tide window chart") || strings.Contains(reply.Body, "<") {
					t.Errorf("reply body = %q, want stripped plain text", reply.Body)
				}
			},
		},
		{
			name:    "mastodon Update{Note} edits only for the original author",
			fixture: "inbound/mastodon_update_note.json",
			signer:  ctMastoUser,
			seed:    func(r fakeRepo) { seedKaisaNote(r) },
			check: func(t *testing.T, r fakeRepo) {
				c := onlyComment(t, r)
				if !strings.HasPrefix(c.Body, "Edit: the drone shots") {
					t.Errorf("body = %q, want the edited content", c.Body)
				}
			},
		},
		{
			name:    "mastodon Update{Note} from another actor is ignored",
			fixture: "inbound/mastodon_update_note.json",
			signer:  ctMastoUser2, // pekka may not edit kaisa's note
			seed:    func(r fakeRepo) { seedKaisaNote(r) },
			wantErr: ErrActorMismatch, // the activity names kaisa as its actor
			check: func(t *testing.T, r fakeRepo) {
				c := onlyComment(t, r)
				if c.Body != "The drone shots are stunning!" {
					t.Errorf("body = %q, want unchanged", c.Body)
				}
			},
		},
		{
			name:    "mastodon Delete with a Tombstone removes the comment",
			fixture: "inbound/mastodon_delete_note.json",
			signer:  ctMastoUser,
			seed:    func(r fakeRepo) { seedKaisaNote(r) },
			check: func(t *testing.T, r fakeRepo) {
				if len(r.commentsByID) != 0 {
					t.Errorf("comment survived its Tombstone Delete: %+v", r.commentsByID)
				}
			},
		},
		{
			name:    "mastodon Delete of an actor cascades its content away",
			fixture: "inbound/mastodon_delete_actor.json",
			signer:  ctMastoUser,
			seed: func(r fakeRepo) {
				cacheContractActor(r, ctMastoUser, "Person", "kaisa", "mastodon.example")
				seedKaisaNote(r)
				r.remoteVideos["https://mastodon.example/objects/v1"] = &fakeRemoteVideo{
					id: uuid.New(), params: sqlcgen.UpsertRemoteVideoParams{
						ObjectUrl: "https://mastodon.example/objects/v1", RemoteActorUrl: ctMastoUser,
					},
				}
				seedPendingContractFollow(r, ctMastoUser)
			},
			check: func(t *testing.T, r fakeRepo) {
				if _, ok := r.remoteActors[ctMastoUser]; ok {
					t.Error("actor survived its own Delete")
				}
				if len(r.commentsByID) != 0 || len(r.remoteVideos) != 0 || len(r.rcFollows) != 0 {
					t.Errorf("actor content did not cascade: comments=%d videos=%d follows=%d",
						len(r.commentsByID), len(r.remoteVideos), len(r.rcFollows))
				}
			},
		},
		{
			name:    "peertube Like is accepted-and-ignored",
			fixture: "inbound/peertube_like.json",
			signer:  ctPTAccount,
			check: func(t *testing.T, r fakeRepo) {
				if !r.processed["https://peertube.example/accounts/marta/likes/77"] {
					t.Error("ignored activity should still be marked processed")
				}
			},
		},
		{
			name:    "peertube View is accepted-and-ignored",
			fixture: "inbound/peertube_view.json",
			signer:  "https://peertube.example/accounts/peertube",
			check: func(t *testing.T, r fakeRepo) {
				if !r.processed["https://peertube.example/views/videos/e2b7c9d4/9a01f3"] {
					t.Error("ignored activity should still be marked processed")
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newContractRepo()
			if tc.seed != nil {
				tc.seed(repo)
			}
			svc := NewService(repo,
				WithBaseURL(contractBase),
				WithFollowEdgeChecker(edgesFor(tc.edges...)),
			)
			err := svc.HandleInbox(context.Background(), tc.signer, readFixture(t, tc.fixture))
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("HandleInbox err = %v, want %v", err, tc.wantErr)
				}
			} else if err != nil {
				t.Fatalf("HandleInbox: %v", err)
			}
			if tc.check != nil {
				tc.check(t, repo)
			}
		})
	}
}

// fixtureTransport serves corpus files as HTTP responses for exact request
// URLs — the "remote instance" the WebFinger/actor/object fetch paths talk to.
type fixtureTransport struct {
	t      *testing.T
	routes map[string]string // exact URL → fixture path under testdata/
}

func (ft fixtureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	name, ok := ft.routes[req.URL.String()]
	if !ok {
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Body:       io.NopCloser(strings.NewReader("no fixture for " + req.URL.String())),
			Header:     http.Header{},
			Request:    req,
		}, nil
	}
	h := http.Header{}
	h.Set("Content-Type", "application/activity+json")
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(string(readFixture(ft.t, name)))),
		Header:     h,
		Request:    req,
	}, nil
}

func webfingerURL(domain, acct string) string {
	return "https://" + domain + "/.well-known/webfinger?resource=" + url.QueryEscape("acct:"+acct)
}

// TestContractWebFingerResolution follows a remote handle end to end through
// the real-shaped WebFinger JRD + actor documents: handle → JRD self link →
// actor fetch/parse/cache → pending follow + queued Follow to the sharedInbox.
func TestContractWebFingerResolution(t *testing.T) {
	cases := []struct {
		name        string
		handle      string
		actorURL    string
		wfFixture   string
		actorDoc    string
		wantHandle  string
		sharedInbox string
	}{
		{
			name:        "peertube channel",
			handle:      "kayak_diaries@peertube.example",
			actorURL:    ctPTChannel,
			wfFixture:   "inbound/peertube_webfinger.json",
			actorDoc:    "inbound/peertube_actor_group.json",
			wantHandle:  "kayak_diaries@peertube.example",
			sharedInbox: "https://peertube.example/inbox",
		},
		{
			name:        "mastodon account",
			handle:      "kaisa@mastodon.example",
			actorURL:    ctMastoUser,
			wfFixture:   "inbound/mastodon_webfinger.json",
			actorDoc:    "inbound/mastodon_actor_person.json",
			wantHandle:  "kaisa@mastodon.example",
			sharedInbox: "https://mastodon.example/inbox",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			domain := strings.SplitN(tc.handle, "@", 2)[1]
			repo := newContractRepo()
			client := &http.Client{Transport: fixtureTransport{t: t, routes: map[string]string{
				webfingerURL(domain, tc.handle): tc.wfFixture,
				tc.actorURL:                     tc.actorDoc,
			}}}
			svc := NewService(repo, WithBaseURL(contractBase), WithFetchClient(client))

			follow, err := svc.FollowRemoteChannel(context.Background(), ctUserID, tc.handle)
			if err != nil {
				t.Fatalf("FollowRemoteChannel(%q): %v", tc.handle, err)
			}
			if follow.State != "pending" {
				t.Errorf("state = %q, want pending", follow.State)
			}
			if follow.Handle != tc.wantHandle {
				t.Errorf("handle = %q, want %q (parsed from the actor doc)", follow.Handle, tc.wantHandle)
			}
			ra, ok := repo.remoteActors[tc.actorURL]
			if !ok {
				t.Fatalf("actor was not cached: %+v", repo.remoteActors)
			}
			if ra.SharedInboxUrl == nil || *ra.SharedInboxUrl != tc.sharedInbox {
				t.Errorf("sharedInbox = %v, want %q", ra.SharedInboxUrl, tc.sharedInbox)
			}
			if len(repo.deliveries) != 1 {
				t.Fatalf("deliveries = %d, want 1 (the Follow)", len(repo.deliveries))
			}
			for _, d := range repo.deliveries {
				if d.row.InboxUrl != tc.sharedInbox {
					t.Errorf("Follow queued to %q, want the sharedInbox %q", d.row.InboxUrl, tc.sharedInbox)
				}
				if !d.row.SigningUserID.Valid || uuid.UUID(d.row.SigningUserID.Bytes) != ctUserID {
					t.Errorf("Follow signer = %+v, want ada's account actor", d.row.SigningUserID)
				}
			}
		})
	}
}

// TestContractAnnounceFetchesFixtureObject drives the URL-only PeerTube
// Announce through the object-fetch path: the "origin" serves the standalone
// Video document (attributed Person-first), whose owner actor is then resolved
// from its own fixture document and cached for the FK.
func TestContractAnnounceFetchesFixtureObject(t *testing.T) {
	repo := newContractRepo()
	client := &http.Client{Transport: fixtureTransport{t: t, routes: map[string]string{
		ctPTVideo:   "inbound/peertube_video_object.json",
		ctPTAccount: "inbound/peertube_actor_person.json",
	}}}
	svc := NewService(repo,
		WithBaseURL(contractBase),
		WithFollowEdgeChecker(edgesFor(ctPTChannel)),
		WithFetchClient(client),
	)

	err := svc.HandleInbox(context.Background(), ctPTChannel, readFixture(t, "inbound/peertube_announce_video.json"))
	if err != nil {
		t.Fatalf("HandleInbox: %v", err)
	}
	rv, ok := repo.remoteVideos[ctPTVideo]
	if !ok {
		t.Fatalf("announced video not stored: %+v", repo.remoteVideos)
	}
	if rv.params.RemoteActorUrl != ctPTAccount {
		t.Errorf("owner = %q, want the first attributed actor %q", rv.params.RemoteActorUrl, ctPTAccount)
	}
	if rv.params.Title != "Dawn crossing at Whitegull Sound" {
		t.Errorf("title = %q", rv.params.Title)
	}
	if rv.params.StreamUrl == nil || *rv.params.StreamUrl != ctPTHLSLink {
		t.Errorf("stream_url = %v, want the HLS playlist", rv.params.StreamUrl)
	}
	if ra, ok := repo.remoteActors[ctPTAccount]; !ok || ra.PreferredUsername != "marta" {
		t.Errorf("owner actor not resolved+cached from its fixture: %+v", repo.remoteActors)
	}
}
