package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/instancesettings"
)

// TestAgeGateNeedsAMinimumAge: shipped off; it cannot be switched on without a
// minimum age, and the age cannot be cleared while it is on. Each refusal is a
// 422 on the key the batch changed and writes nothing. Set together, both land.
func TestAgeGateNeedsAMinimumAge(t *testing.T) {
	srv := videoServer(t)
	adminTok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	if publicInstance(t, srv).AgeGateEnabled {
		t.Fatal("age_gate_enabled default = true, want false")
	}
	refused := func(body, wantField string) {
		t.Helper()
		rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/admin/instance-settings", body, adminTok)
		var env ErrorResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &env)
		if rec.Code != http.StatusUnprocessableEntity || len(env.Error.Fields) != 1 || env.Error.Fields[0].Field != wantField {
			t.Fatalf("PATCH %s = %d %s, want a 422 on %s", body, rec.Code, rec.Body.String(), wantField)
		}
	}

	refused(`{"age_gate_enabled":true}`, "age_gate_enabled")
	refused(`{"age_gate_enabled":true,"registration_minimum_age":0}`, "age_gate_enabled")
	if srv.settingssvc.Bool(instancesettings.KeyAgeGateEnabled) {
		t.Fatal("a refused batch switched the gate on")
	}

	patchSettings(t, srv, adminTok, `{"age_gate_enabled":true,"registration_minimum_age":18}`)
	if inst := publicInstance(t, srv); !inst.AgeGateEnabled || inst.RegistrationMinimumAge != 18 {
		t.Fatalf("after setting both: age_gate_enabled=%v registration_minimum_age=%d, want true / 18", inst.AgeGateEnabled, inst.RegistrationMinimumAge)
	}

	refused(`{"registration_minimum_age":0}`, "registration_minimum_age")
	refused(`{"registration_minimum_age":null}`, "registration_minimum_age")
	if got := publicInstance(t, srv).RegistrationMinimumAge; got != 18 {
		t.Fatalf("a refused batch changed the age to %d", got)
	}

	// Raising the age, or switching the gate off and clearing the age together,
	// is allowed.
	patchSettings(t, srv, adminTok, `{"registration_minimum_age":21}`)
	patchSettings(t, srv, adminTok, `{"age_gate_enabled":false,"registration_minimum_age":0}`)
	if publicInstance(t, srv).AgeGateEnabled {
		t.Fatal("age_gate_enabled = true after switching it off")
	}
}

// TestAgeGateIsEffectiveOnlyWithAnAge: the public document never reports a gate
// with no age to ask for, even if the setting got there without the PATCH guard.
func TestAgeGateIsEffectiveOnlyWithAnAge(t *testing.T) {
	srv := videoServer(t)
	if err := srv.settingssvc.Apply(context.Background(),
		map[string]instancesettings.Update{instancesettings.KeyAgeGateEnabled: {Value: "true"}}, uuid.New()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if publicInstance(t, srv).AgeGateEnabled {
		t.Fatal("age_gate_enabled reported true with no minimum age")
	}
}
