package setup

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seqReader is a deterministic entropy source: byte i of the stream is i. Real
// generation uses crypto/rand — this exists so the golden file below is stable
// and a drift in WHICH secret is minted (or in what order) shows up as a diff.
type seqReader struct{ n byte }

func (r *seqReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = r.n
		r.n++
	}
	return len(p), nil
}

func fixtureTemplate(t *testing.T) *EnvFile {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "template.env.example"))
	if err != nil {
		t.Fatalf("read fixture template: %v", err)
	}
	f, err := ParseEnvFile(b)
	if err != nil {
		t.Fatalf("parse fixture template: %v", err)
	}
	return f
}

// baseAnswers is a complete, valid answer set: a domain, a pinned release, local
// storage, closed registration, no mail.
func baseAnswers() Answers {
	return Answers{
		Domain:         "video.example.org",
		ReleaseTag:     "v0.1.1",
		StorageBackend: "local",
		Registration:   &RegistrationAnswers{Enabled: false},
	}
}

func generate(t *testing.T, req Request) *Result {
	t.Helper()
	if req.Template == nil {
		req.Template = fixtureTemplate(t)
	}
	if req.Rand == nil {
		req.Rand = &seqReader{}
	}
	res, err := Generate(req)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return res
}

// The whole contract in one artifact: the template's comments, section rules and
// ordering survive verbatim, every answer landed in the right key, and every
// blank/placeholder secret came back filled. Review the diff like source code —
// a change here is a change to what operators' env files look like.
// UPDATE_GOLDEN=1 rewrites it.
func TestGenerateGolden(t *testing.T) {
	res := generate(t, Request{Answers: baseAnswers()})

	path := filepath.Join("testdata", "generated.env.golden")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(path, res.Content, 0o644); err != nil {
			t.Fatalf("update golden: %v", err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden — regenerate with UPDATE_GOLDEN=1 go test ./internal/setup -run GenerateGolden: %v", err)
	}
	if string(res.Content) != string(want) {
		t.Errorf("generated file drifted from golden\n--- got ---\n%s--- want ---\n%s", res.Content, want)
	}
}

// The generated file keeps every comment line of the template, in place: that is
// what makes it self-documenting, and it is the first thing a careless rewrite
// of Render would lose.
func TestGeneratePreservesCommentsAndOrder(t *testing.T) {
	tmpl := fixtureTemplate(t)
	raw, err := os.ReadFile(filepath.Join("testdata", "template.env.example"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	res := generate(t, Request{Template: tmpl, Answers: baseAnswers()})

	var wantComments, gotComments []string
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "#") {
			wantComments = append(wantComments, line)
		}
	}
	for _, line := range strings.Split(string(res.Content), "\n") {
		if strings.HasPrefix(line, "#") {
			gotComments = append(gotComments, line)
		}
	}
	if len(wantComments) == 0 {
		t.Fatal("fixture has no comment lines")
	}
	if strings.Join(gotComments, "\n") != strings.Join(wantComments, "\n") {
		t.Errorf("comment lines changed\ngot:\n%s\nwant:\n%s", strings.Join(gotComments, "\n"), strings.Join(wantComments, "\n"))
	}

	// Key order is the template's, and the commented-out keys stayed comments.
	got, err := ParseEnvFile(res.Content)
	if err != nil {
		t.Fatalf("parse generated: %v", err)
	}
	if strings.Join(got.Keys(), ",") != strings.Join(tmpl.Keys(), ",") {
		t.Errorf("key order changed\ngot:  %v\nwant: %v", got.Keys(), tmpl.Keys())
	}
	for _, k := range []string{"DATABASE_URL", "FEDERATION_KEY_KEK"} {
		if got.Has(k) {
			t.Errorf("%s was commented out in the template and must not become an active assignment", k)
		}
	}
}

// Every secret the file assigns has to be the SHAPE the api validates and the
// compose chain substitutes — hex where a DSN carries it, base64 of exactly 32
// bytes for a KEK — or the deployment fails at boot instead of here.
func TestGenerateFillsEverySecretWithAValidShape(t *testing.T) {
	res := generate(t, Request{Answers: baseAnswers()})

	want := map[string]bool{"JWT_SECRET": true, "MFA_KEY_KEK": true, "POSTGRES_PASSWORD": true, "REDIS_PASSWORD": true, "SEARCH_INTERNAL_SECRET": true}
	got := map[string]bool{}
	for _, k := range res.Generated {
		got[k] = true
	}
	for k := range want {
		if !got[k] {
			t.Errorf("%s was not generated (Generated=%v)", k, res.Generated)
		}
	}
	for k := range got {
		if !want[k] {
			t.Errorf("unexpected generated secret %s", k)
		}
	}

	for key, spec := range secretManifest {
		v, ok := res.Values[key]
		if !ok || v == "" {
			continue // not assigned by this template (FEDERATION_KEY_KEK et al.)
		}
		var raw []byte
		var err error
		switch spec.kind {
		case secretHex:
			raw, err = hex.DecodeString(v)
		case secretBase64:
			raw, err = base64.StdEncoding.DecodeString(v)
		}
		if err != nil {
			t.Errorf("%s: value is not valid %s: %v", key, spec.openssl, err)
			continue
		}
		if len(raw) != spec.size {
			t.Errorf("%s: got %d random bytes, want %d (%s)", key, len(raw), spec.size, spec.openssl)
		}
	}
	if issues := Check(res.Values); len(issues) > 0 {
		t.Errorf("generated file does not pass the boot validation: %v", issues)
	}
}

// The KEK-durability rule: a second run over the first run's output must be
// byte-identical. Anything else means a secret was silently re-minted, and for
// MFA_KEY_KEK that is every enrolled TOTP secret orphaned.
func TestMergePreservesEverySecretByteForByte(t *testing.T) {
	first := generate(t, Request{Answers: baseAnswers()})
	existing, err := ParseEnvFile(first.Content)
	if err != nil {
		t.Fatalf("parse first output: %v", err)
	}

	// A different entropy source on the second run: if anything re-minted, the
	// values could not possibly match.
	second := generate(t, Request{Existing: existing, Answers: baseAnswers(), Rand: &seqReader{n: 200}})

	if string(second.Content) != string(first.Content) {
		t.Errorf("re-running setup changed the file\n--- second ---\n%s--- first ---\n%s", second.Content, first.Content)
	}
	if len(second.Generated) != 0 {
		t.Errorf("second run minted %v; nothing should have been generated", second.Generated)
	}
	for _, k := range []string{"JWT_SECRET", "MFA_KEY_KEK", "POSTGRES_PASSWORD", "REDIS_PASSWORD", "SEARCH_INTERNAL_SECRET"} {
		if second.Values[k] != first.Values[k] {
			t.Errorf("%s changed on re-run: %q -> %q", k, first.Values[k], second.Values[k])
		}
		if !contains(second.Preserved, k) {
			t.Errorf("%s not reported as preserved (Preserved=%v)", k, second.Preserved)
		}
	}
}

// An answer still wins over an existing value — that is how an instance changes
// its domain, its release tag or its storage — while the secrets around it stay
// untouched.
func TestMergeAppliesNewAnswersWithoutTouchingSecrets(t *testing.T) {
	first := generate(t, Request{Answers: baseAnswers()})
	existing, _ := ParseEnvFile(first.Content)

	answers := baseAnswers()
	answers.Domain = "https://video.example.net"
	answers.ReleaseTag = "v0.2.0"
	second := generate(t, Request{Existing: existing, Answers: answers, Rand: &seqReader{n: 200}})

	for _, k := range singleOriginKeys {
		if second.Values[k] != "https://video.example.net" {
			t.Errorf("%s = %q, want the new domain", k, second.Values[k])
		}
	}
	for _, k := range releaseTagKeys {
		if second.Values[k] != "v0.2.0" {
			t.Errorf("%s = %q, want v0.2.0", k, second.Values[k])
		}
	}
	if second.Values["JWT_SECRET"] != first.Values["JWT_SECRET"] {
		t.Error("JWT_SECRET changed while only answers were updated")
	}
}

// Keys the previous file set that the template does not define — an uncommented
// FEDERATION_KEY_KEK, a managed DATABASE_URL — must survive the regeneration.
// Dropping the KEK would orphan every sealed actor key.
func TestMergeCarriesKeysTheTemplateDoesNotDefine(t *testing.T) {
	first := generate(t, Request{Answers: baseAnswers()})
	extra := string(first.Content) + "\nFEDERATION_KEY_KEK=" + base64.StdEncoding.EncodeToString(make([]byte, 32)) +
		"\nDATABASE_URL=postgresql://doadmin:pw@db.example.net:25060/defaultdb?sslmode=require\n"
	existing, err := ParseEnvFile([]byte(extra))
	if err != nil {
		t.Fatalf("parse existing: %v", err)
	}

	second := generate(t, Request{Existing: existing, Answers: baseAnswers(), Rand: &seqReader{n: 200}})

	for _, k := range []string{"FEDERATION_KEY_KEK", "DATABASE_URL"} {
		want, _ := existing.Value(k)
		if second.Values[k] != want {
			t.Errorf("%s = %q, want the carried value %q", k, second.Values[k], want)
		}
		if !contains(second.Carried, k) {
			t.Errorf("%s not reported as carried (Carried=%v)", k, second.Carried)
		}
	}
	if !strings.Contains(string(second.Content), "Carried over from the previous env file") {
		t.Error("carried keys were not written under their explanatory header")
	}

	// And the carry is stable: a third run over the second output changes nothing.
	third := generate(t, Request{Existing: mustParse(t, second.Content), Answers: baseAnswers(), Rand: &seqReader{n: 90}})
	if string(third.Content) != string(second.Content) {
		t.Errorf("carrying keys is not idempotent\n--- third ---\n%s--- second ---\n%s", third.Content, second.Content)
	}
}

// Rotation is the ONLY way a live secret changes, and rotating a KEK is gated
// behind explicit confirmation because it orphans data already sealed in the
// database.
func TestRotate(t *testing.T) {
	first := generate(t, Request{Answers: baseAnswers()})
	existing := mustParse(t, first.Content)

	t.Run("KEK requires confirmation", func(t *testing.T) {
		_, err := Generate(Request{
			Template: fixtureTemplate(t), Existing: existing, Answers: baseAnswers(),
			Rotate: []string{"MFA_KEY_KEK"}, Rand: &seqReader{n: 200},
		})
		if err == nil {
			t.Fatal("rotating MFA_KEY_KEK without --yes was allowed")
		}
		for _, want := range []string{"DESTRUCTIVE", "TOTP", "--yes"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not mention %q", err, want)
			}
		}
	})

	t.Run("KEK rotates with confirmation, alone", func(t *testing.T) {
		res := generate(t, Request{
			Existing: existing, Answers: baseAnswers(),
			Rotate: []string{"MFA_KEY_KEK"}, ConfirmDestructive: true, Rand: &seqReader{n: 200},
		})
		if res.Values["MFA_KEY_KEK"] == first.Values["MFA_KEY_KEK"] {
			t.Error("MFA_KEY_KEK was not rotated")
		}
		if !contains(res.Rotated, "MFA_KEY_KEK") {
			t.Errorf("Rotated=%v, want MFA_KEY_KEK", res.Rotated)
		}
		for _, k := range []string{"JWT_SECRET", "POSTGRES_PASSWORD", "REDIS_PASSWORD", "SEARCH_INTERNAL_SECRET"} {
			if res.Values[k] != first.Values[k] {
				t.Errorf("%s changed while only MFA_KEY_KEK was rotated", k)
			}
		}
	})

	t.Run("non-KEK secret rotates without confirmation", func(t *testing.T) {
		res := generate(t, Request{
			Existing: existing, Answers: baseAnswers(),
			Rotate: []string{"JWT_SECRET"}, Rand: &seqReader{n: 200},
		})
		if res.Values["JWT_SECRET"] == first.Values["JWT_SECRET"] {
			t.Error("JWT_SECRET was not rotated")
		}
	})

	t.Run("unknown and unused variables are refused", func(t *testing.T) {
		for _, tc := range []struct{ name, v, want string }{
			{"not a secret", "PUBLIC_BASE_URL", "is not a generated secret"},
			{"not assigned by this deployment", "LIVE_INGEST_SECRET", "cannot rotate"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				_, err := Generate(Request{
					Template: fixtureTemplate(t), Existing: existing, Answers: baseAnswers(),
					Rotate: []string{tc.v}, ConfirmDestructive: true, Rand: &seqReader{n: 200},
				})
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
				}
			})
		}
	})
}

// A leftover <...> placeholder is a value that WOULD reach a container, and
// `<your Spaces access key>` even satisfies config's "required" check — so the
// engine refuses to write the file rather than deploy a lie.
func TestGenerateRefusesLeftoverPlaceholders(t *testing.T) {
	answers := baseAnswers()
	answers.StorageBackend = "" // keep the template's s3 block, credentials unfilled

	_, err := Generate(Request{Template: fixtureTemplate(t), Answers: answers, Rand: &seqReader{}})
	var invalid *ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("err = %v, want a *ValidationError", err)
	}
	for _, k := range []string{"STORAGE_S3_ACCESS_KEY", "STORAGE_S3_SECRET_KEY"} {
		if !hasIssue(invalid.Issues, k) {
			t.Errorf("no issue for %s: %v", k, invalid.Issues)
		}
	}
}

// Choosing local storage answers the S3 questions with "unused": the leftover
// placeholders go, and the api's local-backend validation is satisfied.
func TestStorageLocalClearsPlaceholdersButKeepsRealCredentials(t *testing.T) {
	res := generate(t, Request{Answers: baseAnswers()})
	if res.Values["STORAGE_BACKEND"] != "local" {
		t.Fatalf("STORAGE_BACKEND = %q", res.Values["STORAGE_BACKEND"])
	}
	for _, k := range []string{"STORAGE_S3_ACCESS_KEY", "STORAGE_S3_SECRET_KEY"} {
		if res.Values[k] != "" {
			t.Errorf("%s = %q, want it blanked", k, res.Values[k])
		}
	}
	// A real credential is not thrown away — the operator may switch back.
	existing := mustParse(t, []byte("STORAGE_S3_ACCESS_KEY=AKIAREAL\nSTORAGE_S3_SECRET_KEY=s3cr3t\n"))
	res = generate(t, Request{Existing: existing, Answers: baseAnswers()})
	if res.Values["STORAGE_S3_ACCESS_KEY"] != "AKIAREAL" {
		t.Errorf("STORAGE_S3_ACCESS_KEY = %q, want the existing credential preserved", res.Values["STORAGE_S3_ACCESS_KEY"])
	}
}

func TestStorageS3Answers(t *testing.T) {
	answers := baseAnswers()
	answers.StorageBackend = "s3"
	answers.S3 = S3Answers{Endpoint: "fra1.digitaloceanspaces.com", Region: "fra1", Bucket: "media", AccessKey: "AKIA", SecretKey: "shh"}

	res := generate(t, Request{Answers: answers})
	for k, want := range map[string]string{
		"STORAGE_BACKEND":       "s3",
		"STORAGE_S3_ENDPOINT":   "fra1.digitaloceanspaces.com",
		"STORAGE_S3_REGION":     "fra1",
		"STORAGE_S3_BUCKET":     "media",
		"STORAGE_S3_ACCESS_KEY": "AKIA",
		"STORAGE_S3_SECRET_KEY": "shh",
	} {
		if res.Values[k] != want {
			t.Errorf("%s = %q, want %q", k, res.Values[k], want)
		}
	}
}

// The template ships MAIL_ENABLED=true with a blank SMTP_HOST — a prompt, not a
// configuration — and the api refuses that combination. Unanswered means off.
func TestMail(t *testing.T) {
	t.Run("no answers turns mail off", func(t *testing.T) {
		res := generate(t, Request{Answers: baseAnswers()})
		if res.Values["MAIL_ENABLED"] != "false" {
			t.Errorf("MAIL_ENABLED = %q, want false", res.Values["MAIL_ENABLED"])
		}
		if !warned(res.Warnings, "mail is disabled") {
			t.Errorf("no warning about mail being disabled: %v", res.Warnings)
		}
	})

	t.Run("answers turn it on", func(t *testing.T) {
		answers := baseAnswers()
		answers.Mail = &MailAnswers{Host: "smtp.example.net", Username: "postmaster", Password: "pw", From: "hello@example.net"}
		res := generate(t, Request{Answers: answers})
		for k, want := range map[string]string{
			"MAIL_ENABLED": "true", "SMTP_HOST": "smtp.example.net", "SMTP_PORT": "587",
			"SMTP_USERNAME": "postmaster", "SMTP_PASSWORD": "pw", "SMTP_FROM": "hello@example.net",
		} {
			if res.Values[k] != want {
				t.Errorf("%s = %q, want %q", k, res.Values[k], want)
			}
		}
	})
}

func TestRegistrationAnswers(t *testing.T) {
	answers := baseAnswers()
	answers.Registration = &RegistrationAnswers{Enabled: true, RequireApproval: true}
	res := generate(t, Request{Answers: answers})
	if res.Values["REGISTRATION_ENABLED"] != "true" || res.Values["REGISTRATION_REQUIRE_APPROVAL"] != "true" {
		t.Errorf("registration policy not applied: %q/%q", res.Values["REGISTRATION_ENABLED"], res.Values["REGISTRATION_REQUIRE_APPROVAL"])
	}
}

// PUBLIC_BASE_URL is the origin for watch links AND for OAuth/federation
// identity, so the template's example value is documentation, never an answer.
func TestDomainIsRequired(t *testing.T) {
	answers := baseAnswers()
	answers.Domain = ""
	_, err := Generate(Request{Template: fixtureTemplate(t), Answers: answers, Rand: &seqReader{}})
	if err == nil || !strings.Contains(err.Error(), "domain is required") {
		t.Fatalf("err = %v, want the domain requirement", err)
	}
}

func TestNormalizeOrigin(t *testing.T) {
	for _, tc := range []struct {
		in      string
		want    string
		wantErr string
	}{
		{in: "video.example.org", want: "https://video.example.org"},
		{in: "https://video.example.org", want: "https://video.example.org"},
		{in: "https://video.example.org/", want: "https://video.example.org"},
		{in: "video.example.org:8443", want: "https://video.example.org:8443"},
		{in: "http://video.example.org", wantErr: "must be https"},
		{in: "https://video.example.org/videos", wantErr: "must not include a path"},
		{in: "  ", wantErr: "domain is empty"},
	} {
		got, err := normalizeOrigin(tc.in)
		switch {
		case tc.wantErr != "":
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("normalizeOrigin(%q) err = %v, want one mentioning %q", tc.in, err, tc.wantErr)
			}
		case err != nil:
			t.Errorf("normalizeOrigin(%q): %v", tc.in, err)
		case got != tc.want:
			t.Errorf("normalizeOrigin(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestReleaseTagWarning(t *testing.T) {
	answers := baseAnswers()
	answers.ReleaseTag = ""
	res := generate(t, Request{Answers: answers})
	if !warned(res.Warnings, "template's example tag") {
		t.Errorf("no warning about unpinned release tags: %v", res.Warnings)
	}
	if res.Values["VIDRA_CORE_TAG"] != "v0.1.0" {
		t.Errorf("VIDRA_CORE_TAG = %q, want the template's value kept", res.Values["VIDRA_CORE_TAG"])
	}
}

func TestReleaseTagPerServiceOverride(t *testing.T) {
	answers := baseAnswers()
	answers.SearchTag = "v0.3.0"
	res := generate(t, Request{Answers: answers})
	if res.Values["VIDRA_CORE_TAG"] != "v0.1.1" || res.Values["VIDRA_SEARCH_TAG"] != "v0.3.0" {
		t.Errorf("tags = %q/%q, want v0.1.1 with the search override at v0.3.0", res.Values["VIDRA_CORE_TAG"], res.Values["VIDRA_SEARCH_TAG"])
	}
}

// Check is the doctor precursor: a problem is reported against the variable an
// operator has to fix, never as a raw Go error. It inherits the boot engine's
// reporting shape — every MALFORMED value at once, then the first semantic
// failure — so this test asserts both halves.
func TestCheckSurfacesEachBadVariableByName(t *testing.T) {
	issues := Check(map[string]string{
		"VIDRA_ENV":             "production",
		"HTTP_PORT":             "not-a-number",
		"HTTP_REQUEST_TIMEOUT":  "soon",
		"RATE_LIMIT_ENABLED":    "sometimes",
		"STORAGE_S3_ACCESS_KEY": "<your Spaces access key>",
		"VIDRA_CORE_TAG":        "v0.1.1", // compose-only: config never looks it up
	})
	for _, want := range []string{"HTTP_PORT", "HTTP_REQUEST_TIMEOUT", "RATE_LIMIT_ENABLED", "STORAGE_S3_ACCESS_KEY"} {
		if !hasIssue(issues, want) {
			t.Errorf("no issue for %s: %v", want, issues)
		}
	}
	if hasIssue(issues, "VIDRA_CORE_TAG") {
		t.Errorf("compose-only key was reported: %v", issues)
	}

	// With parsing clean, each semantic rule attributes itself to its variable.
	valid := map[string]string{
		"VIDRA_ENV":  "production",
		"JWT_SECRET": strings.Repeat("k", 48),
	}
	if got := Check(valid); len(got) > 0 {
		t.Fatalf("the minimal valid production environment was rejected: %v", got)
	}
	for _, tc := range []struct{ key, value string }{
		{"LOG_LEVEL", "loud"},
		{"JWT_SECRET", "too-short"},
		{"MFA_KEY_KEK", "not-base64"},
		{"CORS_ALLOWED_ORIGINS", "*"},
		{"MAIL_ENABLED", "true"},
	} {
		vars := map[string]string{}
		for k, v := range valid {
			vars[k] = v
		}
		vars[tc.key] = tc.value
		want := tc.key
		if tc.key == "MAIL_ENABLED" {
			want = "SMTP_HOST" // the variable that has to change is the missing one
		}
		if got := Check(vars); !hasIssue(got, want) {
			t.Errorf("%s=%q: no issue for %s: %v", tc.key, tc.value, want, got)
		}
	}
	if issues := Check(map[string]string{"VIDRA_ENV": "development"}); len(issues) > 0 {
		t.Errorf("a minimal development environment should be valid, got %v", issues)
	}
}

func TestGenerateRejectsUnknownStorageBackend(t *testing.T) {
	answers := baseAnswers()
	answers.StorageBackend = "gcs"
	_, err := Generate(Request{Template: fixtureTemplate(t), Answers: answers, Rand: &seqReader{}})
	if err == nil || !strings.Contains(err.Error(), "want local|s3") {
		t.Fatalf("err = %v, want the backend to be rejected", err)
	}
}

func TestGenerateRejectsAnEmptyTemplate(t *testing.T) {
	empty := mustParse(t, []byte("# nothing but a comment\n"))
	if _, err := Generate(Request{Template: empty, Answers: baseAnswers()}); err == nil {
		t.Fatal("an assignment-free template was accepted")
	}
}

func TestWriteFileIsPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "production.env")
	if err := WriteFile(path, []byte("VIDRA_ENV=production\n")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600 — the file holds every secret the instance has", info.Mode().Perm())
	}
	// Overwriting an existing, world-readable file must not inherit its mode.
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if err := WriteFile(path, []byte("VIDRA_ENV=production\n")); err != nil {
		t.Fatalf("WriteFile (rewrite): %v", err)
	}
	info, _ = os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode after rewrite = %v, want 0600", info.Mode().Perm())
	}
}

func TestRenderCheckCommandMirrorsTheDeployScript(t *testing.T) {
	got := RenderCheckCommand("env/production.env")
	for _, want := range []string{"-f docker-compose.yml", "-f docker-compose.prod.yml", "--env-file env/production.env", "--profile core --profile frontend", "config -q"} {
		if !strings.Contains(got, want) {
			t.Errorf("render-check command %q is missing %q", got, want)
		}
	}
}

func mustParse(t *testing.T, b []byte) *EnvFile {
	t.Helper()
	f, err := ParseEnvFile(b)
	if err != nil {
		t.Fatalf("ParseEnvFile: %v", err)
	}
	return f
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

func hasIssue(issues []Issue, v string) bool {
	for _, is := range issues {
		if is.Var == v {
			return true
		}
	}
	return false
}

func warned(warnings []string, substr string) bool {
	for _, w := range warnings {
		if strings.Contains(w, substr) {
			return true
		}
	}
	return false
}
