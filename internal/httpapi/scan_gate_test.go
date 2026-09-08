package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/account"
	"github.com/vidra/vidra-core/internal/channelsync"
	"github.com/vidra/vidra-core/internal/config"
	"github.com/vidra/vidra-core/internal/profileimage"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/video"
)

// The ingestion coverage table, as an executable list.
//
// A28's INT-04 lane asked whether scanning gates ALL ingestion and found that
// it gated two paths: video originals and DM attachments. Posters, avatars,
// banners, playlist covers, caption tracks, instance branding and account-import
// archives all reached the object store unscanned, and with CLAMAV_ADDR unset
// even the two that WERE covered gated nothing.
//
// This table is the answer, and it is the reason it is a table rather than
// seven tests: a route added later that forgets the gate is a silent hole, and
// the only defence is a list somebody has to edit deliberately.
type ingestRoute struct {
	name   string
	method string
	path   string
	// body/contentType are irrelevant to the gate (it runs as middleware,
	// before the handler reads anything) but are supplied so the request is
	// well formed enough to reach it.
	multipart bool
	body      string
}

func userSuppliedIngestRoutes() []ingestRoute {
	vid := uuid.New().String()
	return []ingestRoute{
		{"video original (direct)", http.MethodPost, "/api/v1/videos/" + vid + "/file", true, ""},
		{"video original (resumable session)", http.MethodPost, "/api/v1/videos/" + vid + "/upload-session", false, `{"filename":"a.mp4","total_size":1}`},
		{"video original (resumable complete)", http.MethodPost, "/api/v1/uploads/" + uuid.New().String() + "/complete", false, `{}`},
		{"video source replace (direct)", http.MethodPost, "/api/v1/videos/" + vid + "/replace", true, ""},
		{"video source replace (session)", http.MethodPost, "/api/v1/videos/" + vid + "/replace-session", false, `{"filename":"a.mp4","total_size":1}`},
		{"URL import", http.MethodPost, "/api/v1/videos/" + vid + "/import", false, `{"url":"https://example.test/a.mp4"}`},
		{"channel sync (create)", http.MethodPost, "/api/v1/channel-syncs", false, `{"handle":"ada","source_url":"https://example.test/c"}`},
		{"channel sync (run now)", http.MethodPost, "/api/v1/channel-syncs/" + uuid.New().String() + "/sync-now", false, `{}`},
		{"custom poster", http.MethodPost, "/api/v1/videos/" + vid + "/thumbnail", true, ""},
		{"caption track", http.MethodPost, "/api/v1/videos/" + vid + "/captions", true, ""},
		{"playlist cover", http.MethodPost, "/api/v1/playlists/" + uuid.New().String() + "/thumbnail", true, ""},
		{"user avatar", http.MethodPost, "/api/v1/me/avatar", true, ""},
		{"user banner", http.MethodPost, "/api/v1/me/banner", true, ""},
		{"channel avatar", http.MethodPost, "/api/v1/channels/ada/avatar", true, ""},
		{"channel banner", http.MethodPost, "/api/v1/channels/ada/banner", true, ""},
		{"account import archive", http.MethodPost, "/api/v1/me/import", false, `{"version":1}`},
		{"DM attachment", http.MethodPost, "/api/v1/conversations/" + uuid.New().String() + "/attachments", true, ""},
	}
}

// adminIngestRoutes are the operator-supplied files. They are user-supplied too
// — an admin is a person with a file — and they carry the same gate.
func adminIngestRoutes() []ingestRoute {
	return []ingestRoute{
		{"instance avatar", http.MethodPost, "/api/v1/admin/instance-avatar", true, ""},
		{"instance banner", http.MethodPost, "/api/v1/admin/instance-banner", true, ""},
		{"instance logo", http.MethodPost, "/api/v1/admin/instance-logo/light", true, ""},
		{"PeerTube migration", http.MethodPost, "/api/v1/admin/peertube-import", false, `{"source_dsn":"postgres://x"}`},
	}
}

func ingestRequest(r ingestRoute, token string) *http.Request {
	if !r.multipart {
		req := httptest.NewRequest(r.method, r.path, strings.NewReader(r.body))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("authorization", "Bearer "+token)
		}
		return req
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, _ := w.CreateFormFile("file", "f.jpg")
	_, _ = part.Write([]byte("not really a jpeg"))
	_ = w.WriteField("language", "en")
	_ = w.Close()
	req := httptest.NewRequest(r.method, r.path, &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	if token != "" {
		req.Header.Set("authorization", "Bearer "+token)
	}
	return req
}

// TestUnconfiguredScannerRefusesEveryIngestionRoute is the whole of the new
// default posture: no CLAMAV_ADDR and no explicit opt-out means this instance
// accepts nothing, with a typed 503 that names both levers.
func TestUnconfiguredScannerRefusesEveryIngestionRoute(t *testing.T) {
	cfg := testConfig()
	cfg.MalwareScanMode = "" // neither a scanner nor an opt-out — the shipped A28 default
	srv := scanGateServer(t, cfg)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")

	routes := append(userSuppliedIngestRoutes(), adminIngestRoutes()...)
	for _, r := range routes {
		t.Run(r.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, ingestRequest(r, tok))
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("%s %s = %d, want 503; body=%s", r.method, r.path, rec.Code, rec.Body.String())
			}
			var errResp ErrorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if errResp.Error.Code != "scanner_not_configured" {
				t.Errorf("code = %q, want scanner_not_configured", errResp.Error.Code)
			}
			// The 503 must survive the 5xx message scrubber with its operator
			// sentence intact — that is the entire reason it is typed.
			for _, want := range []string{"CLAMAV_ADDR", "MALWARE_SCAN_MODE=disabled"} {
				if !strings.Contains(errResp.Error.Message, want) {
					t.Errorf("message %q does not name %s", errResp.Error.Message, want)
				}
			}
		})
	}
}

// TestExplicitOptOutAllowsIngestion: the operator said so, so the gate stands
// down. Without this the opt-out would not be an opt-out.
func TestExplicitOptOutAllowsIngestion(t *testing.T) {
	cfg := testConfig() // testConfig already declares the opt-out
	if !cfg.MalwareScanOptedOut() {
		t.Fatal("testConfig no longer declares the opt-out; this test proves nothing")
	}
	srv := videoServerCfg(t, cfg)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"My Draft","privacy":"public"}`)
	rec := uploadVideoFile(srv, id, "clip.mp4", "video/mp4", "pretend this is an mp4", tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload under the opt-out = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
}

// TestUnauthenticatedCallerStillGets401 keeps the gate from becoming a posture
// disclosure: an anonymous request must not learn whether this instance has a
// scanner configured.
func TestUnauthenticatedCallerStillGets401(t *testing.T) {
	cfg := testConfig()
	cfg.MalwareScanMode = ""
	srv := scanGateServer(t, cfg)
	for _, r := range userSuppliedIngestRoutes() {
		t.Run(r.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, ingestRequest(r, ""))
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("%s anonymous = %d, want 401", r.path, rec.Code)
			}
		})
	}
}

// TestInstanceHidesIngestionWhenTheScannerIsMissing. A17's capability truth: the
// Studio must hide an upload control the server will refuse, and it reads
// /instance to decide.
func TestInstanceHidesIngestionWhenTheScannerIsMissing(t *testing.T) {
	for _, tc := range []struct {
		name  string
		mode  string
		addr  string
		avail bool
	}{
		{"no scanner, no opt-out", "", "", false},
		{"explicit opt-out", config.ScanOptOut, "", true},
		{"scanner configured", "fail-closed", "clamav:3310", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig()
			cfg.MalwareScanMode = tc.mode
			cfg.ClamAVAddr = tc.addr
			cfg.MalwareScanEnabled = tc.addr != ""
			srv := videoServerCfg(t, cfg)

			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/instance", nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("/instance = %d", rec.Code)
			}
			var body struct {
				Features struct {
					Uploads      bool `json:"uploads"`
					Imports      bool `json:"imports"`
					UserImport   bool `json:"user_import"`
					VideoReplace bool `json:"video_replace"`
				} `json:"features"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if body.Features.Uploads != tc.avail {
				t.Errorf("features.uploads = %v, want %v", body.Features.Uploads, tc.avail)
			}
			if body.Features.Imports != tc.avail {
				t.Errorf("features.imports = %v, want %v", body.Features.Imports, tc.avail)
			}
			if body.Features.UserImport != tc.avail {
				t.Errorf("features.user_import = %v, want %v", body.Features.UserImport, tc.avail)
			}
			if !tc.avail && body.Features.VideoReplace {
				t.Error("features.video_replace stayed true with no scanner")
			}
		})
	}
}

// errDeadScanner stands in for an unreachable clamd.
var errDeadScanner = errors.New("clamav: dial: connection refused")

// scanGateServer mounts every service whose routes carry the ingestion gate, so
// the coverage table can assert on ALL of them from one fixture rather than
// silently skipping the ones a narrower fixture never registered.
func scanGateServer(t *testing.T, cfg *config.Config, extra ...Option) *Server {
	t.Helper()
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	opts := append([]Option{
		WithProfileImageService(profileimage.NewService(newImageFakeRepo(), blobs)),
		WithAccountService(account.NewService(nil, nil, nil)),
		WithChannelSyncService(channelsync.NewService(nil, nil, nil)),
		WithPeerTubeImportService(fakePTImport{configured: true}),
	}, extra...)
	srv, _, _, _, _ := videoServerFullWith(t, cfg, opts)
	return srv
}

// infectedScanner is a FileScanner that condemns everything.
type infectedScanner struct{ err error }

func (f infectedScanner) ScanBytes(context.Context, []byte) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	return false, nil
}

// TestNonVideoFilesAreScannedBeforeTheyAreStored is SC3's core claim: the six
// object classes A28 found ingesting unscanned now go through the same seam,
// refuse with the SAME neutral sentence, and — because they are scanned before
// the write — leave nothing behind.
func TestNonVideoFilesAreScannedBeforeTheyAreStored(t *testing.T) {
	cfg := testConfig()
	cfg.MalwareScanMode = "fail-closed"
	cfg.ClamAVAddr = "clamav:3310"
	cfg.MalwareScanEnabled = true

	for _, r := range []ingestRoute{
		{"custom poster", http.MethodPost, "/api/v1/videos/%VIDEO%/thumbnail", true, ""},
		{"caption track", http.MethodPost, "/api/v1/videos/%VIDEO%/captions", true, ""},
		{"user avatar", http.MethodPost, "/api/v1/me/avatar", true, ""},
		{"user banner", http.MethodPost, "/api/v1/me/banner", true, ""},
		{"channel avatar", http.MethodPost, "/api/v1/channels/ada/avatar", true, ""},
		{"account import archive", http.MethodPost, "/api/v1/me/import", false, `{"version":1}`},
	} {
		t.Run(r.name, func(t *testing.T) {
			srv := scanGateServer(t, cfg, WithFileScanner(infectedScanner{}))
			tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
			id := createVideo(t, srv, tok, "ada", `{"title":"My Draft","privacy":"public"}`)
			r.path = strings.ReplaceAll(r.path, "%VIDEO%", id)

			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, ingestRequest(r, tok))
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("%s = %d, want 422; body=%s", r.path, rec.Code, rec.Body.String())
			}
			var errResp ErrorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if errResp.Error.Code != video.SafetyScanRejectedCode {
				t.Errorf("code = %q, want %q", errResp.Error.Code, video.SafetyScanRejectedCode)
			}
			if errResp.Error.Message != video.SafetyScanRejectedMessage {
				t.Errorf("message = %q, want the neutral sentence", errResp.Error.Message)
			}
		})
	}
}

// TestFailOpenStoresNonVideoFilesUnscanned pins the OTHER half of the policy on
// these routes: fail-open is still fail-open, and the file lands.
func TestFailOpenStoresNonVideoFilesUnscanned(t *testing.T) {
	cfg := testConfig()
	cfg.MalwareScanMode = "fail-open"
	cfg.ClamAVAddr = "clamav:3310"
	cfg.MalwareScanEnabled = true
	srv := scanGateServer(t, cfg, WithFileScanner(infectedScanner{err: errDeadScanner}))
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, ingestRequest(ingestRoute{
		method: http.MethodPost, path: "/api/v1/me/avatar", multipart: true,
	}, tok))
	// A scan ERROR under fail-open must not refuse. The route still applies its
	// own extension gate (the fixture part is named .jpg), so anything but a
	// safety-scan refusal proves the policy was honoured.
	if rec.Code == http.StatusUnprocessableEntity && strings.Contains(rec.Body.String(), video.SafetyScanRejectedCode) {
		t.Fatalf("fail-open refused an unscannable avatar: %s", rec.Body.String())
	}
}

// TestQuarantineFallsBackToFailClosedForNonVideoFiles documents the ruling: an
// avatar has no moderation queue to be "held for review" in, so quarantine mode
// refuses rather than silently publishing.
func TestQuarantineFallsBackToFailClosedForNonVideoFiles(t *testing.T) {
	cfg := testConfig()
	cfg.MalwareScanMode = "quarantine"
	cfg.ClamAVAddr = "clamav:3310"
	cfg.MalwareScanEnabled = true
	srv := scanGateServer(t, cfg, WithFileScanner(infectedScanner{err: errDeadScanner}))
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, ingestRequest(ingestRoute{
		method: http.MethodPost, path: "/api/v1/me/avatar", multipart: true,
	}, tok))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("quarantine on an avatar = %d, want 422 (no queue exists for this class); body=%s", rec.Code, rec.Body.String())
	}
}
