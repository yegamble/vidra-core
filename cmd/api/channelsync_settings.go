package main

import (
	"time"

	"github.com/vidra/vidra-core/internal/instancesettings"
)

// channelSyncCooldownFromSetting resolves the sync-now cooldown from the
// channel_sync_cooldown_minutes overlay. The setting is whole minutes and its
// default is CHANNEL_SYNC_COOLDOWN rounded up (never below 1), so while it still
// equals that default hand back the env duration EXACTLY: an operator's 90s
// must not silently become 2m, and CHANNEL_SYNC_COOLDOWN=0 (throttle off) must
// not become 1m just because the overlay exists. Once the admin changes it, the
// setting wins; a whole-minute setting cannot express "off", so turning the
// throttle off stays an env-only choice.
func channelSyncCooldownFromSetting(env time.Duration, minutes int64) time.Duration {
	if minutes == instancesettings.MinutesCeil(env) {
		return env
	}
	return time.Duration(minutes) * time.Minute
}

// channelSyncBackoffMaxFromSetting resolves the failure-backoff cap from the
// channel_sync_backoff_max_hours overlay, by the same rule as the cooldown: the
// setting is whole hours with the env value rounded UP as its default, so while
// it still equals that default the env duration comes back EXACTLY (a 90m cap
// must not silently become 2h). Once the admin changes it, the setting wins.
func channelSyncBackoffMaxFromSetting(env time.Duration, hours int64) time.Duration {
	if hours == instancesettings.HoursCeil(env) {
		return env
	}
	return time.Duration(hours) * time.Hour
}
