package catalog

import (
	"encoding/json"
	"strings"
	"testing"
)

const emptyStatus = `{"protocol_version":2,"supported_scopes":["local"],"supported_event_types":["catalog.snapshot.page","catalog.snapshot.end"],"scope":"local","epoch":null,"ready":false,"highest_observed_generation":"0","completed_generation":"0","completed_at":null,"completed_manifest":null,"staged_snapshots":0,"oldest_staged_at":null}`

func TestStatusRequiresExplicitCapabilityAndConsistentCompletion(t *testing.T) {
	if _, err := ParseStatus([]byte(emptyStatus)); err != nil {
		t.Fatal(err)
	}
	for key := range statusFields(t, emptyStatus) {
		missing := statusFields(t, emptyStatus)
		delete(missing, key)
		wire, _ := json.Marshal(missing)
		if _, err := ParseStatus(wire); err == nil {
			t.Errorf("accepted missing contract key %s", key)
		}
	}
	unready := statusFields(t, emptyStatus)
	unready["epoch"] = "c9895c40-13af-4aa9-bf07-9ca761b7e172"
	unready["highest_observed_generation"] = "1" // expired staging is not completion
	staged, _ := json.Marshal(unready)
	if _, err := ParseStatus(staged); err != nil {
		t.Fatal(err)
	}
	valid := statusFields(t, emptyStatus)
	valid["epoch"] = "c9895c40-13af-4aa9-bf07-9ca761b7e172"
	valid["highest_observed_generation"] = "9007199254740993"
	valid["completed_generation"] = "9007199254740992"
	valid["ready"] = true
	valid["completed_at"] = "2026-11-03T12:00:00Z"
	const emptyHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	valid["completed_manifest"] = Manifest{MembershipSHA256: emptyHash, PagesSHA256: emptyHash}
	valid["staged_snapshots"] = 1
	valid["oldest_staged_at"] = "2026-11-03T12:01:00Z"
	raw, _ := json.Marshal(valid)
	got, err := ParseStatus(raw)
	if err != nil || got.CompletedGeneration != "9007199254740992" || got.CompletedManifest == nil {
		t.Fatalf("completed full with newer staging: %+v, %v", got, err)
	}
	for name, mutate := range map[string]func(map[string]any){
		"version":           func(v map[string]any) { v["protocol_version"] = 1 },
		"capability":        func(v map[string]any) { v["supported_event_types"] = []string{"catalog.snapshot.end"} },
		"unsupported scope": func(v map[string]any) { v["supported_scopes"] = []string{"remote"} },
		"scope":             func(v map[string]any) { v["scope"] = "remote" },
		"missing":           func(v map[string]any) { delete(v, "ready") },
		"null bool":         func(v map[string]any) { v["ready"] = nil },
		"unknown":           func(v map[string]any) { v["surprise"] = true },
		"numeric":           func(v map[string]any) { v["completed_generation"] = 1 },
		"leading zero":      func(v map[string]any) { v["completed_generation"] = "01" },
		"overflow":          func(v map[string]any) { v["highest_observed_generation"] = "9223372036854775808" },
		"rewind":            func(v map[string]any) { v["highest_observed_generation"] = "1" },
		"missing epoch":     func(v map[string]any) { v["epoch"] = nil },
		"zero epoch":        func(v map[string]any) { v["epoch"] = "00000000-0000-0000-0000-000000000000" },
		"loose epoch":       func(v map[string]any) { v["epoch"] = strings.ToUpper(v["epoch"].(string)) },
		"not ready":         func(v map[string]any) { v["ready"] = false },
		"missing manifest":  func(v map[string]any) { v["completed_manifest"] = nil },
		"partial manifest": func(v map[string]any) {
			v["completed_manifest"] = map[string]any{"document_count": 0, "membership_sha256": emptyHash, "pages_sha256": emptyHash}
		},
		"bad manifest":         func(v map[string]any) { v["completed_manifest"] = Manifest{} },
		"missing date":         func(v map[string]any) { v["completed_at"] = nil },
		"too many staging":     func(v map[string]any) { v["staged_snapshots"] = 2 },
		"missing staging date": func(v map[string]any) { v["oldest_staged_at"] = nil },
		"staging not newer":    func(v map[string]any) { v["highest_observed_generation"] = v["completed_generation"] },
	} {
		t.Run(name, func(t *testing.T) {
			bad := statusFields(t, string(raw))
			mutate(bad)
			wire, _ := json.Marshal(bad)
			if _, err := ParseStatus(wire); err == nil {
				t.Fatalf("accepted inconsistent status %s", wire)
			}
		})
	}
	if _, err := ParseStatus([]byte(emptyStatus + `{}`)); err == nil {
		t.Error("accepted trailing response")
	}
	for name, wire := range map[string]string{
		"duplicate":          strings.Replace(emptyStatus, `"ready":false`, `"ready":false,"ready":false`, 1),
		"case alias":         strings.Replace(emptyStatus, `"ready":false`, `"ready":false,"Ready":false`, 1),
		"manifest duplicate": strings.Replace(string(raw), `"page_count":0`, `"page_count":0,"page_count":0`, 1),
		"manifest alias":     strings.Replace(string(raw), `"page_count":0`, `"page_count":0,"Page_Count":0`, 1),
	} {
		if _, err := ParseStatus([]byte(wire)); err == nil {
			t.Errorf("accepted ambiguous %s status", name)
		}
	}
}

func statusFields(t *testing.T, raw string) map[string]any {
	t.Helper()
	var fields map[string]any
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		t.Fatal(err)
	}
	return fields
}
