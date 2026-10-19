package instancesettings

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestGoogleAnalyticsSettings(t *testing.T) {
	const enabled, id = "analytics_google_enabled", "analytics_google_measurement_id"
	repo := newFakeRepo()
	svc := NewService(repo, testDefaults())
	if svc.Bool(enabled) || svc.String(id) != "" {
		t.Fatal("Google Analytics must default off and unconfigured")
	}
	for key, want := range map[string]Kind{enabled: KindBool, id: KindString} {
		if kind, ok := KindOf(key); !ok || kind != want {
			t.Errorf("incorrect analytics setting kind: %s", key)
		}
	}
	for _, entry := range svc.Snapshot() {
		if entry.Key == enabled || entry.Key == id {
			if entry.Page != PageGeneral || entry.Section != "analytics" || entry.Overridden {
				t.Errorf("unexpected analytics metadata: %+v", entry)
			}
		}
	}
	for _, value := range []string{"", "G-AB12", "G-PSW1MY7HB4", "G-" + strings.Repeat("A", 20)} {
		if err := Validate(id, value); err != nil {
			t.Errorf("valid measurement ID rejected: %q: %v", value, err)
		}
	}
	for _, value := range []string{"G-", "G-ABC", "G-" + strings.Repeat("A", 21), "g-AB12", "G-ab12", "UA-123456", "GTM-AB12", " G-AB12", "G-AB12\n", "G-AB12/", "G-AB12<script>", "G-ＡＢ１２"} {
		if err := Validate(id, value); err == nil {
			t.Errorf("invalid measurement ID accepted: %q", value)
		}
	}
	ctx, actor := context.Background(), uuid.New()
	if err := svc.Apply(ctx, map[string]Update{enabled: {Value: "true"}, id: {Value: "G-AB12"}}, actor); err != nil {
		t.Fatal(err)
	}
	reloaded := NewService(repo, testDefaults())
	if err := reloaded.Load(ctx); err != nil || !reloaded.Bool(enabled) || reloaded.String(id) != "G-AB12" {
		t.Fatalf("analytics settings did not survive reload: %v", err)
	}
	if err := reloaded.Apply(ctx, map[string]Update{enabled: {Delete: true}, id: {Delete: true}}, actor); err != nil || reloaded.Bool(enabled) || reloaded.String(id) != "" {
		t.Fatalf("reset did not restore disabled defaults: %v", err)
	}
}
