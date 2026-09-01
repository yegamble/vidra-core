package httpapi

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"testing"

	"github.com/vidra/vidra-core/internal/instancesettings"
)

// Instance-owner control over direct messaging. Before this change every Vidra
// instance shipped always-on 1:1 DMs — with attachments, link previews and
// end-to-end encryption — that the operator could neither moderate nor turn
// off. These tests pin the two registry toggles, their route gates, the
// GET /instance disclosure, and (most importantly) that BOTH default ON so an
// instance upgrading past this change sees no behaviour difference.

// startPlainConversation opens a plaintext conversation and returns its id.
func startPlainConversation(t *testing.T, srv *Server, token, recipientID string) string {
	t.Helper()
	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/conversations", `{"recipient_id":"`+recipientID+`"}`, token)
	if rec.Code != http.StatusCreated {
		t.Fatalf("start conversation = %d; body=%s", rec.Code, rec.Body.String())
	}
	var v conversationView
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("unmarshal conversation: %v", err)
	}
	return v.ID
}

// assertFeatureDisabled asserts one request answers 403 feature_disabled. The
// status is checked BEFORE the envelope is decoded: a route that wrongly
// succeeds may answer 204 with an empty body, and errorCode would then Fatalf
// and hide every remaining route in the caller's table.
func assertFeatureDisabled(t *testing.T, srv *Server, method, path, body, token string) {
	t.Helper()
	rec := sendJSONAuth(srv, method, path, body, token)
	if rec.Code != http.StatusForbidden {
		t.Errorf("%s %s = %d, want 403 feature_disabled; body=%s", method, path, rec.Code, rec.Body.String())
		return
	}
	if code := errorCode(t, rec); code != "feature_disabled" {
		t.Errorf("%s %s code = %q, want feature_disabled", method, path, code)
	}
}

// TestMessagingDisabledGatesEveryDMRoute proves the coarse operator switch:
// with messaging_enabled off every direct-messaging route answers 403
// feature_disabled, the E2EE surface goes off WITH it (a device directory and
// envelope store are meaningless with no conversations), GET /instance
// discloses both off so the UI can hide the affordances instead of offering
// controls that 403, and the change is audited by key name only.
func TestMessagingDisabledGatesEveryDMRoute(t *testing.T) {
	srv := videoServer(t)
	var buf bytes.Buffer
	srv.logger = slog.New(slog.NewJSONHandler(&buf, nil))

	adaTok, _ := registerAndUser(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	_, bobID := registerAndUser(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	// Build a conversation and a device BEFORE the switch flips, so the gated
	// routes below are exercised against real ids rather than 404 paths.
	convID := startPlainConversation(t, srv, adaTok, bobID)
	devID := registerE2EEDevice(t, srv, adaTok, "ada-laptop")

	// ada is the first registered user, hence the instance admin.
	setToggle(t, srv, adaTok, instancesettings.KeyMessagingEnabled, false)

	for _, r := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/conversations", `{"recipient_id":"` + bobID + `"}`},
		{http.MethodGet, "/api/v1/me/conversations", ""},
		{http.MethodGet, "/api/v1/conversations/" + convID + "/messages", ""},
		{http.MethodPost, "/api/v1/conversations/" + convID + "/messages", `{"body":"hi"}`},
		{http.MethodPost, "/api/v1/conversations/" + convID + "/read", ""},
		{http.MethodGet, "/api/v1/me/messaging-prefs", ""},
		{http.MethodPatch, "/api/v1/me/messaging-prefs", `{"read_receipts_enabled":false}`},
		{http.MethodGet, "/api/v1/attachments/" + convID, ""},
		// E2EE rides messaging: the master switch takes it down too.
		{http.MethodPost, "/api/v1/e2ee/devices", `{"device_name":"x","identity_key":"i","signing_key":"s"}`},
		{http.MethodGet, "/api/v1/e2ee/devices", ""},
		{http.MethodDelete, "/api/v1/e2ee/devices/" + devID, ""},
		{http.MethodGet, "/api/v1/e2ee/devices/" + devID + "/one-time-keys/count", ""},
		{http.MethodGet, "/api/v1/users/" + bobID + "/e2ee/devices", ""},
		{http.MethodPost, "/api/v1/users/" + bobID + "/e2ee/claim", `{"device_ids":["` + devID + `"]}`},
	} {
		assertFeatureDisabled(t, srv, r.method, r.path, r.body, adaTok)
	}

	// The gate sits AFTER auth: an anonymous caller still gets 401, so a
	// disabled feature never becomes an authentication oracle.
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/me/conversations", "", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anon GET /me/conversations = %d, want 401", rec.Code)
	}

	// Disclosure: both flags off, in lock-step with the gates.
	f := publicInstance(t, srv).Features
	if f.Messaging {
		t.Error("GET /instance features.messaging = true after disabling messaging")
	}
	if f.MessagingE2EE {
		t.Error("GET /instance features.messaging_e2ee = true after disabling messaging")
	}

	if reason := latestInstanceUpdateReason(t, &buf); reason != "keys=messaging_enabled" {
		t.Errorf("audit reason = %q, want keys=messaging_enabled", reason)
	}
}

// TestMessagingE2EEDisabledKeepsPlaintextDMs proves the finer switch is
// genuinely nested and not a second master: with messaging_e2ee_enabled off but
// messaging on, plaintext DMs keep working end to end while the encrypted
// surface — the device directory and the encrypted-conversation/envelope write
// path — answers 403 feature_disabled.
func TestMessagingE2EEDisabledKeepsPlaintextDMs(t *testing.T) {
	srv := videoServer(t)
	adaTok, _ := registerAndUser(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	bobTok, bobID := registerAndUser(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	// Stand an encrypted conversation up BEFORE the switch flips, so the
	// envelope-send branch (which the route-level gate cannot see — it rides
	// POST /conversations/{id}/messages) is exercised against a real one.
	adaDev := registerE2EEDevice(t, srv, adaTok, "ada-laptop")
	bobDev := registerE2EEDevice(t, srv, bobTok, "bob-phone")
	encRec := sendJSONAuth(srv, http.MethodPost, "/api/v1/conversations", `{"recipient_id":"`+bobID+`","encrypted":true}`, adaTok)
	if encRec.Code != http.StatusCreated {
		t.Fatalf("start encrypted conversation = %d; body=%s", encRec.Code, encRec.Body.String())
	}
	var encConv conversationView
	if err := json.Unmarshal(encRec.Body.Bytes(), &encConv); err != nil {
		t.Fatalf("unmarshal encrypted conversation: %v", err)
	}
	envelopeBody := `{"sender_device_id":"` + adaDev + `","envelopes":[{"recipient_device_id":"` + bobDev + `","message_type":1,"ciphertext":"zzz"}]}`

	setToggle(t, srv, adaTok, instancesettings.KeyMessagingE2EEEnabled, false)

	// Plaintext messaging is untouched: start, send, list.
	convID := startPlainConversation(t, srv, adaTok, bobID)
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/conversations/"+convID+"/messages", `{"body":"still fine"}`, adaTok); rec.Code != http.StatusCreated {
		t.Fatalf("plaintext send with E2EE off = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec := getWithAuth(srv, "/api/v1/conversations/"+convID+"/messages", adaTok); rec.Code != http.StatusOK {
		t.Errorf("plaintext list with E2EE off = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec := getWithAuth(srv, "/api/v1/me/conversations", adaTok); rec.Code != http.StatusOK {
		t.Errorf("conversation list with E2EE off = %d", rec.Code)
	}

	// The encrypted surface is off.
	for _, r := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/e2ee/devices", `{"device_name":"x","identity_key":"i","signing_key":"s"}`},
		{http.MethodGet, "/api/v1/e2ee/devices", ""},
		{http.MethodGet, "/api/v1/users/" + bobID + "/e2ee/devices", ""},
		// Creating a NEW encrypted conversation, and sending envelopes into an
		// existing one, both ride the shared /conversations routes, so those
		// gates live in the handler branches rather than on the routes.
		{http.MethodPost, "/api/v1/conversations", `{"recipient_id":"` + bobID + `","encrypted":true}`},
		{http.MethodPost, "/api/v1/conversations/" + encConv.ID + "/messages", envelopeBody},
	} {
		assertFeatureDisabled(t, srv, r.method, r.path, r.body, adaTok)
	}

	// Existing encrypted history stays READABLE: turning E2EE off stops new
	// encrypted traffic, it does not strand what participants already hold.
	if rec := getWithAuth(srv, "/api/v1/conversations/"+encConv.ID+"/messages", adaTok); rec.Code != http.StatusOK {
		t.Errorf("reading an existing encrypted conversation with E2EE off = %d; body=%s", rec.Code, rec.Body.String())
	}

	f := publicInstance(t, srv).Features
	if !f.Messaging {
		t.Error("GET /instance features.messaging = false; turning E2EE off must not disable messaging")
	}
	if f.MessagingE2EE {
		t.Error("GET /instance features.messaging_e2ee = true after disabling E2EE")
	}
}

// TestMessagingTogglesDefaultOn is the counter-test: with both defaults
// untouched — the state of an instance upgrading past this change — every
// messaging and E2EE route behaves exactly as it does today. An over-broad
// gate that disabled messaging by default would be a far worse bug than the
// one this change fixes.
func TestMessagingTogglesDefaultOn(t *testing.T) {
	srv := videoServer(t)
	adaTok, _ := registerAndUser(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	bobTok, bobID := registerAndUser(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	// Plaintext DM round trip.
	convID := startPlainConversation(t, srv, adaTok, bobID)
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/conversations/"+convID+"/messages", `{"body":"hello"}`, adaTok); rec.Code != http.StatusCreated {
		t.Fatalf("default send = %d; body=%s", rec.Code, rec.Body.String())
	}
	for _, p := range []string{"/api/v1/me/conversations", "/api/v1/conversations/" + convID + "/messages", "/api/v1/me/messaging-prefs"} {
		if rec := getWithAuth(srv, p, adaTok); rec.Code != http.StatusOK {
			t.Errorf("default GET %s = %d; body=%s", p, rec.Code, rec.Body.String())
		}
	}

	// E2EE round trip: devices, prekeys, an encrypted conversation.
	adaDev := registerE2EEDevice(t, srv, adaTok, "ada-laptop")
	bobDev := registerE2EEDevice(t, srv, bobTok, "bob-phone")
	uploadOTKs(t, srv, bobTok, bobDev, 2)
	if rec := getWithAuth(srv, "/api/v1/users/"+bobID+"/e2ee/devices", adaTok); rec.Code != http.StatusOK {
		t.Errorf("default peer device list = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec := getWithAuth(srv, "/api/v1/e2ee/devices/"+adaDev+"/one-time-keys/count", adaTok); rec.Code != http.StatusOK {
		t.Errorf("default OTK count = %d", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/conversations", `{"recipient_id":"`+bobID+`","encrypted":true}`, adaTok); rec.Code != http.StatusCreated {
		t.Fatalf("default encrypted conversation = %d; body=%s", rec.Code, rec.Body.String())
	}

	// Disclosure agrees: both features on out of the box.
	f := publicInstance(t, srv).Features
	if !f.Messaging || !f.MessagingE2EE {
		t.Errorf("GET /instance features = messaging:%v messaging_e2ee:%v, want both true by default", f.Messaging, f.MessagingE2EE)
	}

	// The admin view shows both keys present, on, and NOT overridden — the
	// upgrade state, not a migration-written row.
	got := instanceSettings(t, srv, adaTok)
	for _, key := range []string{instancesettings.KeyMessagingEnabled, instancesettings.KeyMessagingE2EEEnabled} {
		v := settingView(t, got, key)
		if v.Value != true || v.Overridden {
			t.Errorf("%s default view = %+v, want value=true overridden=false", key, v)
		}
	}
}
