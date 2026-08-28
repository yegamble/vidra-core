package config

import "testing"

// FEATURE_LIVE_ENABLED's default is DERIVED, not constant: a bare install has
// no RTMP media server, and a default-on toggle there boots live
// enabled-but-unconfigured — a permanent orange "Needs setup" finding on the
// infrastructure page and, worse, creators offered a "Go live" that mints
// streams nobody can ever publish to. When the operator has wired an ingest
// URL, defaulting on is the right convenience; when they have not, off is the
// only honest default. An explicitly set value always wins over the
// derivation, in both directions.
func TestFeatureLiveEnabledDefaultDerivedFromIngest(t *testing.T) {
	load := func(t *testing.T, featureLive, rtmpURL string) *Config {
		t.Helper()
		t.Setenv("FEATURE_LIVE_ENABLED", featureLive)
		t.Setenv("LIVE_RTMP_URL", rtmpURL)
		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		return cfg
	}

	if cfg := load(t, "", ""); cfg.LiveEnabled {
		t.Error("LiveEnabled = true on a bare install (no LIVE_RTMP_URL), want the dead-stream trap defaulted off")
	}
	if cfg := load(t, "", "rtmp://media-server/live"); !cfg.LiveEnabled {
		t.Error("LiveEnabled = false with an ingest URL wired, want the derived default on")
	}
	// The explicit spellings beat the derivation both ways: an operator who
	// wants live off while an ingest URL exists (maintenance), or on without
	// one yet (staging the toggle before the media server), said so.
	if cfg := load(t, "false", "rtmp://media-server/live"); cfg.LiveEnabled {
		t.Error("LiveEnabled = true with FEATURE_LIVE_ENABLED=false set, want the explicit value to win")
	}
	if cfg := load(t, "true", ""); !cfg.LiveEnabled {
		t.Error("LiveEnabled = false with FEATURE_LIVE_ENABLED=true set, want the explicit value to win")
	}
}
