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
