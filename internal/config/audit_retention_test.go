package config

import (
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/audit"
)

// loadCandidate boots the production candidate with overrides applied.
func loadCandidate(t *testing.T, overrides map[string]string) (*Config, error) {
	t.Helper()
	vars := productionCandidate()
	for k, v := range overrides {
		vars[k] = v
	}
	return LoadFrom(func(key string) (string, bool) {
		v, ok := vars[key]
		return v, ok
	})
}

// TestAuditLogRetentionDefaultsToTheShippedWindow. An operator who sets nothing
// gets a bounded trail — which is the whole point of the change, since the table
// was previously kept forever with no way to say otherwise.
func TestAuditLogRetentionDefaultsToTheShippedWindow(t *testing.T) {
	cfg, err := loadCandidate(t, nil)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.AuditLogRetention != DefaultAuditLogRetention {
		t.Errorf("AuditLogRetention = %s, want %s", cfg.AuditLogRetention, DefaultAuditLogRetention)
	}
	if DefaultAuditLogRetention != 400*24*time.Hour {
		t.Errorf("DefaultAuditLogRetention = %s, want 400 days", DefaultAuditLogRetention)
	}
}

// TestAuditRetentionDefaultMatchesTheAuditPackage guards the one copy this
// package makes to stay free of internal imports: internal/audit re-states the
// window for its own readers, and a drift between the two would mean the number
// documented next to the sweep is not the number the sweep runs with.
func TestAuditRetentionDefaultMatchesTheAuditPackage(t *testing.T) {
	if DefaultAuditLogRetention != audit.DefaultRetention {
		t.Errorf("config default %s != audit.DefaultRetention %s", DefaultAuditLogRetention, audit.DefaultRetention)
	}
}

// TestAuditLogRetentionIsSettableAndZeroIsKeepForever.
func TestAuditLogRetentionIsSettableAndZeroIsKeepForever(t *testing.T) {
	cfg, err := loadCandidate(t, map[string]string{"AUDIT_LOG_RETENTION": "720h"})
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.AuditLogRetention != 720*time.Hour {
		t.Errorf("AuditLogRetention = %s, want 720h", cfg.AuditLogRetention)
	}

	cfg, err = loadCandidate(t, map[string]string{"AUDIT_LOG_RETENTION": "0s"})
	if err != nil {
		t.Fatalf("LoadFrom(0s): %v", err)
	}
	if cfg.AuditLogRetention != 0 {
		t.Errorf("AuditLogRetention = %s, want 0 (keep forever)", cfg.AuditLogRetention)
	}
}

// TestAuditLogRetentionRejectsNegative. A negative window computes a cutoff in
// the FUTURE, so the next tick would delete the entire trail — the one mistake
// here that cannot be undone, and therefore the one that must not boot.
func TestAuditLogRetentionRejectsNegative(t *testing.T) {
	_, err := loadCandidate(t, map[string]string{"AUDIT_LOG_RETENTION": "-24h"})
	if err == nil {
		t.Fatal("a negative AUDIT_LOG_RETENTION booted")
	}
	if _, ok := collectVarErrors(err)["AUDIT_LOG_RETENTION"]; !ok {
		t.Errorf("error is not attributed to AUDIT_LOG_RETENTION: %v", err)
	}
}
