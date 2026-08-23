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

// Semantic failures are collected the same way malformed values are: an env file
// with several broken RULES reports all of them in one pass, each attributed, so
// an operator (or the wizard rendering one message per input) is not walked
// through a fix-one-rerun-discover-the-next loop.
func TestCheckEnvReportsEverySemanticFailureAttributed(t *testing.T) {
	vars := productionCandidate()
	vars["LOG_LEVEL"] = "loud"                                 // enum
	vars["HTTP_PORT"] = "70000"                                // range
	vars["JWT_SECRET"] = "too-short"                           // production floor
	vars["CORS_ALLOWED_ORIGINS"] = "*"                         // production refusal
	vars["MAIL_ENABLED"] = "true"                              // makes SMTP_HOST required
	vars["MALWARE_SCAN_MODE"] = "fail-sideways"                // enum
	vars["MFA_KEY_KEK"] = "not-base64"                         // key shape
	vars["WHISPER_DEFAULT_LANGUAGE"] = "not a language tag!!!" // pattern

	err := CheckEnv(vars)
	if err == nil {
		t.Fatal("CheckEnv() expected errors for eight broken rules, got nil")
	}
	got := collectVarErrors(err)
	for _, key := range []string{
		"LOG_LEVEL", "HTTP_PORT", "JWT_SECRET", "CORS_ALLOWED_ORIGINS",
		"SMTP_HOST", "MALWARE_SCAN_MODE", "MFA_KEY_KEK", "WHISPER_DEFAULT_LANGUAGE",
	} {
		ve, ok := got[key]
		if !ok {
			t.Errorf("no VarError for %s; error tree = %v", key, err)
			continue
		}
		if !strings.Contains(err.Error(), ve.Error()) {
			t.Errorf("joined error %q is missing the %s message %q", err, key, ve)
		}
	}
}

// A guard that fails must not spray the rules it was guarding. An unrecognised
// STORAGE_BACKEND selects NO branch, so the five s3 keys it does not have are
// not five more problems — they are the same problem, and reporting them sends
// the operator to fix the wrong lines.
func TestCheckEnvGuardedBranchesStaySilent(t *testing.T) {
	vars := productionCandidate()
	vars["STORAGE_BACKEND"] = "ipfs"

	err := CheckEnv(vars)
	if err == nil {
		t.Fatal("CheckEnv() accepted an unsupported STORAGE_BACKEND")
	}
	got := collectVarErrors(err)
	if _, ok := got["STORAGE_BACKEND"]; !ok {
		t.Fatalf("no VarError for STORAGE_BACKEND; error tree = %v", err)
	}
	for _, key := range []string{
		"STORAGE_S3_ENDPOINT", "STORAGE_S3_BUCKET", "STORAGE_S3_ACCESS_KEY",
		"STORAGE_S3_SECRET_KEY", "STORAGE_LOCAL_ROOT",
	} {
		if ve, ok := got[key]; ok {
			t.Errorf("unsupported backend also reported %s (%q); the branch was never taken", key, ve)
		}
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

// Collecting instead of returning early made a genuine duplicate possible for
// the first time: ONE http:// origin in production trips the plain-http gate,
// the federation https rule and the OAuth https rule, and the last two produce
// a byte-identical sentence. Nothing downstream dedupes — setup.configIssues
// turns every leaf into an Issue — so a duplicate reaches doctor and the wizard
// as two lines, and an operator goes looking for a second thing to fix.
func TestCheckEnvDeduplicatesIdenticalFindings(t *testing.T) {
	vars := productionCandidate()
	vars["PUBLIC_BASE_URL"] = "http://videos.internal"
	vars["CORS_ALLOWED_ORIGINS"] = "http://videos.internal"
	vars["FEDERATION_ENABLED"] = "true"
	vars["FEDERATION_KEY_KEK"] = strings.Repeat("A", 43) + "=" // base64 of exactly 32 bytes
	vars["OAUTH_PROVIDERS"] = "my-idp"
	vars["OAUTH_MY_IDP_ISSUER"] = "https://idp.example"
	vars["OAUTH_MY_IDP_CLIENT_ID"] = "vidra"
	vars["OAUTH_MY_IDP_CLIENT_SECRET"] = "s3cret"

	err := CheckEnv(vars)
	if err == nil {
		t.Fatal("CheckEnv() accepted a plain-http production origin with federation and OAuth on")
	}
	const httpsRule = "config: PUBLIC_BASE_URL must be https in production"
	if n := strings.Count(err.Error(), httpsRule); n != 1 {
		t.Errorf("the https rule appears %d times, want exactly 1:\n%s", n, err)
	}

	// Dedupe must collapse only IDENTICAL findings: the plain-http refusal is a
	// different sentence about the same variable and still has to be said, or
	// the operator never learns about VIDRA_ALLOW_PLAIN_HTTP.
	if !strings.Contains(err.Error(), "VIDRA_ALLOW_PLAIN_HTTP=true") {
		t.Errorf("the plain-http refusal was swallowed with the duplicate:\n%s", err)
	}
	if ve, ok := collectVarErrors(err)["PUBLIC_BASE_URL"]; !ok {
		t.Errorf("no VarError for PUBLIC_BASE_URL; tree = %v", err)
	} else if ve.Var != "PUBLIC_BASE_URL" {
		t.Errorf("VarError.Var = %q, want PUBLIC_BASE_URL", ve.Var)
	}
}

// The two passes stay separate, and nothing else guards it. A candidate with a
// MALFORMED value and a semantically-broken one must report only the malformed
// one: the semantic rules ran against the default the bad value fell back to, so
// reporting them would describe a value the operator never wrote.
func TestCheckEnvParseErrorsShortCircuitTheSemanticPass(t *testing.T) {
	vars := productionCandidate()
	vars["HTTP_PORT"] = "eighty" // malformed: never parses
	vars["LOG_LEVEL"] = "loud"   // semantic: parses fine, fails a rule

	err := CheckEnv(vars)
	if err == nil {
		t.Fatal("CheckEnv() expected an error, got nil")
	}
	got := collectVarErrors(err)
	if _, ok := got["HTTP_PORT"]; !ok {
		t.Errorf("no VarError for the malformed HTTP_PORT; tree = %v", err)
	}
	if ve, ok := got["LOG_LEVEL"]; ok {
		t.Errorf("the semantic pass ran anyway and reported %q; it must not run over values that never parsed", ve)
	}

	// With the typo fixed, the semantic failure surfaces on the next run — the
	// operator sees one class of problem at a time, in the order they can act on.
	vars["HTTP_PORT"] = "8080"
	err = CheckEnv(vars)
	if err == nil {
		t.Fatal("CheckEnv() accepted an invalid LOG_LEVEL once the port parsed")
	}
	if _, ok := collectVarErrors(err)["LOG_LEVEL"]; !ok {
		t.Errorf("no VarError for LOG_LEVEL once parsing was clean; tree = %v", err)
	}
}

// VIDRA_EXTERNAL_POSTGRES is read the way the deploy shell scripts read it, and
// is never a boot error. It governs one paragraph of backup advice on an admin
// page; refusing to start an instance over its spelling is not a trade anybody
// would choose, and `yes` is a spelling setup.Check accepts and does not flag.
func TestCheckEnvExternalPostgresFollowsTheShellSpellings(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"true", true},
		{"True", true},
		{"yes", true},
		{"on", true},
		{"1", true},
		{"false", false},
		{"no", false},
		{"", false},
		// The deploy scripts read these as FALSE. strconv.ParseBool reads `t` as
		// true and would have disagreed; a case-folding reader would disagree
		// about `tRue`. Either disagreement puts the managed-database backup
		// advice on a deployment whose own nightly dump is its only backup.
		{"t", false},
		{"tRue", false},
		{"banana", false},
	} {
		vars := productionCandidate()
		vars["VIDRA_EXTERNAL_POSTGRES"] = tc.value

		if err := CheckEnv(vars); err != nil {
			t.Errorf("CheckEnv() with VIDRA_EXTERNAL_POSTGRES=%q = %v; this key must never refuse a boot", tc.value, err)
			continue
		}
		cfg, err := LoadFrom(func(key string) (string, bool) {
			v, ok := vars[key]
			return v, ok
		})
		if err != nil {
			t.Errorf("LoadFrom() with VIDRA_EXTERNAL_POSTGRES=%q = %v", tc.value, err)
			continue
		}
		if cfg.ExternalPostgres != tc.want {
			t.Errorf("VIDRA_EXTERNAL_POSTGRES=%q read as %v, want %v (the deploy scripts' answer)", tc.value, cfg.ExternalPostgres, tc.want)
		}
	}
}

// The media-GC knobs are the two that decide whether an unattended job may
// delete an operator's media, so their defaults and their refusals are worth
// pinning rather than inferring from the parser's generic behaviour.
func TestMediaGCKnobs(t *testing.T) {
	load := func(extra map[string]string) (*Config, error) {
		env := productionCandidate()
		for k, v := range extra {
			env[k] = v
		}
		return LoadFrom(func(key string) (string, bool) {
			v, ok := env[key]
			return v, ok
		})
	}

	t.Run("the defaults are on, with the breaker armed", func(t *testing.T) {
		cfg, err := load(nil)
		if err != nil {
			t.Fatalf("LoadFrom: %v", err)
		}
		if !cfg.MediaGCEnabled {
			t.Error("MEDIA_GC_ENABLED defaults off; an install that upgrades into this release must keep collecting its orphans")
		}
		if cfg.MediaGCMaxOrphanPercent != 25 {
			t.Errorf("MEDIA_GC_MAX_ORPHAN_PERCENT default = %d, want 25", cfg.MediaGCMaxOrphanPercent)
		}
	})

	t.Run("the off switch is honoured", func(t *testing.T) {
		cfg, err := load(map[string]string{"MEDIA_GC_ENABLED": "false"})
		if err != nil {
			t.Fatalf("LoadFrom: %v", err)
		}
		if cfg.MediaGCEnabled {
			t.Error("MEDIA_GC_ENABLED=false was ignored")
		}
	})

	t.Run("an out-of-range percentage refuses to boot, named", func(t *testing.T) {
		for _, bad := range []string{"-1", "101"} {
			_, err := load(map[string]string{"MEDIA_GC_MAX_ORPHAN_PERCENT": bad})
			if err == nil {
				t.Fatalf("MEDIA_GC_MAX_ORPHAN_PERCENT=%s was accepted", bad)
			}
			if _, ok := collectVarErrors(err)["MEDIA_GC_MAX_ORPHAN_PERCENT"]; !ok {
				t.Errorf("MEDIA_GC_MAX_ORPHAN_PERCENT=%s: the error is not attributed to the variable: %v", bad, err)
			}
		}
	})

	t.Run("the boundaries are legal", func(t *testing.T) {
		for _, ok := range []string{"0", "100"} {
			if _, err := load(map[string]string{"MEDIA_GC_MAX_ORPHAN_PERCENT": ok}); err != nil {
				t.Errorf("MEDIA_GC_MAX_ORPHAN_PERCENT=%s was rejected: %v", ok, err)
			}
		}
	})

	t.Run("an unparsable value is attributed to its variable", func(t *testing.T) {
		_, err := load(map[string]string{"MEDIA_GC_ENABLED": "yes"})
		if err == nil {
			t.Fatal("MEDIA_GC_ENABLED=yes was accepted; the api parses booleans with strconv")
		}
		if _, ok := collectVarErrors(err)["MEDIA_GC_ENABLED"]; !ok {
			t.Errorf("the error is not attributed to MEDIA_GC_ENABLED: %v", err)
		}
	})
}

// TestStorageMigrationTargetValidation covers the second backend's boot-time
// rules. The one that matters most is the last: a "migration" whose target is
// the source is a loop that copies every object onto itself and then deletes it
// after the grace period, and boot is the only cheap place to catch it.
func TestStorageMigrationTargetValidation(t *testing.T) {
	load := func(extra map[string]string) (*Config, error) {
		env := productionCandidate()
		for k, v := range extra {
			env[k] = v
		}
		return LoadFrom(func(key string) (string, bool) {
			v, ok := env[key]
			return v, ok
		})
	}

	t.Run("unset means the feature is simply off", func(t *testing.T) {
		cfg, err := load(nil)
		if err != nil {
			t.Fatalf("LoadFrom: %v", err)
		}
		if cfg.StorageMigrationConfigured() {
			t.Error("StorageMigrationConfigured() is true with no target set")
		}
		if cfg.StorageMigrationGraceHours != 168 {
			t.Errorf("default grace = %d hours, want 168 (a week: the undo window)", cfg.StorageMigrationGraceHours)
		}
	})

	t.Run("an s3 target requires its own credentials", func(t *testing.T) {
		_, err := load(map[string]string{"STORAGE_MIGRATION_TARGET_BACKEND": "s3"})
		vars := collectVarErrors(err)
		for _, want := range []string{
			"STORAGE_MIGRATION_TARGET_S3_ENDPOINT",
			"STORAGE_MIGRATION_TARGET_S3_BUCKET",
			"STORAGE_MIGRATION_TARGET_S3_ACCESS_KEY",
			"STORAGE_MIGRATION_TARGET_S3_SECRET_KEY",
		} {
			if _, ok := vars[want]; !ok {
				t.Errorf("no error reported for %s; got %v", want, err)
			}
		}
	})

	t.Run("the endpoint is host-only, like the primary", func(t *testing.T) {
		_, err := load(map[string]string{
			"STORAGE_MIGRATION_TARGET_BACKEND":       "s3",
			"STORAGE_MIGRATION_TARGET_S3_ENDPOINT":   "https://nyc3.example",
			"STORAGE_MIGRATION_TARGET_S3_BUCKET":     "new-media",
			"STORAGE_MIGRATION_TARGET_S3_ACCESS_KEY": "key",
			"STORAGE_MIGRATION_TARGET_S3_SECRET_KEY": "secret",
		})
		if _, ok := collectVarErrors(err)["STORAGE_MIGRATION_TARGET_S3_ENDPOINT"]; !ok {
			t.Errorf("a scheme in the endpoint was accepted: %v", err)
		}
	})

	t.Run("an unknown backend names itself", func(t *testing.T) {
		_, err := load(map[string]string{"STORAGE_MIGRATION_TARGET_BACKEND": "ipfs"})
		if _, ok := collectVarErrors(err)["STORAGE_MIGRATION_TARGET_BACKEND"]; !ok {
			t.Errorf("STORAGE_MIGRATION_TARGET_BACKEND=ipfs was accepted: %v", err)
		}
	})

	t.Run("a valid s3 target loads", func(t *testing.T) {
		cfg, err := load(map[string]string{
			"STORAGE_MIGRATION_TARGET_BACKEND":       "s3",
			"STORAGE_MIGRATION_TARGET_S3_ENDPOINT":   "nyc3.example",
			"STORAGE_MIGRATION_TARGET_S3_BUCKET":     "new-media",
			"STORAGE_MIGRATION_TARGET_S3_ACCESS_KEY": "key",
			"STORAGE_MIGRATION_TARGET_S3_SECRET_KEY": "secret",
			"STORAGE_MIGRATION_GRACE_HOURS":          "24",
		})
		if err != nil {
			t.Fatalf("LoadFrom: %v", err)
		}
		if !cfg.StorageMigrationConfigured() || cfg.StorageMigrationGraceHours != 24 {
			t.Errorf("configured=%v grace=%d", cfg.StorageMigrationConfigured(), cfg.StorageMigrationGraceHours)
		}
	})

	t.Run("the target may not be the source", func(t *testing.T) {
		_, err := load(map[string]string{
			"STORAGE_BACKEND":                        "s3",
			"STORAGE_S3_ENDPOINT":                    "nyc3.example",
			"STORAGE_S3_BUCKET":                      "media",
			"STORAGE_S3_ACCESS_KEY":                  "key",
			"STORAGE_S3_SECRET_KEY":                  "secret",
			"STORAGE_MIGRATION_TARGET_BACKEND":       "s3",
			"STORAGE_MIGRATION_TARGET_S3_ENDPOINT":   "nyc3.example",
			"STORAGE_MIGRATION_TARGET_S3_BUCKET":     "media",
			"STORAGE_MIGRATION_TARGET_S3_ACCESS_KEY": "other-key",
			"STORAGE_MIGRATION_TARGET_S3_SECRET_KEY": "other-secret",
		})
		if _, ok := collectVarErrors(err)["STORAGE_MIGRATION_TARGET_BACKEND"]; !ok {
			t.Errorf("a target identical to the source was accepted: %v", err)
		}
	})

	t.Run("a negative grace is refused", func(t *testing.T) {
		_, err := load(map[string]string{
			"STORAGE_MIGRATION_TARGET_BACKEND":    "local",
			"STORAGE_MIGRATION_TARGET_LOCAL_ROOT": "/srv/new-media",
			"STORAGE_MIGRATION_GRACE_HOURS":       "-1",
		})
		if _, ok := collectVarErrors(err)["STORAGE_MIGRATION_GRACE_HOURS"]; !ok {
			t.Errorf("a negative grace period was accepted: %v", err)
		}
	})
}

// TestTranscodingPackagerKnob pins the selector that decides what format every
// new upload is packaged in — and, just as importantly, that the rollback to the
// legacy format is a spelling an operator can actually reach.
func TestTranscodingPackagerKnob(t *testing.T) {
	load := func(extra map[string]string) (*Config, error) {
		env := productionCandidate()
		for k, v := range extra {
			env[k] = v
		}
		return LoadFrom(func(key string) (string, bool) {
			v, ok := env[key]
			return v, ok
		})
	}

	t.Run("new installs package CMAF by default", func(t *testing.T) {
		cfg, err := load(nil)
		if err != nil {
			t.Fatalf("LoadFrom: %v", err)
		}
		if cfg.TranscodingPackager != "cmaf" {
			t.Errorf("TRANSCODING_PACKAGER default = %q, want cmaf — an unset install must get the shared HLS+DASH segment set",
				cfg.TranscodingPackager)
		}
	})

	t.Run("the rollback value is accepted", func(t *testing.T) {
		cfg, err := load(map[string]string{"TRANSCODING_PACKAGER": "ts"})
		if err != nil {
			t.Fatalf("TRANSCODING_PACKAGER=ts was rejected: %v", err)
		}
		if cfg.TranscodingPackager != "ts" {
			t.Errorf("TRANSCODING_PACKAGER = %q, want ts", cfg.TranscodingPackager)
		}
	})

	t.Run("an unknown format refuses to boot, named", func(t *testing.T) {
		// Including the plausible near-misses: silently falling back to a default
		// would package a whole deployment the way the operator did not ask for,
		// and they would find out from a player, weeks later.
		// Not "": the parser's contract is that an EMPTY variable means "use the
		// default" everywhere, so `TRANSCODING_PACKAGER=` in an env file is the
		// same as omitting it (asserted above).
		for _, bad := range []string{"CMAF", "dash", "hls", "hls-ts", "fmp4"} {
			_, err := load(map[string]string{"TRANSCODING_PACKAGER": bad})
			if err == nil {
				t.Errorf("TRANSCODING_PACKAGER=%q was accepted", bad)
				continue
			}
			if _, ok := collectVarErrors(err)["TRANSCODING_PACKAGER"]; !ok {
				t.Errorf("TRANSCODING_PACKAGER=%q: the error is not attributed to the variable: %v", bad, err)
			}
		}
	})
}

// TestProcessRole covers VIDRA_ROLE — the flag that decides whether a process
// serves HTTP, runs the background workers, or both.
//
// The default is the case that must never move: an existing install that has
// never heard of VIDRA_ROLE has to keep booting as one process that does
// everything, or an upgrade silently turns a working instance into one with no
// workers (queues stop draining and nothing in the API says so).
func TestProcessRole(t *testing.T) {
	load := func(extra map[string]string) (*Config, error) {
		env := productionCandidate()
		for k, v := range extra {
			env[k] = v
		}
		return LoadFrom(func(key string) (string, bool) {
			v, ok := env[key]
			return v, ok
		})
	}

	t.Run("unset means all: the pre-flag behaviour", func(t *testing.T) {
		cfg, err := load(nil)
		if err != nil {
			t.Fatalf("LoadFrom: %v", err)
		}
		if cfg.Role != RoleAll {
			t.Errorf("Role = %q, want %q", cfg.Role, RoleAll)
		}
		if !cfg.Role.RunsWorkers() || !cfg.Role.ServesHTTP() {
			t.Errorf("the default role must do both: RunsWorkers=%v ServesHTTP=%v",
				cfg.Role.RunsWorkers(), cfg.Role.ServesHTTP())
		}
	})

	// An EMPTY value is unset everywhere else in this package (`KEY=` == omitted),
	// and a generated env file that ships `VIDRA_ROLE=` blank must not refuse to
	// boot.
	t.Run("empty is unset, not invalid", func(t *testing.T) {
		cfg, err := load(map[string]string{"VIDRA_ROLE": ""})
		if err != nil {
			t.Fatalf("VIDRA_ROLE= (blank) was rejected: %v", err)
		}
		if cfg.Role != RoleAll {
			t.Errorf("Role = %q, want %q", cfg.Role, RoleAll)
		}
	})

	t.Run("each role selects its halves", func(t *testing.T) {
		for _, tc := range []struct {
			in          string
			want        Role
			runsWorkers bool
			servesHTTP  bool
		}{
			{"all", RoleAll, true, true},
			{"api", RoleAPI, false, true},
			{"worker", RoleWorker, true, false},
			// Case and stray whitespace are normalised, as they are for LOG_LEVEL.
			{"  Worker ", RoleWorker, true, false},
			{"API", RoleAPI, false, true},
		} {
			cfg, err := load(map[string]string{"VIDRA_ROLE": tc.in})
			if err != nil {
				t.Fatalf("VIDRA_ROLE=%q: %v", tc.in, err)
			}
			if cfg.Role != tc.want {
				t.Errorf("VIDRA_ROLE=%q: Role = %q, want %q", tc.in, cfg.Role, tc.want)
			}
			if got := cfg.Role.RunsWorkers(); got != tc.runsWorkers {
				t.Errorf("VIDRA_ROLE=%q: RunsWorkers() = %v, want %v", tc.in, got, tc.runsWorkers)
			}
			if got := cfg.Role.ServesHTTP(); got != tc.servesHTTP {
				t.Errorf("VIDRA_ROLE=%q: ServesHTTP() = %v, want %v", tc.in, got, tc.servesHTTP)
			}
		}
	})

	// Every role runs at least one half. A value that ran neither would be a
	// container that boots, logs nothing wrong, and does nothing at all.
	t.Run("no valid role is a no-op process", func(t *testing.T) {
		for _, r := range []Role{RoleAll, RoleAPI, RoleWorker} {
			if !r.RunsWorkers() && !r.ServesHTTP() {
				t.Errorf("role %q neither serves nor works", r)
			}
		}
	})

	// The whole point of making this fatal: "workers", "api-only" and "both" are
	// the plausible typos, and each of them would otherwise boot a process that
	// looks healthy and drains nothing.
	t.Run("an unrecognised role refuses to boot, named", func(t *testing.T) {
		for _, bad := range []string{"workers", "api-only", "both", "none", "worker,api"} {
			_, err := load(map[string]string{"VIDRA_ROLE": bad})
			if err == nil {
				t.Fatalf("VIDRA_ROLE=%s was accepted", bad)
			}
			ve, ok := collectVarErrors(err)["VIDRA_ROLE"]
			if !ok {
				t.Errorf("VIDRA_ROLE=%s: the error is not attributed to the variable: %v", bad, err)
				continue
			}
			if !strings.Contains(ve.Msg, "all|api|worker") {
				t.Errorf("VIDRA_ROLE=%s: the message does not list the legal values: %q", bad, ve.Msg)
			}
		}
	})
}
