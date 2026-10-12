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

// moveWorld: an HTTP target; ada, bob (pending), carol (blocks it) follow the origin; dave blocks the origin.
type moveWorld struct {
	repo                 fakeRepo
	svc                  *Service
	target               string
	alsoKnownAs, inbox   *atomic.Value // what the target's document says, read per fetch
	fetches              *atomic.Int32
	ada, bob, carl, dave uuid.UUID
}

func newMoveWorld(t *testing.T, alsoKnownAs any) moveWorld {
	t.Helper()
	w := moveWorld{ada: uuid.New(), bob: uuid.New(), carl: uuid.New(), dave: uuid.New(),
		fetches: &atomic.Int32{}, alsoKnownAs: &atomic.Value{}, inbox: &atomic.Value{}}
	w.alsoKnownAs.Store([]any{alsoKnownAs})
	w.repo = newFollowRepo(w.ada)
	w.repo.usersByID[w.bob] = sqlcgen.GetUserActorByIDRow{ID: w.bob, Username: "bob"}
	w.repo.usersByID[w.carl] = sqlcgen.GetUserActorByIDRow{ID: w.carl, Username: "carol"}
	w.repo.moveClaims, w.repo.moveStates, w.repo.adminActorBlocks = map[string]string{}, map[string]string{}, map[string]string{}
	w.repo.blockedDomains, w.repo.remoteBlocks = map[string]bool{}, map[string]bool{}
	cacheRemoteChannel(w.repo, moveOrigin, "old.example", nil)
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		w.fetches.Add(1)
		_ = json.NewEncoder(rw).Encode(map[string]any{
			"id": w.target, "type": "Person", "preferredUsername": "mover", "inbox": w.inbox.Load(),
			"alsoKnownAs": w.alsoKnownAs.Load().([]any)[0], "publicKey": map[string]string{"publicKeyPem": "PEM"},
		})
	}))
	t.Cleanup(srv.Close)
	w.target = srv.URL + "/users/new"
	w.inbox.Store(w.target + "/inbox")
	for i, u := range []uuid.UUID{w.ada, w.bob, w.carl} {
		w.followOrigin(u, []string{"accepted", "pending", "accepted"}[i], time.Duration(i-5)*time.Hour)
	}
	w.repo.remoteBlocks[w.carl.String()+"|"+w.target] = true
	w.repo.remoteBlocks[w.dave.String()+"|"+moveOrigin] = true
	w.svc = NewService(w.repo, WithBaseURL("https://videos.example"), WithAllowPrivateFetch(true))
	return w
}

func (w moveWorld) followOrigin(user uuid.UUID, state string, age time.Duration) {
	id := uuid.New()
	w.repo.rcFollows[id] = &sqlcgen.RemoteChannelFollow{ID: id, UserID: user, RemoteActorUrl: moveOrigin, State: state, CreatedAt: time.Now().Add(age)}
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

// A verified Move is recorded for the worker (the inbox re-points nothing),
// carries dave's block, and a second Move inside the cooldown fetches nothing.
func TestInboundMoveIsRecordedAndCarriesBlocks(t *testing.T) {
	w := newMoveWorld(t, []string{"https://elsewhere.example/users/x", moveOrigin})
	if err := w.move(t, "https://old.example/moves/1", moveOrigin, moveOrigin, moveOrigin, w.target); err != nil {
		t.Fatalf("Move: %v", err)
	}
	if len(w.repo.deliveries) != 0 || w.repo.moveStates[moveOrigin] != "moving" || w.repo.moveClaims[moveOrigin] != w.target {
		t.Errorf("after the Move: %d deliveries, state %q, claim %q; want 0, moving, the target", len(w.repo.deliveries), w.repo.moveStates[moveOrigin], w.repo.moveClaims[moveOrigin])
	}
	if _, cached := w.repo.remoteActors[w.target]; !cached || !w.repo.remoteBlocks[w.dave.String()+"|"+w.target] {
		t.Errorf("target cached %v, dave's block carried %v; want both", cached, w.repo.remoteBlocks[w.dave.String()+"|"+w.target])
	}
	if err := w.move(t, "https://old.example/moves/2", moveOrigin, moveOrigin, moveOrigin, w.target); err != nil || w.fetches.Load() != 1 {
		t.Errorf("second Move: err %v, %d fetches; want accepted and ignored after 1 fetch", err, w.fetches.Load())
	}
}

// An unverified Move gives its claim back: a corrected one is not stranded.
func TestInboundMoveReleasesAnUnverifiedClaim(t *testing.T) {
	w := newMoveWorld(t, nil)
	if err := w.move(t, "https://old.example/moves/1", moveOrigin, moveOrigin, moveOrigin, w.target); err != nil || len(w.repo.moveClaims) != 0 {
		t.Fatalf("unverified Move: err %v, claims kept %v", err, w.repo.moveClaims)
	}
	w.alsoKnownAs.Store([]any{moveOrigin})
	if err := w.move(t, "https://old.example/moves/2", moveOrigin, moveOrigin, moveOrigin, w.target); err != nil {
		t.Fatal(err)
	}
	if w.fetches.Load() != 2 || w.repo.moveStates[moveOrigin] != "moving" {
		t.Errorf("retried Move: %d fetches, state %q; want 2, moving", w.fetches.Load(), w.repo.moveStates[moveOrigin])
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
		{name: "target's inbox is on another host", alsoKnownAs: moveOrigin, wantFetches: 1,
			prepare: func(w *moveWorld) { w.inbox.Store("https://other.example/inbox") }},
		{name: "target is admin-blocked", prepare: func(w *moveWorld) { w.repo.adminActorBlocks[w.target] = "" }},
		{name: "origin is admin-blocked", prepare: func(w *moveWorld) { w.repo.adminActorBlocks[moveOrigin] = "" }},
		{name: "target instance is blocked", prepare: func(w *moveWorld) { w.repo.blockedDomains[hostOf(w.target)] = true }},
		{name: "nobody here follows or blocks the origin", prepare: func(w *moveWorld) { clear(w.repo.rcFollows); clear(w.repo.remoteBlocks) }},
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
			if w.fetches.Load() != tc.wantFetches || len(w.repo.moveClaims) != 0 {
				t.Errorf("target fetched %d times (want %d), claims left %v", w.fetches.Load(), tc.wantFetches, w.repo.moveClaims)
			}
			if after := w.following(); len(w.repo.deliveries) != 0 || !maps.Equal(after, before) {
				t.Errorf("a refused Move changed follows: %v → %v, %d deliveries", before, after, len(w.repo.deliveries))
			}
		})
	}
}
