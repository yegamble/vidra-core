package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/vidra/vidra-core/internal/auth"
)

// patchSettings PATCHes the admin instance-settings overlay and asserts 200.
func patchSettings(t *testing.T, srv *Server, adminTok, body string) {
	t.Helper()
	rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/admin/instance-settings", body, adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH instance-settings %s = %d; body=%s", body, rec.Code, rec.Body.String())
	}
}

// TestRegistrationEmailVerificationGate drives the W7 verification hold over
// HTTP: gated signup answers 202 verification_pending (no session), login is
// 403 email_verification_required until the captured token is confirmed, and
// pre-gate accounts are grandfathered.
func TestRegistrationEmailVerificationGate(t *testing.T) {
	// A wired contact mailer marks the deployment mail-capable (features.mail),
	// which the effective verification gate requires.
	srv, _, _, _, _ := videoServerFullWith(t, testConfig(), []Option{WithContactMailer(auth.NewCaptureMailer())})

	adminTok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	patchSettings(t, srv, adminTok, `{"registration_require_email_verification": true}`)

	// The public document reports the EFFECTIVE gate.
	if inst := publicInstance(t, srv); !inst.RegistrationRequiresEmailVerification {
		t.Fatalf("registration_requires_email_verification = false, want true")
	}

	// Gated signup: 202 verification_pending, no tokens revealed.
	rec := postTo(srv, "/api/v1/auth/register", `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("gated register = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	var pending map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &pending)
	if pending["status"] != "verification_pending" {
		t.Fatalf("gated register status = %q, want verification_pending", pending["status"])
	}

	// Login is refused with the typed code while unverified.
	rec = postTo(srv, "/api/v1/auth/login", `{"email":"bob@example.test","password":"supersecret"}`)
	if rec.Code != http.StatusForbidden || errorCode(t, rec) != "email_verification_required" {
		t.Fatalf("held login = %d code=%s, want 403 email_verification_required", rec.Code, rec.Body.String())
	}

	// Grandfather clause: ada registered BEFORE the toggle flipped and is
	// unverified too — she is never retroactively locked.
	rec = postTo(srv, "/api/v1/auth/login", `{"email":"ada@example.test","password":"supersecret"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("grandfathered login = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	// Complete the round trip with the captured token.
	raw, ok := captureMailerBySrv[srv].Latest(auth.TokenKindEmailVerification, "bob@example.test")
	if !ok {
		t.Fatalf("no verification token captured for bob")
	}
	if rec := postTo(srv, "/api/v1/auth/verify-email/confirm", `{"token":"`+raw+`"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("verify-email confirm = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec := postTo(srv, "/api/v1/auth/login", `{"email":"bob@example.test","password":"supersecret"}`); rec.Code != http.StatusOK {
		t.Fatalf("post-verification login = %d; body=%s", rec.Code, rec.Body.String())
	}
}

// TestRegistrationVerificationGateInertWithoutMail proves the setting alone
// cannot hold accounts: without an outbound mail path the effective gate is
// off, /instance reports false, and signup answers a normal 201.
func TestRegistrationVerificationGateInertWithoutMail(t *testing.T) {
	srv := videoServer(t) // no contact mailer wired
	adminTok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	patchSettings(t, srv, adminTok, `{"registration_require_email_verification": true}`)

	if inst := publicInstance(t, srv); inst.RegistrationRequiresEmailVerification {
		t.Fatalf("effective verification gate = true without a mailer, want false")
	}
	rec := postTo(srv, "/api/v1/auth/register", `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register with inert gate = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
}

// TestApprovalThenVerificationComposition proves both gates can be on: the
// signup queues for approval first, approval creates the account HELD pending
// verification, and the emailed token releases it.
func TestApprovalThenVerificationComposition(t *testing.T) {
	srv, _, _, _, _ := videoServerFullWith(t, testConfig(), []Option{WithContactMailer(auth.NewCaptureMailer())})
	adminTok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	patchSettings(t, srv, adminTok, `{"registration_require_approval": true, "registration_require_email_verification": true}`)

	// Approval wins the branch: the signup files a pending request (202 pending).
	rec := postTo(srv, "/api/v1/auth/register", `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("register = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	var st map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &st)
	if st["status"] != "pending" {
		t.Fatalf("status = %q, want pending (approval first)", st["status"])
	}

	// Approve → account exists but is held pending verification.
	list := sendJSONAuth(srv, http.MethodGet, "/api/v1/admin/registration-requests?status=pending", "", adminTok)
	var queue registrationRequestListResponse
	_ = json.Unmarshal(list.Body.Bytes(), &queue)
	if len(queue.Requests) != 1 {
		t.Fatalf("pending queue = %d entries, want 1", len(queue.Requests))
	}
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/registration-requests/"+queue.Requests[0].ID+"/approve", "", adminTok); rec.Code != http.StatusNoContent {
		t.Fatalf("approve = %d; body=%s", rec.Code, rec.Body.String())
	}
	rec = postTo(srv, "/api/v1/auth/login", `{"email":"bob@example.test","password":"supersecret"}`)
	if rec.Code != http.StatusForbidden || errorCode(t, rec) != "email_verification_required" {
		t.Fatalf("approved-but-unverified login = %d, want 403 email_verification_required", rec.Code)
	}
	raw, ok := captureMailerBySrv[srv].Latest(auth.TokenKindEmailVerification, "bob@example.test")
	if !ok {
		t.Fatalf("no verification token captured on approval")
	}
	if rec := postTo(srv, "/api/v1/auth/verify-email/confirm", `{"token":"`+raw+`"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("verify confirm = %d", rec.Code)
	}
	if rec := postTo(srv, "/api/v1/auth/login", `{"email":"bob@example.test","password":"supersecret"}`); rec.Code != http.StatusOK {
		t.Fatalf("post-verification login = %d", rec.Code)
	}
}

// TestRegistrationMinimumAgeAttestation drives the PT-style age attestation:
// while registration_minimum_age is active, signup without the flag is a 422
// field error and with it succeeds. No birthdate is ever collected.
func TestRegistrationMinimumAgeAttestation(t *testing.T) {
	srv := videoServer(t)
	adminTok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	patchSettings(t, srv, adminTok, `{"registration_minimum_age": 16}`)

	if inst := publicInstance(t, srv); inst.RegistrationMinimumAge != 16 {
		t.Fatalf("registration_minimum_age = %d, want 16", inst.RegistrationMinimumAge)
	}

	rec := postTo(srv, "/api/v1/auth/register", `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("register without attestation = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	var env ErrorResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if len(env.Error.Fields) != 1 || env.Error.Fields[0].Field != "age_attestation" {
		t.Fatalf("fields = %+v, want one age_attestation error", env.Error.Fields)
	}

	rec = postTo(srv, "/api/v1/auth/register", `{"username":"bob","email":"bob@example.test","password":"supersecret","age_attestation":true}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register with attestation = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}

	// Clearing the setting (null-PATCH) disables the requirement again.
	patchSettings(t, srv, adminTok, `{"registration_minimum_age": null}`)
	rec = postTo(srv, "/api/v1/auth/register", `{"username":"cal","email":"cal@example.test","password":"supersecret"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register after clearing minimum age = %d, want 201", rec.Code)
	}
}

// TestRegistrationUserLimit drives count-and-refuse plus the /instance
// effective registration_enabled + reason surface.
func TestRegistrationUserLimit(t *testing.T) {
	srv := videoServer(t)
	adminTok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	// Limit 1 with 1 existing account: signup refused, /instance explains why.
	patchSettings(t, srv, adminTok, `{"registration_user_limit": 1}`)
	rec := postTo(srv, "/api/v1/auth/register", `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("register at limit = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	inst := publicInstance(t, srv)
	if inst.RegistrationEnabled || inst.RegistrationDisabledReason != "user_limit_reached" {
		t.Fatalf("instance at limit = enabled=%v reason=%q, want false/user_limit_reached",
			inst.RegistrationEnabled, inst.RegistrationDisabledReason)
	}

	// Raising the limit reopens signup immediately (fresh count, reason clears).
	patchSettings(t, srv, adminTok, `{"registration_user_limit": 2}`)
	inst = publicInstance(t, srv)
	if !inst.RegistrationEnabled || inst.RegistrationDisabledReason != "" {
		t.Fatalf("instance under limit = enabled=%v reason=%q, want true/\"\"", inst.RegistrationEnabled, inst.RegistrationDisabledReason)
	}
	if rec := postTo(srv, "/api/v1/auth/register", `{"username":"bob","email":"bob@example.test","password":"supersecret"}`); rec.Code != http.StatusCreated {
		t.Fatalf("register under raised limit = %d, want 201", rec.Code)
	}
	// The successful signup invalidated the cached count: limit 2 is now full.
	if rec := postTo(srv, "/api/v1/auth/register", `{"username":"cal","email":"cal@example.test","password":"supersecret"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("register after filling limit = %d, want 403", rec.Code)
	}

	// 0 = unlimited (null-PATCH back to the default).
	patchSettings(t, srv, adminTok, `{"registration_user_limit": null}`)
	if rec := postTo(srv, "/api/v1/auth/register", `{"username":"cal","email":"cal@example.test","password":"supersecret"}`); rec.Code != http.StatusCreated {
		t.Fatalf("register with unlimited = %d, want 201", rec.Code)
	}
}

// TestNewUserHistorySeedAndGate drives the per-user history preference: the
// new_user_history_enabled seed at creation, the PATCH /auth/me toggle, and
// the watch-progress write gate honoring it.
func TestNewUserHistorySeedAndGate(t *testing.T) {
	srv := videoServer(t)
	adaTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	vid := createPublishedVideo(t, srv, adaTok, "ada", `{"title":"clip","privacy":"public"}`)

	// ada was created with the default seed (true).
	me := getWithAuth(srv, "/api/v1/auth/me", adaTok)
	var adaView userView
	_ = json.Unmarshal(me.Body.Bytes(), &adaView)
	if !adaView.HistoryEnabled {
		t.Fatalf("default-seeded account history_enabled = false, want true")
	}

	// Flip the seed: accounts created from now on start with history OFF.
	patchSettings(t, srv, adaTok, `{"new_user_history_enabled": false}`)
	bobTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	me = getWithAuth(srv, "/api/v1/auth/me", bobTok)
	var bobView userView
	_ = json.Unmarshal(me.Body.Bytes(), &bobView)
	if bobView.HistoryEnabled {
		t.Fatalf("seeded-off account history_enabled = true, want false")
	}
	// Existing accounts are untouched by the seed change.
	me = getWithAuth(srv, "/api/v1/auth/me", adaTok)
	_ = json.Unmarshal(me.Body.Bytes(), &adaView)
	if !adaView.HistoryEnabled {
		t.Fatalf("existing account history_enabled flipped by the seed setting")
	}

	// History gate: with the preference off, watch-progress writes are no-ops.
	if rec := putProgress(srv, vid, `{"position_seconds":42}`, bobTok); rec.Code != http.StatusNoContent {
		t.Fatalf("gated put progress = %d, want 204", rec.Code)
	}
	if got := historyTitles(t, listHistory(srv, bobTok)); len(got) != 0 {
		t.Fatalf("history while disabled = %v, want empty", got)
	}

	// The user turns history back on via PATCH /auth/me; writes resume.
	rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/auth/me", `{"history_enabled": true}`, bobTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH /auth/me history_enabled = %d; body=%s", rec.Code, rec.Body.String())
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &bobView)
	if !bobView.HistoryEnabled {
		t.Fatalf("PATCH response history_enabled = false, want true")
	}
	if rec := putProgress(srv, vid, `{"position_seconds":42}`, bobTok); rec.Code != http.StatusNoContent {
		t.Fatalf("put progress = %d, want 204", rec.Code)
	}
	if got := historyTitles(t, listHistory(srv, bobTok)); len(got) != 1 {
		t.Fatalf("history after re-enable = %v, want 1 entry", got)
	}
}

// TestDailyQuotaGateAndStatus drives the rolling daily upload quota over HTTP:
// GET /me/quota daily fields, enforcement on the direct upload and the
// resumable-session gate (422 daily_quota_exceeded), the exact boundary, and
// the null-PATCH reset back to unlimited.
func TestDailyQuotaGateAndStatus(t *testing.T) {
	srv := videoServer(t)
	adminTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")

	// Unlimited by default.
	st := getMyQuota(t, srv, adminTok)
	if st.DailyQuotaBytes != nil || st.DailyUsedBytes != 0 {
		t.Fatalf("default daily quota = %+v, want null/0", st)
	}

	patchSettings(t, srv, adminTok, `{"default_user_daily_quota_bytes": 15}`)
	st = getMyQuota(t, srv, adminTok)
	if st.DailyQuotaBytes == nil || *st.DailyQuotaBytes != 15 {
		t.Fatalf("daily quota = %+v, want 15", st.DailyQuotaBytes)
	}

	// A 10-byte upload lands and is accounted against the window.
	v1 := createVideo(t, srv, adminTok, "ada", `{"title":"one"}`)
	if rec := uploadVideoFile(srv, v1, "clip.mp4", "video/mp4", "ten__bytes", adminTok); rec.Code != http.StatusCreated {
		t.Fatalf("upload 1 = %d; body=%s", rec.Code, rec.Body.String())
	}
	st = getMyQuota(t, srv, adminTok)
	if st.DailyUsedBytes != 10 {
		t.Fatalf("daily used = %d, want 10", st.DailyUsedBytes)
	}

	// 10 more bytes exceed the remaining 5 → 422 daily_quota_exceeded on the
	// direct path AND the resumable-session gate.
	v2 := createVideo(t, srv, adminTok, "ada", `{"title":"two"}`)
	rec := uploadVideoFile(srv, v2, "clip.mp4", "video/mp4", "ten__bytes", adminTok)
	if rec.Code != http.StatusUnprocessableEntity || errorCode(t, rec) != "daily_quota_exceeded" {
		t.Fatalf("over-daily upload = %d code=%s, want 422 daily_quota_exceeded", rec.Code, rec.Body.String())
	}
	rec = sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+v2+"/upload-session", `{"size":10,"filename":"clip.mp4"}`, adminTok)
	if rec.Code != http.StatusUnprocessableEntity || errorCode(t, rec) != "daily_quota_exceeded" {
		t.Fatalf("over-daily session = %d code=%s, want 422 daily_quota_exceeded", rec.Code, rec.Body.String())
	}

	// The exact remaining 5 bytes still fit (boundary).
	if rec := uploadVideoFile(srv, v2, "clip.mp4", "video/mp4", "5byte", adminTok); rec.Code != http.StatusCreated {
		t.Fatalf("boundary upload = %d; body=%s", rec.Code, rec.Body.String())
	}

	// null-PATCH clears the override: unlimited again, uploads flow.
	patchSettings(t, srv, adminTok, `{"default_user_daily_quota_bytes": null}`)
	st = getMyQuota(t, srv, adminTok)
	if st.DailyQuotaBytes != nil {
		t.Fatalf("cleared daily quota = %v, want null", *st.DailyQuotaBytes)
	}
	v3 := createVideo(t, srv, adminTok, "ada", `{"title":"three"}`)
	if rec := uploadVideoFile(srv, v3, "clip.mp4", "video/mp4", "ten__bytes", adminTok); rec.Code != http.StatusCreated {
		t.Fatalf("post-clear upload = %d", rec.Code)
	}
}
