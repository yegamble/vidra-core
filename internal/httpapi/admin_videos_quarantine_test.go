package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/video"
)

// quarantineQueueBody parses GET /admin/videos/quarantined.
type quarantineQueueBody struct {
	Videos []struct {
		ID            string `json:"id"`
		Title         string `json:"title"`
		State         string `json:"state"`
		ChannelHandle string `json:"channel_handle"`
		OwnerUsername string `json:"owner_username"`
	} `json:"videos"`
}

// hookRecorder collects publish/transcode hook invocations (mutex-guarded so
// -race stays quiet even though handler tests are single-threaded).
type hookRecorder struct {
	mu         sync.Mutex
	published  []uuid.UUID
	transcoded []string
}

func (h *hookRecorder) options() []video.Option {
	return []video.Option{
		video.WithQuarantineNewUploads(true),
		video.WithPublishHook(func(_ context.Context, id uuid.UUID) {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.published = append(h.published, id)
		}),
		video.WithTranscodeHook(func(_ context.Context, _ uuid.UUID, key string) {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.transcoded = append(h.transcoded, key)
		}),
	}
}

func (h *hookRecorder) counts() (int, int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.published), len(h.transcoded)
}

// TestQuarantineFlow drives §11 end to end: with QUARANTINE_NEW_UPLOADS on, a
// regular user's upload parks in 'quarantined' (no publish hooks), is visible
// only to the owner and moderators, sits in the admin queue, and approval
// publishes it through the real publish transition (hooks fire then).
func TestQuarantineFlow(t *testing.T) {
	hooks := &hookRecorder{}
	srv := videoServerCfg(t, testConfig(), hooks.options()...)
	var buf bytes.Buffer
	srv.logger = slog.New(slog.NewJSONHandler(&buf, nil))

	// First account is the admin; their own upload publishes directly (role
	// admin never quarantines).
	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	adminVid := createPublishedVideo(t, srv, admin, "ada", `{"title":"by admin","privacy":"public"}`)
	if rec := getVideo(srv, adminVid, ""); rec.Code != http.StatusOK {
		t.Fatalf("admin upload should publish directly, get = %d", rec.Code)
	}
	if p, tc := hooks.counts(); p != 1 || tc != 1 {
		t.Fatalf("hooks after admin publish = (%d, %d), want (1, 1)", p, tc)
	}

	// A regular user's upload lands in quarantine instead of publishing.
	bob := createChannelFor(t, srv, "bob", "bob@example.test", "bobtube")
	vid := createVideo(t, srv, bob, "bobtube", `{"title":"held upload","privacy":"public"}`)
	up := uploadVideoFile(srv, vid, "clip.mp4", "video/mp4", "tiny", bob)
	if up.Code != http.StatusCreated {
		t.Fatalf("upload = %d; body=%s", up.Code, up.Body.String())
	}
	var uploaded uploadVideoFileResponse
	_ = json.Unmarshal(up.Body.Bytes(), &uploaded)
	if uploaded.Video.State != "quarantined" {
		t.Fatalf("state after upload = %q, want quarantined", uploaded.Video.State)
	}
	if p, tc := hooks.counts(); p != 1 || tc != 1 {
		t.Fatalf("hooks fired at quarantine time: (%d, %d), want still (1, 1)", p, tc)
	}

	// Public surfaces exclude it; the owner sees it badged; moderators can view.
	var feed videoFeedResponse
	_ = json.Unmarshal(sendJSONAuth(srv, http.MethodGet, "/api/v1/videos", "", "").Body.Bytes(), &feed)
	for _, v := range feed.Videos {
		if v.ID == vid {
			t.Errorf("quarantined video leaked into the public feed")
		}
	}
	if rec := getVideo(srv, vid, ""); rec.Code != http.StatusNotFound {
		t.Errorf("anon get quarantined = %d, want 404", rec.Code)
	}
	charlie := registerAndToken(t, srv, `{"username":"charlie","email":"charlie@example.test","password":"supersecret"}`)
	if rec := getVideo(srv, vid, charlie); rec.Code != http.StatusNotFound {
		t.Errorf("other-user get quarantined = %d, want 404", rec.Code)
	}
	if rec := getWithAuth(srv, "/api/v1/videos/"+vid+"/original", charlie); rec.Code != http.StatusNotFound {
		t.Errorf("other-user original of quarantined = %d, want 404", rec.Code)
	}
	rec := getVideo(srv, vid, bob)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner get quarantined = %d, want 200", rec.Code)
	}
	var ownerView videoView
	_ = json.Unmarshal(rec.Body.Bytes(), &ownerView)
	if ownerView.State != "quarantined" {
		t.Errorf("owner sees state %q, want quarantined (studio badge)", ownerView.State)
	}
	if rec := getWithAuth(srv, "/api/v1/videos/"+vid+"/original", bob); rec.Code != http.StatusOK {
		t.Errorf("owner original of quarantined = %d, want 200", rec.Code)
	}
	if rec := getVideo(srv, vid, admin); rec.Code != http.StatusOK {
		t.Errorf("moderator get quarantined = %d, want 200", rec.Code)
	}

	// The moderation queue lists it (admin/moderator only).
	qrec := getWithAuth(srv, "/api/v1/admin/videos/quarantined", admin)
	if qrec.Code != http.StatusOK {
		t.Fatalf("queue = %d; body=%s", qrec.Code, qrec.Body.String())
	}
	var queue quarantineQueueBody
	_ = json.Unmarshal(qrec.Body.Bytes(), &queue)
	if len(queue.Videos) != 1 || queue.Videos[0].ID != vid || queue.Videos[0].OwnerUsername != "bob" {
		t.Fatalf("queue = %+v, want bob's held upload", queue.Videos)
	}
	if rec := getWithAuth(srv, "/api/v1/admin/videos/quarantined", bob); rec.Code != http.StatusForbidden {
		t.Errorf("non-mod queue = %d, want 403", rec.Code)
	}
	if rec := getWithAuth(srv, "/api/v1/admin/videos/quarantined", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anon queue = %d, want 401", rec.Code)
	}

	// Approval publishes through the real publish transition: hooks fire NOW.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/videos/"+vid+"/approve", "", bob); rec.Code != http.StatusForbidden {
		t.Errorf("non-mod approve = %d, want 403", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/videos/"+vid+"/approve", "", admin); rec.Code != http.StatusNoContent {
		t.Fatalf("approve = %d; body=%s", rec.Code, rec.Body.String())
	}
	if p, tc := hooks.counts(); p != 2 || tc != 2 {
		t.Fatalf("hooks after approve = (%d, %d), want (2, 2)", p, tc)
	}
	if rec := getVideo(srv, vid, ""); rec.Code != http.StatusOK {
		t.Errorf("anon get after approve = %d, want 200", rec.Code)
	}
	_ = json.Unmarshal(sendJSONAuth(srv, http.MethodGet, "/api/v1/videos", "", "").Body.Bytes(), &feed)
	found := false
	for _, v := range feed.Videos {
		if v.ID == vid {
			found = true
		}
	}
	if !found {
		t.Errorf("approved video missing from the public feed")
	}
	if ev := findAudit(auditEvents(t, &buf), observability.ActionVideoApprove, observability.ResultSuccess); ev == nil {
		t.Errorf("no %s audit event on approve", observability.ActionVideoApprove)
	} else if !strings.Contains(ev["reason"].(string), vid) {
		t.Errorf("approve audit reason = %v, want the video id", ev["reason"])
	}

	// Approve is state-guarded, not a general publish button.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/videos/"+vid+"/approve", "", admin); rec.Code != http.StatusConflict {
		t.Errorf("re-approve = %d, want 409", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/videos/"+uuid.NewString()+"/approve", "", admin); rec.Code != http.StatusNotFound {
		t.Errorf("approve unknown = %d, want 404", rec.Code)
	}
	if qrec := getWithAuth(srv, "/api/v1/admin/videos/quarantined", admin); qrec.Code == http.StatusOK {
		var after quarantineQueueBody
		_ = json.Unmarshal(qrec.Body.Bytes(), &after)
		if len(after.Videos) != 0 {
			t.Errorf("queue after approve = %d entries, want 0", len(after.Videos))
		}
	}
}

// TestQuarantineRejectNotifiesOwner proves rejection fails the video, notifies
// the owner (without exposing the moderator), and audits the reason.
func TestQuarantineRejectNotifiesOwner(t *testing.T) {
	hooks := &hookRecorder{}
	srv := videoServerCfg(t, testConfig(), hooks.options()...)
	var buf bytes.Buffer
	srv.logger = slog.New(slog.NewJSONHandler(&buf, nil))

	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	bob := createChannelFor(t, srv, "bob", "bob@example.test", "bobtube")
	vid := createVideo(t, srv, bob, "bobtube", `{"title":"spammy","privacy":"public"}`)
	if rec := uploadVideoFile(srv, vid, "clip.mp4", "video/mp4", "tiny", bob); rec.Code != http.StatusCreated {
		t.Fatalf("upload = %d; body=%s", rec.Code, rec.Body.String())
	}

	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/videos/"+vid+"/reject", `{"reason":"spam"}`, bob); rec.Code != http.StatusForbidden {
		t.Errorf("non-mod reject = %d, want 403", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/videos/"+vid+"/reject", `{"reason":"spam"}`, admin); rec.Code != http.StatusNoContent {
		t.Fatalf("reject = %d; body=%s", rec.Code, rec.Body.String())
	}
	if p, tc := hooks.counts(); p != 0 || tc != 0 {
		t.Fatalf("hooks fired on reject: (%d, %d), want (0, 0)", p, tc)
	}

	// The owner sees the video failed in their studio list.
	rec := getVideo(srv, vid, bob)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner get after reject = %d, want 200", rec.Code)
	}
	var v videoView
	_ = json.Unmarshal(rec.Body.Bytes(), &v)
	if v.State != "failed" {
		t.Errorf("state after reject = %q, want failed", v.State)
	}

	// The owner is notified — type video_rejected with the video context, and
	// the moderator's identity is NOT exposed.
	nrec := getWithAuth(srv, "/api/v1/me/notifications", bob)
	if nrec.Code != http.StatusOK {
		t.Fatalf("notifications = %d; body=%s", nrec.Code, nrec.Body.String())
	}
	var notifs struct {
		Notifications []struct {
			Type       string          `json:"type"`
			VideoID    string          `json:"video_id"`
			VideoTitle string          `json:"video_title"`
			Actor      json.RawMessage `json:"actor"`
		} `json:"notifications"`
	}
	_ = json.Unmarshal(nrec.Body.Bytes(), &notifs)
	foundNotif := false
	for _, n := range notifs.Notifications {
		if n.Type == "video_rejected" {
			foundNotif = true
			if n.VideoID != vid || n.VideoTitle != "spammy" {
				t.Errorf("video_rejected context = (%q, %q), want (%s, spammy)", n.VideoID, n.VideoTitle, vid)
			}
			if len(n.Actor) != 0 {
				t.Errorf("video_rejected exposes the moderator: %s", n.Actor)
			}
		}
	}
	if !foundNotif {
		t.Fatalf("owner got no video_rejected notification: %+v", notifs.Notifications)
	}

	// Audited with the reason; state-guarded on repeat.
	if ev := findAudit(auditEvents(t, &buf), observability.ActionVideoReject, observability.ResultSuccess); ev == nil {
		t.Errorf("no %s audit event on reject", observability.ActionVideoReject)
	} else if reason, _ := ev["reason"].(string); !strings.Contains(reason, "spam") || !strings.Contains(reason, vid) {
		t.Errorf("reject audit reason = %q, want the video id and reason", reason)
	}
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/videos/"+vid+"/reject", "", admin); rec.Code != http.StatusConflict {
		t.Errorf("re-reject = %d, want 409", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/videos/"+vid+"/reject", `{"reason":"`+strings.Repeat("x", 2001)+`"}`, admin); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("oversized reason = %d, want 422", rec.Code)
	}
}

// TestQuarantineBypassFlag proves the admin PATCH grants bypass_quarantine and
// that a bypassed user's uploads publish directly despite the instance gate.
func TestQuarantineBypassFlag(t *testing.T) {
	hooks := &hookRecorder{}
	srv := videoServerCfg(t, testConfig(), hooks.options()...)
	var buf bytes.Buffer
	srv.logger = slog.New(slog.NewJSONHandler(&buf, nil))

	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	bob, bobID := registerAndUser(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/channels", `{"handle":"bobtube","display_name":"Bob"}`, bob); rec.Code != http.StatusCreated {
		t.Fatalf("bob channel = %d", rec.Code)
	}

	// Grant the bypass; the response reflects it and the change is audited.
	prec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/admin/users/"+bobID, `{"bypass_quarantine":true}`, admin)
	if prec.Code != http.StatusOK {
		t.Fatalf("grant bypass = %d; body=%s", prec.Code, prec.Body.String())
	}
	var view adminUserView
	_ = json.Unmarshal(prec.Body.Bytes(), &view)
	if !view.BypassQuarantine {
		t.Fatalf("bypass_quarantine not reflected in the PATCH response: %+v", view)
	}
	if ev := findAudit(auditEvents(t, &buf), observability.ActionAdminUserUpdate, observability.ResultSuccess); ev == nil {
		t.Errorf("no admin.user.update audit event")
	} else if reason, _ := ev["reason"].(string); !strings.Contains(reason, "bypass_quarantine=true") {
		t.Errorf("audit reason = %q, want bypass_quarantine=true", reason)
	}

	// A bypassed regular user publishes directly.
	vid := createPublishedVideo(t, srv, bob, "bobtube", `{"title":"trusted","privacy":"public"}`)
	if rec := getVideo(srv, vid, ""); rec.Code != http.StatusOK {
		t.Errorf("bypassed upload get = %d, want 200 (published)", rec.Code)
	}
	if p, tc := hooks.counts(); p != 1 || tc != 1 {
		t.Errorf("hooks after bypassed publish = (%d, %d), want (1, 1)", p, tc)
	}

	// Revoking re-subjects them to the gate.
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/admin/users/"+bobID, `{"bypass_quarantine":false}`, admin); rec.Code != http.StatusOK {
		t.Fatalf("revoke bypass = %d", rec.Code)
	}
	vid2 := createVideo(t, srv, bob, "bobtube", `{"title":"held again","privacy":"public"}`)
	up := uploadVideoFile(srv, vid2, "clip.mp4", "video/mp4", "tiny", bob)
	var uploaded uploadVideoFileResponse
	_ = json.Unmarshal(up.Body.Bytes(), &uploaded)
	if uploaded.Video.State != "quarantined" {
		t.Errorf("state after revoke = %q, want quarantined", uploaded.Video.State)
	}
}
