package federation

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// memDeduper mirrors cache.Deduper's SETNX within one window.
type memDeduper map[string]bool

func (m memDeduper) First(_ context.Context, key string, _ time.Duration) (bool, error) {
	if m[key] {
		return false, nil
	}
	m[key] = true
	return true, nil
}

// A local viewer's view of a remote video is federated to the origin as
// PeerTube's View: signed by the INSTANCE actor (never the viewer), object the
// origin's video id, a fresh id under the instance actor, no counter.
func TestRecordRemoteVideoViewFederatesFromInstanceActor(t *testing.T) {
	ctx := context.Background()
	repo, c, _ := newAuthoredRepo("https://peer.example/inbox")
	repo.inst = &fakeInstance{reserved: true}
	svc := NewService(repo, WithBaseURL(noteBase), WithRemoteViewDeduper(memDeduper{}))

	if err := svc.RecordRemoteVideoView(ctx, c.RemoteVideoID, "viewer-a"); err != nil {
		t.Fatalf("RecordRemoteVideoView: %v", err)
	}
	if len(repo.deliveries) != 1 {
		t.Fatalf("deliveries = %d, want 1", len(repo.deliveries))
	}
	instance := noteBase + "/accounts/" + InstanceActorName
	for _, d := range repo.deliveries {
		if d.enqueued.InboxUrl != "https://peer.example/inbox" || !d.enqueued.SigningInstanceActor || d.enqueued.SigningUserID.Valid {
			t.Fatalf("delivery = %+v, want instance-signed to the origin inbox", d.enqueued)
		}
		var a map[string]any
		if err := json.Unmarshal(d.enqueued.Payload, &a); err != nil {
			t.Fatal(err)
		}
		id, _ := a["id"].(string)
		if a["type"] != "View" || a["actor"] != instance || a["object"] != authoredOriginVideoURL ||
			!strings.HasPrefix(id, instance+"/views/videos/"+c.RemoteVideoID.String()+"/") {
			t.Fatalf("View = %v", a)
		}
		if _, ok := a["expires"]; ok {
			t.Fatalf("a view must not carry expires (that is a live-viewer heartbeat): %v", a)
		}
	}

	// The same viewer inside the window sends nothing more; another viewer does.
	if err := svc.RecordRemoteVideoView(ctx, c.RemoteVideoID, "viewer-a"); err != nil {
		t.Fatal(err)
	}
	if len(repo.deliveries) != 1 {
		t.Fatalf("repeat view queued %d deliveries, want 1", len(repo.deliveries))
	}
	if err := svc.RecordRemoteVideoView(ctx, c.RemoteVideoID, "viewer-b"); err != nil {
		t.Fatal(err)
	}
	if len(repo.deliveries) != 2 {
		t.Fatalf("second viewer: deliveries = %d, want 2", len(repo.deliveries))
	}
}

func TestRecordRemoteVideoViewRefusals(t *testing.T) {
	ctx := context.Background()
	t.Run("unknown remote video", func(t *testing.T) {
		repo, _, _ := newAuthoredRepo("https://peer.example/inbox")
		repo.inst = &fakeInstance{reserved: true}
		svc := NewService(repo, WithBaseURL(noteBase), WithRemoteViewDeduper(memDeduper{}))
		if err := svc.RecordRemoteVideoView(ctx, uuid.New(), "v"); err != ErrNotFound {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
	quiet := map[string]func(r *fakeRepo) []Option{
		// Without a deduper every ping would be a delivery: send nothing.
		"no deduper wired": func(r *fakeRepo) []Option { r.inst = &fakeInstance{reserved: true}; return nil },
		// The viewer's own account is never a stand-in for the instance actor.
		"no instance actor": func(r *fakeRepo) []Option { return []Option{WithRemoteViewDeduper(memDeduper{})} },
		"origin inbox unknown": func(r *fakeRepo) []Option {
			r.inst = &fakeInstance{reserved: true}
			r.remoteActors = nil
			return []Option{WithRemoteViewDeduper(memDeduper{})}
		},
	}
	for name, seed := range quiet {
		t.Run(name, func(t *testing.T) {
			repo, c, _ := newAuthoredRepo("https://peer.example/inbox")
			opts := append([]Option{WithBaseURL(noteBase)}, seed(&repo)...)
			if err := NewService(repo, opts...).RecordRemoteVideoView(ctx, c.RemoteVideoID, "v"); err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if len(repo.deliveries) != 0 {
				t.Fatalf("queued %d deliveries, want none", len(repo.deliveries))
			}
		})
	}
}
