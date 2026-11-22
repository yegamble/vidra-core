package doctor

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorRendersLocalPDSOnlyWhenSelected(t *testing.T) {
	for _, tc := range []struct {
		enabled, provider string
		local             bool
	}{
		{"true", "", true},
		{"true", "https://hosted.example.org", false},
		{"false", "", false},
	} {
		h := newFakeHost()
		h.files[filepath.Join(testRoot, "env/production.env")] = healthyEnv + "\nATPROTO_ENABLED=" + tc.enabled + "\nATPROTO_PDS_URL=" + tc.provider + "\n"
		h.respond = func(name string, args []string) (Output, error) {
			if name == "env" && len(args) >= 2 && args[1] == "docker" {
				return Output{Stdout: `{"services":{"pds":{"ports":[{"target":2583,"published":"2583","host_ip":"0.0.0.0"}]}}}`}, nil
			}
			return h.healthyRespond(name, args)
		}
		result := only(t, "published ports", h, nil)
		if hasStatus(result, StatusFail) != tc.local {
			t.Fatalf("doctor did not report the local PDS port exposure: %+v", result)
		}
		var invocation string
		for _, cmd := range h.commands() {
			if strings.Contains(cmd, "config --format json") {
				invocation = cmd
			}
		}
		if invocation == "" || strings.Contains(invocation, "-f docker-compose.atproto-pds.yml") != tc.local || strings.Contains(invocation, "VIDRA_LOCAL_PDS_HOST=pds.tube.vidra.test") != tc.local {
			t.Fatalf("incorrect PDS render for enabled=%s, provider=%q: %s", tc.enabled, tc.provider, invocation)
		}
	}
}
