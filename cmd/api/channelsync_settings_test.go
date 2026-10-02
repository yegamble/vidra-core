package main

import (
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/instancesettings"
)

// TestChannelSyncCooldownFromSetting: while the whole-minute setting still
// equals its env-derived default the env duration comes back EXACTLY (a 90s
// env must not silently become 2m, and CHANNEL_SYNC_COOLDOWN=0, which turns the
// throttle off, must not become 1m); any other value is the setting in minutes.
func TestChannelSyncCooldownFromSetting(t *testing.T) {
	for _, tc := range []struct {
		name    string
		env     time.Duration
		setting int64
		want    time.Duration
	}{
		{"default 1m", time.Minute, 1, time.Minute},
		{"90s env kept exactly at its default", 90 * time.Second, 2, 90 * time.Second},
		{"throttle-off env kept at its default", 0, 1, 0},
		{"admin change over a 90s env", 90 * time.Second, 5, 5 * time.Minute},
		{"admin change over the 1m default", time.Minute, 30, 30 * time.Minute},
		{"admin change over a throttle-off env", 0, 10, 10 * time.Minute},
	} {
		if got := channelSyncCooldownFromSetting(tc.env, tc.setting); got != tc.want {
			t.Errorf("%s: channelSyncCooldownFromSetting(%s, %d) = %s, want %s", tc.name, tc.env, tc.setting, got, tc.want)
		}
		// The default the registry derives from the env is what the helper compares to.
		if tc.setting == instancesettings.MinutesCeil(tc.env) && channelSyncCooldownFromSetting(tc.env, tc.setting) != tc.env {
			t.Errorf("%s: at the env-derived default the env duration must come back exactly", tc.name)
		}
	}
}
