package setup

import (
	"errors"
	"strings"
	"testing"
)

// peerTubeTemplate is the fixture template plus the block the meta repo's
// env/production.env.example now ships. It is a SEPARATE fixture rather than an
// addition to testdata/template.env.example so the golden stays a picture of an
// ordinary install: an env file that shipped ten PeerTube keys to every operator
// who is not migrating would be the wrong artifact to pin.
const peerTubeTemplate = `VIDRA_ENV=production
PUBLIC_BASE_URL=https://example.com
JWT_SECRET=
MFA_KEY_KEK=<generate: openssl rand -base64 32>
POSTGRES_PASSWORD=<generate: openssl rand -hex 32>
REDIS_PASSWORD=
STORAGE_BACKEND=local
MAIL_ENABLED=false
REGISTRATION_ENABLED=false
REGISTRATION_REQUIRE_APPROVAL=false
PEERTUBE_IMPORT_ENABLED=false
PEERTUBE_SOURCE_DATABASE_URL=
PEERTUBE_SOURCE_STORAGE_BACKEND=local
PEERTUBE_SOURCE_STORAGE_LOCAL_ROOT=
PEERTUBE_SOURCE_S3_ENDPOINT=
PEERTUBE_SOURCE_S3_BUCKET=
PEERTUBE_SOURCE_S3_ACCESS_KEY=
PEERTUBE_SOURCE_S3_SECRET_KEY=
PEERTUBE_SOURCE_S3_REGION=
PEERTUBE_SOURCE_S3_USE_SSL=true
PEERTUBE_SOURCE_S3_FORCE_PATH_STYLE=false
PEERTUBE_IMPORT_CONFLICT_POLICY=skip
PEERTUBE_IMPORT_MEDIA_MODE=copy
`

func peerTubeAnswers() *PeerTubeAnswers {
	return &PeerTubeAnswers{
		Enabled:        true,
		DatabaseURL:    "postgres://readonly:pw@10.0.0.5:5432/peertube_prod?sslmode=require",
		StorageBackend: "s3",
		S3: S3Answers{
			Endpoint:  "s3.eu-central-003.backblazeb2.com",
			Region:    "eu-central-003",
			Bucket:    "peertube-source-media",
			AccessKey: "003SOURCEACCESSKEY",
			SecretKey: "source-secret",
		},
		MediaMode:      "copy",
		ConflictPolicy: "rename",
	}
}

// THE BUG THIS CHANGE IS ABOUT, stated as a test: an answered source has to land
// in the generated file. It did not before, because the template these keys are
// resolved against did not define a single one of them — they reached the
// CONTAINER through vidra-core's compose anchor and nothing else, so `vidra
// setup` could not write them and `vidra setup --check` could not validate them.
func TestPeerTubeAnswersLandInTheGeneratedFile(t *testing.T) {
	tmpl := parseTemplate(t, peerTubeTemplate)
	a := baseAnswers()
	a.PeerTube = peerTubeAnswers()
	res := generate(t, Request{Template: tmpl, Answers: a})

	for key, want := range map[string]string{
		"PEERTUBE_IMPORT_ENABLED":         "true",
		"PEERTUBE_SOURCE_DATABASE_URL":    "postgres://readonly:pw@10.0.0.5:5432/peertube_prod?sslmode=require",
		"PEERTUBE_SOURCE_STORAGE_BACKEND": "s3",
		"PEERTUBE_SOURCE_S3_ENDPOINT":     "s3.eu-central-003.backblazeb2.com",
		"PEERTUBE_SOURCE_S3_REGION":       "eu-central-003",
		"PEERTUBE_SOURCE_S3_BUCKET":       "peertube-source-media",
		"PEERTUBE_SOURCE_S3_ACCESS_KEY":   "003SOURCEACCESSKEY",
		"PEERTUBE_SOURCE_S3_SECRET_KEY":   "source-secret",
		"PEERTUBE_IMPORT_MEDIA_MODE":      "copy",
		"PEERTUBE_IMPORT_CONFLICT_POLICY": "rename",
	} {
		if got := res.Values[key]; got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	// The two transport knobs this engine deliberately does not ask about keep the
	// template's values rather than being blanked by an answer that never mentions
	// them.
	if got := res.Values["PEERTUBE_SOURCE_S3_USE_SSL"]; got != "true" {
		t.Errorf("PEERTUBE_SOURCE_S3_USE_SSL = %q, want the template's true", got)
	}
}

// An UNANSWERED block leaves a migration in flight exactly as it was. This is
// the re-run every operator does — `vidra setup --domain …` to change one
// thing — and the failure it guards is a wizard that silently closed the import
// surface halfway through a migration.
func TestPeerTubeUnansweredLeavesTheSourceAlone(t *testing.T) {
	tmpl := parseTemplate(t, peerTubeTemplate)
	existing := MergeSources(parseTemplate(t, `VIDRA_ENV=production
PUBLIC_BASE_URL=https://video.example.org
JWT_SECRET=cGFyaXR5LWp3dC1zZWNyZXQtdGhhdC1pcy1sb25nLWVub3VnaA==
MFA_KEY_KEK=MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=
POSTGRES_PASSWORD=0123456789abcdef0123456789abcdef
REDIS_PASSWORD=fedcba9876543210fedcba9876543210
PEERTUBE_IMPORT_ENABLED=true
PEERTUBE_SOURCE_DATABASE_URL=postgres://readonly:pw@10.0.0.5:5432/peertube_prod?sslmode=require
PEERTUBE_SOURCE_STORAGE_LOCAL_ROOT=/mnt/peertube-media
`))
	a := baseAnswers()
	a.PeerTube = nil
	res := generate(t, Request{Template: tmpl, Existing: existing, Answers: a})

	if got := res.Values["PEERTUBE_IMPORT_ENABLED"]; got != "true" {
		t.Errorf("PEERTUBE_IMPORT_ENABLED = %q; an unanswered block turned the import off", got)
	}
	if got := res.Values["PEERTUBE_SOURCE_DATABASE_URL"]; !strings.HasPrefix(got, "postgres://readonly:") {
		t.Errorf("the source DSN did not survive an unanswered re-run: %q", got)
	}
}

// Turning it OFF writes the gate and keeps the source. A migration is run on a
// schedule up to cutover, and a DSN that exists nowhere else must not be erased
// by an operator saying "not right now" — same rule as applyStorageRule keeping
// real S3 credentials when storage moves to local.
func TestPeerTubeDisablingKeepsTheSource(t *testing.T) {
	tmpl := parseTemplate(t, peerTubeTemplate)
	existing := MergeSources(parseTemplate(t, `VIDRA_ENV=production
PUBLIC_BASE_URL=https://video.example.org
JWT_SECRET=cGFyaXR5LWp3dC1zZWNyZXQtdGhhdC1pcy1sb25nLWVub3VnaA==
MFA_KEY_KEK=MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=
POSTGRES_PASSWORD=0123456789abcdef0123456789abcdef
REDIS_PASSWORD=fedcba9876543210fedcba9876543210
PEERTUBE_IMPORT_ENABLED=true
PEERTUBE_SOURCE_DATABASE_URL=postgres://readonly:pw@10.0.0.5:5432/peertube_prod?sslmode=require
`))
	a := baseAnswers()
	a.PeerTube = &PeerTubeAnswers{Enabled: false}
	res := generate(t, Request{Template: tmpl, Existing: existing, Answers: a})

	if got := res.Values["PEERTUBE_IMPORT_ENABLED"]; got != "false" {
		t.Errorf("PEERTUBE_IMPORT_ENABLED = %q, want false", got)
	}
	if got := res.Values["PEERTUBE_SOURCE_DATABASE_URL"]; !strings.HasPrefix(got, "postgres://readonly:") {
		t.Errorf("turning the import off erased the source DSN: %q", got)
	}
}

// A DEPLOYMENT TREE ON AN OLDER TEMPLATE still gets its answers written — into
// the managed block, exactly as the component keys are. The alternative is the
// silent drop that made this whole change necessary: Generate resolves against
// the template's keys, so an answer for a key the template does not define goes
// nowhere at all.
func TestPeerTubeAnswersAreAppendedToAnOlderTemplate(t *testing.T) {
	tmpl := parseTemplate(t, `VIDRA_ENV=production
PUBLIC_BASE_URL=https://example.com
JWT_SECRET=
MFA_KEY_KEK=<generate: openssl rand -base64 32>
POSTGRES_PASSWORD=<generate: openssl rand -hex 32>
REDIS_PASSWORD=
STORAGE_BACKEND=local
MAIL_ENABLED=false
REGISTRATION_ENABLED=false
`)
	a := baseAnswers()
	a.PeerTube = peerTubeAnswers()
	res := generate(t, Request{Template: tmpl, Answers: a})

	if got := res.Values["PEERTUBE_SOURCE_DATABASE_URL"]; got == "" {
		t.Fatal("the source DSN was dropped: the template does not define the key, which is the exact silent-drop this block exists to prevent")
	}
	if !strings.Contains(string(res.Content), "PEERTUBE_IMPORT_ENABLED=true") {
		t.Errorf("the rendered file does not assign the gate:\n%s", res.Content)
	}
	// And it round-trips: the appended block is a real part of the file, not a
	// comment, so re-reading it sees the same deployment.
	back, err := ParseEnvFile(res.Content)
	if err != nil {
		t.Fatalf("the generated file does not parse back: %v", err)
	}
	if v, _ := back.Value("PEERTUBE_SOURCE_S3_BUCKET"); v != "peertube-source-media" {
		t.Errorf("re-reading the generated file lost the source bucket: %q", v)
	}
}

// "No, I am not migrating" against a template and a file that have never heard
// of the import writes NOTHING. Appending PEERTUBE_IMPORT_ENABLED=false under a
// "Managed by vidra setup" header would be the engine announcing a default to
// every install that has nothing to do with PeerTube.
func TestPeerTubeDecliningOnAnOlderTemplateWritesNothing(t *testing.T) {
	tmpl := parseTemplate(t, `VIDRA_ENV=production
PUBLIC_BASE_URL=https://example.com
JWT_SECRET=
MFA_KEY_KEK=<generate: openssl rand -base64 32>
POSTGRES_PASSWORD=<generate: openssl rand -hex 32>
REDIS_PASSWORD=
STORAGE_BACKEND=local
MAIL_ENABLED=false
REGISTRATION_ENABLED=false
`)
	a := baseAnswers()
	a.PeerTube = &PeerTubeAnswers{Enabled: false}
	res := generate(t, Request{Template: tmpl, Answers: a})

	if strings.Contains(string(res.Content), "PEERTUBE") {
		t.Errorf("declining the migration wrote a PeerTube key into an install that has nothing to do with one:\n%s", res.Content)
	}
}

// Check is the api's boot validation, so a source that would not boot is refused
// BEFORE the file is written rather than discovered by a crash-looping api.
func TestPeerTubeGenerateRefusesASourceThatWouldNotBoot(t *testing.T) {
	tmpl := parseTemplate(t, peerTubeTemplate)
	for _, tc := range []struct {
		name    string
		mutate  func(*PeerTubeAnswers)
		wantVar string
	}{
		{"the gate on with no source at all", func(p *PeerTubeAnswers) { p.DatabaseURL = "" }, "PEERTUBE_SOURCE_DATABASE_URL"},
		{"a DSN that is not one", func(p *PeerTubeAnswers) { p.DatabaseURL = "peertube-db.internal" }, "PEERTUBE_SOURCE_DATABASE_URL"},
		{"an endpoint pasted with its scheme", func(p *PeerTubeAnswers) { p.S3.Endpoint = "https://s3.example.net" }, "PEERTUBE_SOURCE_S3_ENDPOINT"},
		{"a media mode that does not exist", func(p *PeerTubeAnswers) { p.MediaMode = "move" }, "PEERTUBE_IMPORT_MEDIA_MODE"},
		{"a conflict policy that does not exist", func(p *PeerTubeAnswers) { p.ConflictPolicy = "overwrite" }, "PEERTUBE_IMPORT_CONFLICT_POLICY"},
		{"a source backend that does not exist", func(p *PeerTubeAnswers) { p.StorageBackend = "gcs" }, "PEERTUBE_SOURCE_STORAGE_BACKEND"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := baseAnswers()
			p := peerTubeAnswers()
			tc.mutate(p)
			a.PeerTube = p
			_, err := Generate(Request{Template: tmpl, Answers: a, Rand: &seqReader{}})
			if err == nil {
				t.Fatal("the file was written anyway")
			}
			var invalid *ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("not a per-variable refusal: %v", err)
			}
			found := false
			for _, is := range invalid.Issues {
				if is.Var == tc.wantVar {
					found = true
				}
			}
			if !found {
				t.Errorf("no issue attributed to %s: %+v", tc.wantVar, invalid.Issues)
			}
		})
	}
}

// NO FINDING ABOUT THE SOURCE DSN MAY QUOTE IT. It carries the source database's
// password, and these messages are printed by a prompt whose whole point is that
// the answer is not echoed, by `vidra setup --check`, and by the wizard's Review
// step — a %q of the input would put the credential in a scrollback, a CI log
// and a browser.
func TestPeerTubeDSNIsNeverEchoedInAFinding(t *testing.T) {
	const secret = "sup3r-s3cret-passw0rd"
	for _, dsn := range []string{
		"not-a-dsn-" + secret,
		"mysql://root:" + secret + "@db.example.net/peertube",
		"postgres://readonly:" + secret + "@/peertube",
	} {
		err := CheckPeerTubeSourceDatabaseURL(dsn)
		if err == nil {
			t.Errorf("%q was accepted as a PostgreSQL DSN", dsn)
			continue
		}
		if strings.Contains(err.Error(), secret) {
			t.Errorf("the finding echoes the password: %s", err)
		}
	}
}

func TestPeerTubeSourceDSNShape(t *testing.T) {
	for _, tc := range []struct {
		dsn string
		ok  bool
	}{
		{"", true}, // unanswered; whether one is REQUIRED is a separate rule
		{"postgres://readonly:pw@10.0.0.5:5432/peertube_prod?sslmode=require", true},
		{"postgresql://readonly@db.internal/peertube", true},
		// The keyword/value dialect pgx also accepts. An operator whose replica is
		// addressed this way has a working DSN, and refusing it would refuse the
		// deployment this rule was written to help.
		{"host=db.internal user=readonly dbname=peertube sslmode=require", true},
		{"peertube-db.internal", false},
		{"psql -h db.internal -U readonly peertube", false},
		{"mysql://readonly@db.internal/peertube", false},
		{"postgres:///peertube", false},
	} {
		err := CheckPeerTubeSourceDatabaseURL(tc.dsn)
		if tc.ok && err != nil {
			t.Errorf("%q was refused: %v", tc.dsn, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("%q was accepted", tc.dsn)
		}
	}
}

// The prompt validators normalise exactly as config.Load does, so a wizard never
// refuses an answer the api would have accepted.
func TestPeerTubeEnumeratedAnswersAreCaseAndSpaceInsensitive(t *testing.T) {
	for _, tc := range []struct {
		name  string
		check func(string) error
		value string
	}{
		{"media mode", CheckPeerTubeMediaMode, " Copy "},
		{"conflict policy", CheckPeerTubeConflictPolicy, "SKIP"},
		{"source backend", CheckPeerTubeSourceStorageBackend, " S3"},
	} {
		if err := tc.check(tc.value); err != nil {
			t.Errorf("%s refused %q, which config.Load accepts: %v", tc.name, tc.value, err)
		}
	}
}

// The PeerTube keys are the engine's own output, so `vidra doctor` must not
// report a migrating deployment as having drifted from a template that predates
// the block.
func TestManagedKeysCoversThePeerTubeBlock(t *testing.T) {
	got := map[string]bool{}
	for _, k := range ManagedKeys() {
		got[k] = true
	}
	for _, k := range peerTubeKeys {
		if !got[k] {
			t.Errorf("ManagedKeys() is missing %s", k)
		}
	}
	if !got["VIDRA_COMPOSE_PROFILES"] {
		t.Error("ManagedKeys() lost the component keys")
	}
}

func parseTemplate(t *testing.T, content string) *EnvFile {
	t.Helper()
	f, err := ParseEnvFile([]byte(content))
	if err != nil {
		t.Fatalf("parse template: %v", err)
	}
	return f
}
