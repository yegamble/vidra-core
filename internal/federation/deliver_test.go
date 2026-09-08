package federation

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/httpsig"
	"github.com/vidra/vidra-core/internal/secretbox"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// httpsigVerifier returns a verify closure using a fixed public key.
func httpsigVerifier(pub *rsa.PublicKey) func(*http.Request, []byte) (string, error) {
	v := httpsig.Verifier{ResolveKey: func(context.Context, string) (*rsa.PublicKey, error) { return pub, nil }}
	return func(r *http.Request, body []byte) (string, error) {
		return v.Verify(r.Context(), r, body)
	}
}

func TestSendAcceptFollowSignsAndDelivers(t *testing.T) {
	channelID := uuid.New()
	follower := "https://remote.example/accounts/bob"
	followID := "https://remote.example/activities/follow/1"

	repo := fakeRepo{
		channels:     map[string]sqlcgen.Channel{"films": {ID: channelID, Handle: "films"}},
		chanKeys:     map[uuid.UUID]sqlcgen.GetChannelActorKeyRow{},
		remoteActors: map[string]sqlcgen.RemoteActor{},
	}

	// The follower's inbox verifies the signed Accept it receives.
	var (
		gotBody     []byte
		verifiedKey string
		verifyErr   error
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		pub, err := parseRSAPublicKey(repo.chanKeys[channelID].PublicKeyPem)
		if err != nil {
			t.Errorf("parse minted channel key: %v", err)
		}
		v := httpsigVerifier(pub)
		verifiedKey, verifyErr = v(r, gotBody)
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(srv.Close)

	repo.remoteActors[follower] = sqlcgen.RemoteActor{ActorUrl: follower, InboxUrl: srv.URL}
	svc := NewService(repo, WithBaseURL("https://videos.example"), WithAllowPrivateFetch(true))

	if err := svc.SendAcceptFollow(context.Background(), channelID, "films", follower, followID); err != nil {
		t.Fatalf("SendAcceptFollow: %v", err)
	}
	if verifyErr != nil {
		t.Fatalf("inbox could not verify the Accept signature: %v", verifyErr)
	}
	if want := "https://videos.example/video-channels/films#main-key"; verifiedKey != want {
		t.Errorf("signed by %q, want %q", verifiedKey, want)
	}

	var accept struct {
		Type   string `json:"type"`
		Actor  string `json:"actor"`
		Object struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		} `json:"object"`
	}
	if err := json.Unmarshal(gotBody, &accept); err != nil {
		t.Fatalf("unmarshal Accept: %v", err)
	}
	if accept.Type != "Accept" || accept.Actor != "https://videos.example/video-channels/films" {
		t.Errorf("accept = %+v", accept)
	}
	if accept.Object.Type != "Follow" || accept.Object.ID != followID {
		t.Errorf("accept.object = %+v, want the Follow %q", accept.Object, followID)
	}
}

func TestUnlockPrivateKeyRawAndSealed(t *testing.T) {
	priv, rawPEM := testPrivatePEM(t)

	// Raw (dev, no cipher).
	got, err := (&Service{}).unlockPrivateKey(rawPEM)
	if err != nil {
		t.Fatalf("unlock raw: %v", err)
	}
	if got.N.Cmp(priv.N) != 0 {
		t.Error("unlocked raw key differs from the original")
	}

	// Sealed (needs the cipher).
	cipher, err := secretbox.NewCipher(make([]byte, 32))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	sealed, err := cipher.Seal([]byte(rawPEM))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	svc := NewService(fakeRepo{}, WithCipher(cipher))
	got2, err := svc.unlockPrivateKey(sealed)
	if err != nil {
		t.Fatalf("unlock sealed: %v", err)
	}
	if got2.N.Cmp(priv.N) != 0 {
		t.Error("unlocked sealed key differs from the original")
	}

	// Sealed value but no cipher → error (never silently mishandle a secret).
	if _, err := (&Service{}).unlockPrivateKey(sealed); err == nil {
		t.Error("unlock of a sealed key without a cipher must fail")
	}
}

func TestDrainDeliveriesSignsDeliversAndMarks(t *testing.T) {
	channelID := uuid.New()
	repo := fakeRepo{
		chanKeys:   map[uuid.UUID]sqlcgen.GetChannelActorKeyRow{},
		deliveries: map[uuid.UUID]*fakeDelivery{},
	}
	var verifyErr error
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		pub, err := parseRSAPublicKey(repo.chanKeys[channelID].PublicKeyPem)
		if err != nil {
			t.Errorf("parse channel key: %v", err)
		}
		_, verifyErr = httpsigVerifier(pub)(r, body)
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(srv.Close)

	svc := NewService(repo, WithBaseURL("https://videos.example"), WithAllowPrivateFetch(true))
	if err := svc.enqueueChannelDelivery(context.Background(), channelID, "films", srv.URL, []byte(`{"type":"Accept"}`)); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	n, err := svc.DrainDeliveries(context.Background(), 10)
	if err != nil {
		t.Fatalf("DrainDeliveries: %v", err)
	}
	if n != 1 {
		t.Fatalf("delivered = %d, want 1", n)
	}
	if verifyErr != nil {
		t.Errorf("inbox could not verify the delivered signature: %v", verifyErr)
	}
	for _, d := range repo.deliveries {
		if d.state != "delivered" {
			t.Errorf("delivery state = %q, want delivered", d.state)
		}
	}
}

func TestDrainDeliveriesSignsAsAccountActor(t *testing.T) {
	userID := uuid.New()
	repo := fakeRepo{
		acctKeys:   map[uuid.UUID]sqlcgen.GetAccountActorKeyRow{},
		deliveries: map[uuid.UUID]*fakeDelivery{},
	}
	var (
		verifiedKey string
		verifyErr   error
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		pub, err := parseRSAPublicKey(repo.acctKeys[userID].PublicKeyPem)
		if err != nil {
			t.Errorf("parse minted account key: %v", err)
		}
		verifiedKey, verifyErr = httpsigVerifier(pub)(r, body)
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(srv.Close)

	svc := NewService(repo, WithBaseURL("https://videos.example"), WithAllowPrivateFetch(true))
	// Mint the account key first (as FollowRemoteChannel does before enqueue).
	if _, err := svc.ensureAccountKey(context.Background(), userID); err != nil {
		t.Fatalf("ensureAccountKey: %v", err)
	}
	if err := svc.enqueueAccountDelivery(context.Background(), userID, "ada", srv.URL, []byte(`{"type":"Follow"}`)); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	n, err := svc.DrainDeliveries(context.Background(), 10)
	if err != nil {
		t.Fatalf("DrainDeliveries: %v", err)
	}
	if n != 1 {
		t.Fatalf("delivered = %d, want 1", n)
	}
	if verifyErr != nil {
		t.Fatalf("inbox could not verify the account-actor signature: %v", verifyErr)
	}
	if want := "https://videos.example/accounts/ada#main-key"; verifiedKey != want {
		t.Errorf("signed by %q, want %q", verifiedKey, want)
	}
}

func TestDrainDeliveriesReschedulesOnFailure(t *testing.T) {
	channelID := uuid.New()
	repo := fakeRepo{
		chanKeys:   map[uuid.UUID]sqlcgen.GetChannelActorKeyRow{},
		deliveries: map[uuid.UUID]*fakeDelivery{},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	svc := NewService(repo, WithBaseURL("https://videos.example"), WithAllowPrivateFetch(true))
	if err := svc.enqueueChannelDelivery(context.Background(), channelID, "films", srv.URL, []byte(`{}`)); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	n, err := svc.DrainDeliveries(context.Background(), 10)
	if err != nil {
		t.Fatalf("DrainDeliveries: %v", err)
	}
	if n != 0 {
		t.Fatalf("delivered = %d, want 0 (target 500s)", n)
	}
	for _, d := range repo.deliveries {
		if d.state != "pending" {
			t.Errorf("state = %q, want pending (rescheduled, not failed before the cap)", d.state)
		}
		if d.row.Attempts != 1 {
			t.Errorf("attempts = %d, want 1", d.row.Attempts)
		}
		if !d.nextAttempt.After(time.Now()) {
			t.Error("next attempt should be scheduled in the future (backoff)")
		}
	}
}

func testPrivatePEM(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return key, string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

// --- redelivery after an instance unblock (A29-F4) ---------------------------

// TestRedeliverAfterUnblock is the half of A29-F4 that stayed open when the
// audit row for a refused INBOUND activity landed.
//
// A block is symmetric in effect and asymmetric in recoverability. Inbound
// activities were answered 202 and dropped, so the remote will never resend
// them and nothing can bring them back. But this instance's own OUTBOUND
// deliveries were cancelled with their payloads intact, and leaving them
// cancelled means a remote follower misses every video published during the
// block, forever, with no reconciliation path. This is the repairable half.
//
// The test drives the CANCELLATION through DrainDeliveries rather than writing
// the marker by hand: the string the drain writes and the string the redelivery
// matches on have to be the same one, and a test that spelled it twice would
// pass while they drifted apart.
func TestRedeliverAfterUnblock(t *testing.T) {
	const blockedHost = "blocked.example"
	const otherHost = "other.example"
	channelID := uuid.New()
	ch := sqlcgen.Channel{ID: channelID, Handle: "films", ActivitypubEnabled: true}

	publicVideo := sqlcgen.GetVideoByIDRow{ID: uuid.New(), ChannelID: channelID, Privacy: "public", State: "published"}
	privatedVideo := sqlcgen.GetVideoByIDRow{ID: uuid.New(), ChannelID: channelID, Privacy: "private", State: "published"}
	deletedVideoID := uuid.New()

	repo := fakeRepo{
		channels:       map[string]sqlcgen.Channel{"films": ch},
		channelsByID:   map[uuid.UUID]sqlcgen.Channel{channelID: ch},
		videosByID:     map[uuid.UUID]sqlcgen.GetVideoByIDRow{publicVideo.ID: publicVideo, privatedVideo.ID: privatedVideo},
		deliveries:     map[uuid.UUID]*fakeDelivery{},
		blockedDomains: map[string]bool{blockedHost: true},
	}
	svc := NewService(repo, WithBaseURL("https://videos.example"))

	build := func(t *testing.T, kind string, v sqlcgen.GetVideoByIDRow) []byte {
		t.Helper()
		payload, err := svc.buildVideoActivity(kind, "films", v)
		if err != nil {
			t.Fatalf("buildVideoActivity(%s): %v", kind, err)
		}
		return payload
	}
	deletePayload, err := svc.buildDeleteVideo("films", deletedVideoID)
	if err != nil {
		t.Fatalf("buildDeleteVideo: %v", err)
	}
	// Only the envelope type is read for an Accept; it carries no object this
	// instance could re-check, and its absence strands a granted follow.
	acceptPayload := []byte(`{"@context":"https://www.w3.org/ns/activitystreams","type":"Accept","actor":"https://videos.example/video-channels/films","object":"https://blocked.example/activities/follow/1"}`)

	inbox := "https://" + blockedHost + "/inbox"
	cases := []struct {
		name    string
		inbox   string
		payload []byte
		want    bool
	}{
		{"Create of a still-public video", inbox, build(t, "Create", publicVideo), true},
		{"Update of a still-public video", inbox, build(t, "Update", publicVideo), true},
		{"Create of a video that went private", inbox, build(t, "Create", privatedVideo), false},
		{"Create of a video that is gone", inbox, build(t, "Create", sqlcgen.GetVideoByIDRow{ID: deletedVideoID, ChannelID: channelID, Privacy: "public", State: "published"}), false},
		{"Delete, which can only reduce what the remote holds", inbox, deletePayload, true},
		{"Accept, which strands a granted follow if dropped", inbox, acceptPayload, true},
		{"an Announce, which is not in the resumed set", inbox, []byte(`{"type":"Announce","object":"https://videos.example/videos/x"}`), false},
		{"an unparseable payload", inbox, []byte(`not json`), false},
	}

	ids := map[string]uuid.UUID{}
	for _, tc := range cases {
		if err := repo.EnqueueDelivery(context.Background(), sqlcgen.EnqueueDeliveryParams{
			InboxUrl: tc.inbox, Payload: tc.payload,
			SigningChannelHandle: "films",
		}); err != nil {
			t.Fatalf("EnqueueDelivery: %v", err)
		}
	}
	// A delivery to a DIFFERENT host, so the domain filter has something to
	// exclude that is otherwise identical.
	if err := repo.EnqueueDelivery(context.Background(), sqlcgen.EnqueueDeliveryParams{
		InboxUrl: "https://" + otherHost + "/inbox", Payload: build(t, "Create", publicVideo),
		SigningChannelHandle: "films",
	}); err != nil {
		t.Fatalf("EnqueueDelivery: %v", err)
	}
	repo.blockedDomains[otherHost] = true

	// THE CANCELLATION, through the real drain. Both hosts are blocked, so no
	// HTTP is attempted and every row is cancelled with the drain's own marker.
	blockedAt := time.Now().Add(-time.Minute)
	if n, err := svc.DrainDeliveries(context.Background(), 50); err != nil || n != 0 {
		t.Fatalf("DrainDeliveries = (%d, %v), want (0, nil) — every destination is blocked", n, err)
	}
	for id, d := range repo.deliveries {
		if d.state != "failed" || d.lastError != deliveryCancelledBlocked {
			t.Fatalf("delivery %s = (%s, %q), want a cancelled row", id, d.state, d.lastError)
		}
		for _, tc := range cases {
			if string(d.row.Payload) == string(tc.payload) && d.row.InboxUrl == tc.inbox {
				ids[tc.name] = id
			}
		}
	}

	// A row cancelled by an EARLIER block, already lifted: outside this
	// unblock's window and not its business to resume.
	if err := repo.EnqueueDelivery(context.Background(), sqlcgen.EnqueueDeliveryParams{
		InboxUrl: inbox, Payload: build(t, "Create", publicVideo), SigningChannelHandle: "films",
	}); err != nil {
		t.Fatalf("EnqueueDelivery: %v", err)
	}
	var stale uuid.UUID
	for id, d := range repo.deliveries {
		if d.state == "pending" {
			stale = id
			d.state = "failed"
			d.lastError = deliveryCancelledBlocked
			d.updatedAt = blockedAt.Add(-time.Hour)
		}
	}

	requeued, err := svc.RedeliverAfterUnblock(context.Background(), blockedHost, blockedAt)
	if err != nil {
		t.Fatalf("RedeliverAfterUnblock: %v", err)
	}

	wantRequeued := 0
	for _, tc := range cases {
		id, ok := ids[tc.name]
		if !ok {
			t.Fatalf("%s: the enqueued row was not found", tc.name)
		}
		got := repo.deliveries[id].state == "pending"
		if got != tc.want {
			t.Errorf("%s: requeued = %v, want %v", tc.name, got, tc.want)
		}
		if tc.want {
			wantRequeued++
			// A cancellation was never an attempt on the remote side, so the
			// budget must not be carried forward.
			if a := repo.deliveries[id].row.Attempts; a != 0 {
				t.Errorf("%s: attempts = %d after requeue, want 0", tc.name, a)
			}
			if e := repo.deliveries[id].lastError; e != "" {
				t.Errorf("%s: last_error = %q after requeue, want cleared", tc.name, e)
			}
		}
	}
	if requeued != wantRequeued {
		t.Errorf("RedeliverAfterUnblock = %d, want %d", requeued, wantRequeued)
	}
	if repo.deliveries[stale].state != "failed" {
		t.Error("a delivery cancelled BEFORE this block began was resumed; the window is not bounded")
	}
	// The other host's cancelled delivery is untouched: unblocking one instance
	// says nothing about another that is still blocked.
	for id, d := range repo.deliveries {
		if hostOf(d.row.InboxUrl) == otherHost && d.state != "failed" {
			t.Errorf("delivery %s to a still-blocked host was resumed", id)
		}
	}

	// Idempotent: a second unblock of the same domain resumes nothing, because
	// the rows it would have matched are no longer cancelled.
	if n, err := svc.RedeliverAfterUnblock(context.Background(), blockedHost, blockedAt); err != nil || n != 0 {
		t.Errorf("second RedeliverAfterUnblock = (%d, %v), want (0, nil)", n, err)
	}
}

// TestRedeliverAfterUnblockIsNotStarvedByOtherDomains: the cap on one unblock's
// work is spent on THAT domain's rows.
//
// An instance with several blocked domains accumulates cancelled deliveries for
// all of them in the same table. If the page were filtered to the unblocked
// domain only in Go, a window holding more than the cap for the OTHER domains
// would come back full of rows this unblock must not touch — and the one
// delivery that should resume would never be looked at. The failure is silent
// and looks exactly like "there was nothing to resume".
//
// The row ids here are chosen so the still-blocked domain's rows sort FIRST,
// which is what makes the assertion deterministic rather than a coin flip on
// random uuids.
func TestRedeliverAfterUnblockIsNotStarvedByOtherDomains(t *testing.T) {
	const unblocked = "lifted.example"
	const stillBlocked = "other.example"
	channelID := uuid.New()
	ch := sqlcgen.Channel{ID: channelID, Handle: "films", ActivitypubEnabled: true}
	v := sqlcgen.GetVideoByIDRow{ID: uuid.New(), ChannelID: channelID, Privacy: "public", State: "published"}

	repo := fakeRepo{
		channels:     map[string]sqlcgen.Channel{"films": ch},
		channelsByID: map[uuid.UUID]sqlcgen.Channel{channelID: ch},
		videosByID:   map[uuid.UUID]sqlcgen.GetVideoByIDRow{v.ID: v},
		deliveries:   map[uuid.UUID]*fakeDelivery{},
	}
	svc := NewService(repo, WithBaseURL("https://videos.example"))
	payload, err := svc.buildVideoActivity("Create", "films", v)
	if err != nil {
		t.Fatalf("buildVideoActivity: %v", err)
	}
	blockedAt := time.Now().Add(-time.Minute)

	cancelled := func(id uuid.UUID, host string) {
		repo.deliveries[id] = &fakeDelivery{
			row:       sqlcgen.ClaimDueDeliveriesRow{ID: id, InboxUrl: "https://" + host + "/inbox", Payload: payload},
			state:     "failed",
			lastError: deliveryCancelledBlocked,
			updatedAt: time.Now(),
		}
	}
	// One more than the cap, all sorting ahead of the target.
	for i := 0; i <= maxRedeliverAfterUnblock; i++ {
		cancelled(uuid.MustParse(fmt.Sprintf("00000000-0000-4000-8000-%012d", i)), stillBlocked)
	}
	target := uuid.MustParse("ffffffff-ffff-4fff-8fff-ffffffffffff")
	cancelled(target, unblocked)

	n, err := svc.RedeliverAfterUnblock(context.Background(), unblocked, blockedAt)
	if err != nil {
		t.Fatalf("RedeliverAfterUnblock: %v", err)
	}
	if n != 1 || repo.deliveries[target].state != "pending" {
		t.Fatalf("requeued %d and the target is %q; the cap was spent on another domain's rows",
			n, repo.deliveries[target].state)
	}
	for id, d := range repo.deliveries {
		if id != target && d.state != "failed" {
			t.Fatalf("delivery %s to a still-blocked domain was resumed", id)
		}
	}
}
