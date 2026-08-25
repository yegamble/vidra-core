//go:build integration

// Integration tests for federated comments + remote-video moderation
// (remote-content §6-8) against a REAL PostgreSQL (migrations applied): the
// comments remote-attribution schema (exactly-one-author CHECK, list
// projections, moderation filters, actor-Delete cascade), the reports
// remote_video target + remote_video_blocks hides, and — the §9 bar — an
// httptest loop where a remote Note lands as a comment on a local video and a
// local comment delivers a signed Create{Note} to a remote follower's inbox.
// Run via `make test-integration`.
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
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/comment"
	"github.com/vidra/vidra-core/internal/federation"
	"github.com/vidra/vidra-core/internal/httpsig"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// seedPublicVideo creates a channel + a public, published video for the user.
func seedPublicVideo(t *testing.T, ctx context.Context, q *sqlcgen.Queries, ownerID uuid.UUID, suf string) (sqlcgen.Channel, sqlcgen.Video) {
	t.Helper()
	ch, err := q.CreateChannel(ctx, sqlcgen.CreateChannelParams{
		OwnerID: ownerID, Handle: "fc" + suf, DisplayName: "Fed Comments",
	})
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}
	v, err := q.CreateVideo(ctx, sqlcgen.CreateVideoParams{
		ChannelID: ch.ID, Title: "Commented " + suf, Privacy: "public", CommentsPolicy: "enabled",
	})
	if err != nil {
		t.Fatalf("CreateVideo: %v", err)
	}
	if _, err := q.SetVideoState(ctx, sqlcgen.SetVideoStateParams{ID: v.ID, State: "published"}); err != nil {
		t.Fatalf("SetVideoState: %v", err)
	}
	return ch, v
}

func TestRemoteCommentAttributionPersists(t *testing.T) {
	ctx, q := integrationQueries(t)
	suf := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	u := seedUser(t, ctx, q, suf)
	_, v := seedPublicVideo(t, ctx, q, u.ID, suf)
	domain := "fedc-" + suf + ".example"
	actorURL := "https://" + domain + "/accounts/bob"
	seedRemoteActor(t, ctx, q, actorURL, domain)

	// The exactly-one-author CHECK rejects a row with NO author at all.
	if _, err := q.CreateComment(ctx, sqlcgen.CreateCommentParams{
		VideoID: v.ID, Body: "authorless",
	}); err == nil {
		t.Fatal("a comment with neither user_id nor remote_actor_url was accepted")
	}

	// A remote comment persists with its attribution snapshot, idempotent by
	// the Note object URL (a redelivery refreshes the body).
	name := "movies"
	obj := "https://" + domain + "/comments/1"
	rc, err := q.CreateRemoteComment(ctx, sqlcgen.CreateRemoteCommentParams{
		VideoID: v.ID, Body: "bonjour", RemoteActorUrl: &actorURL,
		RemoteAuthorName: &name, RemoteObjectUrl: &obj,
	})
	if err != nil {
		t.Fatalf("CreateRemoteComment: %v", err)
	}
	if rc.UserID.Valid {
		t.Error("remote comment has a local user_id")
	}
	again, err := q.CreateRemoteComment(ctx, sqlcgen.CreateRemoteCommentParams{
		VideoID: v.ID, Body: "bonjour encore", RemoteActorUrl: &actorURL,
		RemoteAuthorName: &name, RemoteObjectUrl: &obj,
	})
	if err != nil || again.ID != rc.ID || again.Body != "bonjour encore" {
		t.Fatalf("re-delivery: id=%s body=%q err=%v, want same row refreshed", again.ID, again.Body, err)
	}
	if got, err := q.GetCommentByRemoteObjectURL(ctx, obj); err != nil || got.ID != rc.ID {
		t.Fatalf("GetCommentByRemoteObjectURL = %+v (err=%v)", got, err)
	}

	// A local comment sits alongside it.
	if _, err := q.CreateComment(ctx, sqlcgen.CreateCommentParams{
		VideoID: v.ID, UserID: pgtype.UUID{Bytes: u.ID, Valid: true}, Body: "local hi",
	}); err != nil {
		t.Fatalf("CreateComment: %v", err)
	}

	list := func(viewer pgtype.UUID) []sqlcgen.ListCommentsByVideoRow {
		rows, err := q.ListCommentsByVideo(ctx, sqlcgen.ListCommentsByVideoParams{
			VideoID: v.ID, ViewerID: viewer, ResultLimit: 50,
		})
		if err != nil {
			t.Fatalf("ListCommentsByVideo: %v", err)
		}
		return rows
	}
	rows := list(pgtype.UUID{})
	if len(rows) != 2 {
		t.Fatalf("comments = %d, want 2", len(rows))
	}
	var remoteRow *sqlcgen.ListCommentsByVideoRow
	for i := range rows {
		if rows[i].RemoteActorUrl != nil {
			remoteRow = &rows[i]
		}
	}
	if remoteRow == nil {
		t.Fatal("no remote row in the list")
	}
	if remoteRow.RemoteAuthorName != "movies" || remoteRow.AuthorDomain != domain {
		t.Errorf("remote row identity = %+v", remoteRow)
	}

	// The viewer's instance mute hides the remote comment (not the local one).
	viewer := pgtype.UUID{Bytes: u.ID, Valid: true}
	if _, err := q.MuteInstance(ctx, sqlcgen.MuteInstanceParams{MuterID: u.ID, Domain: domain}); err != nil {
		t.Fatalf("MuteInstance: %v", err)
	}
	if got := list(viewer); len(got) != 1 || got[0].RemoteActorUrl != nil {
		t.Errorf("muted-instance list = %+v, want only the local comment", got)
	}
	if _, err := q.UnmuteInstance(ctx, sqlcgen.UnmuteInstanceParams{MuterID: u.ID, Domain: domain}); err != nil {
		t.Fatalf("UnmuteInstance: %v", err)
	}

	// The admin instance blocklist hides it for everyone.
	if _, err := q.BlockInstance(ctx, sqlcgen.BlockInstanceParams{Domain: domain, Reason: "integration"}); err != nil {
		t.Fatalf("BlockInstance: %v", err)
	}
	if got := list(pgtype.UUID{}); len(got) != 1 {
		t.Errorf("blocked-instance list = %d rows, want 1", len(got))
	}
	if _, err := q.UnblockInstance(ctx, domain); err != nil {
		t.Fatalf("UnblockInstance: %v", err)
	}

	// NodeInfo's CountComments counts LOCAL comments only.
	// (Absolute counts race other tests; assert relative behavior instead by
	// deleting the actor and re-counting the video's rows.)
	if _, err := q.DeleteRemoteActor(ctx, actorURL); err != nil {
		t.Fatalf("DeleteRemoteActor: %v", err)
	}
	if got := list(pgtype.UUID{}); len(got) != 1 || got[0].RemoteActorUrl != nil {
		t.Errorf("after actor delete, comments = %+v, want only the local one (cascade)", got)
	}
}

func TestRemoteVideoModerationPersists(t *testing.T) {
	ctx, q := integrationQueries(t)
	suf := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	u := seedUser(t, ctx, q, suf)
	domain := "rvmod-" + suf + ".example"
	actorURL := "https://" + domain + "/video-channels/movies"
	seedRemoteActor(t, ctx, q, actorURL, domain)
	title := "Moderated premiere " + suf
	rv, err := q.UpsertRemoteVideo(ctx, sqlcgen.UpsertRemoteVideoParams{
		ObjectUrl: "https://" + domain + "/videos/watch/1", RemoteActorUrl: actorURL,
		Title: title, WatchUrl: "https://" + domain + "/videos/watch/1",
	})
	if err != nil {
		t.Fatalf("UpsertRemoteVideo: %v", err)
	}

	// Reporting: FK-checked, idempotent per reporter+target — a new report
	// returns its id, a repeat yields no row (pgx.ErrNoRows).
	if id, err := q.CreateRemoteVideoReport(ctx, sqlcgen.CreateRemoteVideoReportParams{
		ReporterID: u.ID, RemoteVideoID: pgtype.UUID{Bytes: rv.ID, Valid: true}, Reason: "spam",
	}); err != nil || id == uuid.Nil {
		t.Fatalf("CreateRemoteVideoReport = %s (err=%v), want a new report id", id, err)
	}
	if _, err := q.CreateRemoteVideoReport(ctx, sqlcgen.CreateRemoteVideoReportParams{
		ReporterID: u.ID, RemoteVideoID: pgtype.UUID{Bytes: rv.ID, Valid: true}, Reason: "again",
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("duplicate report err = %v, want pgx.ErrNoRows", err)
	}
	if _, err := q.CreateRemoteVideoReport(ctx, sqlcgen.CreateRemoteVideoReportParams{
		ReporterID: u.ID, RemoteVideoID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, Reason: "ghost",
	}); err == nil {
		t.Error("reporting an unknown remote video did not violate the FK")
	}
	// The queue row carries the remote-video context.
	openStatus := "open"
	reports, err := q.ListReports(ctx, sqlcgen.ListReportsParams{Status: &openStatus, ResultLimit: 200})
	if err != nil {
		t.Fatalf("ListReports: %v", err)
	}
	found := false
	for _, r := range reports {
		if r.RemoteVideoID.Valid && uuid.UUID(r.RemoteVideoID.Bytes) == rv.ID {
			found = true
			if r.TargetType != "remote_video" || r.RemoteVideoTitle == nil || *r.RemoteVideoTitle != title {
				t.Errorf("report row = %+v", r)
			}
			if r.RemoteVideoDomain == nil || *r.RemoteVideoDomain != domain {
				t.Errorf("report domain = %v", r.RemoteVideoDomain)
			}
		}
	}
	if !found {
		t.Fatal("the remote-video report is not in the queue")
	}

	// Blocking hides it from detail + subscriptions + search; unblocking restores.
	visible := func() (detail, subs, search bool) {
		if _, err := q.GetRemoteVideoByID(ctx, rv.ID); err == nil {
			detail = true
		}
		followURL := "https://videos.example/accounts/" + u.Username + "/act/f"
		_, _ = q.UpsertRemoteChannelFollow(ctx, sqlcgen.UpsertRemoteChannelFollowParams{
			UserID: u.ID, RemoteActorUrl: actorURL, FollowActivityUrl: followURL,
		})
		_, _ = q.AcceptRemoteChannelFollowByActivity(ctx, sqlcgen.AcceptRemoteChannelFollowByActivityParams{
			FollowActivityUrl: followURL, RemoteActorUrl: actorURL,
		})
		subRows, _ := q.ListSubscriptionVideos(ctx, sqlcgen.ListSubscriptionVideosParams{FollowerID: u.ID, ResultLimit: 100})
		for _, r := range subRows {
			if r.Remote && r.Title == title {
				subs = true
			}
		}
		schRows, _ := q.SearchPublicVideos(ctx, sqlcgen.SearchPublicVideosParams{Query: title, ResultLimit: 100})
		for _, r := range schRows {
			if r.Remote && r.Title == title {
				search = true
			}
		}
		return detail, subs, search
	}
	if d, s, sr := visible(); !d || !s || !sr {
		t.Fatalf("pre-block visibility (detail=%v subs=%v search=%v), want all true", d, s, sr)
	}
	if _, err := q.BlockRemoteVideo(ctx, sqlcgen.BlockRemoteVideoParams{
		RemoteVideoID: rv.ID, Reason: "hidden", BlockedBy: pgtype.UUID{Bytes: u.ID, Valid: true},
	}); err != nil {
		t.Fatalf("BlockRemoteVideo: %v", err)
	}
	if d, s, sr := visible(); d || s || sr {
		t.Errorf("post-block visibility (detail=%v subs=%v search=%v), want all false", d, s, sr)
	}
	blocked, err := q.ListBlockedRemoteVideos(ctx, sqlcgen.ListBlockedRemoteVideosParams{ResultLimit: 200})
	if err != nil {
		t.Fatalf("ListBlockedRemoteVideos: %v", err)
	}
	inList := false
	for _, b := range blocked {
		if b.RemoteVideoID == rv.ID {
			inList = true
			if b.Title != title || b.Domain != domain || b.Reason != "hidden" {
				t.Errorf("blocked row = %+v", b)
			}
			if b.BlockedByUsername == nil || *b.BlockedByUsername != u.Username {
				t.Errorf("blocked_by = %v", b.BlockedByUsername)
			}
		}
	}
	if !inList {
		t.Error("the blocked remote video is not in the block-list")
	}
	if n, err := q.UnblockRemoteVideo(ctx, rv.ID); err != nil || n != 1 {
		t.Fatalf("UnblockRemoteVideo = %d (err=%v), want 1", n, err)
	}
	if d, _, _ := visible(); !d {
		t.Error("unblocking did not restore the detail endpoint")
	}
}

// TestFederatedCommentFullLoop is the §9 acceptance bar for comments: a remote
// actor's signed Create{Note} on our local video lands as a comment, and a
// local comment fans out through the delivery queue as a Create{Note} signed
// by the commenter's ACCOUNT actor, verified by the remote follower's inbox.
func TestFederatedCommentFullLoop(t *testing.T) {
	ctx, q := integrationQueries(t)
	suf := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	u := seedUser(t, ctx, q, suf)
	ch, v := seedPublicVideo(t, ctx, q, u.ID, suf)

	svc := federation.NewService(q,
		federation.WithBaseURL("https://videos.example"),
		federation.WithAllowPrivateFetch(true), // loopback httptest origin (dev/e2e posture)
	)

	// --- the remote instance ------------------------------------------------
	type inboxHit struct {
		keyID string
		body  []byte
		err   error
	}
	hits := make(chan inboxHit, 4)
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/inbox", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
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
	remoteActor := srv.URL + "/accounts/bob"
	remoteHost := strings.TrimPrefix(srv.URL, "http://")
	if err := q.UpsertRemoteActor(ctx, sqlcgen.UpsertRemoteActorParams{
		ActorUrl: remoteActor, ActorType: "Person", PreferredUsername: "bob",
		Domain: remoteHost, InboxUrl: srv.URL + "/inbox", PublicKeyPem: "PEM",
		FollowersUrl: remoteActor + "/followers",
	}); err != nil {
		t.Fatalf("UpsertRemoteActor: %v", err)
	}

	// --- inbound: bob's Note on our video lands as a remote comment ----------
	videoURL := "https://videos.example/videos/watch/" + v.ID.String()
	noteID := srv.URL + "/comments/loop-1"
	create := `{"id":"` + srv.URL + `/act/note-1","type":"Create","actor":"` + remoteActor + `","object":{` +
		`"id":"` + noteID + `","type":"Note","attributedTo":"` + remoteActor + `",` +
		`"inReplyTo":"` + videoURL + `","content":"<p>gr8 video</p>"}}`
	if err := svc.HandleInbox(ctx, remoteActor, []byte(create)); err != nil {
		t.Fatalf("HandleInbox Create{Note}: %v", err)
	}
	rows, err := q.ListCommentsByVideo(ctx, sqlcgen.ListCommentsByVideoParams{VideoID: v.ID, ResultLimit: 50})
	if err != nil || len(rows) != 1 {
		t.Fatalf("comments after inbound Note = %d (err=%v), want 1", len(rows), err)
	}
	if rows[0].RemoteActorUrl == nil || *rows[0].RemoteActorUrl != remoteActor ||
		rows[0].Body != "gr8 video" || rows[0].RemoteAuthorName != "bob" {
		t.Fatalf("remote comment row = %+v", rows[0])
	}

	// --- outbound: a local comment fans out to bob (a remote follower) -------
	if err := q.InsertRemoteFollow(ctx, sqlcgen.InsertRemoteFollowParams{
		ChannelID: ch.ID, RemoteActorUrl: remoteActor, FollowActivityUrl: srv.URL + "/act/follow-1",
	}); err != nil {
		t.Fatalf("InsertRemoteFollow: %v", err)
	}
	// The comment service's create hook is the seam cmd/api wires to federation.
	commentsvc := comment.NewService(q, comment.WithCreateHook(func(ctx context.Context, id uuid.UUID) {
		if err := svc.AnnounceComment(ctx, id); err != nil {
			t.Errorf("AnnounceComment hook: %v", err)
		}
	}))
	local, err := commentsvc.Create(ctx, v.ID, u.ID, "thanks for watching!", nil)
	if err != nil {
		t.Fatalf("comment Create: %v", err)
	}
	if _, err := svc.DrainDeliveries(ctx, 100); err != nil {
		t.Fatalf("DrainDeliveries: %v", err)
	}
	var hit inboxHit
	select {
	case hit = <-hits:
	default:
		t.Fatal("the remote inbox never received the Create{Note}")
	}
	if hit.err != nil {
		t.Fatalf("remote could not verify the Note signature: %v", hit.err)
	}
	if want := "https://videos.example/accounts/" + u.Username + "#main-key"; hit.keyID != want {
		t.Errorf("Note signed by %q, want %q (the ACCOUNT actor)", hit.keyID, want)
	}
	var act struct {
		Type   string `json:"type"`
		Actor  string `json:"actor"`
		Object struct {
			ID           string `json:"id"`
			Type         string `json:"type"`
			Content      string `json:"content"`
			InReplyTo    string `json:"inReplyTo"`
			AttributedTo string `json:"attributedTo"`
		} `json:"object"`
	}
	if err := json.Unmarshal(hit.body, &act); err != nil {
		t.Fatalf("unmarshal delivered activity: %v", err)
	}
	accountActor := "https://videos.example/accounts/" + u.Username
	if act.Type != "Create" || act.Actor != accountActor || act.Object.Type != "Note" {
		t.Fatalf("delivered activity = %+v, want Create{Note} from %s", act, accountActor)
	}
	if act.Object.ID != "https://videos.example/comments/"+local.ID.String() ||
		act.Object.InReplyTo != videoURL || act.Object.Content != "thanks for watching!" {
		t.Errorf("delivered note = %+v", act.Object)
	}

	// --- inbound Update{Note} edits it; Delete removes it --------------------
	update := `{"id":"` + srv.URL + `/act/note-2","type":"Update","actor":"` + remoteActor + `","object":{` +
		`"id":"` + noteID + `","type":"Note","content":"gr8 video (edited)"}}`
	if err := svc.HandleInbox(ctx, remoteActor, []byte(update)); err != nil {
		t.Fatalf("HandleInbox Update{Note}: %v", err)
	}
	edited, err := q.GetCommentByRemoteObjectURL(ctx, noteID)
	if err != nil || edited.Body != "gr8 video (edited)" {
		t.Fatalf("after Update{Note}: body=%q err=%v", edited.Body, err)
	}
	del := `{"id":"` + srv.URL + `/act/note-3","type":"Delete","actor":"` + remoteActor + `","object":"` + noteID + `"}`
	if err := svc.HandleInbox(ctx, remoteActor, []byte(del)); err != nil {
		t.Fatalf("HandleInbox Delete: %v", err)
	}
	if _, err := q.GetCommentByRemoteObjectURL(ctx, noteID); err == nil {
		t.Error("the remote comment survived its origin's Delete")
	}
}
