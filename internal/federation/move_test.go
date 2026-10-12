package federation

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

const moveOrigin = "https://old.example/users/mover"

// moveWorld is a target actor served over HTTP plus three local users following
// the origin: ada (accepted), bob (pending) and carol, who blocks the target.
type moveWorld struct {
	repo           fakeRepo
	svc            *Service
	target         string
	fetches        *atomic.Int32
	ada, bob, carl uuid.UUID
}

func newMoveWorld(t *testing.T, alsoKnownAs any) moveWorld {
	t.Helper()
	w := moveWorld{ada: uuid.New(), bob: uuid.New(), carl: uuid.New(), fetches: &atomic.Int32{}}
	w.repo = newFollowRepo(w.ada)
	w.repo.usersByID[w.bob] = sqlcgen.GetUserActorByIDRow{ID: w.bob, Username: "bob"}
	w.repo.usersByID[w.carl] = sqlcgen.GetUserActorByIDRow{ID: w.carl, Username: "carol"}
	w.repo.moveClaims, w.repo.adminActorBlocks = map[string]string{}, map[string]string{}
	w.repo.blockedDomains, w.repo.remoteBlocks = map[string]bool{}, map[string]bool{}
	cacheRemoteChannel(w.repo, moveOrigin, "old.example", nil)
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		w.fetches.Add(1)
		_ = json.NewEncoder(rw).Encode(map[string]any{
			"id": w.target, "type": "Person", "preferredUsername": "mover", "inbox": w.target + "/inbox",
			"alsoKnownAs": alsoKnownAs, "publicKey": map[string]string{"publicKeyPem": "PEM"},
		})
	}))
	t.Cleanup(srv.Close)
	w.target = srv.URL + "/users/new"
	for i, u := range []uuid.UUID{w.ada, w.bob, w.carl} {
		id := uuid.New()
		w.repo.rcFollows[id] = &sqlcgen.RemoteChannelFollow{ID: id, UserID: u, RemoteActorUrl: moveOrigin,
			State: []string{"accepted", "pending", "accepted"}[i], CreatedAt: time.Now().Add(time.Duration(i-5) * time.Hour)}
	}
	w.repo.remoteBlocks[w.carl.String()+"|"+w.target] = true
	w.svc = NewService(w.repo, WithBaseURL("https://videos.example"), WithAllowPrivateFetch(true))
	return w
}

func (w moveWorld) move(t *testing.T, id, signer, actor, object, target string) error {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"id": id, "type": "Move", "actor": actor, "object": object, "target": target})
	return w.svc.HandleInbox(context.Background(), signer, body)
}

// following returns the actor each user follows (one edge each here).
func (w moveWorld) following() map[uuid.UUID]string {
	out := map[uuid.UUID]string{}
	for _, r := range w.repo.rcFollows {
		out[r.UserID] = r.RemoteActorUrl
	}
	return out
}

func TestInboundMoveRepointsLocalFollows(t *testing.T) {
	w := newMoveWorld(t, []string{"https://elsewhere.example/users/x", moveOrigin})
	if err := w.move(t, "https://old.example/moves/1", moveOrigin, moveOrigin, moveOrigin, w.target); err != nil {
		t.Fatalf("Move: %v", err)
	}
	got := w.following()
	if got[w.ada] != w.target || got[w.bob] != w.target {
		t.Errorf("ada/bob follow %q/%q, want the target", got[w.ada], got[w.bob])
	}
	if got[w.carl] != moveOrigin {
		t.Errorf("carol, who blocks the target, follows %q; want the old follow kept", got[w.carl])
	}
	follows, undos := 0, 0
	for _, d := range w.repo.deliveries {
		var act struct{ Type string }
		_ = json.Unmarshal(d.row.Payload, &act)
		switch {
		case act.Type == "Follow" && d.row.InboxUrl == w.target+"/inbox":
			follows++
		case act.Type == "Undo" && d.row.InboxUrl == "https://old.example/channel-inbox":
			undos++
		}
	}
	if follows != 2 || undos != 2 || len(w.repo.deliveries) != 4 {
		t.Errorf("deliveries: %d Follow to the target, %d Undo to the origin, %d total; want 2, 2, 4", follows, undos, len(w.repo.deliveries))
	}

	// One Move per origin per cooldown: a second one fetches and moves nothing.
	again := uuid.New()
	w.repo.rcFollows[again] = &sqlcgen.RemoteChannelFollow{ID: again, UserID: w.ada, RemoteActorUrl: moveOrigin, State: "accepted"}
	if err := w.move(t, "https://old.example/moves/2", moveOrigin, moveOrigin, moveOrigin, w.target); err != nil || w.fetches.Load() != 1 {
		t.Errorf("second Move: err %v, %d fetches; want accepted and ignored after 1 fetch", err, w.fetches.Load())
	}
}

func TestInboundMoveRefusals(t *testing.T) {
	cases := []struct {
		name        string
		alsoKnownAs any
		prepare     func(*moveWorld)
		signer      string
		object      string
		target      func(moveWorld) string
		wantErr     error
		wantFetches int32
	}{
		{name: "signed by someone else", signer: "https://old.example/users/eve", wantErr: ErrActorMismatch},
		{name: "moves another actor", object: "https://old.example/users/other", wantErr: ErrActorMismatch},
		{name: "target does not vouch for the origin", alsoKnownAs: "https://old.example/users/other", wantFetches: 1},
		{name: "target has no alsoKnownAs", wantFetches: 1},
		{name: "target is admin-blocked", prepare: func(w *moveWorld) { w.repo.adminActorBlocks[w.target] = "" }},
		{name: "origin is admin-blocked", prepare: func(w *moveWorld) { w.repo.adminActorBlocks[moveOrigin] = "" }},
		{name: "target instance is blocked", prepare: func(w *moveWorld) { w.repo.blockedDomains[hostOf(w.target)] = true }},
		{name: "nobody here follows the origin", prepare: func(w *moveWorld) { clear(w.repo.rcFollows) }},
		{name: "target is local", target: func(moveWorld) string { return "https://videos.example/accounts/ada" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newMoveWorld(t, tc.alsoKnownAs)
			if tc.prepare != nil {
				tc.prepare(&w)
			}
			before := w.following()
			signer, object, target := moveOrigin, moveOrigin, w.target
			if tc.signer != "" {
				signer = tc.signer
			}
			if tc.object != "" {
				object = tc.object
			}
			if tc.target != nil {
				target = tc.target(w)
			}
			err := w.move(t, "https://old.example/moves/x", signer, moveOrigin, object, target)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if w.fetches.Load() != tc.wantFetches {
				t.Errorf("target fetched %d times, want %d", w.fetches.Load(), tc.wantFetches)
			}
			if after := w.following(); len(w.repo.deliveries) != 0 || !maps.Equal(after, before) {
				t.Errorf("a refused Move changed follows: %v → %v, %d deliveries", before, after, len(w.repo.deliveries))
			}
		})
	}
}
