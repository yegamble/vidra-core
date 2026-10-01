package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/instancesettings"
)

// totpIssuerFor enrolls a fresh account against a server whose auth service
// carries the BOOT issuer "Boot Label" and whose config is cfg, after the
// admin overlay has renamed the instance to "Renamed", and returns the issuer
// the otpauth:// URI actually carries.
func totpIssuerFor(t *testing.T, pinned string) string {
	t.Helper()
	cfg := testConfig()
	cfg.TOTPIssuer = "Boot Label"
	if pinned != "" {
		cfg.TOTPIssuer = pinned
		cfg.TOTPIssuerPinned = true
	}
	issuer := auth.NewTokenIssuer(mfaTestSecret, "vidra", "vidra", 15*time.Minute)
	svc := auth.NewService(newAuthFakeRepo(), issuer, 720*time.Hour,
		auth.WithMFA(newMFAFakeRepo(), nil, cfg.TOTPIssuer))
	settings := instancesettings.NewService(newInstanceSettingsFakeRepo(), settingsDefaultsFromConfig(cfg))
	srv := New(cfg, nil, nil, WithAuthService(svc, 15*time.Minute), WithSettingsService(settings))
	if err := settings.Apply(context.Background(),
		map[string]instancesettings.Update{instancesettings.KeyInstanceName: {Value: "Renamed"}}, uuid.New()); err != nil {
		t.Fatalf("rename instance: %v", err)
	}

	token := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	rec := postJSONWithAuth(srv, "/api/v1/auth/mfa/totp", token, `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("enroll status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var enr totpEnrollmentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &enr); err != nil {
		t.Fatalf("unmarshal enrollment: %v", err)
	}
	u, err := url.Parse(enr.OtpauthURI)
	if err != nil {
		t.Fatalf("parse otpauth URI: %v", err)
	}
	return u.Query().Get("issuer")
}

// TestTOTPEnrollmentIssuerFollowsTheAdminOverlay: the issuer label is what an
// authenticator app displays beside the code. It used to be frozen at boot
// (cfg.TOTPIssuer defaulted to the env INSTANCE_NAME), so an admin who renamed
// the instance in the UI kept enrolling users under the old name until the
// next restart. Unset TOTP_ISSUER must follow the overlay; an operator-pinned
// TOTP_ISSUER must keep winning.
func TestTOTPEnrollmentIssuerFollowsTheAdminOverlay(t *testing.T) {
	if got := totpIssuerFor(t, ""); got != "Renamed" {
		t.Errorf("issuer with TOTP_ISSUER unset = %q, want the overlay name Renamed", got)
	}
	if got := totpIssuerFor(t, "Pinned"); got != "Pinned" {
		t.Errorf("issuer with TOTP_ISSUER pinned = %q, want Pinned", got)
	}
}
