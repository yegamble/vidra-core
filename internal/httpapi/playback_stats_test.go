package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/playbackstats"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

type recordingPlayback struct {
	samples []playbackstats.Sample
	report  playbackstats.Report
}

func (r *recordingPlayback) Record(_ context.Context, in playbackstats.Sample) error {
	r.samples = append(r.samples, in)
	return nil
}
func (r *recordingPlayback) Report(context.Context, uuid.UUID, int, time.Time) (playbackstats.Report, error) {
	return r.report, nil
}

func playbackFixture(t *testing.T) (*Server, *recordingPlayback, string, string) {
	t.Helper()
	rec := &recordingPlayback{report: playbackstats.Report{Plays: 2, WatchSeconds: 30, AvgViewDurationSeconds: 15}}
	srv, _, _, _, repo := videoServerFullWith(t, testConfig(), []Option{WithPlaybackStats(rec)})
	ownerTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPublishedVideo(t, srv, ownerTok, "ada", `{"title":"clip","privacy":"public"}`)
	d := int32(200)
	if _, err := repo.UpsertVideoMetadata(context.Background(), sqlcgen.UpsertVideoMetadataParams{VideoID: uuid.MustParse(id), DurationSeconds: &d}); err != nil {
		t.Fatal(err)
	}
	return srv, rec, ownerTok, id
}

func beacon(srv *Server, id, body, ua string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/"+id+"/playback", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	if ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestPlaybackBeaconRecordsBucketedSampleOnly(t *testing.T) {
	srv, rec, _, id := playbackFixture(t)
	r := beacon(srv, id, `{"position_seconds":42,"watched_seconds":10,"started":true}`, "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) Mobile/15E148")
	if r.Code != http.StatusNoContent {
		t.Fatalf("beacon = %d; %s", r.Code, r.Body.String())
	}
	if len(rec.samples) != 1 {
		t.Fatalf("samples = %d", len(rec.samples))
	}
	s := rec.samples[0]
	if s.Device != "mobile" || s.OS != "ios" || s.DurationSeconds != 200 || !s.Started || s.WatchedSeconds != 10 || s.PositionSeconds != 42 {
		t.Fatalf("sample = %+v", s)
	}
}

func TestPlaybackBeaconValidation(t *testing.T) {
	srv, rec, _, id := playbackFixture(t)
	for name, body := range map[string]string{
		"negative watched": `{"position_seconds":1,"watched_seconds":-1}`,
		"huge watched":     `{"position_seconds":1,"watched_seconds":13}`,
		"negative pos":     `{"position_seconds":-1,"watched_seconds":1}`,
		"past the end":     `{"position_seconds":206,"watched_seconds":1}`,
		"not json":         `nope`,
	} {
		if r := beacon(srv, id, body, ""); r.Code != http.StatusBadRequest && r.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s = %d, want 4xx validation", name, r.Code)
		}
	}
	if r := beacon(srv, uuid.NewString(), `{"position_seconds":1,"watched_seconds":1}`, ""); r.Code != http.StatusNotFound {
		t.Errorf("unknown video = %d, want 404", r.Code)
	}
	if len(rec.samples) != 0 {
		t.Fatalf("invalid beacons recorded %d samples", len(rec.samples))
	}
}

func TestPlaybackBeaconRateLimited(t *testing.T) {
	srv, rec, _, id := playbackFixture(t)
	var got429 bool
	for i := 0; i < defaultPlaybackRateLimit+5; i++ {
		if r := beacon(srv, id, `{"position_seconds":1,"watched_seconds":1}`, ""); r.Code == http.StatusTooManyRequests {
			got429 = true
			break
		}
	}
	if !got429 {
		t.Fatal("no 429 after exceeding the beacon budget")
	}
	if len(rec.samples) > defaultPlaybackRateLimit {
		t.Fatalf("recorded %d samples past the budget", len(rec.samples))
	}
}

func TestPlaybackBeaconIgnoresOwnerPlays(t *testing.T) {
	srv, rec, ownerTok, id := playbackFixture(t)
	if r := postJSONAuth(srv, "/api/v1/videos/"+id+"/playback", `{"position_seconds":1,"watched_seconds":5}`, ownerTok); r.Code != http.StatusNoContent {
		t.Fatalf("owner beacon = %d", r.Code)
	}
	if len(rec.samples) != 0 {
		t.Fatal("owner's own play was counted")
	}
}

func TestPlaybackStatsAuthz(t *testing.T) {
	srv, _, ownerTok, id := playbackFixture(t)
	editorTok, _ := inviteEditor(t, srv, ownerTok, "ada", "bob", "bob@example.test")
	strangerTok := registerAndToken(t, srv, `{"username":"eve","email":"eve@example.test","password":"supersecret"}`)
	path := "/api/v1/videos/" + id + "/playback-stats"

	if r := getWithAuth(srv, path, ""); r.Code != http.StatusUnauthorized {
		t.Errorf("anonymous = %d, want 401", r.Code)
	}
	if r := getWithAuth(srv, path, strangerTok); r.Code != http.StatusNotFound {
		t.Errorf("stranger = %d, want 404", r.Code)
	}
	if r := getWithAuth(srv, "/api/v1/videos/"+uuid.NewString()+"/playback-stats", ownerTok); r.Code != http.StatusNotFound {
		t.Errorf("unknown = %d, want 404", r.Code)
	}
	for who, tok := range map[string]string{"owner": ownerTok, "editor": editorTok} {
		r := getWithAuth(srv, path, tok)
		if r.Code != http.StatusOK {
			t.Fatalf("%s = %d; %s", who, r.Code, r.Body.String())
		}
		var resp playbackStatsResponse
		_ = json.Unmarshal(r.Body.Bytes(), &resp)
		if resp.Plays != 2 || resp.AvgViewDurationSeconds != 15 || resp.DurationSeconds != 200 {
			t.Errorf("%s body = %+v", who, resp)
		}
	}
}

func TestPlaybackStartedCountsOncePerClientAndVideo(t *testing.T) {
	srv, rec, _, id := playbackFixture(t)
	for i := 0; i < 3; i++ {
		if r := beacon(srv, id, `{"position_seconds":1,"watched_seconds":5,"started":true}`, ""); r.Code != http.StatusNoContent {
			t.Fatalf("beacon %d = %d", i, r.Code)
		}
	}
	starts := 0
	for _, s := range rec.samples {
		if s.Started {
			starts++
		}
	}
	if len(rec.samples) != 3 || starts != 1 {
		t.Fatalf("samples=%d starts=%d, want 3 and 1", len(rec.samples), starts)
	}
}

func TestPlaybackLimiterKeyedPerVideoAndIPv6Prefix(t *testing.T) {
	key := func(ip, id string) string {
		e := echo.New()
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.RemoteAddr = ip
		c := e.NewContext(req, httptest.NewRecorder())
		c.SetParamNames("id")
		c.SetParamValues(id)
		return playbackClientKey(c)
	}
	if key("[2001:db8:1:2::1]:80", "a") != key("[2001:db8:1:2:ffff::9]:80", "a") {
		t.Error("addresses inside one /64 must share a key")
	}
	if key("[2001:db8:1:2::1]:80", "a") == key("[2001:db8:1:3::1]:80", "a") {
		t.Error("different /64s must not share a key")
	}
	if key("203.0.113.5:80", "a") == key("203.0.113.5:80", "b") {
		t.Error("different videos must not share a key")
	}
}

func TestPlaybackBeaconVisibility(t *testing.T) {
	rec := &recordingPlayback{}
	srv, _, _, _, _ := videoServerFullWith(t, testConfig(), []Option{WithPlaybackStats(rec)})
	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	draft := createVideo(t, srv, admin, "ada", `{"title":"d","privacy":"public"}`)
	private := createPublishedVideo(t, srv, admin, "ada", `{"title":"p","privacy":"private"}`)
	unlisted := createPublishedVideo(t, srv, admin, "ada", `{"title":"u","privacy":"unlisted"}`)
	blocked := createPublishedVideo(t, srv, admin, "ada", `{"title":"b","privacy":"public"}`)
	if r := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/videos/"+blocked+"/block", `{"reason":"x"}`, admin); r.Code != http.StatusNoContent {
		t.Fatalf("block = %d", r.Code)
	}
	body := `{"position_seconds":1,"watched_seconds":5}`
	for name, id := range map[string]string{"draft": draft, "private": private, "blocked": blocked} {
		if r := beacon(srv, id, body, ""); r.Code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", name, r.Code)
		}
	}
	if len(rec.samples) != 0 {
		t.Fatalf("hidden videos recorded %d samples", len(rec.samples))
	}
	if r := beacon(srv, unlisted, body, ""); r.Code != http.StatusNoContent || len(rec.samples) != 1 {
		t.Fatalf("unlisted = %d samples=%d, want 204 and 1", r.Code, len(rec.samples))
	}
}
