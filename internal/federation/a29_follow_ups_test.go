package federation

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// A29 follow-ups: the two clauses the third two-instance run left open — a block
// that says nothing, and a comment Create that never comes back out of a block
// window. Every test here names the lab observation it encodes.

// --- a block that speaks ---------------------------------------------------

const bobInbox = "https://remote.example/accounts/bob/inbox"

// blockedSenderRepo is rehearsal-3's topology: this instance holds channel
// `films`, `remote.example` is on the admin blocklist, and the would-be
// follower's actor is in the cache because signature verification put it there
// before the block check ever ran.
func blockedSenderRepo() fakeRepo {
	repo := newInboxRepo()
	repo.remoteActors = map[string]sqlcgen.RemoteActor{
		remoteBob: {ActorUrl: remoteBob, InboxUrl: bobInbox},
	}
	repo.deliveries = map[uuid.UUID]*fakeDelivery{}
	repo.blockedDomains = map[string]bool{"remote.example": true}
	repo.chanKeys = map[uuid.UUID]sqlcgen.GetChannelActorKeyRow{}
	repo.apDisabled = map[uuid.UUID]bool{}
	return repo
}

// THE RULING. The lab watched B's user follow A's channel while A had B's domain
// blocked: A answered 202 and dropped it, B's delivery read `delivered`, and B's
// row sat at `pending` forever because nothing on either side re-attempts or
// expires one. The owner ruled that an instance block SPEAKS — the sender is
// told, in the same signed Reject a creator's manual rejection sends, so the
// request reaches a terminal state the person who made it can see.
func TestBlockedInstanceFollowIsAnsweredWithAReject(t *testing.T) {
	repo := blockedSenderRepo()
	svc := NewService(repo, WithBaseURL("https://videos.example"))

	if err := svc.HandleInbox(context.Background(), remoteBob, []byte(filmsFollow)); !errors.Is(err, ErrSenderBlocked) {
		t.Fatalf("HandleInbox = %v, want ErrSenderBlocked (the wire answer stays 202 + the audit row)", err)
	}
	// Nothing about the refusal itself changed: no follow recorded, and the
	// activity is NOT marked processed, so an unblock lets a redelivery in.
	if len(repo.remoteFollows) != 0 {
		t.Fatalf("a blocked sender's Follow was recorded: %+v", repo.remoteFollows)
	}
	if repo.processed[followID] {
		t.Error("a dropped activity must not be marked processed")
	}

	if len(repo.deliveries) != 1 {
		t.Fatalf("queued deliveries = %d, want exactly 1 (the Reject)", len(repo.deliveries))
	}
	var d *fakeDelivery
	for _, row := range repo.deliveries {
		d = row
	}
	if d.row.InboxUrl != bobInbox {
		t.Errorf("the Reject went to %q, want the sender's own inbox %q", d.row.InboxUrl, bobInbox)
	}
	if d.enqueued.SigningChannelHandle != "films" {
		t.Errorf("signed as %q, want the followed channel `films` — vidra has no instance actor",
			d.enqueued.SigningChannelHandle)
	}

	// THE SHAPE. Byte-for-byte the shape RejectFollowerRequest sends, because a
	// sender must not have to recognise two kinds of refusal: the embedded
	// Follow carries the id of the activity being refused, so the sender can
	// match it to the row it is holding.
	var act struct {
		Context string `json:"@context"`
		ID      string `json:"id"`
		Type    string `json:"type"`
		Actor   string `json:"actor"`
		Object  struct {
			ID     string `json:"id"`
			Type   string `json:"type"`
			Actor  string `json:"actor"`
			Object string `json:"object"`
		} `json:"object"`
	}
	if err := json.Unmarshal(d.row.Payload, &act); err != nil {
		t.Fatalf("the Reject is not JSON: %v", err)
	}
	if act.Type != "Reject" || act.Actor != filmsActor {
		t.Errorf("envelope = {type:%q actor:%q}, want a Reject by the channel actor", act.Type, act.Actor)
	}
	if act.Object.ID != followID {
		t.Errorf("the Reject names follow %q, want %q — a sender cannot match a refusal to a request without it",
			act.Object.ID, followID)
	}
	if act.Object.Type != "Follow" || act.Object.Actor != remoteBob || act.Object.Object != filmsActor {
		t.Errorf("embedded Follow = %+v, want the refused Follow verbatim", act.Object)
	}
	if act.Context == "" || act.ID == "" {
		t.Error("the Reject must carry an @context and its own id")
	}
}

// The exception is a Follow and NOTHING else. Every other activity from a
// blocked instance stays 202-and-dropped with the audit row and no outbound
// speech at all, so a block still reveals nothing about content, about anyone
// else's activity, or about who else this instance talks to.
func TestABlockSpeaksOnlyForAFollowAndOnlyToItsOwnActor(t *testing.T) {
	note := `{"id":"https://remote.example/act/n1","type":"Create","actor":"` + remoteBob +
		`","object":{"id":"https://remote.example/notes/1","type":"Note","content":"hi"}}`
	undo := `{"id":"https://remote.example/act/u1","type":"Undo","actor":"` + remoteBob +
		`","object":{"id":"` + followID + `","type":"Follow","actor":"` + remoteBob + `","object":"` + filmsActor + `"}}`
	spoofed := `{"id":"https://remote.example/act/f2","type":"Follow","actor":"https://remote.example/accounts/eve","object":"` + filmsActor + `"}`
	unknownChannel := `{"id":"https://remote.example/act/f3","type":"Follow","actor":"` + remoteBob +
		`","object":"https://videos.example/video-channels/nosuch"}`
	accountFollow := `{"id":"https://remote.example/act/f4","type":"Follow","actor":"` + remoteBob +
		`","object":"https://videos.example/accounts/alice"}`

	tests := []struct {
		name string
		body string
		seed func(fakeRepo)
	}{
		{"a Create{Note} — content, not a relationship", note, nil},
		{"an Undo — already a severing activity, and not ours to answer", undo, nil},
		{"a Follow whose actor is not the signer — a spoof must not make us sign at a third party", spoofed, nil},
		{"a Follow of a channel we do not have", unknownChannel, nil},
		{"a Follow of an ACCOUNT actor, which handleFollow ignores too", accountFollow, nil},
		{"a Follow of a channel that opted out of ActivityPub (0096)", filmsFollow, func(r fakeRepo) {
			r.apDisabled[r.channels["films"].ID] = true
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := blockedSenderRepo()
			if tt.seed != nil {
				tt.seed(repo)
			}
			svc := NewService(repo, WithBaseURL("https://videos.example"))

			signer := remoteBob
			err := svc.HandleInbox(context.Background(), signer, []byte(tt.body))
			if !errors.Is(err, ErrSenderBlocked) {
				t.Fatalf("HandleInbox = %v, want ErrSenderBlocked", err)
			}
			if len(repo.deliveries) != 0 {
				t.Fatalf("a blocked instance was answered: %d deliveries queued", len(repo.deliveries))
			}
		})
	}
}

// A Reject queued while the block STANDS must still go out — otherwise the
// answer this whole slice adds is cancelled by the same block that produced it.
// A29-F5 already exempts a Reject from a destination block's cancellation; this
// pins that the exemption covers the Reject a block itself emits, driven all the
// way through the real drain to a real inbox rather than by re-reading
// severingActivity.
func TestTheBlocksOwnRejectIsDeliveredWhileTheBlockStands(t *testing.T) {
	var landed [][]byte
	inbox := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		landed = append(landed, body)
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(inbox.Close)

	repo := blockedSenderRepo()
	// The sender's inbox is on the blocked host too — which is the whole point:
	// the destination of this Reject is a domain this instance refuses to talk
	// to, and the Reject goes anyway.
	repo.remoteActors[remoteBob] = sqlcgen.RemoteActor{ActorUrl: remoteBob, InboxUrl: inbox.URL}
	repo.blockedDomains[strings.TrimPrefix(inbox.URL, "http://")] = true
	svc := NewService(repo, WithBaseURL("https://videos.example"), WithAllowPrivateFetch(true))

	if err := svc.HandleInbox(context.Background(), remoteBob, []byte(filmsFollow)); !errors.Is(err, ErrSenderBlocked) {
		t.Fatalf("HandleInbox = %v, want ErrSenderBlocked", err)
	}
	n, err := svc.DrainDeliveries(context.Background(), 10)
	if err != nil {
		t.Fatalf("DrainDeliveries: %v", err)
	}
	if n != 1 {
		t.Fatalf("delivered = %d, want 1 — the block cancelled its own Reject", n)
	}
	for id, d := range repo.deliveries {
		if d.lastError == deliveryCancelledBlocked {
			t.Fatalf("delivery %s was cancelled by the very block that queued it", id)
		}
	}
	if len(landed) != 1 {
		t.Fatalf("the blocked sender received %d activities, want 1", len(landed))
	}
	var got struct {
		Type   string `json:"type"`
		Object struct {
			ID string `json:"id"`
		} `json:"object"`
	}
	if err := json.Unmarshal(landed[0], &got); err != nil {
		t.Fatalf("what landed is not JSON: %v", err)
	}
	if got.Type != "Reject" || got.Object.ID != followID {
		t.Errorf("landed = %+v, want a Reject naming the refused Follow", got)
	}
}

// --- comment Creates resume on unblock -------------------------------------

// The lab measured this inside a clean block window: a Create{Note} cancelled by
// a block was left cancelled on unblock, because `redeliverable` resolved a
// Create's object only as `…/videos/<uuid>` and a comment's id is
// `…/comments/<uuid>`. A follower's mirrored thread was therefore permanently
// short every comment written while the block stood.
func TestRedeliverableResolvesCommentObjects(t *testing.T) {
	channelID := uuid.New()
	ownerID := uuid.New()
	ch := sqlcgen.Channel{ID: channelID, OwnerID: ownerID, Handle: "films", ActivitypubEnabled: true}
	apOff := sqlcgen.Channel{ID: uuid.New(), OwnerID: ownerID, Handle: "quiet"}

	public := sqlcgen.GetVideoByIDRow{ID: uuid.New(), ChannelID: channelID, Privacy: "public", State: "published"}
	private := sqlcgen.GetVideoByIDRow{ID: uuid.New(), ChannelID: channelID, Privacy: "private", State: "published"}
	quiet := sqlcgen.GetVideoByIDRow{ID: uuid.New(), ChannelID: apOff.ID, Privacy: "public", State: "published"}

	local := func(v uuid.UUID) sqlcgen.Comment {
		return sqlcgen.Comment{ID: uuid.New(), VideoID: v, UserID: pgtype.UUID{Bytes: ownerID, Valid: true}, Body: "nice"}
	}
	visible := local(public.ID)
	tombstoned := local(public.ID)
	tombstoned.DeletedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	onPrivate := local(private.ID)
	onQuiet := local(quiet.ID)
	remoteAuthored := sqlcgen.Comment{ID: uuid.New(), VideoID: public.ID, Body: "bonjour"}
	gone := uuid.New()

	repo := fakeRepo{
		channels:     map[string]sqlcgen.Channel{"films": ch, "quiet": apOff},
		channelsByID: map[uuid.UUID]sqlcgen.Channel{channelID: ch, apOff.ID: apOff},
		videosByID: map[uuid.UUID]sqlcgen.GetVideoByIDRow{
			public.ID: public, private.ID: private, quiet.ID: quiet,
		},
		commentsByID: map[uuid.UUID]sqlcgen.Comment{
			visible.ID: visible, tombstoned.ID: tombstoned, onPrivate.ID: onPrivate,
			onQuiet.ID: onQuiet, remoteAuthored.ID: remoteAuthored,
		},
		deliveries: map[uuid.UUID]*fakeDelivery{},
		apDisabled: map[uuid.UUID]bool{apOff.ID: true},
	}
	svc := NewService(repo, WithBaseURL("https://videos.example"))

	noteCreate := func(kind string, commentID uuid.UUID) []byte {
		return []byte(`{"@context":"https://www.w3.org/ns/activitystreams","type":"` + kind +
			`","actor":"https://videos.example/accounts/alice","object":{"id":"https://videos.example/comments/` +
			commentID.String() + `","type":"Note","content":"nice"}}`)
	}

	tests := []struct {
		name    string
		payload []byte
		want    bool
	}{
		{"Create{Note} whose comment is still there, on a still-public video", noteCreate("Create", visible.ID), true},
		{"Update{Note} of the same comment", noteCreate("Update", visible.ID), true},
		{"a comment that was hard-deleted while the block stood", noteCreate("Create", gone), false},
		{"a comment tombstoned by an account deletion", noteCreate("Create", tombstoned.ID), false},
		{"a comment whose video went private", noteCreate("Create", onPrivate.ID), false},
		{"a comment on a channel that opted out of ActivityPub", noteCreate("Create", onQuiet.ID), false},
		{"a remote-authored comment we never fan out in the first place", noteCreate("Create", remoteAuthored.ID), false},
		{"a Note id belonging to ANOTHER instance", []byte(`{"type":"Create","object":{"id":"https://peer.example/comments/` +
			visible.ID.String() + `","type":"Note"}}`), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := svc.redeliverable(context.Background(), tt.payload)
			if err != nil {
				t.Fatalf("redeliverable: %v", err)
			}
			if got != tt.want {
				t.Errorf("redeliverable = %v, want %v", got, tt.want)
			}
		})
	}
}

// The whole path, through the real cancellation and the real unblock: the cap
// and the window are the video path's because it IS the video path — one loop,
// one prefilter, one limit.
func TestUnblockRequeuesCancelledCommentCreates(t *testing.T) {
	const blockedHost = "blocked.example"
	channelID, ownerID := uuid.New(), uuid.New()
	ch := sqlcgen.Channel{ID: channelID, OwnerID: ownerID, Handle: "films", ActivitypubEnabled: true}
	public := sqlcgen.GetVideoByIDRow{ID: uuid.New(), ChannelID: channelID, Privacy: "public", State: "published"}
	private := sqlcgen.GetVideoByIDRow{ID: uuid.New(), ChannelID: channelID, Privacy: "private", State: "published"}

	keep := sqlcgen.Comment{ID: uuid.New(), VideoID: public.ID, UserID: pgtype.UUID{Bytes: ownerID, Valid: true}, Body: "keep"}
	hidden := sqlcgen.Comment{ID: uuid.New(), VideoID: private.ID, UserID: pgtype.UUID{Bytes: ownerID, Valid: true}, Body: "hidden"}

	repo := fakeRepo{
		channels:       map[string]sqlcgen.Channel{"films": ch},
		channelsByID:   map[uuid.UUID]sqlcgen.Channel{channelID: ch},
		videosByID:     map[uuid.UUID]sqlcgen.GetVideoByIDRow{public.ID: public, private.ID: private},
		commentsByID:   map[uuid.UUID]sqlcgen.Comment{keep.ID: keep, hidden.ID: hidden},
		deliveries:     map[uuid.UUID]*fakeDelivery{},
		blockedDomains: map[string]bool{blockedHost: true},
	}
	svc := NewService(repo, WithBaseURL("https://videos.example"))

	inbox := "https://" + blockedHost + "/inbox"
	payloads := map[string][]byte{}
	for name, c := range map[string]sqlcgen.Comment{"resumable": keep, "on a private video": hidden} {
		p, err := svc.buildNoteActivity("Create", "alice", c, "https://videos.example/videos/"+c.VideoID.String())
		if err != nil {
			t.Fatalf("buildNoteActivity: %v", err)
		}
		payloads[name] = p
		if err := repo.EnqueueDelivery(context.Background(), sqlcgen.EnqueueDeliveryParams{
			InboxUrl: inbox, Payload: p, SigningUsername: "alice",
		}); err != nil {
			t.Fatalf("EnqueueDelivery: %v", err)
		}
	}

	blockedAt := time.Now().Add(-time.Minute)
	if n, err := svc.DrainDeliveries(context.Background(), 10); err != nil || n != 0 {
		t.Fatalf("DrainDeliveries = (%d, %v), want (0, nil) — the destination is blocked", n, err)
	}
	for id, d := range repo.deliveries {
		if d.lastError != deliveryCancelledBlocked {
			t.Fatalf("delivery %s = %q, want the drain's own cancel marker", id, d.lastError)
		}
	}

	requeued, err := svc.RedeliverAfterUnblock(context.Background(), blockedHost, blockedAt)
	if err != nil {
		t.Fatalf("RedeliverAfterUnblock: %v", err)
	}
	if requeued != 1 {
		t.Fatalf("requeued = %d, want 1 (the comment on the still-public video, and only it)", requeued)
	}
	for _, d := range repo.deliveries {
		want := string(d.row.Payload) == string(payloads["resumable"])
		if got := d.state == "pending"; got != want {
			t.Errorf("payload %s: requeued = %v, want %v", d.row.Payload, got, want)
		}
	}
}

// --- the other end of the same wire ----------------------------------------

// THE SENDER'S HALF, and the reason it is worth a test of its own: the two
// instances in the lab are the same code, so the payload one side MINTS for a
// block and the payload the other side UNDERSTANDS must be the same object, and
// a test that hand-wrote the Reject would prove only that the sender parses what
// the test author imagined.
//
// So the Reject here is built by buildRejectFollow — the exact function the
// blocking instance's inbox path calls — as if the remote had produced it, and
// fed to the SENDER's inbox. The sender's row ends `rejected`: terminal,
// creator-visible in GET /me/remote-follows, and re-armable to `pending` with a
// fresh Follow id by one deliberate re-follow. Nothing tells it a block was the
// reason, which is the point: the sender learns that this channel will not have
// it, and nothing about the blocklist.
func TestAnInstanceBlocksRejectEndsTheSendersRow(t *testing.T) {
	userID := uuid.New()
	repo := newFollowRepo(userID)
	cacheRemoteChannel(repo, remoteChan, "remote.example", nil)
	svc := NewService(repo, WithBaseURL("https://videos.example"))

	follow, err := svc.FollowRemoteChannel(context.Background(), userID, remoteChan)
	if err != nil {
		t.Fatalf("follow: %v", err)
	}
	followURL := repo.rcFollows[follow.ID].FollowActivityUrl

	// The blocking instance, minting its refusal with its own baseURL.
	blocker := NewService(newInboxRepo(), WithBaseURL("https://remote.example"))
	payload, err := blocker.buildRejectFollow("movies", "https://videos.example/accounts/ada", followURL)
	if err != nil {
		t.Fatalf("buildRejectFollow: %v", err)
	}

	if err := svc.HandleInbox(context.Background(), remoteChan, payload); err != nil {
		t.Fatalf("HandleInbox Reject: %v", err)
	}
	if got := repo.rcFollows[follow.ID].State; got != "rejected" {
		t.Fatalf("state after a block's Reject = %q, want rejected (it sat at `pending` forever in rehearsal 3)", got)
	}

	again, err := svc.FollowRemoteChannel(context.Background(), userID, remoteChan)
	if err != nil {
		t.Fatalf("re-follow: %v", err)
	}
	if got := repo.rcFollows[again.ID].State; got != "pending" {
		t.Fatalf("state after re-follow = %q, want pending", got)
	}
	if repo.rcFollows[again.ID].FollowActivityUrl == followURL {
		t.Error("a retry must mint a NEW Follow activity id; the old one was already answered")
	}
}
