package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/config"
	"github.com/vidra/vidra-core/internal/instancesettings"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// instanceSettingsFakeRepo is an in-memory instance_settings store for handler
// tests.
type instanceSettingsFakeRepo struct {
	rows map[string]sqlcgen.InstanceSetting
}

func newInstanceSettingsFakeRepo() *instanceSettingsFakeRepo {
	return &instanceSettingsFakeRepo{rows: map[string]sqlcgen.InstanceSetting{}}
}

func (f *instanceSettingsFakeRepo) ListInstanceSettings(_ context.Context) ([]sqlcgen.InstanceSetting, error) {
	out := make([]sqlcgen.InstanceSetting, 0, len(f.rows))
	for _, r := range f.rows {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func (f *instanceSettingsFakeRepo) UpsertInstanceSetting(_ context.Context, arg sqlcgen.UpsertInstanceSettingParams) error {
	f.rows[arg.Key] = sqlcgen.InstanceSetting{Key: arg.Key, Value: arg.Value, UpdatedBy: arg.UpdatedBy, UpdatedAt: time.Now()}
	return nil
}

func (f *instanceSettingsFakeRepo) DeleteInstanceSetting(_ context.Context, key string) (int64, error) {
	if _, ok := f.rows[key]; ok {
		delete(f.rows, key)
		return 1, nil
	}
	return 0, nil
}

// settingsDefaultsFromConfig mirrors the cmd/api mapping so the test overlay's
// defaults match the running config.
func settingsDefaultsFromConfig(cfg *config.Config) instancesettings.Defaults {
	return instancesettings.Defaults{
		InstanceName:                cfg.InstanceName,
		InstanceDescription:         cfg.InstanceDescription,
		TermsURL:                    cfg.InstanceTermsURL,
		PrivacyURL:                  cfg.InstancePrivacyURL,
		ContactEmail:                cfg.InstanceContactEmail,
		RegistrationEnabled:         cfg.RegistrationEnabled,
		RegistrationRequireApproval: cfg.RegistrationRequireApproval,
		QuarantineNewUploads:        cfg.QuarantineNewUploads,
		UploadsEnabled:              cfg.UploadsEnabled,
		ImportsEnabled:              cfg.ImportsEnabled,
		LiveEnabled:                 cfg.LiveEnabled,
		CommentsEnabled:             cfg.CommentsEnabled,
	}
}

func instanceSettings(t *testing.T, srv *Server, token string) instanceSettingsResponse {
	t.Helper()
	rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/admin/instance-settings", "", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("get instance-settings = %d; body=%s", rec.Code, rec.Body.String())
	}
	var body instanceSettingsResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return body
}

func settingView(t *testing.T, resp instanceSettingsResponse, key string) instanceSettingView {
	t.Helper()
	for _, v := range resp.Settings {
		if v.Key == key {
			return v
		}
	}
	t.Fatalf("setting %q not present in %d settings", key, len(resp.Settings))
	return instanceSettingView{}
}

func instanceName(t *testing.T, srv *Server) string {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/instance", nil))
	var body instanceResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return body.Name
}

// TestInstanceSettingsAdminFlow covers the admin GET/PATCH overlay: defaults,
// override + reload reflected in GET /instance, the audit event, null-resets,
// per-key validation, and the non-admin/anon guards.
func TestInstanceSettingsAdminFlow(t *testing.T) {
	srv := videoServer(t)
	var buf bytes.Buffer
	srv.logger = slog.New(slog.NewJSONHandler(&buf, nil))

	adminTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	bobTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	// Guards: anon 401, non-admin 403 on both verbs.
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/admin/instance-settings", "", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anon GET = %d, want 401", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/admin/instance-settings", "", bobTok); rec.Code != http.StatusForbidden {
		t.Errorf("non-admin GET = %d, want 403", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/admin/instance-settings", `{"uploads_enabled":false}`, bobTok); rec.Code != http.StatusForbidden {
		t.Errorf("non-admin PATCH = %d, want 403", rec.Code)
	}

	// Default state: instance_name is the config value and nothing is overridden.
	got := instanceSettings(t, srv, adminTok)
	if len(got.Settings) != 12 {
		t.Fatalf("settings count = %d, want 12", len(got.Settings))
	}
	nameView := settingView(t, got, instancesettings.KeyInstanceName)
	if nameView.Value != "Vidra Test" || nameView.Overridden {
		t.Errorf("instance_name default view = %+v, want value=Vidra Test overridden=false", nameView)
	}
	if up := settingView(t, got, instancesettings.KeyUploadsEnabled); up.Value != true || up.Type != "bool" {
		t.Errorf("uploads_enabled default view = %+v, want value=true type=bool", up)
	}

	// Override a string + a bool + a URL.
	rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/admin/instance-settings",
		`{"instance_name":"Renamed","uploads_enabled":false,"terms_url":"https://x.test/terms"}`, adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch = %d; body=%s", rec.Code, rec.Body.String())
	}
	after := instanceSettings(t, srv, adminTok)
	if v := settingView(t, after, instancesettings.KeyInstanceName); v.Value != "Renamed" || !v.Overridden || v.Default != "Vidra Test" {
		t.Errorf("instance_name after patch = %+v, want value=Renamed overridden=true default=Vidra Test", v)
	}
	if v := settingView(t, after, instancesettings.KeyUploadsEnabled); v.Value != false || !v.Overridden {
		t.Errorf("uploads_enabled after patch = %+v, want value=false overridden=true", v)
	}

	// The public GET /instance reflects the reloaded overlay.
	if n := instanceName(t, srv); n != "Renamed" {
		t.Errorf("GET /instance name = %q, want Renamed", n)
	}

	// Audit: admin.instance.update success, reason names the changed keys only
	// (sorted), never the values.
	reason := latestInstanceUpdateReason(t, &buf)
	if reason != "keys=instance_name,terms_url,uploads_enabled" {
		t.Errorf("audit reason = %q, want keys=instance_name,terms_url,uploads_enabled", reason)
	}
	if strings.Contains(reason, "Renamed") || strings.Contains(reason, "x.test") {
		t.Errorf("audit reason leaked a value: %q", reason)
	}

	// null resets an override back to the config default.
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/admin/instance-settings", `{"instance_name":null}`, adminTok); rec.Code != http.StatusOK {
		t.Fatalf("null reset = %d; body=%s", rec.Code, rec.Body.String())
	}
	if n := instanceName(t, srv); n != "Vidra Test" {
		t.Errorf("after null-reset name = %q, want Vidra Test", n)
	}
	if v := settingView(t, instanceSettings(t, srv, adminTok), instancesettings.KeyInstanceName); v.Overridden {
		t.Error("instance_name still overridden after null-reset")
	}

	// Validation: unknown key, wrong type, malformed URL, empty name, empty body.
	badCases := []struct {
		name, body string
	}{
		{"unknown key", `{"nope":true}`},
		{"wrong type", `{"uploads_enabled":"yes"}`},
		{"bad url", `{"terms_url":"not a url"}`},
		{"empty name", `{"instance_name":"   "}`},
		{"empty body", `{}`},
	}
	for _, tc := range badCases {
		if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/admin/instance-settings", tc.body, adminTok); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: PATCH = %d, want 422", tc.name, rec.Code)
		}
	}
}

// latestInstanceUpdateReason returns the reason of the most recent successful
// admin.instance.update audit event in buf.
func latestInstanceUpdateReason(t *testing.T, buf *bytes.Buffer) string {
	t.Helper()
	reason := ""
	for _, e := range auditEvents(t, buf) {
		if e["action"] == observability.ActionAdminInstanceUpdate && e["result"] == observability.ResultSuccess {
			reason, _ = e["reason"].(string)
		}
	}
	return reason
}

// setToggle flips a bool setting through the admin PATCH endpoint (exercising the
// end-to-end validate → persist → reload path).
func setToggle(t *testing.T, srv *Server, adminTok, key string, on bool) {
	t.Helper()
	body := `{"` + key + `":` + boolStr(on) + `}`
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/admin/instance-settings", body, adminTok); rec.Code != http.StatusOK {
		t.Fatalf("set %s=%v = %d; body=%s", key, on, rec.Code, rec.Body.String())
	}
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// TestUploadsToggleGate proves uploads_enabled gates BOTH the resumable
// upload-session open and the direct file upload.
func TestUploadsToggleGate(t *testing.T) {
	srv := videoServer(t)
	adminTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, adminTok, "ada", `{"title":"Clip","privacy":"public"}`)

	// ON (default): opening a session succeeds.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+id+"/upload-session", `{"size":40,"filename":"clip.mp4"}`, adminTok); rec.Code != http.StatusCreated {
		t.Fatalf("session ON = %d; body=%s", rec.Code, rec.Body.String())
	}

	// OFF: both upload entrypoints return 403 feature_disabled.
	setToggle(t, srv, adminTok, instancesettings.KeyUploadsEnabled, false)
	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+id+"/upload-session", `{"size":40,"filename":"clip.mp4"}`, adminTok)
	if rec.Code != http.StatusForbidden || errorCode(t, rec) != "feature_disabled" {
		t.Errorf("session OFF = %d code=%q, want 403 feature_disabled", rec.Code, errorCode(t, rec))
	}
	rec = uploadVideoFile(srv, id, "clip.mp4", "video/mp4", "tiny", adminTok)
	if rec.Code != http.StatusForbidden || errorCode(t, rec) != "feature_disabled" {
		t.Errorf("direct upload OFF = %d code=%q, want 403 feature_disabled", rec.Code, errorCode(t, rec))
	}

	// Re-enabling restores it.
	setToggle(t, srv, adminTok, instancesettings.KeyUploadsEnabled, true)
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+id+"/upload-session", `{"size":40,"filename":"clip.mp4"}`, adminTok); rec.Code != http.StatusCreated {
		t.Errorf("session re-enabled = %d; body=%s", rec.Code, rec.Body.String())
	}
}

func TestImportsToggleGate(t *testing.T) {
	srv := videoServer(t)
	adminTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, adminTok, "ada", `{"title":"Clip","privacy":"public"}`)

	// ON (default): a well-formed public URL enqueues an import (202).
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+id+"/import", `{"url":"https://example.com/v.mp4"}`, adminTok); rec.Code != http.StatusAccepted {
		t.Fatalf("import ON = %d; body=%s", rec.Code, rec.Body.String())
	}

	// OFF: 403 feature_disabled (gate fires before URL validation).
	setToggle(t, srv, adminTok, instancesettings.KeyImportsEnabled, false)
	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+id+"/import", `{"url":"https://example.com/v.mp4"}`, adminTok)
	if rec.Code != http.StatusForbidden || errorCode(t, rec) != "feature_disabled" {
		t.Errorf("import OFF = %d code=%q, want 403 feature_disabled", rec.Code, errorCode(t, rec))
	}
}

func TestLiveToggleGate(t *testing.T) {
	srv := videoServer(t)
	adminTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")

	// ON (default): create live stream succeeds.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/channels/ada/live", `{"title":"Stream"}`, adminTok); rec.Code != http.StatusCreated {
		t.Fatalf("live ON = %d; body=%s", rec.Code, rec.Body.String())
	}

	// OFF: 403 feature_disabled.
	setToggle(t, srv, adminTok, instancesettings.KeyLiveEnabled, false)
	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/channels/ada/live", `{"title":"Stream"}`, adminTok)
	if rec.Code != http.StatusForbidden || errorCode(t, rec) != "feature_disabled" {
		t.Errorf("live OFF = %d code=%q, want 403 feature_disabled", rec.Code, errorCode(t, rec))
	}
}

func TestCommentsToggleGate(t *testing.T) {
	srv := videoServer(t)
	adminTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPublishedVideo(t, srv, adminTok, "ada", `{"title":"Clip","privacy":"public"}`)

	// ON (default): posting a comment succeeds.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+id+"/comments", `{"body":"nice"}`, adminTok); rec.Code != http.StatusCreated {
		t.Fatalf("comment ON = %d; body=%s", rec.Code, rec.Body.String())
	}

	// OFF: 403 feature_disabled; reading comments still works.
	setToggle(t, srv, adminTok, instancesettings.KeyCommentsEnabled, false)
	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+id+"/comments", `{"body":"blocked"}`, adminTok)
	if rec.Code != http.StatusForbidden || errorCode(t, rec) != "feature_disabled" {
		t.Errorf("comment OFF = %d code=%q, want 403 feature_disabled", rec.Code, errorCode(t, rec))
	}
	listRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(listRec, httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+id+"/comments", nil))
	if listRec.Code != http.StatusOK {
		t.Errorf("list comments while disabled = %d, want 200 (reading stays open)", listRec.Code)
	}
}

// TestRegistrationOverlayGate proves the registration gate switches from config
// to the DB overlay: disabling registration_enabled at runtime makes signup 403.
func TestRegistrationOverlayGate(t *testing.T) {
	srv := videoServer(t)
	adminTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")

	// Registration is on by default (testConfig).
	if n := instanceName(t, srv); n == "" {
		t.Fatal("sanity: instance endpoint returned empty name")
	}
	setToggle(t, srv, adminTok, instancesettings.KeyRegistrationEnabled, false)

	rec := postTo(srv, "/api/v1/auth/register", `{"username":"cid","email":"cid@example.test","password":"supersecret"}`)
	if rec.Code != http.StatusForbidden {
		t.Errorf("register with overlay-disabled registration = %d, want 403", rec.Code)
	}
	// GET /instance reflects the overlay.
	var body instanceResponse
	rec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/api/v1/instance", nil))
	_ = json.Unmarshal(rec2.Body.Bytes(), &body)
	if body.RegistrationEnabled {
		t.Error("GET /instance registration_enabled = true, want false after overlay")
	}

	// Re-enable → signup works again.
	setToggle(t, srv, adminTok, instancesettings.KeyRegistrationEnabled, true)
	if rec := postTo(srv, "/api/v1/auth/register", `{"username":"cid","email":"cid@example.test","password":"supersecret"}`); rec.Code != http.StatusCreated {
		t.Errorf("register after re-enable = %d; body=%s", rec.Code, rec.Body.String())
	}
}
