package federation

// Inbound federated-comment tests (remote-content §6-7): Create{Note} parsing,
// authority, threading, bounding; Update{Note} authority; Delete authority for
// comments, remote videos, and actors (incl. the actor-content cascade).

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

const noteBase = "https://videos.example"

// newNotesRepo seeds a fake with the signer actor cached and one local public,
// published video.
func newNotesRepo(videoID uuid.UUID) fakeRepo {
	return fakeRepo{
		processed: map[string]bool{},
		remoteActors: map[string]sqlcgen.RemoteActor{
			remoteBob: {ActorUrl: remoteBob, PreferredUsername: "bob", Domain: "remote.example"},
		},
		videosByID: map[uuid.UUID]sqlcgen.GetVideoByIDRow{
			videoID: {ID: videoID, ChannelID: uuid.New(), Privacy: "public", State: "published"},
		},
		commentsByID: map[uuid.UUID]sqlcgen.Comment{},
		remoteVideos: map[string]*fakeRemoteVideo{},
		rcFollows:    map[uuid.UUID]*sqlcgen.RemoteChannelFollow{},
	}
}

// fakeFlagger records watched-words flagging invocations.
type fakeFlagger struct{ flagged []uuid.UUID }

func (f *fakeFlagger) FlagComment(_ context.Context, id uuid.UUID, _ string) (int, error) {
	f.flagged = append(f.flagged, id)
	return 1, nil
}

// createNote renders a Create{Note} activity JSON.
func createNote(actID, actor, noteID, attributedTo, inReplyTo, content string) string {
	return `{"id":"` + actID + `","type":"Create","actor":"` + actor + `","object":{` +
		`"id":"` + noteID + `","type":"Note","attributedTo":"` + attributedTo + `",` +
		`"inReplyTo":"` + inReplyTo + `","content":` + quote(content) + `}}`
}

func onlyComment(t *testing.T, repo fakeRepo) sqlcgen.Comment {
	t.Helper()
	if len(repo.commentsByID) != 1 {
		t.Fatalf("stored comments = %d, want 1", len(repo.commentsByID))
	}
	for _, c := range repo.commentsByID {
		return c
	}
	return sqlcgen.Comment{}
}

func TestHandleInboxCreateNoteStoresRemoteComment(t *testing.T) {
	videoID := uuid.New()
	repo := newNotesRepo(videoID)
	flagger := &fakeFlagger{}
	svc := NewService(repo, WithBaseURL(noteBase), WithCommentFlagger(flagger))

	videoURL := noteBase + "/videos/" + videoID.String()
	body := createNote("https://remote.example/act/n1", remoteBob,
		"https://remote.example/comments/1", remoteBob, videoURL,
		"<p>Hello &amp; welcome</p>")
	if err := svc.HandleInbox(context.Background(), remoteBob, []byte(body)); err != nil {
		t.Fatalf("HandleInbox: %v", err)
	}
	c := onlyComment(t, repo)
	if c.VideoID != videoID || c.ParentID.Valid {
		t.Errorf("comment target = %+v, want top-level on %s", c, videoID)
	}
	if c.Body != "Hello & welcome" {
		t.Errorf("body = %q, want HTML-stripped %q", c.Body, "Hello & welcome")
	}
	if c.UserID.Valid {
		t.Error("remote comment must have NULL user_id")
	}
	if c.RemoteActorUrl == nil || *c.RemoteActorUrl != remoteBob {
		t.Errorf("remote_actor_url = %v, want %q", c.RemoteActorUrl, remoteBob)
	}
	if c.RemoteAuthorName == nil || *c.RemoteAuthorName != "bob" {
		t.Errorf("remote_author_name = %v, want bob", c.RemoteAuthorName)
	}
	if c.RemoteObjectUrl == nil || *c.RemoteObjectUrl != "https://remote.example/comments/1" {
		t.Errorf("remote_object_url = %v", c.RemoteObjectUrl)
	}
	// Watched-words flagging applies to remote comments too (§6).
	if len(flagger.flagged) != 1 || flagger.flagged[0] != c.ID {
		t.Errorf("flagged = %v, want [%s]", flagger.flagged, c.ID)
	}
}

func TestHandleInboxCreateNoteTruncatesBody(t *testing.T) {
	videoID := uuid.New()
	repo := newNotesRepo(videoID)
	svc := NewService(repo, WithBaseURL(noteBase))

	long := strings.Repeat("x", maxRemoteCommentLen+500)
	// Legacy inReplyTo form (/videos/watch/) — still accepted inbound.
	body := createNote("https://remote.example/act/n2", remoteBob,
		"https://remote.example/comments/2", remoteBob,
		noteBase+"/videos/watch/"+videoID.String(), long)
	if err := svc.HandleInbox(context.Background(), remoteBob, []byte(body)); err != nil {
		t.Fatalf("HandleInbox: %v", err)
	}
	if c := onlyComment(t, repo); len([]rune(c.Body)) != maxRemoteCommentLen {
		t.Errorf("body length = %d, want truncated to %d", len([]rune(c.Body)), maxRemoteCommentLen)
	}
}

func TestHandleInboxCreateNoteThreading(t *testing.T) {
	videoID := uuid.New()
	repo := newNotesRepo(videoID)
	svc := NewService(repo, WithBaseURL(noteBase))

	// A local parent comment: replies address it by OUR comment URL.
	localParent := sqlcgen.Comment{ID: uuid.New(), VideoID: videoID, UserID: pgUUIDv(uuid.New()), Body: "parent"}
	repo.commentsByID[localParent.ID] = localParent
	body := createNote("https://remote.example/act/n3", remoteBob,
		"https://remote.example/comments/3", remoteBob,
		noteBase+"/comments/"+localParent.ID.String(), "reply to local")
	if err := svc.HandleInbox(context.Background(), remoteBob, []byte(body)); err != nil {
		t.Fatalf("HandleInbox (local parent): %v", err)
	}
	var reply sqlcgen.Comment
	for _, c := range repo.commentsByID {
		if c.RemoteObjectUrl != nil && *c.RemoteObjectUrl == "https://remote.example/comments/3" {
			reply = c
		}
	}
	if !reply.ParentID.Valid || uuid.UUID(reply.ParentID.Bytes) != localParent.ID || reply.VideoID != videoID {
		t.Errorf("reply = %+v, want parent %s on video %s", reply, localParent.ID, videoID)
	}

	// A remote sibling: replies address it by ITS origin object URL.
	body = createNote("https://remote.example/act/n4", remoteBob,
		"https://remote.example/comments/4", remoteBob,
		"https://remote.example/comments/3", "reply to remote")
	if err := svc.HandleInbox(context.Background(), remoteBob, []byte(body)); err != nil {
		t.Fatalf("HandleInbox (remote parent): %v", err)
	}
	var deep sqlcgen.Comment
	for _, c := range repo.commentsByID {
		if c.RemoteObjectUrl != nil && *c.RemoteObjectUrl == "https://remote.example/comments/4" {
			deep = c
		}
	}
	if !deep.ParentID.Valid || uuid.UUID(deep.ParentID.Bytes) != reply.ID || deep.VideoID != videoID {
		t.Errorf("deep reply = %+v, want parent %s", deep, reply.ID)
	}
}

func TestHandleInboxCreateNoteRejections(t *testing.T) {
	videoID := uuid.New()
	videoURL := noteBase + "/videos/" + videoID.String()
	evil := "https://evil.example/accounts/mallory"

	cases := []struct {
		name    string
		body    string
		wantErr error
	}{
		{"actor is not the signer",
			createNote("https://remote.example/act/r1", evil, "https://remote.example/comments/r1", evil, videoURL, "x"),
			ErrActorMismatch},
		{"attributedTo is not the signer",
			createNote("https://remote.example/act/r2", remoteBob, "https://remote.example/comments/r2", evil, videoURL, "x"),
			nil},
		{"note id on a foreign origin",
			createNote("https://remote.example/act/r3", remoteBob, "https://evil.example/comments/r3", remoteBob, videoURL, "x"),
			nil},
		{"inReplyTo is not one of ours",
			createNote("https://remote.example/act/r4", remoteBob, "https://remote.example/comments/r4", remoteBob, "https://elsewhere.example/videos/watch/9", "x"),
			nil},
		{"inReplyTo names an unknown local video (legacy form)",
			createNote("https://remote.example/act/r5", remoteBob, "https://remote.example/comments/r5", remoteBob, noteBase+"/videos/watch/"+uuid.NewString(), "x"),
			nil},
		{"inReplyTo names an unknown local video (canonical form)",
			createNote("https://remote.example/act/r7", remoteBob, "https://remote.example/comments/r7", remoteBob, noteBase+"/videos/"+uuid.NewString(), "x"),
			nil},
		{"empty body after HTML strip",
			createNote("https://remote.example/act/r6", remoteBob, "https://remote.example/comments/r6", remoteBob, videoURL, "<p>  </p>"),
			nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newNotesRepo(videoID)
			svc := NewService(repo, WithBaseURL(noteBase))
			err := svc.HandleInbox(context.Background(), remoteBob, []byte(tc.body))
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("HandleInbox = %v, want %v", err, tc.wantErr)
			}
			if len(repo.commentsByID) != 0 {
				t.Errorf("stored %d comments, want 0", len(repo.commentsByID))
			}
		})
	}
}

func TestHandleInboxCreateNoteOnNonPublicVideoIgnored(t *testing.T) {
	videoID := uuid.New()
	repo := newNotesRepo(videoID)
	v := repo.videosByID[videoID]
	v.Privacy = "private"
	repo.videosByID[videoID] = v
	svc := NewService(repo, WithBaseURL(noteBase))

	// Legacy inReplyTo form (/videos/watch/) — still accepted inbound.
	body := createNote("https://remote.example/act/p1", remoteBob,
		"https://remote.example/comments/p1", remoteBob,
		noteBase+"/videos/watch/"+videoID.String(), "x")
	if err := svc.HandleInbox(context.Background(), remoteBob, []byte(body)); err != nil {
		t.Fatalf("HandleInbox: %v", err)
	}
	if len(repo.commentsByID) != 0 {
		t.Error("a non-public video accepted a federated comment")
	}
}

func TestHandleInboxUpdateNoteAuthority(t *testing.T) {
	videoID := uuid.New()
	repo := newNotesRepo(videoID)
	svc := NewService(repo, WithBaseURL(noteBase))
	ctx := context.Background()

	create := createNote("https://remote.example/act/u1", remoteBob,
		"https://remote.example/comments/u1", remoteBob,
		noteBase+"/videos/"+videoID.String(), "original")
	if err := svc.HandleInbox(ctx, remoteBob, []byte(create)); err != nil {
		t.Fatalf("HandleInbox Create: %v", err)
	}

	// The original author edits: applied (and truncated/stripped).
	update := `{"id":"https://remote.example/act/u2","type":"Update","actor":"` + remoteBob + `","object":{` +
		`"id":"https://remote.example/comments/u1","type":"Note","content":"<b>revised</b>"}}`
	if err := svc.HandleInbox(ctx, remoteBob, []byte(update)); err != nil {
		t.Fatalf("HandleInbox Update: %v", err)
	}
	if c := onlyComment(t, repo); c.Body != "revised" {
		t.Errorf("body after Update = %q, want revised", c.Body)
	}

	// A different (even same-host-looking) actor cannot edit it.
	stranger := "https://remote.example/accounts/mallory"
	repo.remoteActors[stranger] = sqlcgen.RemoteActor{ActorUrl: stranger, PreferredUsername: "mallory", Domain: "remote.example"}
	foreign := `{"id":"https://remote.example/act/u3","type":"Update","actor":"` + stranger + `","object":{` +
		`"id":"https://remote.example/comments/u1","type":"Note","content":"hijacked"}}`
	if err := svc.HandleInbox(ctx, stranger, []byte(foreign)); err != nil {
		t.Fatalf("HandleInbox foreign Update: %v", err)
	}
	if c := onlyComment(t, repo); c.Body != "revised" {
		t.Errorf("body after foreign Update = %q, want revised (unchanged)", c.Body)
	}
}

func TestHandleInboxDeleteComment(t *testing.T) {
	videoID := uuid.New()
	repo := newNotesRepo(videoID)
	svc := NewService(repo, WithBaseURL(noteBase))
	ctx := context.Background()

	// Legacy inReplyTo form (/videos/watch/) — still accepted inbound.
	create := createNote("https://remote.example/act/d1", remoteBob,
		"https://remote.example/comments/d1", remoteBob,
		noteBase+"/videos/watch/"+videoID.String(), "doomed")
	if err := svc.HandleInbox(ctx, remoteBob, []byte(create)); err != nil {
		t.Fatalf("HandleInbox Create: %v", err)
	}

	// A foreign-origin signer cannot delete it.
	evil := "https://evil.example/accounts/mallory"
	repo.remoteActors[evil] = sqlcgen.RemoteActor{ActorUrl: evil, Domain: "evil.example"}
	del := `{"id":"https://evil.example/act/d2","type":"Delete","actor":"` + evil + `","object":"https://remote.example/comments/d1"}`
	if err := svc.HandleInbox(ctx, evil, []byte(del)); err != nil {
		t.Fatalf("HandleInbox foreign Delete: %v", err)
	}
	if len(repo.commentsByID) != 1 {
		t.Fatal("a foreign actor deleted the comment")
	}

	// The origin (same host — e.g. the server, via a Tombstone) can.
	del = `{"id":"https://remote.example/act/d3","type":"Delete","actor":"` + remoteBob + `","object":{"id":"https://remote.example/comments/d1","type":"Tombstone"}}`
	if err := svc.HandleInbox(ctx, remoteBob, []byte(del)); err != nil {
		t.Fatalf("HandleInbox Delete: %v", err)
	}
	if len(repo.commentsByID) != 0 {
		t.Error("the comment survived the origin's Delete")
	}
}

func TestHandleInboxDeleteRemoteVideo(t *testing.T) {
	repo := newNotesRepo(uuid.New())
	repo.remoteVideos[remoteVideo] = &fakeRemoteVideo{
		id:     uuid.New(),
		params: sqlcgen.UpsertRemoteVideoParams{ObjectUrl: remoteVideo, RemoteActorUrl: remoteChan},
	}
	repo.remoteActors[remoteChan] = sqlcgen.RemoteActor{ActorUrl: remoteChan, Domain: "remote.example"}
	svc := NewService(repo, WithBaseURL(noteBase))
	ctx := context.Background()

	// A foreign-origin signer is ignored.
	evil := "https://evil.example/accounts/mallory"
	del := `{"id":"https://evil.example/act/v1","type":"Delete","actor":"` + evil + `","object":"` + remoteVideo + `"}`
	if err := svc.HandleInbox(ctx, evil, []byte(del)); err != nil {
		t.Fatalf("HandleInbox foreign Delete: %v", err)
	}
	if len(repo.remoteVideos) != 1 {
		t.Fatal("a foreign actor deleted the remote video")
	}

	// The attributed channel actor retracts it.
	del = `{"id":"https://remote.example/act/v2","type":"Delete","actor":"` + remoteChan + `","object":"` + remoteVideo + `"}`
	if err := svc.HandleInbox(ctx, remoteChan, []byte(del)); err != nil {
		t.Fatalf("HandleInbox Delete: %v", err)
	}
	if len(repo.remoteVideos) != 0 {
		t.Error("the remote video survived the origin's Delete")
	}
}

func TestHandleInboxDeleteActorCascades(t *testing.T) {
	videoID := uuid.New()
	repo := newNotesRepo(videoID)
	svc := NewService(repo, WithBaseURL(noteBase))
	ctx := context.Background()

	// bob has a comment here and (as a channel would) a remote video + an edge.
	create := createNote("https://remote.example/act/a1", remoteBob,
		"https://remote.example/comments/a1", remoteBob,
		noteBase+"/videos/"+videoID.String(), "from bob")
	if err := svc.HandleInbox(ctx, remoteBob, []byte(create)); err != nil {
		t.Fatalf("HandleInbox Create: %v", err)
	}
	repo.remoteVideos["https://remote.example/videos/watch/z"] = &fakeRemoteVideo{
		id:     uuid.New(),
		params: sqlcgen.UpsertRemoteVideoParams{ObjectUrl: "https://remote.example/videos/watch/z", RemoteActorUrl: remoteBob},
	}

	// An actor deleting itself removes the actor AND its videos + comments (§7).
	del := `{"id":"https://remote.example/act/a2","type":"Delete","actor":"` + remoteBob + `","object":"` + remoteBob + `"}`
	if err := svc.HandleInbox(ctx, remoteBob, []byte(del)); err != nil {
		t.Fatalf("HandleInbox actor Delete: %v", err)
	}
	if _, ok := repo.remoteActors[remoteBob]; ok {
		t.Error("the actor row survived its own Delete")
	}
	if len(repo.commentsByID) != 0 {
		t.Error("the actor's comments survived the actor Delete")
	}
	if len(repo.remoteVideos) != 0 {
		t.Error("the actor's videos survived the actor Delete")
	}
}

// TestLocalVideoIDAcceptsBothWatchURLForms pins the dual-accept contract for
// inbound video object URLs. We now MINT the canonical /videos/<uuid> form (the
// one RSS/sitemap/oEmbed emit and the frontend routes), but replies federated
// while we minted /videos/watch/<uuid> must keep resolving to the same video —
// so both prefixes are accepted, and nothing else is.
func TestLocalVideoIDAcceptsBothWatchURLForms(t *testing.T) {
	videoID := uuid.New()
	svc := NewService(newNotesRepo(videoID), WithBaseURL(noteBase))

	cases := []struct {
		name      string
		objectURL string
		wantID    uuid.UUID
		wantOK    bool
	}{
		{"canonical form", noteBase + "/videos/" + videoID.String(), videoID, true},
		{"legacy watch form", noteBase + "/videos/watch/" + videoID.String(), videoID, true},
		{"legacy prefix with a non-uuid remainder", noteBase + "/videos/watch/not-a-uuid", uuid.UUID{}, false},
		{"canonical prefix with a non-uuid remainder", noteBase + "/videos/not-a-uuid", uuid.UUID{}, false},
		{"canonical prefix with an empty remainder", noteBase + "/videos/", uuid.UUID{}, false},
		{"legacy prefix with an empty remainder", noteBase + "/videos/watch/", uuid.UUID{}, false},
		{"another host, canonical form", "https://remote.example/videos/" + videoID.String(), uuid.UUID{}, false},
		{"another host, legacy form", "https://remote.example/videos/watch/" + videoID.String(), uuid.UUID{}, false},
		{"one of our comment URLs", noteBase + "/comments/" + videoID.String(), uuid.UUID{}, false},
		{"empty", "", uuid.UUID{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := svc.localVideoID(tc.objectURL)
			if ok != tc.wantOK || got != tc.wantID {
				t.Errorf("localVideoID(%q) = %v, %v; want %v, %v", tc.objectURL, got, ok, tc.wantID, tc.wantOK)
			}
		})
	}
}

// TestResolveNoteTargetAcceptsBothWatchURLForms proves the dual-accept reaches
// the threading seam: either form resolves to the same publicly visible video.
func TestResolveNoteTargetAcceptsBothWatchURLForms(t *testing.T) {
	videoID := uuid.New()
	svc := NewService(newNotesRepo(videoID), WithBaseURL(noteBase))

	for _, inReplyTo := range []string{
		noteBase + "/videos/" + videoID.String(),
		noteBase + "/videos/watch/" + videoID.String(),
	} {
		vid, parent, ok, err := svc.resolveNoteTarget(context.Background(), inReplyTo)
		if err != nil {
			t.Fatalf("resolveNoteTarget(%q): %v", inReplyTo, err)
		}
		if !ok || vid != videoID || parent.Valid {
			t.Errorf("resolveNoteTarget(%q) = %v, %+v, %v; want top-level on %s", inReplyTo, vid, parent, ok, videoID)
		}
	}
}

// pgUUIDv wraps a uuid as a valid pgtype.UUID (test shorthand).
func pgUUIDv(id uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: id, Valid: true} }
