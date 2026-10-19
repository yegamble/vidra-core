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

type googleAnalyticsConfig struct {
	Enabled bool   `json:"google_enabled"`
	ID      string `json:"google_measurement_id"`
}

func readGoogleAnalytics(t *testing.T, srv *Server) (googleAnalyticsConfig, string) {
	t.Helper()
	rec := getInstanceRec(srv)
	var body struct {
		Analytics *googleAnalyticsConfig `json:"analytics"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Analytics == nil {
		t.Fatalf("public analytics configuration missing: %v", err)
	}
	return *body.Analytics, rec.Header().Get("ETag")
}

func TestGoogleAnalyticsConfigFailsClosed(t *testing.T) {
	repo := newInstanceSettingsFakeRepo()
	repo.rows["analytics_google_enabled"] = sqlcgen.InstanceSetting{Key: "analytics_google_enabled", Value: "true"}
	repo.rows["analytics_google_measurement_id"] = sqlcgen.InstanceSetting{Key: "analytics_google_measurement_id", Value: "G-INVALID<script>"}
	svc := instancesettings.NewService(repo, instancesettings.Defaults{})
	if err := svc.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, srv := range []*Server{New(testConfig(), nil, nil), New(testConfig(), nil, nil, WithSettingsService(svc))} {
		if analytics, _ := readGoogleAnalytics(t, srv); analytics.Enabled || analytics.ID != "" {
			t.Fatal("unwired or invalid stored analytics settings must stay disabled")
		}
	}
}

func TestGoogleAnalyticsConfig(t *testing.T) {
	e := newSettingsAuditEnv(t)
	if analytics, _ := readGoogleAnalytics(t, e.srv); analytics.Enabled || analytics.ID != "" {
		t.Fatal("fresh instance must not enable Google Analytics")
	}
	member := registerAndToken(t, e.srv, `{"username":"bob","email":"bob@example.test","password":"supersecret123"}`)
	for _, tc := range []struct {
		token  string
		status int
	}{{"", http.StatusUnauthorized}, {member, http.StatusForbidden}} {
		if rec := sendJSONAuth(e.srv, http.MethodPatch, "/api/v1/admin/instance-settings", `{"analytics_google_enabled":true}`, tc.token); rec.Code != tc.status {
			t.Fatalf("unauthorized analytics write = %d", rec.Code)
		}
	}
	e.patch(t, `{"analytics_google_enabled":true}`)
	if analytics, _ := readGoogleAnalytics(t, e.srv); analytics.Enabled {
		t.Fatal("toggle without a measurement ID must remain ineffective")
	}
	_, before := readGoogleAnalytics(t, e.srv)
	changes, raw := e.patch(t, `{"analytics_google_measurement_id":"G-PSW1MY7HB4"}`)
	if analytics, after := readGoogleAnalytics(t, e.srv); !analytics.Enabled || analytics.ID != "G-PSW1MY7HB4" || after == before {
		t.Fatal("valid configuration must activate analytics and change the public ETag")
	}
	if change, ok := changes["setting.analytics_google_measurement_id"]; !ok || change.Before != "" || change.After != "" || strings.Contains(raw+e.logs.String(), "G-PSW1MY7HB4") {
		t.Fatal("measurement ID must follow existing audit-value redaction")
	}
	const invalid = `{"analytics_google_measurement_id":"G-invalid"}`
	if rec := sendJSONAuth(e.srv, http.MethodPost, "/api/v1/admin/instance-settings/validate", invalid, e.admin); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "analytics_google_measurement_id") {
		t.Fatal("dry-run must identify the invalid measurement ID")
	}
	if rec := sendJSONAuth(e.srv, http.MethodPatch, "/api/v1/admin/instance-settings", invalid, e.admin); rec.Code != http.StatusUnprocessableEntity {
		t.Fatal("invalid measurement ID write must be rejected")
	}
	if analytics, _ := readGoogleAnalytics(t, e.srv); !analytics.Enabled || analytics.ID != "G-PSW1MY7HB4" {
		t.Fatal("rejected write changed the effective configuration")
	}
	e.patch(t, `{"analytics_google_enabled":false}`)
	if analytics, _ := readGoogleAnalytics(t, e.srv); analytics.Enabled || analytics.ID != "" {
		t.Fatal("disabling must clear the public ID")
	}
	if got := settingView(t, instanceSettings(t, e.srv, e.admin), instancesettings.KeyAnalyticsGoogleMeasurementID).Value; got != "G-PSW1MY7HB4" {
		t.Fatal("disabling must retain the saved admin setting")
	}
	e.patch(t, `{"analytics_google_enabled":true,"analytics_google_measurement_id":""}`)
	if analytics, _ := readGoogleAnalytics(t, e.srv); analytics.Enabled || analytics.ID != "" {
		t.Fatal("clearing the ID must disable analytics")
	}
	e.patch(t, `{"analytics_google_enabled":null,"analytics_google_measurement_id":null}`)
	if analytics, _ := readGoogleAnalytics(t, e.srv); analytics.Enabled || analytics.ID != "" {
		t.Fatal("reset must restore disabled defaults")
	}
}
