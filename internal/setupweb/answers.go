package setupweb

import (
	"strconv"
	"strings"

	"github.com/vidra/vidra-core/internal/setup"
)

// BuildAnswers is the ONE translation from the wizard's wire form to
// setup.Answers. Every endpoint that generates anything — Review and Apply —
// goes through it, so there is exactly one place where a browser's JSON becomes
// something the engine will act on.
//
// IT IS THE PARITY SEAM, and that is why it is one function with one job. The
// terminal builds the same struct in runSetup and interview(); this builds it
// from a form. Given the same answers the two must produce the same
// setup.Answers to the field, because everything after this point — validation,
// resolution, the file that lands on disk — is shared. TestWebAnswersMatchThe
// TerminalInterview in cmd/vidra is what holds that true, and it is deliberately
// in cmd/vidra rather than here: the terminal interview is the thing being
// matched, and a parity test that could not call it would be a parity test in
// name only.
//
// WHAT IT DOES NOT DO IS VALIDATE. Not one field is inspected for correctness
// here. NormalizeTLSMode, NormalizeOriginForMode and CheckAcmeEmail run at the
// prompt (POST /api/validate) so a typo costs one field rather than a whole
// wizard, and Generate runs them again over the assembled struct because it is
// the only opinion that counts. A third opinion in this function would be the
// "friendlier second check" that makes a wizard accept what the engine refuses.
func BuildAnswers(form Form, tmpl, existing *setup.EnvFile) setup.Answers {
	a := setup.Answers{
		Domain:       strings.TrimSpace(form.Domain),
		InstanceName: strings.TrimSpace(form.InstanceName),
		TLSMode:      strings.TrimSpace(form.TLSMode),
		AcmeEmail:    strings.TrimSpace(form.AcmeEmail),
		ReleaseTag:   strings.TrimSpace(form.ReleaseTag),
		CoreTag:      strings.TrimSpace(form.CoreTag),
		UserTag:      strings.TrimSpace(form.UserTag),
		SearchTag:    strings.TrimSpace(form.SearchTag),

		StorageBackend: strings.TrimSpace(form.Storage),
		S3: setup.S3Answers{
			Endpoint:  strings.TrimSpace(form.S3.Endpoint),
			Region:    strings.TrimSpace(form.S3.Region),
			Bucket:    strings.TrimSpace(form.S3.Bucket),
			AccessKey: strings.TrimSpace(form.S3.AccessKey),
			// NOT trimmed: a secret is bytes, and trimming one is a silent edit to
			// a credential. The terminal does not trim these either — readSecretFlag
			// strips only the trailing newline a file or a pipe adds.
			SecretKey: form.S3.SecretKey,
		},
		Database: setup.DatabaseAnswers{Mode: strings.TrimSpace(form.Database.Mode), URL: form.Database.URL},
		Redis:    setup.RedisAnswers{Mode: strings.TrimSpace(form.Redis.Mode), URL: form.Redis.URL},
	}
	// Giving the connection string IS the answer — the same inference runSetup
	// makes for `--database-url` with no `--database external` beside it. The
	// wizard's form always sends a mode, so this is belt to that braces: a caller
	// driving the API with curl who sends only a DSN gets the deployment they
	// obviously meant rather than a bundled Postgres and an ignored field.
	if a.Database.Mode == "" && strings.TrimSpace(a.Database.URL) != "" {
		a.Database.Mode = "external"
	}
	if a.Redis.Mode == "" && strings.TrimSpace(a.Redis.URL) != "" {
		a.Redis.Mode = "external"
	}
	// SEEDED FIRST, then overlaid — runSetup's order, and for runSetup's reason.
	// Answers.Features is AUTHORITATIVE rather than fall-through (Profiles(a) is
	// written to VIDRA_COMPOSE_PROFILES on every run), so a form that says nothing
	// about the optional components must mean "leave them as they are" and not
	// "turn all five off". A nil *FeatureForm is that silence; a non-nil one is
	// the operator having looked at the five checkboxes the wizard seeded from
	// this same call.
	a.Features = setup.FeaturesFromProfiles(setup.Effective(tmpl, existing, composeProfilesKey))
	if f := form.Features; f != nil {
		a.Features = setup.FeatureAnswers{Scan: f.Scan, Captions: f.Captions, Media: f.Media, Otel: f.Otel, IPFS: f.IPFS}
	}
	if m := form.Mail; m != nil {
		a.Mail = &setup.MailAnswers{
			Host:     strings.TrimSpace(m.Host),
			Port:     strings.TrimSpace(m.Port),
			Username: strings.TrimSpace(m.Username),
			Password: m.Password,
			From:     strings.TrimSpace(m.From),
		}
	}
	if r := form.Registration; r != nil {
		a.Registration = &setup.RegistrationAnswers{Enabled: r.Enabled, RequireApproval: r.RequireApproval}
	}
	if p := form.PeerTube; p != nil {
		a.PeerTube = &setup.PeerTubeAnswers{
			Enabled: p.Enabled,
			// NOT trimmed, for the reason S3.SecretKey is not: a connection string
			// is a credential, and trimming one is a silent edit to it. The terminal
			// does not trim it either.
			DatabaseURL:    p.SourceURL,
			StorageBackend: strings.TrimSpace(p.Storage),
			LocalRoot:      strings.TrimSpace(p.LocalRoot),
			S3: setup.S3Answers{
				Endpoint:  strings.TrimSpace(p.S3.Endpoint),
				Region:    strings.TrimSpace(p.S3.Region),
				Bucket:    strings.TrimSpace(p.S3.Bucket),
				AccessKey: strings.TrimSpace(p.S3.AccessKey),
				SecretKey: p.S3.SecretKey,
			},
			MediaMode:      strings.TrimSpace(p.MediaMode),
			ConflictPolicy: strings.TrimSpace(p.ConflictPol),
		}
	}
	return a
}

// composeProfilesKey is the profile list the features are seeded from. It is
// spelled here because internal/setup keeps its own copy unexported; the string
// is a cross-repo contract with the deploy scripts and changing it would break
// far more than this file.
const composeProfilesKey = "VIDRA_COMPOSE_PROFILES"

// SeedFor is every answer as it stands today, for the form fields to open with.
// Secrets are reported as PRESENT and never as a value — see the rules at the
// top of wire.go — and every non-secret default comes from setup.Effective, so
// the wizard's brackets and the interview's are filled from one rule.
//
// It is exported for the parity test in cmd/vidra, which has to build the form
// the PAGE would have posted after opening on these seeds. That test is the
// thing keeping the two front ends honest — pressing enter through the terminal
// interview and pressing Next through the wizard must produce the same
// deployment — and it could not be written against an unexported seed.
func SeedFor(tmpl, existing *setup.EnvFile) Seed {
	eff := func(key string) string { return setup.Effective(tmpl, existing, key) }
	set := func(key string) bool { return eff(key) != "" }

	s := Seed{
		TLSMode:      eff("VIDRA_TLS_MODE"),
		AcmeEmail:    eff("VIDRA_ACME_EMAIL"),
		InstanceName: eff("INSTANCE_NAME"),
		ReleaseTag:   eff("VIDRA_CORE_TAG"),
		Storage:      eff("STORAGE_BACKEND"),
		S3: S3Seed{
			Endpoint: eff("STORAGE_S3_ENDPOINT"),
			Region:   eff("STORAGE_S3_REGION"),
			Bucket:   eff("STORAGE_S3_BUCKET"),
			// The access key is echoed and the secret key is not, which is the
			// same split the terminal interview makes: it prompts for four of the
			// five S3 fields with the current value in brackets and asks for the
			// fifth with echo off. An access key identifies a credential; the
			// secret key IS one.
			AccessKey:           eff("STORAGE_S3_ACCESS_KEY"),
			SecretKeySet:        set("STORAGE_S3_SECRET_KEY"),
			CredentialsAnswered: setup.S3CredentialsAnswered(tmpl, existing),
		},
		Database: ConnSeed{Mode: componentMode(eff("VIDRA_EXTERNAL_POSTGRES")), URLSet: set("DATABASE_URL")},
		Redis:    ConnSeed{Mode: componentMode(eff("VIDRA_EXTERNAL_REDIS")), URLSet: set("REDIS_URL")},
		Features: featureSeed(setup.FeaturesFromProfiles(eff(composeProfilesKey))),
		Mail: MailSeed{
			Host:        eff("SMTP_HOST"),
			Port:        firstNonEmpty(eff("SMTP_PORT"), "587"),
			Username:    eff("SMTP_USERNAME"),
			From:        eff("SMTP_FROM"),
			PasswordSet: set("SMTP_PASSWORD"),
		},
		Registration: RegistrationForm{
			Enabled:         boolValue(eff("REGISTRATION_ENABLED"), false),
			RequireApproval: boolValue(eff("REGISTRATION_REQUIRE_APPROVAL"), false),
		},
		// The three enumerated answers fall back to the CODE's defaults, exactly
		// as the interview's brackets do (firstAnswer in cmd/vidra/setup.go): a
		// template that predates this block would otherwise open the wizard's
		// dropdowns on an empty option nobody chose.
		PeerTube: PeerTubeSeed{
			Enabled:        boolValue(eff("PEERTUBE_IMPORT_ENABLED"), false),
			SourceURLSet:   set("PEERTUBE_SOURCE_DATABASE_URL"),
			Storage:        firstNonEmpty(eff("PEERTUBE_SOURCE_STORAGE_BACKEND"), "local"),
			LocalRoot:      eff("PEERTUBE_SOURCE_STORAGE_LOCAL_ROOT"),
			S3Endpoint:     eff("PEERTUBE_SOURCE_S3_ENDPOINT"),
			S3Region:       eff("PEERTUBE_SOURCE_S3_REGION"),
			S3Bucket:       eff("PEERTUBE_SOURCE_S3_BUCKET"),
			S3AccessKey:    eff("PEERTUBE_SOURCE_S3_ACCESS_KEY"),
			S3SecretKeySet: set("PEERTUBE_SOURCE_S3_SECRET_KEY"),
			MediaMode:      firstNonEmpty(eff("PEERTUBE_IMPORT_MEDIA_MODE"), "copy"),
			ConflictPol:    firstNonEmpty(eff("PEERTUBE_IMPORT_CONFLICT_POLICY"), "skip"),
		},
	}
	// The interview's own test for "is SMTP working today", restated because the
	// wizard has to open the mail block in the same position: MAIL_ENABLED alone
	// does not count, because the template ships it true beside a blank SMTP_HOST
	// — which is a question, not a configuration.
	s.MailConfigured = boolValue(eff("MAIL_ENABLED"), false) && eff("SMTP_HOST") != ""

	// A DEFAULT THAT CANNOT PASS Check IS NOT A DEFAULT: the shipped template says
	// s3 next to <your Spaces access key> placeholders, so an operator who
	// accepted it would choose a backend with no credentials and have the whole
	// run refused at the end. Same flip, same rule, same engine call as the
	// interview's storage prompt.
	if s.Storage == "s3" && !s.S3.CredentialsAnswered {
		s.Storage = "local"
	}
	// The domain deliberately has NO template fallback: PUBLIC_BASE_URL in the
	// template is an example, and offering it would pre-fill the wizard with a
	// form the operator can press through to generate a file for example.com.
	// Only an existing deployment's own origin is a seed.
	if existing != nil {
		if v, ok := existing.Value("PUBLIC_BASE_URL"); ok && !setup.IsPlaceholder(v) {
			s.Domain = strings.TrimSpace(v)
		}
	}
	return s
}

// componentMode turns the env file's external-service switch back into the
// local|external answer the form carries.
func componentMode(v string) string {
	if boolValue(v, false) {
		return "external"
	}
	return "local"
}

func featureSeed(f setup.FeatureAnswers) FeatureForm {
	return FeatureForm{Scan: f.Scan, Captions: f.Captions, Media: f.Media, Otel: f.Otel, IPFS: f.IPFS}
}

// boolValue reads an env-file boolean, falling back to def for anything that is
// not one — a form field's default is never worth failing a wizard over. It is
// strconv.ParseBool and nothing else, which is why the interview having its own
// copy of this two-line helper is not the drift the seed RULES above would be.
func boolValue(v string, def bool) bool {
	b, err := strconv.ParseBool(strings.TrimSpace(v))
	if err != nil {
		return def
	}
	return b
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// secretNames lists the secret variables the existing file already holds a value
// for, sorted. NAMES ONLY — this is what the Welcome and Review steps show so an
// operator can see their KEKs will survive a re-run, and it is the shape the
// terminal's report() prints for the same reason.
func secretNames(existing *setup.EnvFile) []string {
	if existing == nil {
		return nil
	}
	var out []string
	for _, key := range setup.SecretVars() {
		if v, ok := existing.Value(key); ok && strings.TrimSpace(v) != "" && !setup.IsPlaceholder(v) {
			out = append(out, key)
		}
	}
	return out
}
