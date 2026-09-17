package httpapi

import (
	"context"
	"github.com/google/uuid"
	"github.com/vidra/vidra-core/internal/instancesettings"
	"testing"
)

type managedDeliveryMirror struct{ *fakeIPFSMirror }

func (managedDeliveryMirror) PublicConfigured() bool { return true }

func TestManagedDeliveryRequiresExplicitOverrideWhenBootDisabled(t *testing.T) {
	cfg := testConfig()
	cfg.IPFSEnabled = false
	settings := instancesettings.NewService(newInstanceSettingsFakeRepo(), settingsDefaultsFromConfig(cfg))
	s := &Server{cfg: cfg, settingssvc: settings, ipfsmirrorsvc: managedDeliveryMirror{&fakeIPFSMirror{}}, ipfsHealth: stubIPFSHealth{public: okHealth()}}
	if s.ipfsDeliveryEnabled() {
		t.Fatal("boot-disabled default silently enabled delivery")
	}
	for _, enabled := range []bool{true, false} {
		value := "false"
		if enabled {
			value = "true"
		}
		if err := settings.Apply(context.Background(), map[string]instancesettings.Update{instancesettings.KeyDeliveryIPFSEnabled: {Value: value}}, uuid.Nil); err != nil {
			t.Fatal(err)
		}
		if got := s.ipfsDeliveryEnabled(); got != enabled {
			t.Fatalf("explicit delivery override %v resolved %v", enabled, got)
		}
	}
}
