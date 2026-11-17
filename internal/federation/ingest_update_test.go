package federation

// An edit of a remote video this instance already holds is applied whoever on
// the video's origin signs it. PeerTube sends a video's Update as the
// uploader's ACCOUNT (send-update.ts), and a follow of the CHANNEL is no follow
// edge for that account: the interop lab measured the edit answered 202 and
// dropped, the title unchanged a minute later (meta
// docs/evidence/peertube-interop-2026-11-16.md, S7).

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func TestUpdateOfAHeldVideo(t *testing.T) {
	const (
		oldTitle = "Dawn crossing at Whitegull Sound"
		newTitle = "Dawn crossing at Whitegull Sound (remastered)"
		eve      = "https://elsewhere.example/accounts/eve"
	)
	// The corpus signs the Update as the channel; PeerTube signs it as the account.
	signedBy := func(t *testing.T, fixture, actor string) []byte {
		body := readFixture(t, fixture)
		out := bytes.Replace(body, []byte(`"actor": "`+ctPTChannel+`"`), []byte(`"actor": "`+actor+`"`), 1)
		if bytes.Equal(out, body) {
			t.Fatalf("fixture %s names no channel actor to re-address", fixture)
		}
		return out
	}
	for _, tc := range []struct {
		name      string
		held      bool
		signer    string
		body      func(*testing.T) []byte
		wantTitle string // "" = no row
	}{
		{"held video, signed by the account nobody follows: applied", true, ctPTAccount,
			func(t *testing.T) []byte { return signedBy(t, "inbound/peertube_update_video.json", ctPTAccount) }, newTitle},
		{"unknown video: still needs a follow edge", false, ctPTAccount,
			func(t *testing.T) []byte { return signedBy(t, "inbound/peertube_update_video.json", ctPTAccount) }, ""},
		{"held video, signer on another host: refused", true, eve,
			func(*testing.T) []byte {
				return []byte(`{"id":"https://elsewhere.example/act/u1","type":"Update","actor":"` + eve + `","object":{` +
					`"id":"` + ctPTVideo + `","type":"Video","name":"hijacked","attributedTo":"` + eve + `"}}`)
			}, oldTitle},
		{"held video, a Create from the account nobody follows: not an edit", true, ctPTAccount,
			func(t *testing.T) []byte { return signedBy(t, "inbound/peertube_create_video.json", ctPTAccount) }, oldTitle},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := newContractRepo()
			// What the lab had: the CHANNEL is followed, the account is not.
			svc := NewService(repo, WithBaseURL(contractBase), WithFollowEdgeChecker(edgesFor(ctPTChannel)))
			if tc.held {
				// A held video's actor is always cached: it is the row's FK target.
				cacheContractActor(repo, ctPTChannel, "Group", "kayak_diaries", "peertube.example")
				repo.remoteVideos[ctPTVideo] = &fakeRemoteVideo{id: uuid.New(), params: sqlcgen.UpsertRemoteVideoParams{
					ObjectUrl: ctPTVideo, RemoteActorUrl: ctPTChannel, Title: oldTitle,
				}}
			}
			if err := svc.HandleInbox(context.Background(), tc.signer, tc.body(t)); err != nil {
				t.Fatalf("HandleInbox: %v", err)
			}
			rv, ok := repo.remoteVideos[ctPTVideo]
			if tc.wantTitle == "" {
				if ok {
					t.Fatalf("an unknown video was stored from an Update with no follow edge: %+v", rv.params)
				}
				return
			}
			if !ok {
				t.Fatal("the held video is gone")
			}
			if rv.params.Title != tc.wantTitle {
				t.Errorf("title = %q, want %q", rv.params.Title, tc.wantTitle)
			}
			if rv.params.RemoteActorUrl != ctPTChannel {
				t.Errorf("the edit moved the video under %q; it must stay under %q", rv.params.RemoteActorUrl, ctPTChannel)
			}
		})
	}
}

// PeerTube tells its followers a video stopped being public with a Delete —
// and, when one of its own federation jobs was still running, with an Update
// of the same video straight after. An Update that could CREATE a row put the
// deleted video back: the interop lab found 4 of 5 videos made private on
// PeerTube still listed here, and 0 of 5 on a stock PeerTube.
func TestUpdateNeverCreatesAVideo(t *testing.T) {
	ctx := context.Background()
	const originTitle = "Dawn crossing at Whitegull Sound"
	update := func(t *testing.T) []byte { return asSentBy(t, "inbound/peertube_update_video.json", ctPTAccount) }
	// The channel AND the account are followed: no gate hides what is tested.
	serve := func(t *testing.T, repo fakeRepo, originServesVideo bool) *Service {
		routes := map[string]string{ctPTChannel: "inbound/peertube_actor_group.json"}
		if originServesVideo {
			routes[ctPTVideo] = "inbound/peertube_video_object.json"
		}
		cacheContractActor(repo, ctPTChannel, "Group", "kayak_diaries", "peertube.example")
		return NewService(repo, WithBaseURL(contractBase), WithFollowEdgeChecker(edgesFor(ctPTChannel, ctPTAccount)),
			WithFetchClient(&http.Client{Transport: fixtureTransport{t: t, routes: routes}}))
	}
	hold := func(repo fakeRepo) {
		repo.remoteVideos[ctPTVideo] = &fakeRemoteVideo{id: uuid.New(), params: sqlcgen.UpsertRemoteVideoParams{
			ObjectUrl: ctPTVideo, RemoteActorUrl: ctPTChannel, Title: originTitle,
		}}
	}

	t.Run("a Delete, then a stale Update: the video stays gone", func(t *testing.T) {
		repo := newContractRepo()
		svc := serve(t, repo, false) // private now: its origin no longer serves it
		hold(repo)
		for _, body := range [][]byte{asSentBy(t, "inbound/peertube_delete_video.json", ctPTAccount), update(t)} {
			if err := svc.HandleInbox(ctx, ctPTAccount, body); err != nil {
				t.Fatalf("HandleInbox: %v", err)
			}
		}
		if rv, ok := repo.remoteVideos[ctPTVideo]; ok {
			t.Errorf("the Update that followed the Delete put the video back: %+v", rv.params)
		}
	})
	t.Run("an unknown video is stored as its origin serves it, not as the Update says", func(t *testing.T) {
		repo := newContractRepo()
		if err := serve(t, repo, true).HandleInbox(ctx, ctPTAccount, update(t)); err != nil {
			t.Fatalf("HandleInbox: %v", err)
		}
		rv, ok := repo.remoteVideos[ctPTVideo]
		if !ok {
			t.Fatal("a video its origin serves was not stored")
		}
		if rv.params.Title != originTitle {
			t.Errorf("title = %q, want the origin's %q", rv.params.Title, originTitle)
		}
	})
	t.Run("a held video deleted while its Update is in flight is not re-created", func(t *testing.T) {
		repo := newContractRepo()
		hold(repo)
		repo.afterRemoteVideoLookup = func() { delete(repo.remoteVideos, ctPTVideo) }
		if err := serve(t, repo, true).HandleInbox(ctx, ctPTAccount, update(t)); err != nil {
			t.Fatalf("HandleInbox: %v", err)
		}
		if rv, ok := repo.remoteVideos[ctPTVideo]; ok {
			t.Errorf("an edit re-inserted a video deleted under it: %+v", rv.params)
		}
	})
}
