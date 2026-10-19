package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/vidra/vidra-core/internal/instancesettings"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func TestVASTPlayerConfigFailsClosed(t *testing.T) {
	srv := New(testConfig(), nil, nil)
	if ads := publicInstance(t, srv).Ads; ads.Enabled || ads.VASTTagURL != "" {
		t.Fatal("unwired settings must default to no ads")
	}
	repo := newInstanceSettingsFakeRepo()
	repo.rows["player_vast_enabled"] = sqlcgen.InstanceSetting{Key: "player_vast_enabled", Value: "true"}
	repo.rows["player_vast_tag_url"] = sqlcgen.InstanceSetting{Key: "player_vast_tag_url", Value: "https://user:pass@ads.example.test/vast"}
	svc := instancesettings.NewService(repo, instancesettings.Defaults{})
	if err := svc.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	srv = New(testConfig(), nil, nil, WithSettingsService(svc))
	if ads := publicInstance(t, srv).Ads; ads.Enabled || ads.VASTTagURL != "" {
		t.Fatal("invalid stored URL must not activate or reach browser configuration")
	}
}

func TestVASTPlayerConfig(t *testing.T) {
	e := newSettingsAuditEnv(t)
	read := func() (bool, string, string) {
		t.Helper()
		rec := getInstanceRec(e.srv)
		var body struct {
			Ads *struct {
				Enabled bool   `json:"enabled"`
				TagURL  string `json:"vast_tag_url"`
			} `json:"ads"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Ads == nil {
			t.Fatalf("missing public ads configuration: %v", err)
		}
		return body.Ads.Enabled, body.Ads.TagURL, rec.Header().Get("ETag")
	}
	if enabled, tag, _ := read(); enabled || tag != "" {
		t.Fatal("fresh instance must not serve ads")
	}
	member := registerAndToken(t, e.srv, `{"username":"bob","email":"bob@example.test","password":"supersecret123"}`)
	for _, tc := range []struct {
		token  string
		status int
	}{{"", http.StatusUnauthorized}, {member, http.StatusForbidden}} {
		if rec := sendJSONAuth(e.srv, http.MethodPatch, "/api/v1/admin/instance-settings", `{"player_vast_enabled":true}`, tc.token); rec.Code != tc.status {
			t.Fatalf("unauthorized configuration write = %d", rec.Code)
		}
	}
	e.patch(t, `{"player_vast_enabled":true}`)
	if enabled, _, _ := read(); enabled {
		t.Fatal("enabled toggle without a tag must remain inactive")
	}
	_, _, before := read()
	const tag = "https://ads.example.test/vast?campaign=public-canary"
	changes, raw := e.patch(t, `{"player_vast_tag_url":"`+tag+`"}`)
	if enabled, got, after := read(); !enabled || got != tag || after == before {
		t.Fatal("configured preroll must activate and change the public ETag")
	}
	change, ok := changes["setting.player_vast_tag_url"]
	if !ok || change.Before != "" || change.After != "" || strings.Contains(raw+e.logs.String(), tag) {
		t.Fatal("tag URL must not appear in audit values or logs")
	}
	const invalid = `{"player_vast_tag_url":"https://user:pass@ads.example.test/vast"}`
	if rec := sendJSONAuth(e.srv, http.MethodPost, "/api/v1/admin/instance-settings/validate", invalid, e.admin); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "player_vast_tag_url") {
		t.Fatal("dry-run must report the invalid URL")
	}
	if rec := sendJSONAuth(e.srv, http.MethodPatch, "/api/v1/admin/instance-settings", invalid, e.admin); rec.Code != http.StatusUnprocessableEntity {
		t.Fatal("invalid URL write must be rejected")
	}
	if enabled, got, _ := read(); !enabled || got != tag {
		t.Fatal("invalid write changed the configured preroll")
	}
	e.patch(t, `{"player_vast_enabled":false}`)
	if enabled, got, _ := read(); enabled || got != tag {
		t.Fatal("disabling must retain the configured tag")
	}
	e.patch(t, `{"player_vast_tag_url":""}`)
	if _, got, _ := read(); got != "" {
		t.Fatal("empty tag did not clear configuration")
	}
	e.patch(t, `{"player_vast_enabled":null,"player_vast_tag_url":null}`)
	if enabled, got, _ := read(); enabled || got != "" {
		t.Fatal("reset must restore disabled defaults")
	}
}

func TestVASTPlacementConfig(t *testing.T) {
	e := newSettingsAuditEnv(t)
	read := func() map[string]any {
		t.Helper()
		var body struct {
			Ads map[string]any `json:"ads"`
		}
		if err := json.Unmarshal(getInstanceRec(e.srv).Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body.Ads
	}
	if ads := read(); ads["midroll_tag_url"] != "" || ads["postroll_tag_url"] != "" || ads["midroll_offset_seconds"] != float64(300) {
		t.Fatalf("unexpected placement defaults: %+v", ads)
	}
	e.patch(t, `{"player_vast_enabled":true,"player_vast_midroll_tag_url":"https://ads.example.test/midroll","player_vast_midroll_offset_seconds":120}`)
	if ads := read(); ads["enabled"] != true || ads["vast_tag_url"] != "" || ads["midroll_tag_url"] != "https://ads.example.test/midroll" || ads["midroll_offset_seconds"] != float64(120) {
		t.Fatalf("midroll-only configuration is not effective: %+v", ads)
	}
	changes, raw := e.patch(t, `{"player_vast_midroll_tag_url":"","player_vast_postroll_tag_url":"https://ads.example.test/postroll"}`)
	if ads := read(); ads["enabled"] != true || ads["postroll_tag_url"] != "https://ads.example.test/postroll" || ads["midroll_tag_url"] != "" {
		t.Fatalf("postroll-only configuration is not effective: %+v", ads)
	}
	if c := changes["setting.player_vast_postroll_tag_url"]; c.Before != "" || c.After != "" || strings.Contains(raw+e.logs.String(), "https://ads.example.test/postroll") {
		t.Fatal("placement URLs must not appear in audit values or logs")
	}
	if rec := sendJSONAuth(e.srv, http.MethodPatch, "/api/v1/admin/instance-settings", `{"player_vast_midroll_offset_seconds":0}`, e.admin); rec.Code != http.StatusUnprocessableEntity {
		t.Fatal("invalid midroll offset must be rejected")
	}
	e.patch(t, `{"player_vast_postroll_tag_url":null,"player_vast_midroll_offset_seconds":null}`)
	if ads := read(); ads["enabled"] != false || ads["postroll_tag_url"] != "" || ads["midroll_offset_seconds"] != float64(300) {
		t.Fatalf("placement reset failed: %+v", ads)
	}
}
