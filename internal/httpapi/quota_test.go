package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vidra/vidra-core/internal/config"
)

// quotaCfg is testConfig with a finite instance-default storage quota.
func quotaCfg(defaultBytes int64) *config.Config {
	cfg := testConfig()
	cfg.InstanceDefaultQuotaBytes = defaultBytes
	return cfg
}

func getMyQuota(t *testing.T, srv *Server, token string) quotaStatusResponse {
	t.Helper()
	rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/me/quota", "", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /me/quota = %d; body=%s", rec.Code, rec.Body.String())
	}
	var body quotaStatusResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return body
}

// errorCode extracts the envelope's machine-readable code.
func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var env ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v; body=%s", err, rec.Body.String())
	}
	return env.Error.Code
}

func TestMeQuotaRequiresAuth(t *testing.T) {
	srv := videoServer(t)
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/me/quota", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anon /me/quota = %d, want 401", rec.Code)
	}
}

func TestMeQuotaUnlimitedByDefaultAndTracksUsage(t *testing.T) {
	srv := videoServer(t) // InstanceDefaultQuotaBytes zero-value → unlimited
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")

	st := getMyQuota(t, srv, tok)
	if st.UsedBytes != 0 || st.QuotaBytes != nil {
		t.Fatalf("fresh quota = %+v, want used=0 quota=null", st)
	}

	// Upload counts toward usage; the quota stays unlimited.
	id := createVideo(t, srv, tok, "ada", `{"title":"Clip"}`)
	const content = "ten__bytes"
	if rec := uploadVideoFile(srv, id, "clip.mp4", "video/mp4", content, tok); rec.Code != http.StatusCreated {
		t.Fatalf("upload = %d; body=%s", rec.Code, rec.Body.String())
	}
	st = getMyQuota(t, srv, tok)
	if st.UsedBytes != int64(len(content)) || st.QuotaBytes != nil {
		t.Errorf("quota after upload = %+v, want used=%d quota=null", st, len(content))
	}
}

func TestUploadBlockedWhenOverQuota(t *testing.T) {
	srv := videoServerCfg(t, quotaCfg(10))
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")

	// The instance default surfaces on /me/quota.
	if st := getMyQuota(t, srv, tok); st.QuotaBytes == nil || *st.QuotaBytes != 10 {
		t.Fatalf("quota = %+v, want quota=10", st)
	}

	// Over the cap in one go → 422 quota_exceeded, nothing stored.
	id := createVideo(t, srv, tok, "ada", `{"title":"Big"}`)
	rec := uploadVideoFile(srv, id, "clip.mp4", "video/mp4", "elevenbytes", tok) // 11 > 10
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("over-quota upload = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "quota_exceeded" {
		t.Errorf("error code = %q, want quota_exceeded", code)
	}
	if st := getMyQuota(t, srv, tok); st.UsedBytes != 0 {
		t.Errorf("used after rejected upload = %d, want 0", st.UsedBytes)
	}

	// Under the cap → allowed; usage accumulates.
	if rec := uploadVideoFile(srv, id, "clip.mp4", "video/mp4", "12345", tok); rec.Code != http.StatusCreated {
		t.Fatalf("under-quota upload = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	// A second video that would push past the cap (5 used + 6 > 10) → 422.
	id2 := createVideo(t, srv, tok, "ada", `{"title":"Second"}`)
	if rec := uploadVideoFile(srv, id2, "clip.mp4", "video/mp4", "123456", tok); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("cumulative over-quota upload = %d, want 422", rec.Code)
	}
	// Exactly filling the cap (5 used + 5 = 10) is allowed.
	if rec := uploadVideoFile(srv, id2, "clip.mp4", "video/mp4", "54321", tok); rec.Code != http.StatusCreated {
		t.Errorf("exactly-at-quota upload = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if st := getMyQuota(t, srv, tok); st.UsedBytes != 10 {
		t.Errorf("used = %d, want 10", st.UsedBytes)
	}
}

func TestAdminSetsAndResetsUserQuota(t *testing.T) {
	srv := videoServer(t) // instance unlimited; only the override applies
	adminTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	bobTok := createChannelFor(t, srv, "bob", "bob@example.test", "bob")

	all := adminUsers(t, srv, "", adminTok)
	bobID := userIDByName(t, all.Users, "bob")

	// Set a 4-byte override on bob.
	rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/admin/users/"+bobID, `{"storage_quota_bytes":4}`, adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("set quota = %d; body=%s", rec.Code, rec.Body.String())
	}
	var view adminUserView
	_ = json.Unmarshal(rec.Body.Bytes(), &view)
	if view.StorageQuotaBytes == nil || *view.StorageQuotaBytes != 4 {
		t.Fatalf("storage_quota_bytes = %v, want 4", view.StorageQuotaBytes)
	}

	// Bob sees the override; a 5-byte upload is refused.
	if st := getMyQuota(t, srv, bobTok); st.QuotaBytes == nil || *st.QuotaBytes != 4 {
		t.Errorf("bob quota = %+v, want 4", st)
	}
	id := createVideo(t, srv, bobTok, "bob", `{"title":"Clip"}`)
	rec = uploadVideoFile(srv, id, "clip.mp4", "video/mp4", "12345", bobTok)
	if rec.Code != http.StatusUnprocessableEntity || errorCode(t, rec) != "quota_exceeded" {
		t.Fatalf("over-override upload = %d (%s), want 422 quota_exceeded", rec.Code, rec.Body.String())
	}

	// Reset to the instance default (null) → unlimited again; the upload passes
	// and the admin view now reflects usage.
	rec = sendJSONAuth(srv, http.MethodPatch, "/api/v1/admin/users/"+bobID, `{"storage_quota_bytes":null}`, adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("reset quota = %d; body=%s", rec.Code, rec.Body.String())
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &view)
	if view.StorageQuotaBytes != nil {
		t.Fatalf("storage_quota_bytes after reset = %d, want null", *view.StorageQuotaBytes)
	}
	if rec := uploadVideoFile(srv, id, "clip.mp4", "video/mp4", "12345", bobTok); rec.Code != http.StatusCreated {
		t.Fatalf("upload after reset = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if bob := adminUsers(t, srv, "?q=bob", adminTok); len(bob.Users) != 1 || bob.Users[0].StorageUsedBytes != 5 {
		t.Errorf("admin list usage = %+v, want storage_used_bytes=5", bob.Users)
	}

	// Validation: negative and non-integer values are 422; a quota-only PATCH
	// body is accepted (it counts as a provided field).
	for _, body := range []string{`{"storage_quota_bytes":-1}`, `{"storage_quota_bytes":"lots"}`} {
		if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/admin/users/"+bobID, body, adminTok); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("PATCH %s = %d, want 422", body, rec.Code)
		}
	}

	// A non-admin cannot set quotas.
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/admin/users/"+bobID, `{"storage_quota_bytes":4}`, bobTok); rec.Code != http.StatusForbidden {
		t.Errorf("non-admin set quota = %d, want 403", rec.Code)
	}
}

// quotaImportServer serves fixtures for the import-quota tests: a body with a
// declared Content-Length and a chunked body with no Content-Length at all
// (forcing the streaming cap to do the enforcement).
func quotaImportServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sized.mp4":
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write(make([]byte, 64)) // Content-Length: 64
		case "/chunked.mp4":
			w.Header().Set("Content-Type", "video/mp4")
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush() // commit headers before the body → chunked, no Content-Length
			_, _ = w.Write(make([]byte, 64))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestImportBlockedWhenOverQuota(t *testing.T) {
	srv := videoServerCfg(t, quotaCfg(10))
	srv.importClient = &http.Client{} // reach loopback; prod uses urlsafety.NewClient
	media := quotaImportServer(t)

	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")

	// Declared Content-Length over the headroom → 422 before storing.
	id := createVideo(t, srv, tok, "ada", `{"title":"Sized"}`)
	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+id+"/import",
		`{"url":"`+loopbackAsLocalhost(media.URL)+`/sized.mp4"}`, tok)
	if rec.Code != http.StatusUnprocessableEntity || errorCode(t, rec) != "quota_exceeded" {
		t.Fatalf("sized import = %d (%s), want 422 quota_exceeded", rec.Code, rec.Body.String())
	}

	// No Content-Length (chunked): the streaming hard cap rejects mid-transfer.
	rec = sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+id+"/import",
		`{"url":"`+loopbackAsLocalhost(media.URL)+`/chunked.mp4"}`, tok)
	if rec.Code != http.StatusUnprocessableEntity || errorCode(t, rec) != "quota_exceeded" {
		t.Fatalf("chunked import = %d (%s), want 422 quota_exceeded", rec.Code, rec.Body.String())
	}
	if st := getMyQuota(t, srv, tok); st.UsedBytes != 0 {
		t.Errorf("used after rejected imports = %d, want 0", st.UsedBytes)
	}
}

func TestImportAllowedWhenUnlimited(t *testing.T) {
	srv := videoServer(t) // unlimited
	srv.importClient = &http.Client{}
	media := quotaImportServer(t)

	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"Free"}`)
	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+id+"/import",
		`{"url":"`+loopbackAsLocalhost(media.URL)+`/chunked.mp4"}`, tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("unlimited chunked import = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if st := getMyQuota(t, srv, tok); st.UsedBytes != 64 {
		t.Errorf("used = %d, want 64", st.UsedBytes)
	}
}
