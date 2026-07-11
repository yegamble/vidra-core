package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/instancesettings"
	"github.com/vidra/vidra-core/internal/ratelimit"
)

func TestInstanceEndpoint(t *testing.T) {
	srv := New(testConfig(), nil, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/instance", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body instanceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Name != "Vidra Test" {
		t.Errorf("name = %q, want Vidra Test", body.Name)
	}
	if body.Software.Name != "vidra" {
		t.Errorf("software.name = %q, want vidra", body.Software.Name)
	}
	if !body.RegistrationEnabled {
		t.Error("registration_enabled = false, want true (testConfig default)")
	}
	// Feature toggles default on, so the frontend can gate affordances in
	// lock-step with backend enforcement (fix_plan P10 instance features).
	if !body.Features.Uploads || !body.Features.Imports || !body.Features.Live ||
		!body.Features.Comments || !body.Features.Downloads {
		t.Errorf("features = %+v, want all enabled by default", body.Features)
	}
}

func TestInstanceAboutMetadata(t *testing.T) {
	cfg := testConfig()
	cfg.InstanceDescription = "A friendly instance"
	cfg.InstanceTermsURL = "https://example.test/terms"
	cfg.InstancePrivacyURL = "https://example.test/privacy"
	cfg.InstanceContactEmail = "admin@example.test"
	srv := New(cfg, nil, nil)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/instance", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body instanceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Description != "A friendly instance" || body.TermsURL != "https://example.test/terms" ||
		body.PrivacyURL != "https://example.test/privacy" || body.ContactEmail != "admin@example.test" {
		t.Errorf("unexpected about metadata: %+v", body)
	}
}

func TestInstancePlatformInfoOverlayAndAbout(t *testing.T) {
	cfg := testConfig()
	cfg.InstanceDescription = "Config description"
	settingssvc := instancesettings.NewService(newInstanceSettingsFakeRepo(), settingsDefaultsFromConfig(cfg))
	if err := settingssvc.Load(context.Background()); err != nil {
		t.Fatalf("settings load: %v", err)
	}
	if err := settingssvc.Apply(context.Background(), map[string]instancesettings.Update{
		instancesettings.KeyInstanceShortDescription: {Value: "Short and bright"},
		instancesettings.KeyDefaultLanguage:          {Value: "fr"},
		instancesettings.KeyInstanceCategories:       {Value: instancesettings.FormatList([]string{"1", "7"})},
		instancesettings.KeyModeratorLanguages:       {Value: instancesettings.FormatList([]string{"en", "es"})},
		instancesettings.KeyServerCountry:            {Value: "France"},
		instancesettings.KeyInstanceIsSensitive:      {Value: instancesettings.FormatBool(true)},
		instancesettings.KeySensitiveContentPolicy:   {Value: instancesettings.SensitiveContentPolicyWarn},
		instancesettings.KeyWebsiteLink:              {Value: "https://example.test"},
		instancesettings.KeyMastodonLink:             {Value: "https://social.example.test/@vidra"},
		instancesettings.KeyXLink:                    {Value: "https://x.com/vidra"},
		instancesettings.KeyBlueskyLink:              {Value: "https://bsky.app/profile/vidra.example.test"},
		instancesettings.KeyTerms:                    {Value: "## Terms"},
		instancesettings.KeyCodeOfConduct:            {Value: "Be kind."},
		instancesettings.KeyModerationInfo:           {Value: "Moderated daily."},
		instancesettings.KeyAdministratorInfo:        {Value: "A small team."},
		instancesettings.KeyCreationReason:           {Value: "To share videos."},
		instancesettings.KeyMaintenanceLifetime:      {Value: "Long term."},
		instancesettings.KeyBusinessModel:            {Value: "Donations."},
		instancesettings.KeyHardwareInfo:             {Value: "One server."},
		instancesettings.KeySupportText:              {Value: "Support us."},
	}, uuid.New()); err != nil {
		t.Fatalf("apply platform info: %v", err)
	}
	srv := New(cfg, nil, nil, WithSettingsService(settingssvc))

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/instance", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /instance = %d", rec.Code)
	}
	var body instanceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal instance: %v", err)
	}
	if body.ShortDescription != "Short and bright" || body.DefaultLanguage != "fr" ||
		body.ServerCountry != "France" || !body.IsSensitive ||
		body.SensitiveContentPolicy != "warn" {
		t.Errorf("platform instance fields = %+v", body)
	}
	if len(body.Categories) != 2 || body.Categories[0] != "1" || body.Categories[1] != "7" {
		t.Errorf("categories = %#v, want [1 7]", body.Categories)
	}
	if len(body.ModeratorLanguages) != 2 || body.ModeratorLanguages[0] != "en" || body.ModeratorLanguages[1] != "es" {
		t.Errorf("moderator_languages = %#v, want [en es]", body.ModeratorLanguages)
	}
	if body.SocialLinks.Website != "https://example.test" || body.SocialLinks.Mastodon == "" ||
		body.SocialLinks.X == "" || body.SocialLinks.Bluesky == "" {
		t.Errorf("social_links = %+v", body.SocialLinks)
	}
	if body.ContactFormEnabled {
		t.Error("contact_form_enabled = true without toggle/email/mailer, want false")
	}

	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/instance/about", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /instance/about = %d", rec.Code)
	}
	var about instanceAboutResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &about); err != nil {
		t.Fatalf("unmarshal about: %v", err)
	}
	if about.Description != "Config description" || about.Terms != "## Terms" ||
		about.CodeOfConduct != "Be kind." || about.SupportText != "Support us." {
		t.Errorf("about = %+v", about)
	}
}

func TestInstanceContactAvailabilitySendAndRateLimit(t *testing.T) {
	cfg := testConfig()
	settingssvc := instancesettings.NewService(newInstanceSettingsFakeRepo(), settingsDefaultsFromConfig(cfg))
	if err := settingssvc.Load(context.Background()); err != nil {
		t.Fatalf("settings load: %v", err)
	}
	capture := auth.NewCaptureMailer()
	srv := New(cfg, nil, nil, WithSettingsService(settingssvc), WithContactMailer(capture))

	validBody := `{"from_name":"Ada","from_email":"ada@example.test","subject":"Hello","body":"This is a friendly message."}`
	rec := postTo(srv, "/api/v1/instance/contact", validBody)
	if rec.Code != http.StatusConflict || errorCode(t, rec) != "contact_form_disabled" {
		t.Fatalf("contact disabled = %d code=%q, want 409 contact_form_disabled", rec.Code, errorCode(t, rec))
	}

	if err := settingssvc.Apply(context.Background(), map[string]instancesettings.Update{
		instancesettings.KeyContactEmail:        {Value: "admin@example.test"},
		instancesettings.KeyContactFormEnabled:  {Value: instancesettings.FormatBool(true)},
		instancesettings.KeyInstanceDescription: {Value: "mail-enabled instance"},
	}, uuid.New()); err != nil {
		t.Fatalf("enable contact form: %v", err)
	}
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/instance", nil))
	var inst instanceResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &inst)
	if !inst.ContactFormEnabled {
		t.Fatal("contact_form_enabled = false after toggle+email+mailer")
	}

	rec = postTo(srv, "/api/v1/instance/contact", validBody)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("contact send = %d; body=%s", rec.Code, rec.Body.String())
	}
	contacts := capture.Contacts()
	if len(contacts) != 1 {
		t.Fatalf("captured contacts = %d, want 1", len(contacts))
	}
	if got := contacts[0]; got.To != "admin@example.test" || got.FromEmail != "ada@example.test" ||
		got.FromName != "Ada" || got.Subject != "Hello" || got.Body != "This is a friendly message." {
		t.Errorf("captured contact = %+v", got)
	}

	limited := New(cfg, nil, nil,
		WithSettingsService(settingssvc),
		WithContactMailer(auth.NewCaptureMailer()),
		WithContactRateLimiter(ratelimit.NewLimiter(&fakeCounter{}, 1, time.Hour)),
	)
	if rec := postTo(limited, "/api/v1/instance/contact", validBody); rec.Code != http.StatusAccepted {
		t.Fatalf("first limited contact = %d; body=%s", rec.Code, rec.Body.String())
	}
	rec = postTo(limited, "/api/v1/instance/contact", validBody)
	if rec.Code != http.StatusTooManyRequests || errorCode(t, rec) != "rate_limited" {
		t.Fatalf("second limited contact = %d code=%q, want 429 rate_limited", rec.Code, errorCode(t, rec))
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("Retry-After missing on rate-limited contact form")
	}
}

func TestInstanceContactValidation(t *testing.T) {
	srv := New(testConfig(), nil, nil)
	rec := postTo(srv, "/api/v1/instance/contact", `{"from_name":"","from_email":"nope","subject":"","body":"short"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid contact = %d, want 422", rec.Code)
	}
	var er ErrorResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &er)
	if len(er.Error.Fields) < 4 {
		t.Errorf("field errors = %#v, want name/email/subject/body", er.Error.Fields)
	}
}

// registrationDisabledServer wires auth over the fake repo with registration off.
func registrationDisabledServer(t *testing.T) *Server {
	t.Helper()
	cfg := testConfig()
	cfg.RegistrationEnabled = false
	issuer := auth.NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", 15*time.Minute)
	svc := auth.NewService(newAuthFakeRepo(), issuer, 720*time.Hour)
	return New(cfg, nil, nil, WithAuthService(svc, 15*time.Minute))
}

func TestInstanceReflectsRegistrationDisabled(t *testing.T) {
	srv := registrationDisabledServer(t)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/instance", nil))
	var body instanceResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.RegistrationEnabled {
		t.Error("registration_enabled = true, want false")
	}
}

func TestRegisterForbiddenWhenDisabled(t *testing.T) {
	srv := registrationDisabledServer(t)
	rec := postTo(srv, "/api/v1/auth/register", `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	var er ErrorResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &er)
	if er.Error.Code != "forbidden" {
		t.Errorf("code = %q, want forbidden", er.Error.Code)
	}
}
