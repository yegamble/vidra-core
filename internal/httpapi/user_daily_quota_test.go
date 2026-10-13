package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func patchUserDaily(t *testing.T, srv *Server, userID, body, tok string) adminUserView {
	t.Helper()
	rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/admin/users/"+userID, body, tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH %s = %d; body=%s", body, rec.Code, rec.Body.String())
	}
	var v adminUserView
	_ = json.Unmarshal(rec.Body.Bytes(), &v)
	return v
}

// TestPerUserDailyQuota drives the PeerTube videoQuotaDaily parity: an admin
// sets a per-user rolling-24h cap; it is enforced on the direct upload, the
// resumable session and URL import (exact boundary included), surfaces in the
// user's own /me/quota and the admin view, beats the instance default in both
// directions (0 = unlimited), and null resets to the instance default.
func TestPerUserDailyQuota(t *testing.T) {
	srv := videoServer(t)
	adminTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	bobTok := createChannelFor(t, srv, "bob", "bob@example.test", "bob")
	bobID := userIDByName(t, adminUsers(t, srv, "", adminTok).Users, "bob")

	// Validation: negative / non-integer is a 422, never stored.
	for _, bad := range []string{`{"daily_quota_bytes":-1}`, `{"daily_quota_bytes":"x"}`} {
		if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/admin/users/"+bobID, bad, adminTok); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("PATCH %s = %d, want 422", bad, rec.Code)
		}
	}

	view := patchUserDaily(t, srv, bobID, `{"daily_quota_bytes":20}`, adminTok)
	if view.DailyQuotaBytes == nil || *view.DailyQuotaBytes != 20 {
		t.Fatalf("admin view daily_quota_bytes = %v, want 20", view.DailyQuotaBytes)
	}
	if st := getMyQuota(t, srv, bobTok); st.DailyQuotaBytes == nil || *st.DailyQuotaBytes != 20 || st.DailyUsedBytes != 0 {
		t.Fatalf("bob /me/quota = %+v, want daily 20 used 0", st)
	}

	v1 := createVideo(t, srv, bobTok, "bob", `{"title":"one"}`)
	if rec := uploadVideoFile(srv, v1, "clip.mp4", "video/mp4", "ten__bytes", bobTok); rec.Code != http.StatusCreated {
		t.Fatalf("upload 1 = %d; body=%s", rec.Code, rec.Body.String())
	}
	// Exactly filling the cap (10 + 10 = 20) is allowed.
	v2 := createVideo(t, srv, bobTok, "bob", `{"title":"two"}`)
	if rec := uploadVideoFile(srv, v2, "clip.mp4", "video/mp4", "ten__bytes", bobTok); rec.Code != http.StatusCreated {
		t.Fatalf("boundary upload = %d; body=%s", rec.Code, rec.Body.String())
	}
	if st := getMyQuota(t, srv, bobTok); st.DailyUsedBytes != 20 {
		t.Fatalf("daily used = %d, want 20", st.DailyUsedBytes)
	}
	// One byte past the cap: direct upload, resumable session and import all refuse.
	v3 := createVideo(t, srv, bobTok, "bob", `{"title":"three"}`)
	rec := uploadVideoFile(srv, v3, "clip.mp4", "video/mp4", "1", bobTok)
	if rec.Code != http.StatusUnprocessableEntity || errorCode(t, rec) != "daily_quota_exceeded" {
		t.Fatalf("over-cap upload = %d %s, want 422 daily_quota_exceeded", rec.Code, rec.Body.String())
	}
	rec = sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+v3+"/upload-session", `{"size":1,"filename":"clip.mp4"}`, bobTok)
	if rec.Code != http.StatusUnprocessableEntity || errorCode(t, rec) != "daily_quota_exceeded" {
		t.Fatalf("over-cap session = %d %s, want 422 daily_quota_exceeded", rec.Code, rec.Body.String())
	}
	media := importMediaServer(t)
	enqueueImport(t, srv, v3, loopbackAsLocalhost(media.URL)+"/clip.mp4", bobTok)
	if _, err := srv.importsvc.DrainJobs(context.Background(), 5); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if job := getImportStatus(t, srv, v3, bobTok); job.State == "done" || !strings.Contains(job.Error, "quota") {
		t.Fatalf("over-cap import state=%q err=%q, want a quota failure", job.State, job.Error)
	}

	// Another account is unaffected by bob's override.
	if st := getMyQuota(t, srv, adminTok); st.DailyQuotaBytes != nil {
		t.Errorf("ada daily quota = %v, want null", *st.DailyQuotaBytes)
	}

	// The admin list carries the override and the 24h usage.
	for _, u := range adminUsers(t, srv, "", adminTok).Users {
		if u.ID == bobID && (u.DailyQuotaBytes == nil || *u.DailyQuotaBytes != 20 || u.DailyUsedBytes != 20) {
			t.Errorf("list row = quota %v used %d, want 20/20", u.DailyQuotaBytes, u.DailyUsedBytes)
		}
	}

	// 0 = unlimited beats a tight instance default; null falls back to it.
	patchSettings(t, srv, adminTok, `{"default_user_daily_quota_bytes": 5}`)
	patchUserDaily(t, srv, bobID, `{"daily_quota_bytes":0}`, adminTok)
	if st := getMyQuota(t, srv, bobTok); st.DailyQuotaBytes != nil {
		t.Fatalf("override 0 /me/quota = %v, want null (unlimited)", *st.DailyQuotaBytes)
	}
	if rec := uploadVideoFile(srv, v3, "clip.mp4", "video/mp4", "ten__bytes", bobTok); rec.Code != http.StatusCreated {
		t.Fatalf("unlimited-override upload = %d; body=%s", rec.Code, rec.Body.String())
	}
	view = patchUserDaily(t, srv, bobID, `{"daily_quota_bytes":null}`, adminTok)
	if view.DailyQuotaBytes != nil {
		t.Fatalf("reset view daily_quota_bytes = %v, want null", *view.DailyQuotaBytes)
	}
	if st := getMyQuota(t, srv, bobTok); st.DailyQuotaBytes == nil || *st.DailyQuotaBytes != 5 {
		t.Fatalf("reset /me/quota = %+v, want the instance default 5", st.DailyQuotaBytes)
	}
}
