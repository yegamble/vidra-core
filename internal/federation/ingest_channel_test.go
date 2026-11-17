package federation

// A PeerTube video is attributed to [Person, Group]: the account that uploaded
// it and the channel it belongs to. It is filed under the CHANNEL, and the
// channel's own actor document names the account that owns it — the edge a
// block of that account travels down (remote_actor_block_reach, 0142). The
// interop lab measured the opposite: every PeerTube video landed under the
// uploader's account, so `root_channel`'s videos were shown as `root`'s
// (meta docs/evidence/peertube-interop-2026-11-16.md, S5).

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// ctPTInstance is the fixture instance's Application actor: who signs the
// Announce an instance follower receives.
const ctPTInstance = "https://peertube.example/accounts/peertube"

// asSentBy re-addresses a corpus activity to the actor that really sends it.
// The corpus signs every video activity as the channel. PeerTube signs a
// video's Create and Update as the ACCOUNT (send-create.ts, send-update.ts:
// byActor = video.VideoChannel.Account.Actor) and shares it as the instance.
func asSentBy(t *testing.T, fixture, actor string) []byte {
	t.Helper()
	body := readFixture(t, fixture)
	if actor == ctPTChannel {
		return body
	}
	out := bytes.Replace(body, []byte(`"actor": "`+ctPTChannel+`"`), []byte(`"actor": "`+actor+`"`), 1)
	if bytes.Equal(out, body) {
		t.Fatalf("fixture %s names no channel actor to re-address", fixture)
	}
	return out
}

// newPeerTubeIngest serves the fixture origin and caches the signer the way
// inbound signature verification does before HandleInbox ever runs.
func newPeerTubeIngest(t *testing.T, signer string) (*Service, fakeRepo) {
	t.Helper()
	repo := newContractRepo()
	svc := NewService(repo,
		WithBaseURL(contractBase),
		WithFollowEdgeChecker(edgesFor(signer)),
		WithFetchClient(&http.Client{Transport: fixtureTransport{t: t, routes: map[string]string{
			ctPTVideo:   "inbound/peertube_video_object.json",
			ctPTChannel: "inbound/peertube_actor_group.json",
			ctPTAccount: "inbound/peertube_actor_person.json",
		}}}),
	)
	if signer == ctPTInstance {
		cacheContractActor(repo, ctPTInstance, "Application", "peertube", "peertube.example")
	} else if _, err := svc.resolveRemoteActor(context.Background(), signer); err != nil {
		t.Fatalf("resolve signer %s: %v", signer, err)
	}
	return svc, repo
}

func assertFiledUnderChannel(t *testing.T, repo fakeRepo) {
	t.Helper()
	rv, ok := repo.remoteVideos[ctPTVideo]
	if !ok {
		t.Fatalf("video not stored: %+v", repo.remoteVideos)
	}
	if rv.params.RemoteActorUrl != ctPTChannel {
		t.Errorf("filed under %q, want the channel %q", rv.params.RemoteActorUrl, ctPTChannel)
	}
	ch := repo.remoteActors[ctPTChannel]
	if ch.ActorType != "Group" || ch.PreferredUsername != "kayak_diaries" {
		t.Errorf("channel actor = %q %q, want the Group kayak_diaries", ch.ActorType, ch.PreferredUsername)
	}
	if ch.AttributedTo != ctPTAccount {
		t.Errorf("channel owner = %q, want the account %q — a block of the account would not reach this video", ch.AttributedTo, ctPTAccount)
	}
}

func TestPeerTubeVideoIsFiledUnderItsChannel(t *testing.T) {
	for _, tc := range []struct{ name, fixture, signer string }{
		{"Announce by the channel", "inbound/peertube_announce_video.json", ctPTChannel},
		{"Announce by the instance actor", "inbound/peertube_announce_video.json", ctPTInstance},
		{"Create by the account", "inbound/peertube_create_video.json", ctPTAccount},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, repo := newPeerTubeIngest(t, tc.signer)
			if err := svc.HandleInbox(context.Background(), tc.signer, asSentBy(t, tc.fixture, tc.signer)); err != nil {
				t.Fatalf("HandleInbox: %v", err)
			}
			assertFiledUnderChannel(t, repo)
		})
	}
	t.Run("search by URL", func(t *testing.T) {
		svc, repo := newPeerTubeIngest(t, ctPTChannel)
		if _, err := svc.ResolveSearchTarget(context.Background(), ctPTVideo); err != nil {
			t.Fatalf("ResolveSearchTarget: %v", err)
		}
		assertFiledUnderChannel(t, repo)
	})
}

// An edit arrives signed by the ACCOUNT. It refreshes the video where it is;
// it does not move it under the actor that signed.
func TestPeerTubeUpdateKeepsTheVideoUnderItsChannel(t *testing.T) {
	svc, repo := newPeerTubeIngest(t, ctPTAccount)
	if _, err := svc.resolveRemoteActor(context.Background(), ctPTChannel); err != nil {
		t.Fatal(err)
	}
	repo.remoteVideos[ctPTVideo] = &fakeRemoteVideo{id: uuid.New(), params: sqlcgen.UpsertRemoteVideoParams{
		ObjectUrl: ctPTVideo, RemoteActorUrl: ctPTChannel, Title: "Dawn crossing at Whitegull Sound",
	}}
	if err := svc.HandleInbox(context.Background(), ctPTAccount, asSentBy(t, "inbound/peertube_update_video.json", ctPTAccount)); err != nil {
		t.Fatalf("HandleInbox: %v", err)
	}
	if got := repo.remoteVideos[ctPTVideo].params.Title; got != "Dawn crossing at Whitegull Sound (remastered)" {
		t.Errorf("title = %q, want the edit applied", got)
	}
	assertFiledUnderChannel(t, repo)
}

// A Group on another host is not this video's channel, whatever the object
// says: the video stays under the actor that vouched for it, and nothing is
// fetched from the host it named.
func TestForeignGroupIsNotTheVideosChannel(t *testing.T) {
	repo := newIngestRepo()
	svc := NewService(repo, WithBaseURL("https://videos.example"), WithFollowEdgeChecker(edgesFor(remoteChan)))
	body := `{"id":"https://remote.example/act/g1","type":"Create","actor":"` + remoteChan + `","object":{` +
		`"id":"` + remoteVideo + `","type":"Video","name":"Clip","attributedTo":[` +
		`{"type":"Group","id":"https://elsewhere.example/video-channels/borrowed"},"` + remoteChan + `"]}}`
	if err := svc.HandleInbox(context.Background(), remoteChan, []byte(body)); err != nil {
		t.Fatalf("HandleInbox: %v", err)
	}
	rv, ok := repo.remoteVideos[remoteVideo]
	if !ok || rv.params.RemoteActorUrl != remoteChan {
		t.Fatalf("stored = %+v, want the video under its signer %q", repo.remoteVideos, remoteChan)
	}
	if _, cached := repo.remoteActors["https://elsewhere.example/video-channels/borrowed"]; cached {
		t.Error("an actor on a foreign host was resolved on the object's say-so")
	}
}
