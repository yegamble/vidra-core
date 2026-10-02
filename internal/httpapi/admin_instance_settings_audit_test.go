package httpapi

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/config"
	"github.com/vidra/vidra-core/internal/instancesettings"
	"github.com/vidra/vidra-core/internal/observability"
)

// settingsAuditEnv is a settings-enabled server whose durable ledger and slog
// capture are both reachable, so a test can prove a value is absent from EVERY
// place the event lands. The ledger goes through the real audit envelope
// validation (audit.NewService), so a change the envelope would refuse shows up
// here as a missing row, exactly as it would in production.
type settingsAuditEnv struct {
	srv    *Server
	ledger *httpAuditFakeRepo
	logs   *bytes.Buffer
	admin  string
}

func newSettingsAuditEnv(t *testing.T) *settingsAuditEnv {
	t.Helper()
	return newSettingsAuditEnvWith(t, testConfig())
}

func newSettingsAuditEnvWith(t *testing.T, cfg *config.Config) *settingsAuditEnv {
	t.Helper()
	ledger := &httpAuditFakeRepo{}
	srv, _, _, _, _ := videoServerFullWith(t, cfg, []Option{WithAuditLog(audit.NewService(ledger))})
	var buf bytes.Buffer
	srv.logger = slog.New(slog.NewJSONHandler(&buf, nil))
	return &settingsAuditEnv{srv: srv, ledger: ledger, logs: &buf,
		admin: createChannelFor(t, srv, "ada", "ada@example.test", "ada")}
}

// patch sends a PATCH and returns the changes recorded by THAT request's
// success event (nil when it recorded none) plus the raw event JSON.
func (e *settingsAuditEnv) patch(t *testing.T, body string) (map[string]audit.Change, string) {
	t.Helper()
	before := len(e.ledger.rows)
	if rec := sendJSONAuth(e.srv, http.MethodPatch, "/api/v1/admin/instance-settings", body, e.admin); rec.Code != http.StatusOK {
		t.Fatalf("PATCH %s = %d; body=%s", body, rec.Code, rec.Body.String())
	}
	for _, row := range e.ledger.rows[before:] {
		if row.Action != observability.ActionAdminInstanceUpdate || row.Result != observability.ResultSuccess {
			continue
		}
		var cs []audit.Change
		if err := json.Unmarshal(row.Changes, &cs); err != nil {
			t.Fatalf("decode changes %s: %v", row.Changes, err)
		}
		out := map[string]audit.Change{}
		for _, c := range cs {
			out[c.Field] = c
		}
		raw, _ := json.Marshal(row)
		return out, string(raw)
	}
	t.Fatalf("PATCH %s wrote no %s success row to the ledger (envelope refused it?)", body, observability.ActionAdminInstanceUpdate)
	return nil, ""
}

func (e *settingsAuditEnv) view(t *testing.T, key string) instanceSettingView {
	return settingView(t, instanceSettings(t, e.srv, e.admin), key)
}

func TestInstanceSettingsAuditRecordsIntOldToNew(t *testing.T) {
	e := newSettingsAuditEnv(t)
	def := int64(e.view(t, instancesettings.KeyDefaultUserQuotaBytes).Default.(float64))

	// First override: the old value is the EFFECTIVE one (the default), not blank.
	ch, _ := e.patch(t, `{"default_user_quota_bytes":5000000}`)
	got := ch["setting.default_user_quota_bytes"]
	if got.Before != strconv.FormatInt(def, 10) || got.After != "5000000" {
		t.Errorf("first override = %+v, want %d -> 5000000", got, def)
	}
	ch, _ = e.patch(t, `{"default_user_quota_bytes":7000000}`)
	if got := ch["setting.default_user_quota_bytes"]; got.Before != "5000000" || got.After != "7000000" {
		t.Errorf("second override = %+v, want 5000000 -> 7000000", got)
	}
	// A clear records old -> default.
	ch, _ = e.patch(t, `{"default_user_quota_bytes":null}`)
	if got := ch["setting.default_user_quota_bytes"]; got.Before != "7000000" || got.After != strconv.FormatInt(def, 10) {
		t.Errorf("clear = %+v, want 7000000 -> %d", got, def)
	}
}

// TestInstanceSettingsAuditRetentionFloor: audit_log_retention_days is audited
// old -> new like any int key, and a value below the AUDIT_LOG_RETENTION floor is
// a 422 that names the floor and changes nothing.
func TestInstanceSettingsAuditRetentionFloor(t *testing.T) {
	cfg := testConfig()
	cfg.AuditLogRetention = 400 * 24 * time.Hour
	e := newSettingsAuditEnvWith(t, cfg)

	rec := sendJSONAuth(e.srv, http.MethodPatch, "/api/v1/admin/instance-settings", `{"audit_log_retention_days":30}`, e.admin)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "at least 400 days") {
		t.Fatalf("below-floor PATCH = %d %s, want 422 naming the 400-day floor", rec.Code, rec.Body.String())
	}
	if v := e.view(t, instancesettings.KeyAuditLogRetentionDays); v.Overridden {
		t.Fatalf("a refused write left an override: %+v", v)
	}
	// The dry-run endpoint gives the same answer as the write.
	dry := sendJSONAuth(e.srv, http.MethodPost, "/api/v1/admin/instance-settings/validate", `{"audit_log_retention_days":30}`, e.admin)
	if dry.Code != http.StatusOK || !strings.Contains(dry.Body.String(), "at least 400 days") {
		t.Fatalf("dry-run = %d %s, want the floor message", dry.Code, dry.Body.String())
	}

	ch, _ := e.patch(t, `{"audit_log_retention_days":800}`)
	if got := ch["setting.audit_log_retention_days"]; got.Before != "400" || got.After != "800" {
		t.Errorf("audit row = %+v, want 400 -> 800", got)
	}
}

func TestInstanceSettingsAuditRecordsBoolAndEnumValues(t *testing.T) {
	e := newSettingsAuditEnv(t)
	ch, _ := e.patch(t, `{"uploads_enabled":false,"broadcast_level":"warning","default_language":"fr"}`)
	for field, want := range map[string]string{
		"setting.uploads_enabled":  "true->false",
		"setting.broadcast_level":  instancesettings.DefaultBroadcastLevel + "->warning",
		"setting.default_language": instancesettings.DefaultDefaultLanguage + "->fr",
	} {
		if got := ch[field].Before + "->" + ch[field].After; got != want {
			t.Errorf("%s = %q, want %q", field, got, want)
		}
	}
}

// TestInstanceSettingsAuditFreeTextIsValueLess pins hard rule 6: contact_email
// is an email address and broadcast text is a message body, so the row says the
// key changed and nothing about what it changed to or from.
func TestInstanceSettingsAuditFreeTextIsValueLess(t *testing.T) {
	e := newSettingsAuditEnv(t)
	const mail, banner, desc = "ops-secret@example.test", "zz-banner-body-canary", "zz-description-canary"
	ch, raw := e.patch(t, `{"contact_email":"`+mail+`","broadcast_message":"`+banner+`","instance_description":"`+desc+`"}`)
	for _, k := range []string{"contact_email", "broadcast_message", "instance_description"} {
		c, ok := ch["setting."+k]
		if !ok {
			t.Errorf("%s: no change entry, want a value-less one", k)
		}
		if c.Before != "" || c.After != "" {
			t.Errorf("%s recorded values %+v", k, c)
		}
	}
	// Overwrite them so a "before" leak would also be observable.
	_, raw2 := e.patch(t, `{"contact_email":"ops-two@example.test","broadcast_message":"zz-banner-two"}`)
	for _, leak := range []string{mail, banner, desc, "ops-two@example.test", "zz-banner-two"} {
		for name, text := range map[string]string{"ledger row": raw + raw2, "slog": e.logs.String()} {
			if bytes.Contains([]byte(text), []byte(leak)) {
				t.Errorf("%q leaked into the %s", leak, name)
			}
		}
	}
}

func TestInstanceSettingsAuditUnchangedValueRecordsNothing(t *testing.T) {
	e := newSettingsAuditEnv(t)
	e.patch(t, `{"default_user_quota_bytes":5000000}`)
	ch, _ := e.patch(t, `{"default_user_quota_bytes":5000000,"uploads_enabled":true}`) // uploads_enabled is already true
	if len(ch) != 0 {
		t.Errorf("a PATCH that changed nothing recorded %+v", ch)
	}
}

// TestInstanceSettingsAuditInvalidPathRecordsNoValues: the write did not happen,
// so the failure row names the key and carries no changes and no value.
func TestInstanceSettingsAuditInvalidPathRecordsNoValues(t *testing.T) {
	e := newSettingsAuditEnv(t)
	if rec := sendJSONAuth(e.srv, http.MethodPatch, "/api/v1/admin/instance-settings", `{"default_user_quota_bytes":-987654}`, e.admin); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid PATCH = %d, want 422", rec.Code)
	}
	for _, row := range e.ledger.rows {
		if row.Action == observability.ActionAdminInstanceUpdate && row.Result == observability.ResultFailure {
			raw, _ := json.Marshal(row)
			if string(row.Changes) != "[]" || bytes.Contains(raw, []byte("987654")) {
				t.Errorf("failure row carries values: %s", raw)
			}
			return
		}
	}
	t.Fatal("no failure row for the invalid PATCH")
}

// TestInstanceSettingsAuditRecordableValuesPassTheEnvelope guards the failure
// that would silently lose the whole row: the envelope refuses an event if any
// before/after value is not a safe scalar, and a refused event is dropped. Every
// option of every enum key must therefore be recordable, and every allowlisted
// string key must still exist as a string-kind setting.
func TestInstanceSettingsAuditRecordableValuesPassTheEnvelope(t *testing.T) {
	e := newSettingsAuditEnv(t)
	svc := audit.NewService(&httpAuditFakeRepo{})
	kinds := map[string]instancesettings.Kind{}
	for _, eff := range e.srv.settingssvc.Snapshot() {
		kinds[eff.Key] = eff.Kind
		for _, opt := range eff.Options {
			err := svc.Record(t.Context(), audit.Event{
				Action: observability.ActionAdminInstanceUpdate, Result: observability.ResultSuccess,
				Changes: []audit.Change{{Field: audit.SettingChangeFieldPrefix + eff.Key, Before: opt, After: opt}},
			})
			if err != nil {
				t.Errorf("%s option %q is not envelope-safe: %v", eff.Key, opt, err)
			}
		}
	}
	for key := range settingValueStrings {
		if kinds[key] != instancesettings.KindString {
			t.Errorf("settingValueStrings lists %q, which is not a string-kind setting (%q)", key, kinds[key])
		}
	}
}
