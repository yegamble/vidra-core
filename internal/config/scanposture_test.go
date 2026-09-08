package config

import (
	"strings"
	"testing"
)

// The scan-by-default posture, read out of the loader.
//
// The posture it replaces was measured in A28: with CLAMAV_ADDR unset an
// instance published an mp4 with EICAR appended, silently. "Unset" now means
// "refuse everything" unless the operator explicitly said otherwise, and these
// pin each corner of that.

// TestScanningIsDerivedFromTheAddress: setting CLAMAV_ADDR is what turns
// scanning on. MALWARE_SCAN_ENABLED no longer decides it.
func TestScanningIsDerivedFromTheAddress(t *testing.T) {
	for _, tc := range []struct {
		name       string
		env        map[string]string
		wantOn     bool
		wantRefuse bool
	}{
		{"address alone turns scanning on", map[string]string{"CLAMAV_ADDR": "clamav:3310"}, true, false},
		{
			"the deprecated switch cannot turn scanning OFF while an address is set",
			map[string]string{"CLAMAV_ADDR": "clamav:3310", "MALWARE_SCAN_ENABLED": "false"},
			true, false,
		},
		{
			"no address and no opt-out: scanning is off AND every ingestion route refuses",
			map[string]string{},
			false, true,
		},
		{
			"no address with the explicit opt-out: scanning is off and ingestion is allowed",
			map[string]string{"MALWARE_SCAN_MODE": "disabled"},
			false, false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := loadCandidate(t, tc.env)
			if err != nil {
				t.Fatalf("LoadFrom: %v", err)
			}
			if cfg.MalwareScanEnabled != tc.wantOn {
				t.Errorf("MalwareScanEnabled = %v, want %v", cfg.MalwareScanEnabled, tc.wantOn)
			}
			if got := cfg.IngestionRefusedForScanner(); got != tc.wantRefuse {
				t.Errorf("IngestionRefusedForScanner() = %v, want %v", got, tc.wantRefuse)
			}
		})
	}
}

// TestDeprecatedSwitchIsFlaggedButNotObeyed: the variable is still READ, once,
// so boot can WARN about it. It must never become a policy input again.
func TestDeprecatedSwitchIsFlaggedButNotObeyed(t *testing.T) {
	cfg, err := loadCandidate(t, map[string]string{
		"CLAMAV_ADDR": "clamav:3310", "MALWARE_SCAN_ENABLED": "false",
	})
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if !cfg.MalwareScanEnabledDeprecated {
		t.Error("MalwareScanEnabledDeprecated = false; boot cannot warn about a variable it does not notice")
	}
	if !cfg.MalwareScanEnabled {
		t.Error("the deprecated switch overrode the derived posture")
	}

	unset, err := loadCandidate(t, map[string]string{"CLAMAV_ADDR": "clamav:3310"})
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if unset.MalwareScanEnabledDeprecated {
		t.Error("MalwareScanEnabledDeprecated = true with the variable absent")
	}
}

// TestScanModeDisabledWithAnAddressIsRefused. The two together are a
// contradiction — a wired daemon and a declaration that nothing is scanned —
// and resolving it silently in either direction picks a security posture on the
// operator's behalf.
func TestScanModeDisabledWithAnAddressIsRefused(t *testing.T) {
	_, err := loadCandidate(t, map[string]string{
		"CLAMAV_ADDR": "clamav:3310", "MALWARE_SCAN_MODE": "disabled",
	})
	if err == nil {
		t.Fatal("LoadFrom accepted MALWARE_SCAN_MODE=disabled alongside CLAMAV_ADDR")
	}
	msg := err.Error()
	for _, want := range []string{"MALWARE_SCAN_MODE", "CLAMAV_ADDR"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal %q does not name %s — the operator cannot tell which lever to move", msg, want)
		}
	}
}

// TestDeprecatedTrueWithNoAddressStillRefusesBoot. An operator who explicitly
// asked for scanning and gave no daemon is stopped at boot rather than handed an
// instance that refuses every upload at runtime, which is far harder to
// diagnose.
func TestDeprecatedTrueWithNoAddressStillRefusesBoot(t *testing.T) {
	_, err := loadCandidate(t, map[string]string{"MALWARE_SCAN_ENABLED": "true"})
	if err == nil {
		t.Fatal("LoadFrom accepted MALWARE_SCAN_ENABLED=true with no CLAMAV_ADDR")
	}
	if !strings.Contains(err.Error(), "CLAMAV_ADDR") {
		t.Errorf("refusal %q does not name CLAMAV_ADDR", err.Error())
	}
}

// TestScanModeRejectsUnknownValuesAndNamesAllFour.
func TestScanModeRejectsUnknownValuesAndNamesAllFour(t *testing.T) {
	_, err := loadCandidate(t, map[string]string{"MALWARE_SCAN_MODE": "off"})
	if err == nil {
		t.Fatal("LoadFrom accepted MALWARE_SCAN_MODE=off")
	}
	for _, mode := range []string{"fail-closed", "fail-open", "quarantine", "disabled"} {
		if !strings.Contains(err.Error(), mode) {
			t.Errorf("refusal %q does not offer %q as a valid mode", err.Error(), mode)
		}
	}
}

// TestValidModesStillLoad keeps the three fallback policies working with a
// scanner wired.
func TestValidModesStillLoad(t *testing.T) {
	for _, mode := range []string{"fail-closed", "fail-open", "quarantine"} {
		cfg, err := loadCandidate(t, map[string]string{"CLAMAV_ADDR": "clamav:3310", "MALWARE_SCAN_MODE": mode})
		if err != nil {
			t.Fatalf("LoadFrom(%s): %v", mode, err)
		}
		if cfg.MalwareScanOptedOut() {
			t.Errorf("mode %s read as the opt-out", mode)
		}
		if cfg.IngestionRefusedForScanner() {
			t.Errorf("mode %s with an address refuses ingestion", mode)
		}
	}
}
