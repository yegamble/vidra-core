package httpapi

import (
	"strconv"

	"github.com/vidra/vidra-core/internal/instancesettings"
)

// The age gate (age_gate_enabled). Off by default. When on, the frontend asks a
// visitor without an account to confirm they are at least
// registration_minimum_age before it renders any page or embed, and keeps ads
// and the operator's custom script off until they do. It is self-declared and
// enforced in the browser: no API or media request is refused, so it is not
// age verification. The same age is the one every signup path must attest.

// ageGateEnabled reports whether the gate is up: switched on AND a minimum age
// set. With no age there is nothing to ask, so the public document never
// reports a gate it cannot word.
func (s *Server) ageGateEnabled() bool {
	return s.settingBool(instancesettings.KeyAgeGateEnabled, false) && s.registrationMinimumAge() > 0
}

// ageGateSwitchRefusal is the field and reason a settings batch must be refused
// for, or "" for both: the batch may not leave the gate on with no minimum age,
// whether it switches the gate on or clears the age.
func (s *Server) ageGateSwitchRefusal(updates map[string]instancesettings.Update) (field, msg string) {
	gateOn := s.settingBool(instancesettings.KeyAgeGateEnabled, false)
	gate, gateInBatch := updates[instancesettings.KeyAgeGateEnabled]
	if gateInBatch {
		gateOn = !gate.Delete && gate.Value == instancesettings.FormatBool(true)
	}
	minAge := s.registrationMinimumAge()
	if u, ok := updates[instancesettings.KeyRegistrationMinimumAge]; ok {
		minAge = 0 // the registry default, which a clear restores
		if !u.Delete {
			n, err := strconv.ParseInt(u.Value, 10, 64)
			if err != nil {
				return "", "" // not a number: the registry's own validation answers it
			}
			minAge = n
		}
	}
	if !gateOn || minAge > 0 {
		return "", ""
	}
	if gateInBatch {
		return instancesettings.KeyAgeGateEnabled, "cannot be on while registration_minimum_age is 0: set the minimum age the gate asks for"
	}
	return instancesettings.KeyRegistrationMinimumAge, "cannot be 0 while age_gate_enabled is on: switch the age gate off first, or keep an age"
}
