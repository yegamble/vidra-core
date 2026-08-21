package config

import (
	"errors"
	"net"
	"strings"
	"testing"
)

// collectVarErrors walks a (possibly joined) error tree and returns every
// single-variable error in it, keyed by variable — the mapping a wizard/doctor
// does to attach messages to fields.
func collectVarErrors(err error) map[string]*VarError {
	found := map[string]*VarError{}
	var walk func(error)
	walk = func(e error) {
		if e == nil {
			return
		}
		if joined, ok := e.(interface{ Unwrap() []error }); ok {
			for _, sub := range joined.Unwrap() {
				walk(sub)
			}
			return
		}
		var ve *VarError
		if errors.As(e, &ve) {
			found[ve.Var] = ve
		}
	}
	walk(err)
	return found
}

// productionCandidate is a minimal generated production env — the shape the
// setup engine writes — used as the base for the candidate-map tests.
func productionCandidate() map[string]string {
	return map[string]string{
		"VIDRA_ENV":            "production",
		"JWT_SECRET":           strings.Repeat("k", 48),
		"DATABASE_URL":         "postgres://vidra:secret@postgres:5432/vidra?sslmode=disable",
		"REDIS_URL":            "redis://redis:6379/0",
		"PUBLIC_BASE_URL":      "https://videos.example",
		"CORS_ALLOWED_ORIGINS": "https://videos.example",
		"HTTP_PORT":            "8080",
	}
}

// The whole point of LoadFrom: the candidate environment is the ONLY thing read.
// The process environment here is deliberately full of values that would refuse
// to boot — if any read still went to os.LookupEnv, this would fail.
func TestLoadFromIgnoresTheProcessEnvironment(t *testing.T) {
	for k, v := range map[string]string{
		"VIDRA_ENV":                    "banana",
		"LOG_LEVEL":                    "loud",
		"HTTP_PORT":                    "eighty",
		"HTTP_READ_TIMEOUT":            "soon",
		"REGISTRATION_ENABLED":         "ture",
		"INSTANCE_DEFAULT_QUOTA_BYTES": "5G",
		"STORAGE_BACKEND":              "ipfs",
		"OAUTH_PROVIDERS":              "Not A Name",
		"MALWARE_SCAN_MODE":            "fail-sideways",
	} {
		t.Setenv(k, v)
	}

	cfg, err := LoadFrom(func(key string) (string, bool) {
		v, ok := productionCandidate()[key]
		return v, ok
	})
	if err != nil {
		t.Fatalf("LoadFrom(candidate) error = %v (a stray process-env read?)", err)
	}
	if cfg.Environment != "production" {
		t.Errorf("Environment = %q, want production (from the candidate)", cfg.Environment)
	}
	if cfg.HTTPPort != 8080 {
		t.Errorf("HTTPPort = %d, want 8080 (from the candidate)", cfg.HTTPPort)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want the info default (the candidate omits it)", cfg.LogLevel)
	}
	if len(cfg.OAuthProviders) != 0 {
		t.Errorf("OAuthProviders = %v, want none (the candidate omits OAUTH_PROVIDERS)", cfg.OAuthProviders)
	}

	// Load() itself must still read the process environment.
	if _, err := Load(); err == nil {
		t.Fatal("Load() expected an error from the malformed process environment, got nil")
	}
}

func TestLoadFromAppliesCandidateOverrides(t *testing.T) {
	vars := productionCandidate()
	vars["HTTP_PORT"] = "8443"
	vars["HTTP_READ_TIMEOUT"] = "45s"
	vars["INSTANCE_NAME"] = "Videos"
	cfg, err := LoadFrom(func(key string) (string, bool) {
		v, ok := vars[key]
		return v, ok
	})
	if err != nil {
		t.Fatalf("LoadFrom() error = %v", err)
	}
	if cfg.HTTPPort != 8443 {
		t.Errorf("HTTPPort = %d, want 8443", cfg.HTTPPort)
	}
	if cfg.HTTPReadTimeout.String() != "45s" {
		t.Errorf("HTTPReadTimeout = %v, want 45s", cfg.HTTPReadTimeout)
	}
	if cfg.InstanceName != "Videos" {
		t.Errorf("InstanceName = %q, want Videos", cfg.InstanceName)
	}
	if cfg.TOTPIssuer != "Videos" {
		t.Errorf("TOTPIssuer = %q, want it to default to the instance name", cfg.TOTPIssuer)
	}
}

func TestCheckEnvAcceptsAGeneratedProductionEnv(t *testing.T) {
	if err := CheckEnv(productionCandidate()); err != nil {
		t.Fatalf("CheckEnv(production candidate) error = %v", err)
	}
}

// A candidate with several bad variables must report ALL of them, each
// extractable with its variable name — the wizard renders one message per field.
func TestCheckEnvReportsEveryBadVarAttributed(t *testing.T) {
	vars := productionCandidate()
	vars["HTTP_PORT"] = "eighty"
	vars["HTTP_READ_TIMEOUT"] = "soon"
	vars["REGISTRATION_ENABLED"] = "ture"
	vars["INSTANCE_DEFAULT_QUOTA_BYTES"] = "5G"

	err := CheckEnv(vars)
	if err == nil {
		t.Fatal("CheckEnv() expected an error for four malformed variables, got nil")
	}
	got := collectVarErrors(err)
	for _, key := range []string{"HTTP_PORT", "HTTP_READ_TIMEOUT", "REGISTRATION_ENABLED", "INSTANCE_DEFAULT_QUOTA_BYTES"} {
		ve, ok := got[key]
		if !ok {
			t.Errorf("no VarError for %s; error tree = %v", key, err)
			continue
		}
		if !strings.Contains(ve.Error(), key) {
			t.Errorf("VarError for %s reads %q; the message must still name the variable", key, ve)
		}
		if !strings.Contains(err.Error(), ve.Error()) {
			t.Errorf("joined error %q is missing the %s message %q", err, key, ve)
		}
	}
}

// Semantic (validate()) failures attributable to one variable carry the same
// attribution, with the boot-time message byte-for-byte unchanged.
func TestCheckEnvAttributesValidationErrors(t *testing.T) {
	cases := []struct {
		name string
		key  string
		val  string
		want string
	}{
		{
			name: "invalid enum",
			key:  "LOG_LEVEL",
			val:  "loud",
			want: `config: invalid LOG_LEVEL "loud" (want debug|info|warn|error)`,
		},
		{
			name: "out of range",
			key:  "HTTP_PORT",
			val:  "70000",
			want: "config: HTTP_PORT 70000 out of range",
		},
		{
			name: "wrapped parse failure",
			key:  "UPLOAD_MAX_SIZE",
			val:  "2 gigabytes",
			want: `config: invalid UPLOAD_MAX_SIZE "2 gigabytes"`,
		},
		{
			name: "dev escape hatch in production",
			key:  "DEV_MAIL_CAPTURE_ENABLED",
			val:  "true",
			want: "config: DEV_MAIL_CAPTURE_ENABLED must not be set in production",
		},
		{
			name: "required by a feature toggle",
			key:  "SMTP_HOST",
			val:  "",
			want: "config: SMTP_HOST is required when MAIL_ENABLED=true",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vars := productionCandidate()
			if tc.key == "SMTP_HOST" {
				vars["MAIL_ENABLED"] = "true"
			}
			vars[tc.key] = tc.val

			err := CheckEnv(vars)
			if err == nil {
				t.Fatalf("CheckEnv() expected an error for %s=%q, got nil", tc.key, tc.val)
			}
			var ve *VarError
			if !errors.As(err, &ve) {
				t.Fatalf("error %v is not a *VarError", err)
			}
			if ve.Var != tc.key {
				t.Errorf("VarError.Var = %q, want %q", ve.Var, tc.key)
			}
			if !strings.HasPrefix(ve.Error(), tc.want) {
				t.Errorf("VarError.Error() = %q, want it to start with %q", ve, tc.want)
			}
		})
	}
}

// Per-provider OAuth variables are named dynamically; attribution must name the
// variable the operator actually has to add, not the OAUTH_%s pattern.
func TestCheckEnvAttributesPerProviderOAuthVars(t *testing.T) {
	vars := productionCandidate()
	vars["OAUTH_PROVIDERS"] = "my-idp"
	vars["OAUTH_MY_IDP_ISSUER"] = "https://idp.example"
	vars["OAUTH_MY_IDP_CLIENT_ID"] = "vidra"

	err := CheckEnv(vars)
	if err == nil {
		t.Fatal("CheckEnv() expected an error for the missing client secret, got nil")
	}
	var ve *VarError
	if !errors.As(err, &ve) {
		t.Fatalf("error %v is not a *VarError", err)
	}
	if ve.Var != "OAUTH_MY_IDP_CLIENT_SECRET" {
		t.Errorf("VarError.Var = %q, want OAUTH_MY_IDP_CLIENT_SECRET", ve.Var)
	}
	if want := "config: OAUTH_MY_IDP_CLIENT_SECRET is required"; ve.Error() != want {
		t.Errorf("VarError.Error() = %q, want %q", ve, want)
	}

	vars["OAUTH_MY_IDP_CLIENT_SECRET"] = "s3cret"
	if err := CheckEnv(vars); err != nil {
		t.Fatalf("CheckEnv() error = %v once the provider is complete", err)
	}
}

// Rules that span variables belong to no single field and stay unattributed on
// purpose — a wizard shows them as instance-level errors, not field errors.
func TestCheckEnvLeavesMultiVarRulesUnattributed(t *testing.T) {
	vars := productionCandidate()
	vars["IPFS_ENABLED"] = "true"
	err := CheckEnv(vars)
	if err == nil {
		t.Fatal("CheckEnv() expected an error for IPFS_ENABLED without URLs, got nil")
	}
	var ve *VarError
	if errors.As(err, &ve) {
		t.Errorf("multi-variable rule was attributed to %q: %v", ve.Var, err)
	}
	if want := "config: IPFS_API_URL and IPFS_GATEWAY_URL are required when IPFS_ENABLED"; err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
}

// Keys this package does not know (compose-only variables, operator notes) are
// never looked up, so a whole generated env file can be handed over as-is.
func TestCheckEnvIgnoresUnknownKeys(t *testing.T) {
	vars := productionCandidate()
	vars["VIDRA_CORE_TAG"] = "v0.1.1"
	vars["VIDRA_IMAGE_OWNER"] = "yegamble"
	vars["POSTGRES_PASSWORD"] = "not-a-core-variable"
	vars["COMPLETELY_MADE_UP"] = "!!! not a duration !!!"
	if err := CheckEnv(vars); err != nil {
		t.Fatalf("CheckEnv() error = %v; unknown keys must be ignored", err)
	}
}

// Same contract as Load: a key present but empty means "unset", so `KEY=` in a
// generated file is the same as omitting it.
func TestCheckEnvEmptyValueMeansUnset(t *testing.T) {
	vars := productionCandidate()
	for _, k := range []string{"HTTP_PORT", "HTTP_READ_TIMEOUT", "REGISTRATION_ENABLED", "INSTANCE_DEFAULT_QUOTA_BYTES", "LOG_LEVEL", "STORAGE_BACKEND"} {
		vars[k] = ""
	}
	if err := CheckEnv(vars); err != nil {
		t.Fatalf("CheckEnv() error = %v; empty values must mean unset", err)
	}

	cfg, err := LoadFrom(func(key string) (string, bool) {
		v, ok := vars[key]
		return v, ok
	})
	if err != nil {
		t.Fatalf("LoadFrom() error = %v", err)
	}
	if cfg.HTTPPort != 8080 {
		t.Errorf("HTTPPort = %d, want the 8080 default", cfg.HTTPPort)
	}
	if !cfg.RegistrationEnabled {
		t.Error("RegistrationEnabled should fall back to the true default")
	}
	if cfg.StorageBackend != "local" {
		t.Errorf("StorageBackend = %q, want the local default", cfg.StorageBackend)
	}
}

// A missing key is unset too: the empty candidate must be exactly the dev
// defaults (which are valid), not a pile of "required" errors.
func TestCheckEnvEmptyCandidateIsTheDevelopmentDefault(t *testing.T) {
	if err := CheckEnv(nil); err != nil {
		t.Fatalf("CheckEnv(nil) error = %v", err)
	}
	cfg, err := LoadFrom(func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("LoadFrom(empty) error = %v", err)
	}
	if cfg.Environment != "development" {
		t.Errorf("Environment = %q, want development", cfg.Environment)
	}
}

// The plain-http gate, from both sides. A production env file whose origin is
// http:// is the typo that would silently drop Secure cookies and HSTS on the
// one deployment where they matter, so it refuses to boot — and the refusal has
// to name BOTH variables an operator needs, because setup.Check runs this rule
// with production forced and the operator reading the message may be installing
// a lab instance on purpose.
func TestCheckEnvRefusesPlainHTTPOriginWithoutConsent(t *testing.T) {
	vars := productionCandidate()
	vars["PUBLIC_BASE_URL"] = "http://videos.internal"
	vars["CORS_ALLOWED_ORIGINS"] = "http://videos.internal"

	err := CheckEnv(vars)
	if err == nil {
		t.Fatal("CheckEnv() accepted a plain-http origin in production without VIDRA_ALLOW_PLAIN_HTTP")
	}
	var ve *VarError
	if !errors.As(err, &ve) {
		t.Fatalf("error %v is not a *VarError", err)
	}
	if ve.Var != "PUBLIC_BASE_URL" {
		t.Errorf("VarError.Var = %q, want PUBLIC_BASE_URL — that is the value to fix", ve.Var)
	}
	for _, want := range []string{"VIDRA_ALLOW_PLAIN_HTTP=true", "VIDRA_TLS_MODE=plain-http"} {
		if !strings.Contains(ve.Error(), want) {
			t.Errorf("refusal %q does not name %s, so an operator who MEANT plain http cannot act on it", ve, want)
		}
	}

	// With the consent, the same file boots — and the https-derived defaults go
	// off together.
	vars["VIDRA_ALLOW_PLAIN_HTTP"] = "true"
	cfg, err := LoadFrom(func(key string) (string, bool) {
		v, ok := vars[key]
		return v, ok
	})
	if err != nil {
		t.Fatalf("LoadFrom() error = %v with VIDRA_ALLOW_PLAIN_HTTP=true", err)
	}
	if cfg.PublicOriginIsHTTPS() {
		t.Error("PublicOriginIsHTTPS() = true for an http:// origin")
	}
	if cfg.CookieSecure() {
		t.Error("CookieSecure() = true on a consented plain-http deployment: the browser would never send the cookie back")
	}
}

// An https origin is unaffected by the consent flag either way: the scheme
// already answered the question, and a deployment that set the flag "just in
// case" must not lose Secure cookies for it.
func TestCheckEnvHTTPSOriginIsUnaffectedByPlainHTTPConsent(t *testing.T) {
	vars := productionCandidate()
	vars["VIDRA_ALLOW_PLAIN_HTTP"] = "true"
	cfg, err := LoadFrom(func(key string) (string, bool) {
		v, ok := vars[key]
		return v, ok
	})
	if err != nil {
		t.Fatalf("LoadFrom() error = %v", err)
	}
	if !cfg.PublicOriginIsHTTPS() || !cfg.CookieSecure() {
		t.Error("an https origin must stay https-derived regardless of VIDRA_ALLOW_PLAIN_HTTP")
	}
}

// TRUSTED_PROXY_CIDRS hands addresses the power to forge a client IP, so a value
// that does not parse must refuse to boot rather than trust nothing quietly: the
// symptom of the silent version is every visitor behind the proxy sharing one
// rate-limit bucket, which looks like an outage, not like a typo.
func TestCheckEnvRejectsMalformedTrustedProxyCIDRs(t *testing.T) {
	for _, bad := range []string{"203.0.113.7", "not-a-network", "203.0.113.0/33"} {
		vars := productionCandidate()
		vars["TRUSTED_PROXY_CIDRS"] = "10.0.0.0/8," + bad
		err := CheckEnv(vars)
		if err == nil {
			t.Errorf("CheckEnv() accepted TRUSTED_PROXY_CIDRS entry %q", bad)
			continue
		}
		var ve *VarError
		if !errors.As(err, &ve) {
			t.Errorf("error %v for %q is not a *VarError", err, bad)
			continue
		}
		if ve.Var != "TRUSTED_PROXY_CIDRS" {
			t.Errorf("VarError.Var = %q for %q, want TRUSTED_PROXY_CIDRS", ve.Var, bad)
		}
		if !strings.Contains(ve.Error(), bad) {
			t.Errorf("refusal %q does not quote the offending entry %q", ve, bad)
		}
	}
}

// The parsed networks are what the server hands echo.TrustIPRange, so the list
// has to survive the round trip in order, blanks and stray spaces included (the
// env file is written by hand as often as it is generated).
func TestTrustedProxyCIDRsParseIntoNetworks(t *testing.T) {
	vars := productionCandidate()
	vars["TRUSTED_PROXY_CIDRS"] = " 203.0.113.7/32 , 2001:db8::/32 ,"
	cfg, err := LoadFrom(func(key string) (string, bool) {
		v, ok := vars[key]
		return v, ok
	})
	if err != nil {
		t.Fatalf("LoadFrom() error = %v", err)
	}
	nets := cfg.TrustedProxyNets()
	if len(nets) != 2 {
		t.Fatalf("TrustedProxyNets() = %v, want the two networks", nets)
	}
	if got := nets[0].String(); got != "203.0.113.7/32" {
		t.Errorf("first network = %q, want 203.0.113.7/32", got)
	}
	if !nets[1].Contains(net.ParseIP("2001:db8::1")) {
		t.Errorf("second network %v does not contain 2001:db8::1", nets[1])
	}
	// Unset is the default and must trust nothing extra.
	if len(cfg.TrustedProxyCIDRs) != 2 {
		t.Errorf("TrustedProxyCIDRs = %v, want the two raw entries", cfg.TrustedProxyCIDRs)
	}
}
