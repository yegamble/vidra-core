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
			t.Fatal("rotating MFA_KEY_KEK without ConfirmDestructive was allowed")
		}
		for _, want := range []string{"DESTRUCTIVE", "TOTP", "--yes-i-know"} {
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
	// A real credential is not thrown away — the operator may switch back. (The
	// KEK has to be in the source: an existing configuration with a blank KEK is
	// refused, not minted — see TestBlankKEKIsNeverMintedOverAnExistingSource.)
	existing := mustParse(t, []byte("STORAGE_S3_ACCESS_KEY=AKIAREAL\nSTORAGE_S3_SECRET_KEY=s3cr3t\nMFA_KEY_KEK="+
		base64.StdEncoding.EncodeToString(make([]byte, 32))+"\n"))
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

		// One origin, DNS-shaped and case-folded: this answer is written into
		// CORS_ALLOWED_ORIGINS (a security boundary) and into PUBLIC_BASE_URL,
		// which federation and OAuth compare as a STRING.
		{in: "Video.Example.ORG", want: "https://video.example.org"},
		{in: "https://VIDEO.example.org:8443", want: "https://video.example.org:8443"},
		{in: "*.example.org", wantErr: "wildcards are not a host"},
		{in: "https://*", wantErr: "wildcards are not a host"},
		{in: "video.example.org.", wantErr: "trailing dot"},
		{in: "video..example.org", wantErr: "empty label"},
		{in: "video example.org", wantErr: "not a usable domain"},
		{in: "-video.example.org", wantErr: "may not start or end with '-'"},
		{in: "video_1.example.org", wantErr: `"_" is not allowed`},
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

	// The interactive path offers the template's tag as the prompt default, so
	// pressing enter arrives here as an ANSWER equal to the example — the warning
	// has to fire on the value, not on whether an answer was given.
	answers.ReleaseTag = "v0.1.0"
	res = generate(t, Request{Answers: answers})
	if !warned(res.Warnings, "template's example tag") {
		t.Errorf("accepting the template's example tag as an answer silenced the warning: %v", res.Warnings)
	}

	// An existing file that pins a real release does not warn.
	existing := mustParse(t, []byte("VIDRA_CORE_TAG=v0.1.1\nVIDRA_USER_TAG=v0.1.1\nVIDRA_SEARCH_TAG=v0.1.1\nMFA_KEY_KEK="+
		base64.StdEncoding.EncodeToString(make([]byte, 32))+"\n"))
	answers.ReleaseTag = ""
	res = generate(t, Request{Existing: existing, Answers: answers})
	if warned(res.Warnings, "template's example tag") {
		t.Errorf("warned about tags the existing file pins: %v", res.Warnings)
	}
}

// CRITICAL: a blank KEK slot and an existing configuration is the shape of a
// truncated file or a half-finished restore, not of a first install — and
// minting over it destroys exactly what a refused rotation protects. The gate
// has to be the same whether the old value is present or missing.
func TestBlankKEKIsNeverMintedOverAnExistingSource(t *testing.T) {
	// The template leaves MFA_KEY_KEK as a <generate: ...> placeholder.
	for _, tc := range []struct{ name, content string }{
		{"KEK missing from the source entirely", "PUBLIC_BASE_URL=https://video.example.org\n"},
		{"KEK present but blank", "PUBLIC_BASE_URL=https://video.example.org\nMFA_KEY_KEK=\n"},
		{"KEK still a placeholder", "PUBLIC_BASE_URL=https://video.example.org\nMFA_KEY_KEK=<generate: openssl rand -base64 32>\n"},
		// THE reproducer for deriving "first install" from the KEY COUNT: a
		// production.env truncated to nothing (a full disk, an interrupted
		// scp, a restore that wrote the header and died) parses to zero
		// assignments and used to read as a first install — so the gate below
		// stood down and the KEK was minted over a live database.
		{"source truncated to nothing at all", ""},
		{"source truncated to its header", "# ==========\n# VIDRA — PRODUCTION ENVIRONMENT\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Generate(Request{
				Template: fixtureTemplate(t), Existing: mustParse(t, []byte(tc.content)),
				Answers: baseAnswers(), Rand: &seqReader{},
			})
			if err == nil {
				t.Fatal("a KEK was minted over an existing configuration")
			}
			for _, want := range []string{"MFA_KEY_KEK", "DESTRUCTIVE", "TOTP", "--rotate MFA_KEY_KEK --yes-i-know"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
		})
	}

	// The escape hatch works, and says what it destroys before it does.
	existing := mustParse(t, []byte("PUBLIC_BASE_URL=https://video.example.org\nMFA_KEY_KEK=\n"))
	res := generate(t, Request{Existing: existing, Answers: baseAnswers(),
		Rotate: []string{"MFA_KEY_KEK"}, ConfirmDestructive: true})
	if len(res.Values["MFA_KEY_KEK"]) == 0 || IsPlaceholder(res.Values["MFA_KEY_KEK"]) {
		t.Errorf("MFA_KEY_KEK = %q, want a minted value", res.Values["MFA_KEY_KEK"])
	}
	if !contains(res.Rotated, "MFA_KEY_KEK") {
		t.Errorf("Rotated = %v, want MFA_KEY_KEK", res.Rotated)
	}

	// A genuine first install — no source at all — still mints, silently and
	// correctly: there is nothing sealed for a new key to orphan.
	res = generate(t, Request{Answers: baseAnswers()})
	if !contains(res.Generated, "MFA_KEY_KEK") {
		t.Errorf("Generated = %v, want MFA_KEY_KEK minted on a first install", res.Generated)
	}

	// And the distinction survives the merge the CLI does: a file that EXISTS is
	// a source even when it parses to nothing, so Generate sees a non-nil Existing
	// and keeps the gate armed.
	if got := MergeSources(mustParse(t, nil)); got == nil {
		t.Fatal("MergeSources dropped a present-but-empty source — a truncated env file would read as a first install")
	}
}

// CRITICAL: the file being OVERWRITTEN is a preservation source, and it outranks
// the one named by --from. Merging a staging env into a live production file used
// to hand the live file staging's values and mint everything staging did not
// mention — new KEKs over a live database.
func TestMergeSourcesPrefersTheFileBeingOverwritten(t *testing.T) {
	live := mustParse(t, []byte(strings.Join([]string{
		"PUBLIC_BASE_URL=https://video.example.org",
		"JWT_SECRET=" + strings.Repeat("L", 48),
		"MFA_KEY_KEK=" + base64.StdEncoding.EncodeToString([]byte(strings.Repeat("l", 32))),
		"POSTGRES_PASSWORD=" + strings.Repeat("a", 64),
		"REDIS_PASSWORD=",             // a hole --from may fill
		"SEARCH_INTERNAL_SECRET=",     // ditto
		"INSTANCE_NAME=Live Instance", // must beat staging's
	}, "\n")+"\n"))
	staging := mustParse(t, []byte(strings.Join([]string{
		"PUBLIC_BASE_URL=https://staging.example.org",
		"JWT_SECRET=" + strings.Repeat("S", 48),
		"MFA_KEY_KEK=" + base64.StdEncoding.EncodeToString([]byte(strings.Repeat("s", 32))),
		"REDIS_PASSWORD=" + strings.Repeat("b", 64),
		"SEARCH_INTERNAL_SECRET=" + strings.Repeat("c", 64),
		"INSTANCE_NAME=Staging",
		"EXTRA_COMPOSE_PROFILES=ipfs",
	}, "\n")+"\n"))

	merged := MergeSources(live, staging)
	for _, tc := range []struct{ key, want, why string }{
		{"JWT_SECRET", strings.Repeat("L", 48), "the live value wins"},
		{"MFA_KEY_KEK", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("l", 32))), "the live KEK wins"},
		{"INSTANCE_NAME", "Live Instance", "the live value wins"},
		{"POSTGRES_PASSWORD", strings.Repeat("a", 64), "only the live file has it"},
		{"REDIS_PASSWORD", strings.Repeat("b", 64), "--from fills a blank"},
		{"SEARCH_INTERNAL_SECRET", strings.Repeat("c", 64), "--from fills a blank"},
		{"EXTRA_COMPOSE_PROFILES", "ipfs", "only --from has it"},
	} {
		got, _ := merged.Value(tc.key)
		if got != tc.want {
			t.Errorf("%s = %q, want %q (%s)", tc.key, got, tc.want, tc.why)
		}
	}

	// And through Generate: nothing of the live file is lost or re-minted, while
	// the answers still apply.
	res := generate(t, Request{Existing: merged, Answers: baseAnswers(), Rand: &seqReader{n: 111}})
	for _, k := range []string{"JWT_SECRET", "MFA_KEY_KEK", "POSTGRES_PASSWORD", "REDIS_PASSWORD", "SEARCH_INTERNAL_SECRET", "INSTANCE_NAME"} {
		want, _ := merged.Value(k)
		if res.Values[k] != want {
			t.Errorf("%s = %q, want the merged value %q", k, res.Values[k], want)
		}
	}
	if len(res.Generated) != 0 {
		t.Errorf("Generated = %v, want nothing minted when both sources cover the secrets", res.Generated)
	}
	if res.Values["PUBLIC_BASE_URL"] != "https://video.example.org" {
		t.Errorf("PUBLIC_BASE_URL = %q, want the answered domain", res.Values["PUBLIC_BASE_URL"])
	}
}

func TestMergeSourcesWithNoSourcesIsAFirstInstall(t *testing.T) {
	if got := MergeSources(); got != nil {
		t.Errorf("MergeSources() = %v, want nil so Generate sees a first install", got)
	}
	if got := MergeSources(nil, nil); got != nil {
		t.Errorf("MergeSources(nil, nil) = %v, want nil", got)
	}
	one := mustParse(t, []byte("A=1\n"))
	merged := MergeSources(nil, one, nil)
	if v, ok := merged.Value("A"); !ok || v != "1" {
		t.Errorf("MergeSources dropped the only real source: %q, %v", v, ok)
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
}

// The false green that hid every production-only rule: an env file without
// VIDRA_ENV was validated in DEVELOPMENT mode, where a blank JWT_SECRET falls
// back to the dev constant and `CORS_ALLOWED_ORIGINS=*` is allowed. Check now
// reports the missing VIDRA_ENV *and* the findings production would have, in one
// pass — an operator should not have to fix one line and re-run to discover the
// file was never deployable.
func TestCheckAlwaysAppliesProductionRules(t *testing.T) {
	for _, tc := range []struct{ name, env string }{
		{"missing", ""},
		{"development", "development"},
		{"typo", "prod"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vars := map[string]string{
				"JWT_SECRET":           "",
				"CORS_ALLOWED_ORIGINS": "*",
			}
			if tc.env != "" {
				vars["VIDRA_ENV"] = tc.env
			}
			issues := Check(vars)
			if !hasIssue(issues, "VIDRA_ENV") {
				t.Fatalf("no VIDRA_ENV issue: %v", issues)
			}
			// And the production-only refusals are in the SAME report.
			if !hasIssue(issues, "JWT_SECRET") && !hasIssue(issues, "CORS_ALLOWED_ORIGINS") {
				t.Errorf("the production-only rules were skipped: %v", issues)
			}
		})
	}

	// VIDRA_ENV=production is the only value that reports nothing extra.
	if issues := Check(map[string]string{"VIDRA_ENV": "production", "JWT_SECRET": strings.Repeat("k", 48)}); len(issues) > 0 {
		t.Errorf("the minimal valid production environment was rejected: %v", issues)
	}
}

// A value with a newline in it is not a bad value, it is EXTRA LINES: the secret
// is silently truncated at the break and the remainder becomes another
// assignment. Refuse before rendering, naming the variable.
func TestGenerateRejectsValuesThatWouldInjectLines(t *testing.T) {
	for _, tc := range []struct{ name, value string }{
		{"newline injects an assignment", "shhh\nREGISTRATION_ENABLED=true"},
		{"carriage return", "shhh\rREGISTRATION_ENABLED=true"},
		{"NUL truncates", "shhh\x00rest"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			answers := baseAnswers()
			answers.StorageBackend = "s3"
			answers.S3 = S3Answers{Endpoint: "fra1.example.net", Region: "fra1", Bucket: "media", AccessKey: "AKIA", SecretKey: tc.value}

			_, err := Generate(Request{Template: fixtureTemplate(t), Answers: answers, Rand: &seqReader{}})
			var invalid *ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("err = %v, want a *ValidationError", err)
			}
			if !hasIssue(invalid.Issues, "STORAGE_S3_SECRET_KEY") {
				t.Errorf("the offending variable was not named: %v", invalid.Issues)
			}
			// The injected assignment must not have reached the file either.
			for _, is := range invalid.Issues {
				if is.Var == "REGISTRATION_ENABLED" {
					t.Error("the injected line was parsed as a real assignment")
				}
			}
		})
	}
}

// A leading '$' is interpolated by compose out of `--env-file` values, so the
// container gets an expansion instead of the password. Warn — '$' is legal in a
// secret and escaping it is the operator's call.
func TestGenerateWarnsAboutInterpolatedValues(t *testing.T) {
	answers := baseAnswers()
	answers.Mail = &MailAnswers{Host: "smtp.example.net", Password: "$ecret", From: "hello@example.net"}
	res := generate(t, Request{Answers: answers})
	if !warned(res.Warnings, "SMTP_PASSWORD starts with '$'") {
		t.Errorf("no interpolation warning: %v", res.Warnings)
	}
	if res.Values["SMTP_PASSWORD"] != "$ecret" {
		t.Errorf("SMTP_PASSWORD = %q, want the value kept (a warning, not a rewrite)", res.Values["SMTP_PASSWORD"])
	}
}

// A quoted value is the other half of the same problem: this package stores the
// quote bytes verbatim (compose's dialect is not ours to invent), but compose
// STRIPS a surrounding pair out of an --env-file value — and expands the
// backslash escapes inside double quotes. So the container receives something
// shorter, or with a real tab in it, than the file shows. Warn, never rewrite,
// and never print the value.
func TestGenerateWarnsAboutQuotedValues(t *testing.T) {
	for _, tc := range []struct{ name, value, want string }{
		{"double quotes", `"p@ss"`, "SMTP_PASSWORD is wrapped in double quotes"},
		{"single quotes", `'p@ss'`, "SMTP_PASSWORD is wrapped in single quotes"},
		{"escapes inside double quotes", `"a\tb"`, "expands the backslash escapes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			answers := baseAnswers()
			answers.Mail = &MailAnswers{Host: "smtp.example.net", Password: tc.value, From: "hello@example.net"}
			res := generate(t, Request{Answers: answers})
			if !warned(res.Warnings, tc.want) {
				t.Errorf("no quoting warning mentioning %q: %v", tc.want, res.Warnings)
			}
			if res.Values["SMTP_PASSWORD"] != tc.value {
				t.Errorf("SMTP_PASSWORD = %q, want the value kept verbatim (a warning, not a rewrite)", res.Values["SMTP_PASSWORD"])
			}
			for _, w := range res.Warnings {
				if strings.Contains(w, "p@ss") || strings.Contains(w, `a\tb`) {
					t.Errorf("a warning printed the secret: %q", w)
				}
			}
		})
	}

	// An unquoted value, and a value that merely CONTAINS a quote, are left alone:
	// a warning nobody can act on is noise on every install.
	answers := baseAnswers()
	answers.Mail = &MailAnswers{Host: "smtp.example.net", Password: `p"ss'`, From: "hello@example.net"}
	res := generate(t, Request{Answers: answers})
	if warned(res.Warnings, "wrapped in") {
		t.Errorf("warned about a value that is not quoted: %v", res.Warnings)
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
	if b, err := os.ReadFile(path); err != nil || string(b) != "VIDRA_ENV=production\n" {
		t.Errorf("content = %q, %v", b, err)
	}
	// The temporary file is renamed into place, never left behind: a stray
	// .production.env.tmp* beside a live deployment is a world-readable copy of
	// every secret waiting to be found.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != "production.env" {
			t.Errorf("leftover file %q after WriteFile", e.Name())
		}
	}
}

func TestRenderCheckCommandMirrorsTheDeployScript(t *testing.T) {
	got := RenderCheckCommand("env/production.env", nil)
	for _, want := range []string{"-f docker-compose.yml", "-f docker-compose.prod.yml", "--env-file env/production.env", "--profile core --profile frontend", "config -q"} {
		if !strings.Contains(got, want) {
			t.Errorf("render-check command %q is missing %q", got, want)
		}
	}

	// deploy.sh appends EXTRA_COMPOSE_PROFILES from the env file to its own
	// profile list, so a check command without them renders a DIFFERENT compose
	// chain than the deploy it is supposed to pre-flight.
	got = RenderCheckCommand("env/production.env", map[string]string{"EXTRA_COMPOSE_PROFILES": "ipfs  observability"})
	for _, want := range []string{"--profile core --profile frontend --profile ipfs --profile observability"} {
		if !strings.Contains(got, want) {
			t.Errorf("render-check command %q is missing %q", got, want)
		}
	}

	// A path with a space is one the operator can paste, not a broken command.
	got = RenderCheckCommand("/srv/my deploy/env/production.env", nil)
	if !strings.Contains(got, `--env-file '/srv/my deploy/env/production.env'`) {
		t.Errorf("the env path was not shell-quoted: %s", got)
	}
	if !strings.Contains(RenderCheckCommand("env/it's.env", nil), `'env/it'\''s.env'`) {
		t.Errorf("a quote in the path was not escaped: %s", RenderCheckCommand("env/it's.env", nil))
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
