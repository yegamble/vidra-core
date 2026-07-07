//go:build integration

// Optional live RTMP end-to-end test. Unlike the rest of the integration suite
// (real Postgres/Redis), this one drives the FULL live media plane — the `media`
// Docker Compose profile (nginx-rtmp) plus the api — and pushes a real 2-second
// RTMP stream with ffmpeg. It is self-skipping: it only runs when LIVE_RTMP_TEST=1
// and ffmpeg is on PATH, so the standard `-tags=integration` gate never requires
// the media server.
//
// To run it:
//
//	LIVE_INGEST_SECRET=$(openssl rand -hex 24) LIVE_HLS_ROOT=/live-hls \
//	  docker compose --profile core --profile media up --build -d
//	LIVE_RTMP_TEST=1 \
//	  LIVE_RTMP_API_URL=http://localhost:8080 \
//	  go test -tags=integration -run TestLiveRTMPEndToEnd ./internal/httpapi/...
//
// LIVE_RTMP_API_URL defaults to http://localhost:8080; LIVE_RTMP_PUBLISH_URL
// overrides the RTMP base (defaults to the rtmp_url the api returns, else
// rtmp://localhost:1935/live).
package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func liveRTMPBaseURL() string {
	if v := os.Getenv("LIVE_RTMP_API_URL"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "http://localhost:8080"
}

// rtmpAPI is a tiny JSON HTTP helper for the running api.
type rtmpAPI struct {
	t     *testing.T
	base  string
	token string
}

func (a *rtmpAPI) do(method, path, token string, body any) (int, []byte) {
	a.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, a.base+path, r)
	if err != nil {
		a.t.Fatalf("new request %s %s: %v", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatalf("%s %s: %v (is the compose stack up?)", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data
}

func TestLiveRTMPEndToEnd(t *testing.T) {
	if os.Getenv("LIVE_RTMP_TEST") != "1" {
		t.Skip("LIVE_RTMP_TEST!=1; skipping the optional RTMP media-profile e2e test")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH; skipping the RTMP media-profile e2e test")
	}
	api := &rtmpAPI{t: t, base: liveRTMPBaseURL()}
	suffix := time.Now().UnixNano()
	username := "rtmp"
	// Register + login.
	code, body := api.do(http.MethodPost, "/api/v1/auth/register", "", map[string]any{
		"username": username + itoa64(suffix),
		"email":    "rtmp" + itoa64(suffix) + "@example.test",
		"password": "supersecret123",
	})
	if code != http.StatusCreated && code != http.StatusOK {
		t.Fatalf("register = %d: %s", code, body)
	}
	var reg struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(body, &reg)
	if reg.Token == "" {
		t.Fatalf("register returned no token: %s", body)
	}
	token := reg.Token

	// Channel.
	handle := "rtmp" + itoa64(suffix)
	if code, body = api.do(http.MethodPost, "/api/v1/channels", token, map[string]any{
		"handle": handle, "display_name": "RTMP Test",
	}); code != http.StatusCreated {
		t.Fatalf("create channel = %d: %s", code, body)
	}

	// Live stream with replay enabled.
	code, body = api.do(http.MethodPost, "/api/v1/channels/"+handle+"/live", token, map[string]any{
		"title": "RTMP Session", "replay_enabled": true,
	})
	if code != http.StatusCreated {
		t.Fatalf("create live = %d: %s", code, body)
	}
	var created struct {
		LiveStream struct {
			ID string `json:"id"`
		} `json:"live_stream"`
		StreamKey string `json:"stream_key"`
		RTMPURL   string `json:"rtmp_url"`
	}
	_ = json.Unmarshal(body, &created)
	id, key := created.LiveStream.ID, created.StreamKey
	if id == "" || key == "" {
		t.Fatalf("missing id/key in create response: %s", body)
	}

	rtmpBase := created.RTMPURL
	if v := os.Getenv("LIVE_RTMP_PUBLISH_URL"); v != "" {
		rtmpBase = v
	}
	if rtmpBase == "" {
		rtmpBase = "rtmp://localhost:1935/live"
	}
	target := strings.TrimRight(rtmpBase, "/") + "/" + key

	// Push a 2s test pattern over RTMP.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error",
		"-re", "-f", "lavfi", "-i", "testsrc=size=320x240:rate=15",
		"-f", "lavfi", "-i", "sine=frequency=1000",
		"-t", "2", "-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency",
		"-c:a", "aac", "-f", "flv", target)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg push failed: %v\n%s", err, out)
	}

	// The on-publish hook flipped the stream live (it may already be back to
	// ended by the time we poll, since the push is short — accept either, then
	// confirm it settles to ended).
	sawLiveOrEnded := waitFor(t, 10*time.Second, func() bool {
		st := getLiveState(api, id, token)
		return st == "live" || st == "ended"
	})
	if !sawLiveOrEnded {
		t.Fatalf("stream never left offline after the RTMP publish")
	}

	// HLS should have been produced during the (brief) live window; the api may
	// have already 404'd it once the stream ended, so this is best-effort logged.
	if code, _ := api.do(http.MethodGet, "/api/v1/live/"+id+"/hls/master.m3u8", token, nil); code == http.StatusOK {
		t.Logf("live HLS master served (200) during/after the session")
	}

	// The on-publish-done hook returns the one-shot stream to ended.
	if !waitFor(t, 15*time.Second, func() bool { return getLiveState(api, id, token) == "ended" }) {
		t.Fatalf("stream did not settle to ended after the publish stopped")
	}

	// Replay: a published video titled "RTMP Session (replay)" appears on the
	// channel (best-effort background pipeline; give it time to probe/transcode).
	if !waitFor(t, 60*time.Second, func() bool {
		code, listBody := api.do(http.MethodGet, "/api/v1/channels/"+handle+"/videos", token, nil)
		return code == http.StatusOK && strings.Contains(string(listBody), "RTMP Session (replay)")
	}) {
		t.Fatalf("replay VOD was not published to the channel")
	}
}

func getLiveState(api *rtmpAPI, id, token string) string {
	code, body := api.do(http.MethodGet, "/api/v1/live/"+id, token, nil)
	if code != http.StatusOK {
		return ""
	}
	var v struct {
		State string `json:"state"`
	}
	_ = json.Unmarshal(body, &v)
	return v.State
}

// waitFor polls cond every 500ms until it is true or the deadline passes.
func waitFor(t *testing.T, d time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
	return cond()
}

// itoa64 renders an int64 without importing strconv here (this file's helper;
// hls_test.go owns the untagged int variant named itoa — keep the names distinct
// so the package still builds under -tags integration, where both are compiled).
func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
