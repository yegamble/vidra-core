package federation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/ratelimit"
)

// fakeViews stands in for the video service's RecordView seam: it counts per
// video and dedupes on the viewer key exactly as the Redis deduper does within
// one window, so the tests prove which key a remote View is counted under.
type fakeViews struct {
	seen   map[string]bool
	counts map[uuid.UUID]int
}

func newFakeViews() *fakeViews {
	return &fakeViews{seen: map[string]bool{}, counts: map[uuid.UUID]int{}}
}

func (f *fakeViews) record(ctx context.Context, videoID uuid.UUID, viewerKey string, gate func(context.Context) (bool, error)) error {
	key := videoID.String() + ":" + viewerKey
	if f.seen[key] {
		return nil
	}
	f.seen[key] = true
	if ok, err := gate(ctx); err != nil || !ok {
		return err
	}
	f.counts[videoID]++
	return nil
}

const ctPTServerActor = "https://peertube.example/accounts/peertube"

func viewActivity(id, actor string, extra map[string]any) []byte {
	m := map[string]any{
		"@context": "https://www.w3.org/ns/activitystreams",
		"id":       id, "type": "View", "actor": actor, "object": rtVideoURL,
	}
	for k, v := range extra {
		m[k] = v
	}
	b, _ := json.Marshal(m)
	return b
}

func viewSvc(repo fakeRepo, views *fakeViews) *Service {
	return NewService(repo, WithBaseURL(contractBase),
		WithViewCounting(views.record, ratelimit.NewMemoryCounter()))
}

// PeerTube sends a View per counted view, signed by its instance actor. Each
// one counts through the local view seam; the same actor is one viewer per
// window, a different actor is another viewer, and a remote-supplied counter
// is never read.
func TestInboundViewCountsThroughTheViewSeam(t *testing.T) {
	ctx := context.Background()
	repo := ratingRepo()
	cacheContractActor(repo, ctPTServerActor, "Application", "peertube", "peertube.example")
	views := newFakeViews()
	svc := viewSvc(repo, views)

	send := func(signer string, body []byte) {
		t.Helper()
		if err := svc.HandleInbox(ctx, signer, body); err != nil {
			t.Fatalf("HandleInbox: %v", err)
		}
	}
	send(ctPTServerActor, viewActivity(ctPTServerActor+"/views/videos/1/a", ctPTServerActor,
		map[string]any{"result": map[string]any{"type": "InteractionCounter", "interactionType": "WatchAction", "userInteractionCount": 9999}}))
	if got := views.counts[ctVideoID]; got != 1 {
		t.Fatalf("after one View: count = %d, want 1 (remote counters are ignored)", got)
	}
	send(ctPTServerActor, viewActivity(ctPTServerActor+"/views/videos/1/b", ctPTServerActor, nil))
	if got := views.counts[ctVideoID]; got != 1 {
		t.Fatalf("same actor inside the window: count = %d, want 1", got)
	}
	send(ctPTAccount, viewActivity(ctPTAccount+"/views/videos/1/c", ctPTAccount, nil))
	if got := views.counts[ctVideoID]; got != 2 {
		t.Fatalf("a second actor: count = %d, want 2", got)
	}
}

// A View carrying `expires` is PeerTube's live-viewer heartbeat, not a view:
// PeerTube itself never adds it to the view count, so neither do we.
func TestInboundViewerHeartbeatIsNotAView(t *testing.T) {
	repo := ratingRepo()
	views := newFakeViews()
	svc := viewSvc(repo, views)
	body := viewActivity(ctPTAccount+"/views/videos/1/x", ctPTAccount, map[string]any{"expires": "2026-10-14T12:00:00Z"})
	if err := svc.HandleInbox(context.Background(), ctPTAccount, body); err != nil {
		t.Fatal(err)
	}
	if len(views.counts) != 0 {
		t.Fatalf("viewer heartbeat counted as a view: %v", views.counts)
	}
}

func TestInboundViewRefusals(t *testing.T) {
	other := "https://peertube.example/accounts/olga"
	viewID := ctPTAccount + "/views/videos/1/r"
	cases := []struct {
		name    string
		signer  string
		body    []byte
		seed    func(r fakeRepo)
		wantErr error
	}{
		{name: "actor is not the signer", signer: other, body: viewActivity(viewID, ctPTAccount, nil), wantErr: ErrActorMismatch},
		{name: "activity id on another host", signer: ctPTAccount, body: viewActivity("https://elsewhere.example/views/1", ctPTAccount, nil), wantErr: ErrBadResource},
		{name: "blocked instance", signer: ctPTAccount, body: viewActivity(viewID, ctPTAccount, nil),
			seed: func(r fakeRepo) { r.blockedDomains["peertube.example"] = true }, wantErr: ErrSenderBlocked},
		{name: "actor blocked instance-wide", signer: ctPTAccount, body: viewActivity(viewID, ctPTAccount, nil),
			seed: func(r fakeRepo) { r.adminActorBlocks[ctPTAccount] = "spam" }, wantErr: ErrSenderBlocked},
		{name: "video owner blocks the actor", signer: ctPTAccount, body: viewActivity(viewID, ctPTAccount, nil),
			seed: func(r fakeRepo) { r.remoteBlocks[ctUserID.String()+"|"+ctPTAccount] = true }},
		{name: "unlisted video", signer: ctPTAccount, body: viewActivity(viewID, ctPTAccount, nil), seed: func(r fakeRepo) {
			v := r.videosByID[ctVideoID]
			v.Privacy = "unlisted"
			r.videosByID[ctVideoID] = v
		}},
		{name: "moderator-blocked video", signer: ctPTAccount, body: viewActivity(viewID, ctPTAccount, nil), seed: func(r fakeRepo) {
			v := r.videosByID[ctVideoID]
			v.Blocked = true
			r.videosByID[ctVideoID] = v
		}},
		{name: "not one of our videos", signer: ctPTAccount, body: func() []byte {
			b, _ := json.Marshal(map[string]any{"id": viewID, "type": "View", "actor": ctPTAccount, "object": ctPTVideo})
			return b
		}()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := ratingRepo()
			repo.adminActorBlocks = map[string]string{}
			repo.remoteBlocks = map[string]bool{}
			if tc.seed != nil {
				tc.seed(repo)
			}
			views := newFakeViews()
			err := viewSvc(repo, views).HandleInbox(context.Background(), tc.signer, tc.body)
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			} else if tc.wantErr == nil && err != nil {
				t.Fatalf("err = %v, want accept-and-ignore", err)
			}
			if len(views.counts) != 0 {
				t.Fatalf("refused View was counted: %v", views.counts)
			}
		})
	}
}

// One registrable domain adds at most maxRemoteViewsPerDomainHour views per
// hour, however many actors or subdomains it mints.
func TestInboundViewDomainCap(t *testing.T) {
	ctx := context.Background()
	repo := ratingRepo()
	views := newFakeViews()
	svc := viewSvc(repo, views)
	// Repeats from one legitimate actor do not spend the domain's budget.
	for i := 0; i < maxRemoteViewsPerDomainHour+5; i++ {
		if err := svc.HandleInbox(ctx, ctPTAccount, viewActivity(fmt.Sprintf("%s/views/r%d", ctPTAccount, i), ctPTAccount, nil)); err != nil {
			t.Fatal(err)
		}
	}
	if got := views.counts[ctVideoID]; got != 1 {
		t.Fatalf("repeats: count = %d, want 1", got)
	}
	// Fresh subdomains of one registrable domain share ONE budget.
	for i := 1; i <= 150; i++ {
		actor := fmt.Sprintf("https://sub%d.evil.example/accounts/sock", i)
		if err := svc.HandleInbox(ctx, actor, viewActivity(actor+"/views/1", actor, nil)); err != nil {
			t.Fatal(err)
		}
	}
	if got := views.counts[ctVideoID]; got != 1+maxRemoteViewsPerDomainHour {
		t.Fatalf("count = %d, want 1 + the domain cap %d", got, maxRemoteViewsPerDomainHour)
	}
	// Another domain has its own budget.
	actor := "https://tube.example/accounts/ana"
	if err := svc.HandleInbox(ctx, actor, viewActivity(actor+"/views/1", actor, nil)); err != nil {
		t.Fatal(err)
	}
	if got := views.counts[ctVideoID]; got != maxRemoteViewsPerDomainHour+2 {
		t.Fatalf("other domain: count = %d, want %d", got, maxRemoteViewsPerDomainHour+2)
	}
}

// Without the view seam wired (tests, or a build without Redis) a View is
// accepted and ignored.
func TestInboundViewWithoutSeamIsIgnored(t *testing.T) {
	svc := NewService(ratingRepo(), WithBaseURL(contractBase))
	if err := svc.HandleInbox(context.Background(), ctPTAccount, viewActivity(ctPTAccount+"/views/1", ctPTAccount, nil)); err != nil {
		t.Fatal(err)
	}
}
