package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	gommonbytes "github.com/labstack/gommon/bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/config"
	"github.com/vidra/vidra-core/internal/instancesettings"
	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
	"github.com/vidra/vidra-core/internal/transcode"
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

		DefaultUserQuotaBytes:          cfg.InstanceDefaultQuotaBytes,
		UploadMaxSizeBytes:             uploadMaxSizeBytes(cfg),
		UploadMaxActiveSessionsPerUser: int64(cfg.UploadMaxActiveSessionsPerUser),
		ImportMaxHeight:                int64(cfg.YtdlpMaxHeight),

		ChannelSyncEnabled:    cfg.ChannelSyncEnabled,
		ChannelSyncMaxPerUser: int64(cfg.ChannelSyncMaxPerUser),
		TranscriptionEnabled:  cfg.WhisperEnabled,

		TranscodingEnabled: cfg.TranscodingEnabled,
	}
}

// uploadMaxSizeBytes mirrors cmd/api's parse of the boot-validated UPLOAD_MAX_SIZE.
func uploadMaxSizeBytes(cfg *config.Config) int64 {
	n, _ := gommonbytes.Parse(cfg.UploadMaxSize)
	return n
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

func publicInstance(t *testing.T, srv *Server) instanceResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/instance", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /instance = %d; body=%s", rec.Code, rec.Body.String())
	}
	var body instanceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal instance: %v", err)
	}
	return body
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
	// 13 feature/base keys + 21 platform-information keys + 4 operational limits
	// + 19 core-config-surface keys (config-parity W1) + 10 shipped-feature
	// toggles (config-parity W8) + 8 VOD transcoding/worker-pool knobs
	// (config-parity W10) + 5 live enforcement knobs (config-parity W11)
	// + 4 federation policy gates (config-parity W12) + 2 remote-URI search
	// gates (config-parity W13).
	got := instanceSettings(t, srv, adminTok)
	if len(got.Settings) != 86 {
		t.Fatalf("settings count = %d, want 86", len(got.Settings))
	}
	nameView := settingView(t, got, instancesettings.KeyInstanceName)
	if nameView.Value != "Vidra Test" || nameView.Overridden {
		t.Errorf("instance_name default view = %+v, want value=Vidra Test overridden=false", nameView)
	}
	// Every setting carries its admin-IA placement metadata (W1).
	for _, v := range got.Settings {
		if v.Page == "" || v.Section == "" {
			t.Errorf("%s: page/section metadata missing (page=%q section=%q)", v.Key, v.Page, v.Section)
		}
	}
	if bc := settingView(t, got, instancesettings.KeyBroadcastLevel); bc.Page != "general" || bc.Section != "broadcast" ||
		bc.Type != "enum" || bc.Value != "info" || !reflect.DeepEqual(bc.Options, instancesettings.BroadcastLevelOptions) {
		t.Errorf("broadcast_level view = %+v, want general/broadcast enum info", bc)
	}
	if up := settingView(t, got, instancesettings.KeyUploadsEnabled); up.Page != "vod" || up.Section != "uploads" {
		t.Errorf("uploads_enabled placed at %s/%s, want vod/uploads", up.Page, up.Section)
	}
	if up := settingView(t, got, instancesettings.KeyUploadsEnabled); up.Value != true || up.Type != "bool" {
		t.Errorf("uploads_enabled default view = %+v, want value=true type=bool", up)
	}
	if dl := settingView(t, got, instancesettings.KeyDownloadsEnabled); dl.Value != true || dl.Default != true || dl.Type != "bool" {
		t.Errorf("downloads_enabled default view = %+v, want value/default=true type=bool", dl)
	}
	if policy := settingView(t, got, instancesettings.KeySensitiveContentPolicy); policy.Value != "hide" ||
		policy.Type != "enum" || !reflect.DeepEqual(policy.Options, instancesettings.SensitiveContentPolicyOptions) {
		t.Errorf("sensitive_content_policy default view = %+v, want enum hide with options", policy)
	}
	if cats := settingView(t, got, instancesettings.KeyInstanceCategories); cats.Type != "list" || len(stringSliceValue(t, cats.Value)) != 0 {
		t.Errorf("instance_categories default view = %+v, want empty list", cats)
	}

	// Override a string + a bool + a URL.
	rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/admin/instance-settings",
		`{"instance_name":"Renamed","uploads_enabled":false,"downloads_enabled":false,"terms_url":"https://x.test/terms"}`, adminTok)
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
	if v := settingView(t, after, instancesettings.KeyDownloadsEnabled); v.Value != false || !v.Overridden {
		t.Errorf("downloads_enabled after patch = %+v, want value=false overridden=true", v)
	}

	// The public GET /instance reflects the reloaded overlay.
	if n := instanceName(t, srv); n != "Renamed" {
		t.Errorf("GET /instance name = %q, want Renamed", n)
	}
	if publicInstance(t, srv).Features.Downloads {
		t.Error("GET /instance features.downloads = true after disabling downloads")
	}

	// Audit: admin.instance.update success, reason names the changed keys only
	// (sorted), never the values.
	reason := latestInstanceUpdateReason(t, &buf)
	if reason != "keys=downloads_enabled,instance_name,terms_url,uploads_enabled" {
		t.Errorf("audit reason = %q, want keys=downloads_enabled,instance_name,terms_url,uploads_enabled", reason)
	}
	if strings.Contains(reason, "Renamed") || strings.Contains(reason, "x.test") {
		t.Errorf("audit reason leaked a value: %q", reason)
	}

	// Enum + list values round-trip as JSON strings/arrays, not stringified JSON.
	rec = sendJSONAuth(srv, http.MethodPatch, "/api/v1/admin/instance-settings",
		`{"sensitive_content_policy":"blur","default_language":"fr","instance_categories":["1","7"],"moderator_languages":["en","es"]}`, adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch enum/list = %d; body=%s", rec.Code, rec.Body.String())
	}
	enumList := instanceSettings(t, srv, adminTok)
	if v := settingView(t, enumList, instancesettings.KeySensitiveContentPolicy); v.Value != "blur" || v.Type != "enum" {
		t.Errorf("sensitive_content_policy after patch = %+v, want blur enum", v)
	}
	if v := settingView(t, enumList, instancesettings.KeyDefaultLanguage); v.Value != "fr" {
		t.Errorf("default_language after patch = %+v, want fr", v)
	}
	if v := settingView(t, enumList, instancesettings.KeyInstanceCategories); !reflect.DeepEqual(stringSliceValue(t, v.Value), []string{"1", "7"}) {
		t.Errorf("instance_categories after patch = %+v, want [1 7]", v)
	}
	if v := settingView(t, enumList, instancesettings.KeyModeratorLanguages); !reflect.DeepEqual(stringSliceValue(t, v.Value), []string{"en", "es"}) {
		t.Errorf("moderator_languages after patch = %+v, want [en es]", v)
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
		{"bad enum", `{"sensitive_content_policy":"surprise"}`},
		{"list wrong type", `{"instance_categories":"1"}`},
		{"bad category id", `{"instance_categories":["999"]}`},
		{"bad language id", `{"moderator_languages":["zz-nope"]}`},
		{"int wrong type (string)", `{"upload_max_active_sessions_per_user":"3"}`},
		{"int wrong type (float)", `{"upload_max_active_sessions_per_user":1.5}`},
		{"int negative quota", `{"default_user_quota_bytes":-1}`},
		{"int over range", `{"upload_max_active_sessions_per_user":10001}`},
		{"int below floor", `{"upload_max_size_bytes":1024}`},
		{"empty body", `{}`},
	}
	for _, tc := range badCases {
		if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/admin/instance-settings", tc.body, adminTok); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: PATCH = %d, want 422", tc.name, rec.Code)
		}
	}
}

func stringSliceValue(t *testing.T, v any) []string {
	t.Helper()
	raw, ok := v.([]any)
	if !ok {
		t.Fatalf("value %#v has type %T, want []any from JSON", v, v)
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		s, ok := item.(string)
		if !ok {
			t.Fatalf("list item %#v has type %T, want string", item, item)
		}
		out = append(out, s)
	}
	return out
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

// setInt overrides an int-kind setting through the admin PATCH endpoint.
func setInt(t *testing.T, srv *Server, adminTok, key string, v int64) {
	t.Helper()
	body := `{"` + key + `":` + strconv.FormatInt(v, 10) + `}`
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/admin/instance-settings", body, adminTok); rec.Code != http.StatusOK {
		t.Fatalf("set %s=%d = %d; body=%s", key, v, rec.Code, rec.Body.String())
	}
}

// TestInstanceSettingsIntKind covers the int-kind admin contract: the GET shape
// (type=int, numeric value/default), a numeric PATCH round-trip, and null reset.
func TestInstanceSettingsIntKind(t *testing.T) {
	srv := videoServer(t)
	adminTok := createChannelFor(t, srv, "ida", "ida@example.test", "ida")

	v := settingView(t, instanceSettings(t, srv, adminTok), instancesettings.KeyDefaultUserQuotaBytes)
	if v.Type != "int" {
		t.Fatalf("type = %q, want int", v.Type)
	}
	if _, ok := v.Value.(float64); !ok { // JSON numbers decode to float64
		t.Errorf("value = %v (%T), want a JSON number", v.Value, v.Value)
	}
	if v.Overridden {
		t.Error("fresh int setting overridden = true, want false")
	}

	setInt(t, srv, adminTok, instancesettings.KeyDefaultUserQuotaBytes, 5_000_000)
	after := settingView(t, instanceSettings(t, srv, adminTok), instancesettings.KeyDefaultUserQuotaBytes)
	if after.Value.(float64) != 5_000_000 || !after.Overridden {
		t.Errorf("after patch = %+v, want value=5000000 overridden=true", after)
	}

	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/admin/instance-settings",
		`{"default_user_quota_bytes":null}`, adminTok); rec.Code != http.StatusOK {
		t.Fatalf("reset = %d; body=%s", rec.Code, rec.Body.String())
	}
	if settingView(t, instanceSettings(t, srv, adminTok), instancesettings.KeyDefaultUserQuotaBytes).Overridden {
		t.Error("after reset overridden = true, want false")
	}
}

// TestUploadSizeLimitOverlay proves the upload-size cap follows the overlay at
// runtime: the tiny testConfig default (64K) rejects a 100 KB session, and
// raising the overlay lets it through with no restart.
func TestUploadSizeLimitOverlay(t *testing.T) {
	srv := videoServer(t)
	adminTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, adminTok, "ada", `{"title":"Clip","privacy":"public"}`)

	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+id+"/upload-session",
		`{"size":100000,"filename":"clip.mp4"}`, adminTok); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize vs default cap = %d, want 413; body=%s", rec.Code, rec.Body.String())
	}
	setInt(t, srv, adminTok, instancesettings.KeyUploadMaxSizeBytes, 3<<20) // 3 MiB
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+id+"/upload-session",
		`{"size":100000,"filename":"clip.mp4"}`, adminTok); rec.Code != http.StatusCreated {
		t.Fatalf("session after raising cap = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
}

// TestUploadSessionsLimitOverlay proves the per-user active-session cap follows
// the overlay at runtime: lowering it to 1 makes a second concurrent session 429,
// and lifting it (0 = unlimited) restores it — no restart.
func TestUploadSessionsLimitOverlay(t *testing.T) {
	srv := videoServer(t)
	adminTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, adminTok, "ada", `{"title":"Clip","privacy":"public"}`)

	setInt(t, srv, adminTok, instancesettings.KeyUploadMaxActiveSessionsPerUser, 1)
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+id+"/upload-session",
		`{"size":40,"filename":"a.mp4"}`, adminTok); rec.Code != http.StatusCreated {
		t.Fatalf("first session = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+id+"/upload-session",
		`{"size":40,"filename":"b.mp4"}`, adminTok)
	if rec.Code != http.StatusTooManyRequests || errorCode(t, rec) != "too_many_active_uploads" {
		t.Fatalf("second session over cap = %d code=%q, want 429 too_many_active_uploads", rec.Code, errorCode(t, rec))
	}
	setInt(t, srv, adminTok, instancesettings.KeyUploadMaxActiveSessionsPerUser, 0) // unlimited
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+id+"/upload-session",
		`{"size":40,"filename":"b.mp4"}`, adminTok); rec.Code != http.StatusCreated {
		t.Fatalf("session after lifting cap = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
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

// ---- shipped-feature toggle batch gates (config-parity W8) -----------------

// TestImportHTTPToggleGate: import_http_enabled governs the yt-dlp platform
// path SPECIFICALLY — setting off → an explicit resolver=ytdlp is 403
// feature_disabled (before any enqueue) while direct imports keep working
// under the imports_enabled master; setting on with no extractor wired keeps
// the boot-capability 503.
func TestImportHTTPToggleGate(t *testing.T) {
	srv := videoServer(t) // no yt-dlp extractor wired in this harness
	adminTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, adminTok, "ada", `{"title":"Clip","privacy":"public"}`)

	// Setting ON (default) but extractor unwired: boot 503 preserved.
	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+id+"/import",
		`{"url":"https://platform.example/watch?v=1","resolver":"ytdlp"}`, adminTok)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unwired ytdlp = %d, want 503; body=%s", rec.Code, rec.Body.String())
	}

	// Setting OFF: 403 feature_disabled, the uniform W8 idiom.
	setToggle(t, srv, adminTok, instancesettings.KeyImportHTTPEnabled, false)
	rec = sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+id+"/import",
		`{"url":"https://platform.example/watch?v=1","resolver":"ytdlp"}`, adminTok)
	if rec.Code != http.StatusForbidden || errorCode(t, rec) != "feature_disabled" {
		t.Errorf("gated ytdlp = %d code=%q, want 403 feature_disabled", rec.Code, errorCode(t, rec))
	}
	// The master imports switch still admits direct/auto URL imports.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+id+"/import",
		`{"url":"https://example.com/v.mp4"}`, adminTok); rec.Code != http.StatusAccepted {
		t.Errorf("direct import with import_http off = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
}

// TestMaxChannelsPerUserGate: max_channels_per_user counts-and-refuses at
// channel creation (422), 0 restores unlimited, and other users' counts are
// independent.
func TestMaxChannelsPerUserGate(t *testing.T) {
	srv := videoServer(t)
	adminTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada") // ada now has 1 channel

	setInt(t, srv, adminTok, instancesettings.KeyMaxChannelsPerUser, 2)
	if rec := postJSONAuth(srv, "/api/v1/channels", `{"handle":"second","display_name":"Second"}`, adminTok); rec.Code != http.StatusCreated {
		t.Fatalf("second channel = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	rec := postJSONAuth(srv, "/api/v1/channels", `{"handle":"third","display_name":"Third"}`, adminTok)
	if rec.Code != http.StatusUnprocessableEntity || errorCode(t, rec) != "unprocessable_entity" {
		t.Errorf("at-cap channel = %d code=%q, want 422 unprocessable_entity", rec.Code, errorCode(t, rec))
	}
	// A different user starts at zero and may still create.
	bobTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	if rec := postJSONAuth(srv, "/api/v1/channels", `{"handle":"bobs","display_name":"Bobs"}`, bobTok); rec.Code != http.StatusCreated {
		t.Errorf("other user channel = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	// 0 = unlimited again.
	setInt(t, srv, adminTok, instancesettings.KeyMaxChannelsPerUser, 0)
	if rec := postJSONAuth(srv, "/api/v1/channels", `{"handle":"third","display_name":"Third"}`, adminTok); rec.Code != http.StatusCreated {
		t.Errorf("unlimited channel = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
}

// TestInstanceFeaturesW8Flags: GET /instance features carries the W8 flags,
// tracking the settings (and reporting false where this harness lacks the boot
// capability — no yt-dlp, no Whisper, no channel-sync service).
func TestInstanceFeaturesW8Flags(t *testing.T) {
	srv := videoServer(t)
	adminTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")

	features := func() map[string]bool {
		t.Helper()
		rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/instance", "", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /instance = %d", rec.Code)
		}
		var doc struct {
			Features map[string]bool `json:"features"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return doc.Features
	}

	got := features()
	for flag, want := range map[string]bool{
		"import_http":   false, // setting on, but no yt-dlp extractor wired
		"channel_sync":  false, // no channelsync service in this harness
		"storyboards":   true,
		"transcription": false, // WhisperEnabled=false
		"user_import":   true,
		"user_export":   true,
	} {
		if got[flag] != want {
			t.Errorf("features.%s = %v, want %v", flag, got[flag], want)
		}
	}

	// Flags follow the runtime settings.
	setToggle(t, srv, adminTok, instancesettings.KeyStoryboardsEnabled, false)
	setToggle(t, srv, adminTok, instancesettings.KeyUserExportEnabled, false)
	got = features()
	if got["storyboards"] || got["user_export"] {
		t.Errorf("flags after toggles = storyboards:%v user_export:%v, want false/false", got["storyboards"], got["user_export"])
	}
}

// --- config-parity W10: VOD transcoding knobs & upload container policy ---

// TestUploadAdditionalExtensionsGate: upload_additional_extensions_enabled
// governs the EXTENDED container set at session create (the same gate
// AttachOriginal enforces at the service seam); the base set is unaffected,
// and a runtime flip needs no restart.
func TestUploadAdditionalExtensionsGate(t *testing.T) {
	srv := videoServer(t)
	adminTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, adminTok, "ada", `{"title":"Clip","privacy":"public"}`)

	session := func(filename string) int {
		rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+id+"/upload-session",
			`{"size":40,"filename":"`+filename+`"}`, adminTok)
		return rec.Code
	}

	// Default ON (deviation from PT's off default: the shipped allow-list
	// already included the extended set): .avi opens a session.
	if code := session("clip.avi"); code != http.StatusCreated {
		t.Fatalf("session .avi default = %d, want 201", code)
	}

	// OFF: extended containers are 415; base containers still work.
	setToggle(t, srv, adminTok, instancesettings.KeyUploadAdditionalExtensionsEnabled, false)
	for _, f := range []string{"clip.avi", "clip.mkv", "clip.mov", "clip.ts"} {
		if code := session(f); code != http.StatusUnsupportedMediaType {
			t.Errorf("session %s with additional extensions off = %d, want 415", f, code)
		}
	}
	for _, f := range []string{"clip.mp4", "clip.webm", "clip.ogv"} {
		if code := session(f); code != http.StatusCreated {
			t.Errorf("session %s with additional extensions off = %d, want 201 (base set)", f, code)
		}
	}

	// Back ON without a restart.
	setToggle(t, srv, adminTok, instancesettings.KeyUploadAdditionalExtensionsEnabled, true)
	if code := session("clip.avi"); code != http.StatusCreated {
		t.Errorf("session .avi re-enabled = %d, want 201", code)
	}
}

// capableFakeTranscoder makes a transcode.Service report Capable() (the boot
// half of the effective transcoding flag). Never invoked by these tests.
type capableFakeTranscoder struct{}

func (capableFakeTranscoder) Transcode(context.Context, uuid.UUID, string) (media.HLSResult, error) {
	return media.HLSResult{}, nil
}

// TestInstanceFeaturesTranscodingFlag: features.transcoding is the EFFECTIVE
// availability — the runtime transcoding_enabled setting AND the boot
// capability (a wired transcoder).
func TestInstanceFeaturesTranscodingFlag(t *testing.T) {
	cfg := testConfig()
	cfg.TranscodingEnabled = true

	// Boot-capable deployment: flag follows the runtime setting.
	srv, _, _, _, _ := videoServerFullWith(t, cfg, []Option{
		WithTranscodeService(transcode.NewService(newTranscodeFakeRepo(), capableFakeTranscoder{})),
	})
	adminTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	if got := publicInstance(t, srv); !got.Features.Transcoding {
		t.Fatalf("features.transcoding = false, want true (setting on + capable)")
	}
	setToggle(t, srv, adminTok, instancesettings.KeyTranscodingEnabled, false)
	if got := publicInstance(t, srv); got.Features.Transcoding {
		t.Errorf("features.transcoding after runtime off = true, want false")
	}
	setToggle(t, srv, adminTok, instancesettings.KeyTranscodingEnabled, true)
	if got := publicInstance(t, srv); !got.Features.Transcoding {
		t.Errorf("features.transcoding after re-enable = false, want true (no restart needed)")
	}

	// No boot capability (nil transcoder): the setting can't conjure ffmpeg.
	bare := videoServerCfg(t, cfg)
	if got := publicInstance(t, bare); got.Features.Transcoding {
		t.Errorf("features.transcoding without a capable transcoder = true, want false")
	}
}
