package httpapi

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
)

type demandMirror struct {
	*fakeIPFSMirror
	calls int
	wait  bool
}

func (m *demandMirror) DemandPublicVideo(ctx context.Context, _ uuid.UUID) error {
	m.calls++
	if m.wait {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

func TestPlaybackDemandOnlyAfterPublicAuthorizationAndBounded(t *testing.T) {
	env := newPlaybackSessionEnv(t)
	m := &demandMirror{fakeIPFSMirror: &fakeIPFSMirror{}}
	env.srv.ipfsmirrorsvc = m
	tok := createChannelFor(t, env.srv, "demand", "demand@example.test", "demand")
	id := createVideo(t, env.srv, tok, "demand", `{"title":"demand","privacy":"private"}`)
	if r := uploadVideoFile(env.srv, id, "clip.mp4", "video/mp4", "video", tok); r.Code != http.StatusCreated {
		t.Fatalf("upload %d", r.Code)
	}
	if r := postPlaybackSession(env.srv, id, tok, ""); r.Code != http.StatusOK {
		t.Fatalf("owner playback %d", r.Code)
	}
	if m.calls != 0 {
		t.Fatal("private playback queued public pin")
	}
	patchVideo(t, env.srv, tok, id, `{"privacy":"public"}`)
	m.wait = true
	start := time.Now()
	if r := postPlaybackSession(env.srv, id, "", ""); r.Code != http.StatusOK {
		t.Fatalf("public playback %d", r.Code)
	}
	if m.calls != 1 || time.Since(start) > time.Second {
		t.Fatalf("demand calls=%d elapsed=%v", m.calls, time.Since(start))
	}
}
