// Package setup generates a production env file from the deployment template.
//
// It is the engine behind `vidra setup` (and, later, the web wizard and the
// installer): one library, three front-ends, so an unattended install and a
// hand-held one cannot disagree about what a correct env file looks like. See
// docs/productionization/interfaces.md §1 and phase-1 item 8 in the meta repo.
//
// Three rules shape the whole package:
//
//  1. The TEMPLATE IS THE INPUT. The authoritative format lives in the meta
//     repo (env/production.env.example) — comment blocks that explain every
//     knob, blank secrets with their `generate:` commands, deliberately
//     malformed <...> placeholders. This package takes that file as an argument
//     and re-emits it with values filled, preserving comments and order, so the
//     generated file stays as self-documenting as the template. It deliberately
//     embeds no copy: a stale duplicate of another repo's template is worse
//     than no template at all.
//
//  2. NOTHING IS EVER SILENTLY RE-MINTED. Given an existing env file, every
//     non-empty value is preserved; only missing and blank keys are filled. A
//     secret changes only when --rotate names it, and rotating a KEK
//     additionally needs explicit confirmation because it orphans data already
//     sealed in the database. Re-running setup on a live instance is safe.
//
//     The rule extends to a KEK the existing configuration leaves BLANK: on
//     anything but a genuine first install (no existing source at all) a blank
//     KEK is refused rather than minted, because "blank" there is far more
//     likely to be a truncated/half-restored file than a new feature — and
//     minting over it destroys the same rows a rotation would. See the kek gate
//     in the resolution loop below.
//
//  3. THE PRODUCT IS VALIDATED BY THE BOOT ENGINE. Generation ends by running
//     the api's own config validation (config.CheckEnv) over the file it just
//     rendered, and refuses to hand back a file that would not boot. There is
//     no second validation library to drift.
//
// COMPONENT ANSWERS MAP TO COMPOSE PROFILES, and the mapping is written into the
// env file rather than remembered by the operator. The deploy scripts read
// VIDRA_COMPOSE_PROFILES (which profiles to enable) and VIDRA_EXTERNAL_POSTGRES /
// VIDRA_EXTERNAL_REDIS (whether to add the docker-compose.external-*.yml overlay
// that keeps the bundled service from starting). The whole table:
//
//	answer                     env keys written              compose profiles
//	----------------------------------------------------------------------------
//	(always)                   VIDRA_COMPOSE_PROFILES         core, frontend
//	Features.Scan              (profile list)                 + scan     (clamav)
//	Features.Captions          (profile list)                 + captions (whisper)
//	Features.Media             (profile list)                 + media    (rtmp)
//	Features.Otel              (profile list)                 + otel     (collector, jaeger)
//	Features.IPFS              (profile list)                 + ipfs     (kubo)
//	Database  local            VIDRA_EXTERNAL_POSTGRES=false  (bundled postgres, core)
//	Database  external+URL     VIDRA_EXTERNAL_POSTGRES=true   (+ external-postgres overlay)
//	                           DATABASE_URL
//	Redis     local            VIDRA_EXTERNAL_REDIS=false     (bundled redis, core)
//	Redis     external+URL     VIDRA_EXTERNAL_REDIS=true      (+ external-redis overlay)
//	                           REDIS_URL, SEARCH_REDIS_URL
//	Storage   local            STORAGE_BACKEND=local          NONE
//	Storage   s3               STORAGE_BACKEND=s3             NONE
//	                           STORAGE_S3_*
//	TLS       acme|internal    VIDRA_TLS_MODE                 (caddy, always on)
//	                           VIDRA_ACME_EMAIL
//
// Two absences in that table are deliberate. NEITHER storage answer enables the
// `storage` profile: its minio is a DEV convenience (the compose file says so),
// and STORAGE_BACKEND=s3 in a deployment means a bucket at STORAGE_S3_ENDPOINT —
// an endpoint HOST that is somebody else's service — so starting a local object
// store would be a container nothing talks to. And the private-IPFS variants
// (ipfs-private, ipfs-private-cluster) are not offered: the IPFS answer maps to
// the plain `ipfs` profile only, and a private swarm stays a manual
// EXTRA_COMPOSE_PROFILES choice.
//
// The TLS answers are the exception in that table: they turn no container on and
// the api never reads them. They are the input to the OTHER file this engine
// generates — deploy/Caddyfile.local, rendered from the meta repo's
// deploy/Caddyfile by RenderCaddyfile — and they live in the env file so a re-run
// and `vidra doctor` can both see what the proxy config was generated with.
//
// A profile turns a CONTAINER on; the api-side flag that makes the api USE it
// (MALWARE_SCAN_ENABLED, WHISPER_ENABLED, OTEL_ENABLED, ...) is a separate value
// in the template and stays the operator's, exactly as it was before this
// mapping existed.
//
// What this package does NOT do: run docker. The compose render check is the
// other half of the proof (compose substitution, required-variable assertions,
// profiles) and it belongs to the operator or to deploy.sh — RenderCheckCommand
// returns the exact command to run.
package setup

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/vidra/vidra-core/internal/config"
)

// Answers is the operator-supplied part of the configuration. The surface is
// deliberately small: everything not asked about here keeps the template's
// value, which is the whole reason the template is the input.
type Answers struct {
	// Domain is the instance's public origin, as a bare host
	// (video.example.org) or a full https origin. It fills PUBLIC_BASE_URL and
	// the other single-origin keys the template carries
	// (NEXT_PUBLIC_API_BASE_URL, CORS_ALLOWED_ORIGINS) — one domain, routed by
	// path, is the topology the template documents.
	Domain string

	// ReleaseTag pins all three images (VIDRA_CORE_TAG, VIDRA_USER_TAG,
	// VIDRA_SEARCH_TAG). The per-service fields below override it individually;
	// deploying the exact tags staging validated is the promotion rule.
	ReleaseTag string
	CoreTag    string
	UserTag    string
	SearchTag  string

	// TLSMode chooses the certificate issuer for the managed Caddy: "acme"
	// (Let's Encrypt), "acme-staging" (the same order against the staging
	// directory, as a rehearsal) or "internal" (Caddy's local CA, for a bring-up
	// before DNS points at the host). "" is unanswered — it keeps the existing
	// file's mode, and RenderCaddyfile reads it as acme.
	//
	// AcmeEmail is the contact address Let's Encrypt sends expiry and revocation
	// notices to. It is REQUIRED by both acme modes: an account without one gets
	// no warning before a renewal problem becomes an outage.
	TLSMode   string
	AcmeEmail string

	// StorageBackend is "local" or "s3" ("" keeps the template's choice).
	StorageBackend string
	// S3 is consulted only for StorageBackend == "s3".
	S3 S3Answers

	// Database and Redis choose between the bundled container and a managed
	// service. They are the answers the compose overlays hang off: external means
	// the deploy adds docker-compose.external-postgres.yml /
	// docker-compose.external-redis.yml, so the bundled service never starts and
	// the connection string is the only way to reach a database.
	Database DatabaseAnswers
	Redis    RedisAnswers

	// Features are the optional components, each one a compose profile. Unlike
	// every other answer here, this one is AUTHORITATIVE rather than
	// fall-through: Profiles(a) is written to VIDRA_COMPOSE_PROFILES on every
	// run, because "off" and "unanswered" are the same zero value in a struct of
	// bools and silently keeping a profile an operator just turned off would be
	// the worse failure. A front-end offering a re-run therefore SEEDS this from
	// the existing file — FeaturesFromProfiles does exactly that, and `vidra
	// setup` calls it before applying flags or prompts.
	Features FeatureAnswers

	// Mail nil means "no SMTP answers": mail is turned OFF rather than left on
	// with nowhere to send, because the template ships MAIL_ENABLED=true with a
	// blank SMTP_HOST and the api refuses that combination.
	Mail *MailAnswers

	// Registration nil keeps the template's registration policy.
	Registration *RegistrationAnswers
}

// S3Answers are the STORAGE_S3_* values. Endpoint is a HOST, no scheme — the
// api rejects a value containing "://" and picks http/https from
// STORAGE_S3_USE_SSL, which stays at the template's value.
type S3Answers struct {
	Endpoint  string
	Region    string
	Bucket    string
	AccessKey string
	SecretKey string
}

// DatabaseAnswers picks the PostgreSQL a deployment runs on. Mode is "local"
// (the bundled container in the core profile), "external" (a managed service),
// or "" for unanswered, which keeps whatever the existing file or the template
// says. URL is the managed connection string; it is REQUIRED with external, and
// an empty URL on a re-run keeps the one already in the file rather than
// clearing it.
type DatabaseAnswers struct {
	Mode string
	URL  string
}

// RedisAnswers is the same choice for Redis. It is its own type rather than a
// shared one because the two are answered independently — a managed Postgres
// with the bundled Redis beside it is the common droplet shape — and because a
// single type would invite a single answer.
type RedisAnswers struct {
	Mode string
	URL  string
}

// FeatureAnswers are the optional components, one bool per compose profile. See
// the mapping table in the package doc; the private-IPFS variants are
// deliberately not here.
type FeatureAnswers struct {
	Scan     bool
	Captions bool
	Media    bool
	Otel     bool
	IPFS     bool
}

// MailAnswers is the optional SMTP block. Port is a string so "unanswered" and
// "0" stay distinguishable; the api validates the range.
type MailAnswers struct {
	Host     string
	Port     string
	Username string
	Password string
	From     string
}

// RegistrationAnswers is the signup policy. Both fields are explicit: with the
// owner-claim bootstrap the recommended first boot is Enabled=false, claim the
// owner account, then re-deploy with it open.
type RegistrationAnswers struct {
	Enabled         bool
	RequireApproval bool
}

// Request is one generation. Template is required; Existing is the env file
// currently in use (nil on a first install) and is what makes the run
// non-destructive. When more than one preservation source exists — the file
// being overwritten AND a --from file — combine them with MergeSources first,
// listing the file that will be overwritten FIRST so its own values win.
type Request struct {
	Template *EnvFile
	Existing *EnvFile
	Answers  Answers

	// Rotate names variables whose secret must be re-generated even though a
	// value already exists. Every entry must be in the secret manifest.
	Rotate []string
	// ConfirmDestructive authorises rotating a *_KEK. Without it, a KEK in
	// Rotate is an error that spells out what the rotation would destroy. It is
	// deliberately its OWN answer (the CLI spells it --yes-i-know, matching the
	// migrate force gate) and not the routine "yes, rewrite the file" one: an
	// operator confirming an in-place rewrite has not thereby agreed to orphan
	// every TOTP secret in the database.
	ConfirmDestructive bool

	// Rand is the entropy source for generated secrets; nil means crypto/rand.
	Rand io.Reader
}

// Result is the generated file plus what an operator needs told. Secret VALUES
// appear only in Content — the slices carry variable names, so a summary can be
// printed or logged without leaking anything.
type Result struct {
	// Content is the file to write; WriteFile puts it on disk with mode 0600.
	Content []byte
	// Values is the environment Content describes (every assignment in it).
	Values map[string]string

	// Generated, Preserved and Rotated are secret variable names: newly minted,
	// kept from the existing file, and deliberately re-minted this run.
	Generated []string
	Preserved []string
	Rotated   []string
	// Carried are keys the existing file set that the template does not define;
	// they are appended to Content in their own block rather than dropped.
	Carried []string
	// Warnings are non-fatal things the operator should know (a release tag left
	// at the template default, mail turned off for want of an SMTP host).
	Warnings []string
}

// Issue is one problem with a candidate configuration. Var is the single
// variable to fix, or "" for a rule that spans variables (a required
// combination), which belongs to no field.
type Issue struct {
	Var string
	Msg string
}

// ValidationError is returned by Generate when the file it rendered would not
// boot. Generate returns no content in that case: refusing to write is the
// point.
type ValidationError struct {
	Issues []Issue
}

func (e *ValidationError) Error() string {
	parts := make([]string, 0, len(e.Issues))
	for _, is := range e.Issues {
		if is.Var != "" {
			parts = append(parts, is.Var+": "+is.Msg)
			continue
		}
		parts = append(parts, is.Msg)
	}
	return fmt.Sprintf("setup: the generated configuration is not valid (%d problem(s)): %s", len(e.Issues), strings.Join(parts, "; "))
}

// singleOriginKeys are the keys the domain answer fills. They hold the SAME
// value on purpose: PUBLIC_BASE_URL is used for feed/embed links AND for
// OAuth/federation identity URLs, so a split api/web pair of origins cannot be
// correct — the template's topology is one origin with the reverse proxy
// routing /api/* and friends to the api. Keys absent from the template are
// skipped, never invented.
var singleOriginKeys = []string{"PUBLIC_BASE_URL", "NEXT_PUBLIC_API_BASE_URL", "CORS_ALLOWED_ORIGINS"}

// releaseTagKeys are the image pins, in fan-out order.
var releaseTagKeys = []string{"VIDRA_CORE_TAG", "VIDRA_USER_TAG", "VIDRA_SEARCH_TAG"}

// s3Keys are the STORAGE_S3_* keys the storage answer owns.
var s3Keys = []string{"STORAGE_S3_ENDPOINT", "STORAGE_S3_REGION", "STORAGE_S3_BUCKET", "STORAGE_S3_ACCESS_KEY", "STORAGE_S3_SECRET_KEY"}

// The component keys, spelled once. They are read by the DEPLOY SCRIPTS (and by
// RenderCheckCommand, which has to render the same chain the deploy will), not
// by the api — internal/config has never heard of them, which is why Check waves
// them through and why this package has to validate their combinations itself.
const (
	profilesKey         = "VIDRA_COMPOSE_PROFILES"
	extraProfilesKey    = "EXTRA_COMPOSE_PROFILES"
	externalPostgresKey = "VIDRA_EXTERNAL_POSTGRES"
	externalRedisKey    = "VIDRA_EXTERNAL_REDIS"
	databaseURLKey      = "DATABASE_URL"
	redisURLKey         = "REDIS_URL"
	searchRedisURLKey   = "SEARCH_REDIS_URL"
	tlsModeKey          = "VIDRA_TLS_MODE"
	acmeEmailKey        = "VIDRA_ACME_EMAIL"
)

// searchRedisDefaultDB is the logical Redis database vidra-search runs on when
// the bundled Redis is used: the compose chain hands the search service
// `redis://…/1` while the api gets `…/0`. It is the index deriveSearchRedisURL
// aims at, and the reason a straight copy of REDIS_URL is not an acceptable
// default — see that function.
const searchRedisDefaultDB = 1

// managedKeys are the keys the component and TLS answers own, in the order they
// render. A template that does not define them yet gets them APPENDED (see
// applyComponentRule): the deploy scripts must find VIDRA_COMPOSE_PROFILES in
// every file this engine writes, including one generated from a template that
// predates the key.
var managedKeys = []string{profilesKey, externalPostgresKey, externalRedisKey, databaseURLKey, redisURLKey, searchRedisURLKey, tlsModeKey, acmeEmailKey}

// externalOverlays pairs each external-service switch with the compose overlay
// the deploy adds for it and the connection strings it cannot work without. The
// ORDER is the order the overlays go on the command line.
//
// urlKeys is a LIST because the redis overlay asserts TWO of them. It rewrites
// the api's `REDIS_URL: ${REDIS_URL:?…}` and the search service's
// `REDIS_URL: ${SEARCH_REDIS_URL:?…}`, both with the `:?` form, so an env file
// that names only one of them fails the compose render — before a single
// container starts — with an error about a variable this engine was supposed to
// have written. The first entry is the PRIMARY: it is the one an operator
// answers, and the one the others are derived from.
var externalOverlays = []struct {
	flag    string
	urlKeys []string
	overlay string
	service string
}{
	{externalPostgresKey, []string{databaseURLKey}, "docker-compose.external-postgres.yml", "PostgreSQL"},
	{externalRedisKey, []string{redisURLKey, searchRedisURLKey}, "docker-compose.external-redis.yml", "Redis"},
}

// urlKeyRoles says what each asserted connection string is FOR. The "it is
// blank" half of the message is the same for all of them; what has to go in it
// is not, and for SEARCH_REDIS_URL the difference is the whole point.
var urlKeyRoles = map[string]string{
	databaseURLKey: "the managed database's connection string (postgres://USER:PASSWORD@HOST:5432/DB?sslmode=require)",
	redisURLKey:    "the api's Redis DSN (rediss://:PASSWORD@HOST:6379/0)",
	searchRedisURLKey: "the SEARCH service's Redis DSN, which must name a DIFFERENT logical database from " + redisURLKey +
		" (rediss://:PASSWORD@HOST:6379/1) — the two services share no keyspace, and pointing both at the same one lets search's index writes and the api's cache and rate-limit counters evict each other",
}

// baseProfiles are the profiles every deployment runs: the api and its bundled
// datastores, and the frontend.
var baseProfiles = []string{"core", "frontend"}

// featureProfiles is the ONE mapping from an optional component to its compose
// profile, in the order the profile list is written. Both directions go through
// it — Profiles and FeaturesFromProfiles — so a re-run round-trips a profile
// list it wrote itself.
var featureProfiles = []struct {
	profile string
	field   func(*FeatureAnswers) *bool
}{
	{"scan", func(f *FeatureAnswers) *bool { return &f.Scan }},
	{"captions", func(f *FeatureAnswers) *bool { return &f.Captions }},
	{"media", func(f *FeatureAnswers) *bool { return &f.Media }},
	{"otel", func(f *FeatureAnswers) *bool { return &f.Otel }},
	{"ipfs", func(f *FeatureAnswers) *bool { return &f.IPFS }},
}

// Profiles is the compose profile list an answer set implies: always core and
// frontend, then one profile per enabled feature in a fixed order. It is pure —
// no file, no environment — so the wizard can show the deployment's shape while
// the operator is still answering, and it is what the engine writes to
// VIDRA_COMPOSE_PROFILES.
//
// It never emits `storage`, and never the private-IPFS variants; see the mapping
// table in the package doc for why.
func Profiles(a Answers) []string {
	out := append([]string(nil), baseProfiles...)
	features := a.Features
	for _, fp := range featureProfiles {
		if *fp.field(&features) {
			out = append(out, fp.profile)
		}
	}
	return out
}

// FeaturesFromProfiles reads a VIDRA_COMPOSE_PROFILES value back into the
// answers that would produce it. It is the inverse of Profiles for the profiles
// Profiles emits, and it IGNORES everything else — core, frontend, a manual
// ipfs-private, a profile a later version added — because those are not
// questions this engine asks.
//
// It exists so a re-run preserves what the last one chose: Answers.Features is
// authoritative, so a front-end that did not seed it from the current file would
// silently turn every optional component off.
func FeaturesFromProfiles(value string) FeatureAnswers {
	var out FeatureAnswers
	for _, name := range strings.Fields(value) {
		for _, fp := range featureProfiles {
			if fp.profile == name {
				*fp.field(&out) = true
			}
		}
	}
	return out
}

// Generate resolves every value, renders the file, and validates the rendered
// bytes with the api's own config engine. On a validation failure it returns a
// *ValidationError and NO content.
func Generate(req Request) (*Result, error) {
	if req.Template == nil || len(req.Template.Keys()) == 0 {
		return nil, errors.New("setup: the template has no KEY=value assignments — pass the deployment template (env/production.env.example)")
	}
	rotate, err := rotationSet(req)
	if err != nil {
		return nil, err
	}
	answers, err := answerValues(req.Answers)
	if err != nil {
		return nil, err
	}
	if err := requireDomain(req, answers); err != nil {
		return nil, err
	}

	res := &Result{Values: map[string]string{}}

	// A genuine first install is the ONLY situation in which a blank KEK may be
	// minted: with no preservation source at all there is no database whose rows
	// a fresh KEK could orphan. Any existing source — even one that does not
	// mention the KEK — means this deployment already exists.
	//
	// "No source" means NO FILE, not "a file with nothing in it". A truncated or
	// half-restored production.env parses to zero assignments, and counting that
	// as a first install disarms this gate for precisely the file it exists to
	// protect: the KEK would be minted over a live database. MergeSources returns
	// a non-nil (if empty) result as soon as one real source is passed, so the
	// difference between "the file was empty" and "there was no file" survives
	// the merge.
	firstInstall := req.Existing == nil

	// Resolution, in template order so secret generation is deterministic.
	// Precedence: rotation > answer > existing value > template value >
	// generated secret > left as the template had it (blank or placeholder,
	// which Check then rejects if it matters).
	for _, key := range req.Template.Keys() {
		tv, _ := req.Template.Value(key)
		ev, hasExisting := existingValue(req.Existing, key)

		switch {
		case rotate[key]:
			v, gerr := mint(key, req.Rand)
			if gerr != nil {
				return nil, gerr
			}
			res.Values[key] = v
			res.Rotated = append(res.Rotated, key)
		case answers[key] != "":
			res.Values[key] = answers[key]
		case hasExisting:
			res.Values[key] = ev
			if _, isSecret := secretManifest[key]; isSecret {
				res.Preserved = append(res.Preserved, key)
			}
		case !needsValue(tv):
			res.Values[key] = tv
		default:
			if spec, isSecret := secretManifest[key]; isSecret {
				if spec.kek && !firstInstall {
					return nil, blankKEKError(key, spec)
				}
				v, gerr := mint(key, req.Rand)
				if gerr != nil {
					return nil, gerr
				}
				res.Values[key] = v
				res.Generated = append(res.Generated, key)
				continue
			}
			res.Values[key] = tv
		}
	}

	// Keys the previous file set that this template does not define — a
	// FEDERATION_KEY_KEK the operator uncommented, an EXTRA_COMPOSE_PROFILES
	// line. Dropping them would silently change the deployment, so they are
	// carried into their own block.
	for _, key := range existingKeys(req.Existing) {
		if req.Template.Has(key) {
			continue
		}
		// A component key an older template does not define is not "carried": it
		// is resolved (answer first) and re-emitted by applyComponentRule below,
		// in its own block. Carrying it as well would assign it TWICE in the
		// rendered file, which ParseEnvFile rejects — and the file would be the
		// one this run was supposed to produce.
		if isManagedKey(key) {
			continue
		}
		ev, _ := req.Existing.Value(key)
		if needsValue(ev) {
			continue
		}
		if rotate[key] {
			v, gerr := mint(key, req.Rand)
			if gerr != nil {
				return nil, gerr
			}
			res.Values[key] = v
			res.Rotated = append(res.Rotated, key)
		} else {
			res.Values[key] = ev
			if _, isSecret := secretManifest[key]; isSecret {
				res.Preserved = append(res.Preserved, key)
			}
		}
		res.Carried = append(res.Carried, key)
	}

	if err := unusedRotations(rotate, res.Rotated); err != nil {
		return nil, err
	}

	applyStorageRule(req, res)
	applyMailRule(res)
	added := applyComponentRule(req, answers, res)

	// Line-shape before rendering: a value carrying a newline would not be a bad
	// value, it would be EXTRA LINES — see lineShapeIssues.
	if issues := lineShapeIssues(res.Values); len(issues) > 0 {
		return nil, &ValidationError{Issues: issues}
	}
	res.Warnings = append(res.Warnings, releaseTagWarnings(req, res.Values)...)
	res.Warnings = append(res.Warnings, Warnings(res.Values)...)

	content := req.Template.Render(res.Values, res.Carried, added)
	// Validate the BYTES that would be written, not the map that produced them:
	// re-parsing is the only way to prove the file on disk is the file that
	// passed validation.
	rendered, err := ParseEnvFile(content)
	if err != nil {
		return nil, fmt.Errorf("setup: the generated file does not parse back: %w", err)
	}
	if issues := Check(rendered.Values()); len(issues) > 0 {
		return nil, &ValidationError{Issues: issues}
	}
	res.Content = content
	res.Values = rendered.Values()
	return res, nil
}

// Check validates a candidate environment the way the api will read it: leftover
// <...> placeholders first, then the api's own boot validation — and always with
// PRODUCTION rules.
//
// The VIDRA_ENV pass is the one place Check deliberately does not just mirror
// config: an env file that omits VIDRA_ENV (or sets development) would otherwise
// be validated in development mode, where a blank JWT_SECRET falls back to the
// dev constant and `CORS_ALLOWED_ORIGINS=*` is allowed — a green check on a file
// that cannot be deployed. So a non-production VIDRA_ENV is reported as its own
// problem AND the rest of the file is checked with production forced, so the
// operator sees the real findings in the same pass instead of after a fix and a
// second run.
//
// The placeholder pass is not redundant. `<your Spaces access key>` is a
// NON-EMPTY string, so config's "STORAGE_S3_ACCESS_KEY is required" check is
// perfectly happy with it; the failure would land days later as an S3
// authentication error. Blanks, by contrast, are legitimately "unset" for many
// keys and are left to config.
//
// The reporting shape is the boot engine's, deliberately: every MALFORMED value
// is reported in one pass (config collects parse failures before validating),
// and then the first semantic failure. An operator fixing a generated file
// therefore sees all the typos at once, then one rule at a time — exactly what
// they would see booting the api, which is the point of not having a second
// validation library.
//
// Keys config does not know (VIDRA_CORE_TAG, FRONTEND_PORT, POSTGRES_*) are
// ignored, so a whole env file can be handed over as-is. DATABASE_URL and
// REDIS_URL are normally ABSENT from a production env file — the compose chain
// derives both from POSTGRES_*/REDIS_PASSWORD — so config sees its development
// defaults for them here; only their non-emptiness is validated, which is why
// that is harmless. The compose render check (RenderCheckCommand) is what proves
// the derivation.
func Check(vars map[string]string) []Issue {
	var issues []Issue
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if IsPlaceholder(vars[k]) {
			issues = append(issues, Issue{Var: k, Msg: fmt.Sprintf("unfilled template placeholder %q — replace it with a real value (or blank the key if the feature is unused)", strings.TrimSpace(vars[k]))})
		}
	}
	// The component combinations, before config: they are about which CONTAINERS
	// exist, so a finding here explains a "required" failure config would report
	// second-hand (or, worse, would not report at all because the fallback DSN is
	// syntactically fine).
	issues = append(issues, componentIssues(vars)...)
	issues = append(issues, tlsIssues(vars)...)
	if env := strings.TrimSpace(vars["VIDRA_ENV"]); env != "production" {
		got := strconv.Quote(env)
		if env == "" {
			got = "unset"
		}
		issues = append(issues, Issue{Var: "VIDRA_ENV", Msg: fmt.Sprintf("a deployment env file must set VIDRA_ENV=production (%s) — without it the api boots in development mode, where a blank JWT_SECRET falls back to the built-in dev constant and a wildcard CORS origin is allowed. Everything below was checked with production forced, so these are the problems you would hit after fixing this one", got)})
		forced := make(map[string]string, len(vars)+1)
		for k, v := range vars {
			forced[k] = v
		}
		forced["VIDRA_ENV"] = "production"
		return append(issues, configIssues(forced)...)
	}
	return append(issues, configIssues(vars)...)
}

// configIssues flattens what config.CheckEnv returns: errors.Join gives a tree,
// and the leaves attributable to one variable are *config.VarError.
func configIssues(vars map[string]string) []Issue {
	err := config.CheckEnv(vars)
	if err == nil {
		return nil
	}
	var out []Issue
	var walk func(error)
	walk = func(e error) {
		if joined, ok := e.(interface{ Unwrap() []error }); ok {
			for _, child := range joined.Unwrap() {
				walk(child)
			}
			return
		}
		var ve *config.VarError
		if errors.As(e, &ve) {
			out = append(out, Issue{Var: ve.Var, Msg: ve.Msg})
			return
		}
		out = append(out, Issue{Msg: e.Error()})
	}
	walk(err)
	return out
}

// rotationSet validates --rotate and gates KEK rotation behind confirmation.
func rotationSet(req Request) (map[string]bool, error) {
	out := map[string]bool{}
	for _, name := range req.Rotate {
		key := strings.TrimSpace(name)
		if key == "" {
			continue
		}
		spec, ok := secretManifest[key]
		if !ok {
			return nil, fmt.Errorf("setup: %s is not a generated secret, so there is nothing to rotate (rotatable: %s)", key, strings.Join(SecretVars(), ", "))
		}
		if spec.kek && !req.ConfirmDestructive {
			return nil, fmt.Errorf("setup: rotating %s is DESTRUCTIVE: %s. Re-run with --yes-i-know if that is really what you want, and take a database dump first", key, spec.why)
		}
		out[key] = true
	}
	return out, nil
}

// blankKEKError refuses to mint a key-encryption key over a blank/placeholder
// slot when a previous configuration exists. The destructive gate on a KEK has
// to be the same whether the old value is present (a rotation) or missing (a
// truncated file, a half-finished restore, an env file assembled by hand): both
// end with rows in the database sealed under a key nothing has any more. The
// escape hatch is the rotation path, which says out loud what is being lost.
func blankKEKError(key string, spec secretSpec) error {
	return fmt.Errorf("setup: %s is blank in the configuration being merged, and minting a new one is DESTRUCTIVE: %s. "+
		"If the value was lost, restore it from a backup of the env file and re-run. If this deployment genuinely has nothing sealed under it yet "+
		"(a KEK the template only just turned on), accept that with --rotate %s --yes-i-know", key, spec.why, key)
}

// lineShapeIssues rejects values that cannot survive a KEY=value line. A newline
// is not a bad value, it is EXTRA LINES: `JWT_SECRET=abc\nFOO=bar` writes a
// truncated secret AND an unrelated assignment, and both halves look deliberate
// to compose. A NUL is refused for the same class of reason — it terminates the
// value for every C-based consumer of the env, so what the container receives is
// not what the file shows.
func lineShapeIssues(values map[string]string) []Issue {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []Issue
	for _, k := range keys {
		v := values[k]
		switch {
		case strings.ContainsAny(v, "\n\r"):
			out = append(out, Issue{Var: k, Msg: "the value contains a line break, which would not be stored — it would add lines to the env file: everything after the break becomes a separate assignment (or a stray line compose rejects) and the rest of this value is lost. Supply a single-line value"})
		case strings.ContainsRune(v, 0):
			out = append(out, Issue{Var: k, Msg: "the value contains a NUL byte, which truncates it for every consumer of the environment — the container would receive something shorter than this file shows. Supply a printable value"})
		}
	}
	return out
}

// Warnings are the non-fatal findings about a candidate environment: values
// docker compose REWRITES on the way to the container, so what the process
// receives is not the string the file shows. None of them stops a deployment —
// which is exactly why they are worth printing, since the symptom lands later as
// "the password is wrong" with a file that looks right.
//
// Exported so `vidra setup --check` reports the same things a generation does;
// Check itself stays the list of REAL problems, and never grows a finding an
// operator is expected to ignore. No warning ever quotes a value: several of
// these variables are secrets.
func Warnings(vars map[string]string) []string {
	out := append(interpolationWarnings(vars), quoteWarnings(vars)...)
	out = append(out, componentWarnings(vars)...)
	out = append(out, searchRedisWarnings(vars)...)
	return append(out, tlsWarnings(vars)...)
}

// interpolationWarnings flags a value compose would rewrite before the container
// ever sees it: `--env-file` values go through variable interpolation, so a
// leading '$' means the process gets an expansion (usually empty), not the
// string in the file. A warning, not a refusal — '$' is legal in a password and
// escaping it ('$$') is the operator's call.
func interpolationWarnings(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []string
	for _, k := range keys {
		if strings.HasPrefix(strings.TrimSpace(values[k]), "$") {
			out = append(out, fmt.Sprintf("%s starts with '$': docker compose interpolates variables in --env-file values, so the container would receive an expansion (usually empty) instead of the literal value — double the dollar ('$$') to escape it", k))
		}
	}
	return out
}

// quoteWarnings flags a value WRAPPED IN MATCHING QUOTES. ParseEnvFile counts
// the quote bytes as part of the value — deliberately, because it must not
// invent a second dialect — but compose does not: reading an `--env-file` it
// STRIPS a surrounding pair of quotes, and inside double quotes it also expands
// backslash escapes (\n, \t, \" and friends). So `SMTP_PASSWORD="p@ss"` reaches
// the container as p@ss, six characters shorter than the file shows, and
// `JWT_SECRET="a\tb"` arrives with a TAB in it.
//
// A warning rather than a refusal: quoting is legal, and an operator who meant
// the quotes as delimiters has a working file. It is the operator who meant them
// as part of a secret — the shape of a password pasted with its quotes — who
// needs telling, because that deployment fails as an authentication error nobody
// traces back to the env file.
func quoteWarnings(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []string
	for _, k := range keys {
		v := strings.TrimSpace(values[k])
		if len(v) < 2 || (v[0] != '"' && v[0] != '\'') || v[len(v)-1] != v[0] {
			continue
		}
		inner := v[1 : len(v)-1]
		msg := fmt.Sprintf("%s is wrapped in %s quotes: docker compose strips a surrounding pair from an --env-file value, so the container receives the value WITHOUT them — remove them if they were meant to be part of it", k, quoteName(v[0]))
		if v[0] == '"' && strings.Contains(inner, `\`) {
			msg += `, and double the backslashes: inside double quotes compose expands the backslash escapes too (\n becomes a newline)`
		}
		out = append(out, msg)
	}
	return out
}

func quoteName(q byte) string {
	if q == '\'' {
		return "single"
	}
	return "double"
}

// unusedRotations refuses a --rotate for a variable the generated file does not
// assign: silently doing nothing would leave the operator believing a secret had
// been replaced.
func unusedRotations(requested map[string]bool, done []string) error {
	applied := map[string]bool{}
	for _, k := range done {
		applied[k] = true
	}
	var missing []string
	for k := range requested {
		if !applied[k] {
			missing = append(missing, k)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return fmt.Errorf("setup: cannot rotate %s: neither the template nor the existing env file assigns it (the variable is unused by this deployment)", strings.Join(missing, ", "))
}

// mint generates the secret for key.
func mint(key string, r io.Reader) (string, error) {
	spec, ok := secretManifest[key]
	if !ok {
		return "", fmt.Errorf("setup: no generator for %s", key)
	}
	return spec.generate(r)
}

// answerValues turns the answers into concrete key/value pairs. An empty string
// means "not answered" throughout, so a partially filled answer set falls
// through to the existing file and then the template.
func answerValues(a Answers) (map[string]string, error) {
	out := map[string]string{}
	if a.Domain != "" {
		origin, err := normalizeOrigin(a.Domain)
		if err != nil {
			return nil, err
		}
		for _, k := range singleOriginKeys {
			out[k] = origin
		}
	}
	tags := map[string]string{
		"VIDRA_CORE_TAG":   firstNonEmpty(a.CoreTag, a.ReleaseTag),
		"VIDRA_USER_TAG":   firstNonEmpty(a.UserTag, a.ReleaseTag),
		"VIDRA_SEARCH_TAG": firstNonEmpty(a.SearchTag, a.ReleaseTag),
	}
	for k, v := range tags {
		if v != "" {
			out[k] = v
		}
	}
	switch a.StorageBackend {
	case "":
	case "local":
		out["STORAGE_BACKEND"] = "local"
	case "s3":
		out["STORAGE_BACKEND"] = "s3"
		for k, v := range map[string]string{
			"STORAGE_S3_ENDPOINT":   a.S3.Endpoint,
			"STORAGE_S3_REGION":     a.S3.Region,
			"STORAGE_S3_BUCKET":     a.S3.Bucket,
			"STORAGE_S3_ACCESS_KEY": a.S3.AccessKey,
			"STORAGE_S3_SECRET_KEY": a.S3.SecretKey,
		} {
			if v != "" {
				out[k] = v
			}
		}
	default:
		return nil, fmt.Errorf("setup: unsupported storage backend %q (want local|s3)", a.StorageBackend)
	}
	// The profile list is computed, never merged: see Answers.Features.
	out[profilesKey] = strings.Join(Profiles(a), " ")
	for _, c := range []struct {
		field, mode, url, flagKey, urlKey string
	}{
		{"database", a.Database.Mode, a.Database.URL, externalPostgresKey, databaseURLKey},
		{"redis", a.Redis.Mode, a.Redis.URL, externalRedisKey, redisURLKey},
	} {
		flag, err := componentMode(c.field, c.mode)
		if err != nil {
			return nil, err
		}
		if flag != "" {
			out[c.flagKey] = flag
		}
		// An empty URL is "unanswered", not "clear it": a re-run that only
		// re-confirms the mode must keep the connection string already in the file.
		if c.url != "" {
			out[c.urlKey] = c.url
		}
	}
	// The TLS answers are what deploy/Caddyfile.local is rendered from; they live
	// in the env file so a re-run (and `vidra doctor`) can see what the current
	// Caddyfile was generated with.
	mode, err := tlsMode(a.TLSMode)
	if err != nil {
		return nil, err
	}
	if mode != "" {
		out[tlsModeKey] = mode
	}
	if email := strings.TrimSpace(a.AcmeEmail); email != "" {
		if err := checkAcmeEmailShape(email); err != nil {
			return nil, err
		}
		out[acmeEmailKey] = email
	}
	if a.Mail != nil {
		out["MAIL_ENABLED"] = "true"
		for k, v := range map[string]string{
			"SMTP_HOST":     a.Mail.Host,
			"SMTP_PORT":     a.Mail.Port,
			"SMTP_USERNAME": a.Mail.Username,
			"SMTP_PASSWORD": a.Mail.Password,
			"SMTP_FROM":     a.Mail.From,
		} {
			if v != "" {
				out[k] = v
			}
		}
	}
	if a.Registration != nil {
		out["REGISTRATION_ENABLED"] = strconv.FormatBool(a.Registration.Enabled)
		out["REGISTRATION_REQUIRE_APPROVAL"] = strconv.FormatBool(a.Registration.RequireApproval)
	}
	return out, nil
}

// componentMode turns a local|external answer into the env value the deploy
// scripts test. "" stays "": an unanswered component keeps whatever the existing
// file (or the template) already says, like every other unanswered field.
func componentMode(field, mode string) (string, error) {
	switch mode {
	case "":
		return "", nil
	case "local":
		return "false", nil
	case "external":
		return "true", nil
	default:
		return "", fmt.Errorf("setup: unsupported %s mode %q (want local|external)", field, mode)
	}
}

// requireDomain refuses to generate a production file whose public origin is
// still the template's example. The template's value is documentation, so it can
// never satisfy the requirement — only an answer or an existing file can.
func requireDomain(req Request, answers map[string]string) error {
	if !req.Template.Has("PUBLIC_BASE_URL") {
		return nil
	}
	if answers["PUBLIC_BASE_URL"] != "" {
		return nil
	}
	if _, ok := existingValue(req.Existing, "PUBLIC_BASE_URL"); ok {
		return nil
	}
	return errors.New("setup: the instance domain is required — pass the public origin (e.g. --domain video.example.org). PUBLIC_BASE_URL is the origin for watch/embed links AND for OAuth, NodeInfo and federation identity, so the template's example value cannot be deployed")
}

// normalizeOrigin accepts a bare host or a full origin and returns
// https://host[:port], lowercased. https is not negotiable here: in production
// the api pins Secure cookies and both apps emit HSTS, so a plain-http origin
// silently breaks login (deliberate plain-HTTP and external-terminator modes are
// phase-1 item 12, and they need code, not an env value).
//
// The host must be DNS-SHAPED, because this one answer becomes PUBLIC_BASE_URL
// (federation and OAuth identity) and CORS_ALLOWED_ORIGINS (a browser security
// boundary). `https://*.example.org` parses fine and would be written straight
// into the CORS allow-list as a wildcard nobody chose; `https://example.org.`
// and `https://Example.ORG` are the same host to DNS but three different strings
// to an origin comparison, which is how a "correct" domain still fails CORS and
// signature verification. Normalising here keeps the file's single origin
// byte-identical everywhere it is compared.
func normalizeOrigin(in string) (string, error) {
	raw := strings.TrimRight(strings.TrimSpace(in), "/")
	if raw == "" {
		return "", errors.New("setup: the domain is empty")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("setup: %q is not a usable domain: %w", in, err)
	}
	if u.Scheme != "https" {
		return "", fmt.Errorf("setup: the domain must be https (got %q) — production pins Secure cookies and emits HSTS, so a plain-http origin breaks login", in)
	}
	if u.Host == "" || u.User != nil {
		return "", fmt.Errorf("setup: %q is not a usable domain: expected a host like video.example.org", in)
	}
	if u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("setup: the domain must not include a path or query (got %q) — PUBLIC_BASE_URL is an origin", in)
	}
	if err := checkDNSHost(u.Hostname(), in); err != nil {
		return "", err
	}
	host := strings.ToLower(u.Hostname())
	if port := u.Port(); port != "" {
		host += ":" + port
	}
	return "https://" + host, nil
}

// checkDNSHost requires the host part of an origin to be a hostname: dot-joined
// labels of letters, digits and inner hyphens. That rejects the wildcard, the
// stray space, the empty label from a doubled dot and the fully-qualified
// trailing dot — see normalizeOrigin for why each of those is a real failure and
// not pedantry.
func checkDNSHost(host, in string) error {
	bad := func(why string) error {
		return fmt.Errorf("setup: %q is not a usable domain (%s) — PUBLIC_BASE_URL and CORS_ALLOWED_ORIGINS need one concrete host, like video.example.org", in, why)
	}
	switch {
	case host == "":
		return bad("no host")
	case strings.Contains(host, "*"):
		return bad("wildcards are not a host: CORS_ALLOWED_ORIGINS would allow every subdomain, and federation identity has to be one origin")
	case strings.HasSuffix(host, "."):
		return bad("trailing dot: the same host as without it to DNS, a different string to every origin comparison")
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" {
			return bad("empty label")
		}
		if strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return bad("a label may not start or end with '-'")
		}
		for _, r := range label {
			switch {
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
			default:
				return bad(fmt.Sprintf("%q is not allowed in a hostname", string(r)))
			}
		}
	}
	return nil
}

// applyStorageRule finishes the storage answer. Choosing local leaves the
// template's S3 block irrelevant, so any PLACEHOLDER left in it is blanked —
// real S3 values are preserved, because an operator switching to local storage
// for now should not lose the credentials they may switch back to.
func applyStorageRule(req Request, res *Result) {
	if res.Values["STORAGE_BACKEND"] != "local" {
		return
	}
	for _, k := range s3Keys {
		if IsPlaceholder(res.Values[k]) {
			res.Values[k] = ""
		}
	}
	if req.Answers.StorageBackend == "local" {
		res.Warnings = append(res.Warnings, "storage is local: media lives in the media_data volume mounted at /app/data — include that volume in your backups, since the database rows are useless without it")
	}
}

// applyMailRule turns mail off when it has nowhere to send. The template ships
// MAIL_ENABLED=true with a blank SMTP_HOST/SMTP_FROM (a prompt to fill them in),
// and the api refuses that combination outright — so an unanswered SMTP block
// must flip the flag rather than produce a file that cannot boot. An existing
// file's working SMTP settings survive untouched: they are non-blank.
func applyMailRule(res *Result) {
	on, err := strconv.ParseBool(strings.TrimSpace(res.Values["MAIL_ENABLED"]))
	if err != nil || !on {
		return
	}
	if strings.TrimSpace(res.Values["SMTP_HOST"]) != "" && strings.TrimSpace(res.Values["SMTP_FROM"]) != "" {
		return
	}
	res.Values["MAIL_ENABLED"] = "false"
	res.Warnings = append(res.Warnings, "mail is disabled: no SMTP host/from was given, so password reset and email verification are unavailable — set SMTP_HOST and SMTP_FROM and re-run to enable them")
}

// applyComponentRule finishes the component answers and returns the managed keys
// that have to be APPENDED because the template does not define them.
//
// The appending is the point. VIDRA_COMPOSE_PROFILES is read by deploy.sh, not by
// the api, and a template that predates the key (every env file generated before
// this feature, and every deployment still running one) would otherwise produce a
// file with no profile list at all — the deploy would fall back to its own
// hard-coded core+frontend and quietly drop the ipfs profile the operator chose.
// So the engine writes the key wherever it lands: in place when the template has
// it, in a labelled block at the end when it does not.
//
// The external-service switches are also NORMALISED to a concrete true/false. A
// blank flag is a value shell scripts and Go both read as "not external", but
// only by accident — `[ "$VIDRA_EXTERNAL_POSTGRES" = true ]` and a strconv.ParseBool
// disagree about far too many other spellings for an empty one to be worth
// keeping in a file two languages read.
func applyComponentRule(req Request, answers map[string]string, res *Result) []string {
	var added []string
	for _, key := range managedKeys {
		v, defined := res.Values[key]
		if !defined {
			// Not in the template, so the resolution loop never saw it: answer
			// first, then whatever the previous file had.
			v = answers[key]
			if v == "" {
				if ev, ok := existingValue(req.Existing, key); ok {
					v = ev
				}
			}
		}
		if strings.TrimSpace(v) == "" {
			switch key {
			case profilesKey:
				v = strings.Join(Profiles(req.Answers), " ")
			case externalPostgresKey, externalRedisKey:
				v = "false"
			case searchRedisURLKey:
				// Only meaningful with an EXTERNAL Redis: with the bundled one the
				// compose chain derives both DSNs from REDIS_PASSWORD, and writing an
				// override here would be the hand-written second value the template
				// warns against ("do not set REDIS_URL/SEARCH_REDIS_URL by hand").
				if !isTrue(res.Values[externalRedisKey]) {
					continue
				}
				derived, db, ok := deriveSearchRedisURL(res.Values[redisURLKey])
				if !ok {
					// Nothing safe to derive from. Leaving it ABSENT is deliberate:
					// componentIssues then refuses the file by name, which is a better
					// answer than inventing a DSN that shares a keyspace.
					continue
				}
				v = derived
				res.Warnings = append(res.Warnings, fmt.Sprintf("%s was derived from %s by moving to logical database %d, because the search service must not share a keyspace with the api. Check the managed instance exposes that database (some providers expose only /0, and some disable SELECT entirely) — if it does not, point %s at a SECOND instance and re-run",
					searchRedisURLKey, redisURLKey, db, searchRedisURLKey))
			case tlsModeKey:
				// The deploy scripts read a blank mode as acme; writing it out is
				// how an operator finds the knob at all.
				v = TLSModeACME
			default:
				// A connection string with no value is simply absent: the bundled
				// service's DSN is derived by the compose chain. So is a blank ACME
				// contact address — a key with nothing in it would only be a
				// placeholder Check has to be taught to forgive.
				continue
			}
		}
		res.Values[key] = v
		if !defined {
			added = append(added, key)
		}
	}
	return added
}

// deriveSearchRedisURL is the default SEARCH_REDIS_URL for a managed Redis: the
// api's own DSN moved to the NEXT logical database. It returns the derived URL
// and the index it landed on, or ok=false when there is nothing safe to derive
// from.
//
// It is not a copy of REDIS_URL, and that is the whole reason this function
// exists. The compose model gives the api `…/0` and the search service `…/1`,
// and both the base file and the external-redis overlay say why in as many
// words: "Do NOT feed this from ${REDIS_URL}: that variable carries core's /0
// and the two services would share a keyspace." Search's index writes and the
// api's cache/rate-limit counters are two independent key populations under one
// eviction policy (the bundled Redis runs allkeys-lru with a hard ceiling), so
// sharing a database is not a tidiness question — it is each service quietly
// evicting the other's data.
//
// The rule is deliberately narrow, because a derived DSN is a value nobody
// typed: a redis:// or rediss:// URL whose path is empty or a plain database
// index. Anything else — a unix socket, a provider's bespoke URL shape, a path
// carrying something that is not an index — returns ok=false, and the caller
// leaves the key absent so Check refuses the file by name rather than inventing
// a connection string.
func deriveSearchRedisURL(redisURL string) (string, int, bool) {
	raw := strings.TrimSpace(redisURL)
	if raw == "" {
		return "", 0, false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", 0, false
	}
	switch strings.ToLower(u.Scheme) {
	case "redis", "rediss":
	default:
		return "", 0, false
	}
	if u.Opaque != "" {
		return "", 0, false
	}
	db := 0
	if path := strings.TrimPrefix(u.Path, "/"); path != "" {
		n, err := strconv.Atoi(path)
		if err != nil || n < 0 {
			return "", 0, false
		}
		db = n
	}
	// The NEXT index rather than a hard-coded 1: an operator who put the api on
	// /3 gets /4, which is still a database of its own. searchRedisDefaultDB is
	// what the ordinary /0 answer lands on, and is the value the compose model
	// already uses.
	next := db + searchRedisDefaultDB
	u.Path = "/" + strconv.Itoa(next)
	return u.String(), next, true
}

func isManagedKey(key string) bool {
	for _, k := range managedKeys {
		if k == key {
			return true
		}
	}
	return false
}

// componentWarnings flags the half-configured managed service: a connection
// string pointing at somebody else's database while the switch that stops the
// bundled one from starting is off. Nothing fails — the api uses DATABASE_URL and
// is perfectly happy — which is exactly the problem: the deployment also runs a
// postgres container with a volume, a password and a backup nobody is taking,
// and the day the URL is removed it silently starts serving the empty one.
//
// A warning rather than a refusal, because it is also what every pre-overlay
// managed-database deployment looks like, and those are running fine.
func componentWarnings(vars map[string]string) []string {
	var out []string
	for _, c := range externalOverlays {
		if isTrue(vars[c.flag]) {
			continue
		}
		var set []string
		for _, k := range c.urlKeys {
			if strings.TrimSpace(vars[k]) != "" {
				set = append(set, k)
			}
		}
		if len(set) == 0 {
			continue
		}
		out = append(out, fmt.Sprintf("%s is set but %s is not true: the api would use the managed %s while the deploy still starts the bundled one (its container, volume and password are all still there, unused). Set %s=true so the deploy adds %s and skips the bundled service, or remove %s to go back to it",
			strings.Join(set, " and "), c.flag, c.service, c.flag, c.overlay, strings.Join(set, "/")))
	}
	return out
}

// searchRedisWarnings flags the managed Redis whose two DSNs name the SAME
// logical database. Nothing fails: both services connect, both work, and the
// symptom arrives weeks later as search results that go missing under load, or
// a rate limiter that forgets — because the bundled and managed configurations
// alike run one eviction policy over what are now two key populations.
//
// A warning rather than a refusal, deliberately. A provider that exposes only
// database 0 (or no SELECT at all) leaves an operator with exactly this file
// until they provision a second instance, and refusing to write it would strand
// a deployment that is running. The engine never PRODUCES this state — see
// deriveSearchRedisURL — so a file with it in was hand-edited, which is also the
// only way it gets fixed.
func searchRedisWarnings(vars map[string]string) []string {
	search := strings.TrimSpace(vars[searchRedisURLKey])
	if search == "" || search != strings.TrimSpace(vars[redisURLKey]) {
		return nil
	}
	return []string{fmt.Sprintf("%s and %s are the same DSN, so the api and the search service share one Redis keyspace: their keys evict each other under the instance's eviction policy, and neither failure looks like a configuration problem when it lands. Point %s at a different logical database (…/%d), or at a second instance if the provider does not expose SELECT",
		redisURLKey, searchRedisURLKey, searchRedisURLKey, searchRedisDefaultDB)}
}

// componentIssues is the one combination that cannot boot: an external service
// selected with nowhere to connect. The overlay removes the bundled container
// outright, so the api falls back to a DSN naming a host that is not in the
// compose network any more — a deployment that comes up, fails every query, and
// blames DNS.
//
// It is checked here rather than in internal/config because config has never
// heard of these keys: they are read by the deploy scripts, and this package is
// the only place that sees the env file and the compose chain at once.
func componentIssues(vars map[string]string) []Issue {
	var out []Issue
	for _, c := range externalOverlays {
		raw := strings.TrimSpace(vars[c.flag])
		if raw == "" {
			continue
		}
		if _, err := strconv.ParseBool(raw); err != nil {
			out = append(out, Issue{Var: c.flag, Msg: fmt.Sprintf("%q is not a boolean — write true or false. The deploy scripts and the setup engine read this value with different parsers, and a spelling only one of them accepts is how the overlay gets added without the bundled service being skipped (or the reverse)", raw)})
			continue
		}
		if !isTrue(raw) {
			continue
		}
		for _, k := range c.urlKeys {
			if strings.TrimSpace(vars[k]) != "" {
				continue
			}
			out = append(out, Issue{Var: k, Msg: fmt.Sprintf("%s=true selects an external %s, so the deploy adds %s and the bundled service never starts — but %s is blank, leaving nothing to connect to. The overlay asserts it with the `:?` form, so the compose render fails before any container starts. Set it to %s, or set %s=false to use the bundled service",
				c.flag, c.service, c.overlay, k, urlKeyRoles[k], c.flag)})
		}
	}
	return out
}

// tlsIssues validates the TLS keys the generated Caddyfile is rendered from. It
// only ever complains about a value that IS there and is wrong.
//
// Blank and absent are both legitimate: every env file written before these keys
// existed has neither, and those deployments have no generated Caddyfile at all —
// deploy/Caddyfile is whatever the operator edited by hand. Failing them would
// turn `vidra setup --check` on a working instance into a red screen about a
// feature it does not use. What cannot be waved through is a NON-BLANK value the
// renderer would reject, because that file names a mode (or a contact address)
// the next `vidra setup` refuses to act on.
func tlsIssues(vars map[string]string) []Issue {
	var out []Issue
	if raw := strings.TrimSpace(vars[tlsModeKey]); raw != "" {
		if _, err := tlsMode(raw); err != nil {
			out = append(out, Issue{Var: tlsModeKey, Msg: fmt.Sprintf("%q is not a TLS mode — write one of %s (blank means %s). It decides what `vidra setup` writes into %s, so an unrecognised value is a Caddyfile that cannot be regenerated", raw, strings.Join(tlsModes, ", "), TLSModeACME, CaddyOutputPath)})
		}
	}
	if email := strings.TrimSpace(vars[acmeEmailKey]); email != "" {
		if err := checkAcmeEmailShape(email); err != nil {
			out = append(out, Issue{Var: acmeEmailKey, Msg: strings.TrimPrefix(err.Error(), "setup: ")})
		}
	}
	return out
}

// tlsWarnings reports the two TLS configurations that deploy fine and are still
// not what a public instance wants.
//
// Neither is a failure. A rehearsal mode is a legitimate state to be in for an
// afternoon — that is what it is for — and the blank contact address costs
// nothing until a renewal fails 60 days later, which is exactly why it needs
// saying now rather than then. An env file that does not mention the mode at all
// gets nothing: it predates the managed Caddyfile, so there is no generated file
// for either warning to be about.
func tlsWarnings(vars map[string]string) []string {
	raw := strings.TrimSpace(vars[tlsModeKey])
	if raw == "" {
		return nil
	}
	mode, err := tlsMode(raw)
	if err != nil {
		// Already reported as an issue; a warning about it would be noise.
		return nil
	}
	if mode != TLSModeACME {
		return []string{fmt.Sprintf("%s=%s does not produce a publicly trusted certificate: %s is a rehearsal/bring-up mode, and every browser reaching this instance will refuse the connection. Re-run `vidra setup --tls-mode %s` once DNS points at this host", tlsModeKey, mode, mode, TLSModeACME)}
	}
	if strings.TrimSpace(vars[acmeEmailKey]) != "" {
		return nil
	}
	return []string{fmt.Sprintf("%s is %s but %s is blank: Let's Encrypt sends expiry and revocation notices to that address and nowhere else, so the first sign of a renewal problem would be the outage. Re-run `vidra setup --acme-email ops@your-domain`", tlsModeKey, TLSModeACME, acmeEmailKey)}
}

// isTrue reads a component switch the way this engine writes it, tolerating the
// spellings strconv.ParseBool accepts and treating everything else (including a
// blank) as false. componentIssues is what tells an operator their unrecognised
// spelling was read as false, rather than this silently doing it.
func isTrue(v string) bool {
	b, err := strconv.ParseBool(strings.TrimSpace(v))
	return err == nil && b
}

// releaseTagWarnings flags image pins left at the template's example tag: the
// file renders and deploys, it just deploys the wrong release.
//
// The test is the RESOLVED value against the template's, not "was there an
// answer": the interactive path offers the template's tag as the default, and an
// operator pressing enter has answered with the example — exactly the case the
// warning exists for. Attributing the answer to the operator instead of the
// template is how the warning went missing in the interview.
func releaseTagWarnings(req Request, values map[string]string) []string {
	var stale []string
	for _, k := range releaseTagKeys {
		tv, ok := req.Template.Value(k)
		if !ok {
			continue
		}
		if strings.TrimSpace(values[k]) != strings.TrimSpace(tv) {
			continue
		}
		stale = append(stale, k)
	}
	if len(stale) == 0 {
		return nil
	}
	return []string{fmt.Sprintf("%s kept the template's example tag — pass a release tag to pin the images you validated", strings.Join(stale, ", "))}
}

// existingValue reads a value from the existing env file, treating blanks and
// leftover placeholders as absent (they carry no information to preserve).
func existingValue(f *EnvFile, key string) (string, bool) {
	if f == nil {
		return "", false
	}
	v, ok := f.Value(key)
	if !ok || needsValue(v) {
		return "", false
	}
	return v, true
}

func existingKeys(f *EnvFile) []string {
	if f == nil {
		return nil
	}
	return f.Keys()
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// WriteFile writes a generated env file with mode 0600 — it holds every secret
// the instance has. The write goes to a temporary file in the same directory and
// is renamed into place, so a re-run that fails halfway cannot leave a live
// deployment with a truncated env file.
func WriteFile(path string, content []byte) error {
	return writeFile(path, content, 0o600)
}

// WriteCaddyfile writes a generated Caddyfile with the same atomic rename, at
// mode 0644. The wider mode is deliberate: the file carries no secret — a
// hostname, a contact address and the routing every reader of the meta repo can
// already see — and it is bind-mounted into a container that does not run as the
// user who generated it.
func WriteCaddyfile(path string, content []byte) error {
	return writeFile(path, content, 0o644)
}

func writeFile(path string, content []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return fmt.Errorf("setup: create temporary file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer func() {
		// No-op once the rename succeeded.
		_ = os.Remove(tmpName)
	}()
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return fmt.Errorf("setup: chmod %s: %w", tmpName, err)
	}
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return fmt.Errorf("setup: write %s: %w", tmpName, err)
	}
	// Durability before visibility: the rename is what makes the file the
	// deployment's env file, and an unsynced rename can survive a crash pointing
	// at zero bytes — which is the one outcome this whole function exists to
	// prevent.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("setup: flush %s to disk: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("setup: close %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("setup: install %s: %w", path, err)
	}
	// The rename is a DIRECTORY write, and it is no more durable than the bytes
	// were before tmp.Sync above: a crash before the filesystem flushes the
	// directory can leave the deployment with the old file, or with neither name.
	// Syncing the directory is what makes the new file's NAME survive, which is
	// the other half of "a re-run that fails halfway cannot truncate a live env
	// file".
	if err := syncDir(dir); err != nil {
		// The file IS installed: the rename succeeded and only the flush after it
		// did not. Saying so is the difference between an operator re-running this
		// (harmless — every value is preserved) and one hunting for a file that is
		// already on disk.
		return fmt.Errorf("setup: %s is written, but flushing %s — which is what makes the rename survive a crash — failed: %w", path, dir, err)
	}
	return nil
}

// syncDir flushes a directory entry, tolerating the filesystems that cannot do
// it. Directory fsync is unsupported on some network and virtualised mounts
// (ENOTSUP/ENOSYS) and rejected outright by others (EINVAL); neither says
// anything about the file, which is already written and renamed, so failing the
// run over it would turn a durability nicety into an install failure.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	if err := d.Sync(); err != nil && !errors.Is(err, errors.ErrUnsupported) && !errors.Is(err, syscall.ENOTSUP) && !errors.Is(err, syscall.EINVAL) {
		return err
	}
	return nil
}

// RenderCheckCommand returns the command that proves the OTHER half: compose
// substitution, the required-variable (`:?`) assertions in the prod overlay, and
// the profile set. It mirrors the meta repo's `make prod-config` /
// deploy/deploy.sh step 0 exactly, and must be run from the deployment
// directory. This package never shells out to docker itself — the engine has to
// work on a machine where the daemon is not up yet.
//
// values is the environment the file describes, and it is not decoration: the
// deploy builds its compose chain FROM the env file, so an instance running the
// ipfs profile or a managed database renders a DIFFERENT chain than `--profile
// core --profile frontend`. A check command that ignored the file would pass
// while the deploy it is meant to pre-flight failed. Three keys shape it:
//
//	VIDRA_COMPOSE_PROFILES   the profile list this engine writes; absent (an env
//	                         file older than the key) falls back to core+frontend,
//	                         which is what the deploy scripts hard-coded before it
//	EXTRA_COMPOSE_PROFILES   the operator's own additions, appended and deduped
//	VIDRA_EXTERNAL_POSTGRES  each true adds its docker-compose.external-*.yml
//	VIDRA_EXTERNAL_REDIS     overlay, IMMEDIATELY after the prod overlay so it can
//	                         remove the bundled service that overlay defines
//
// Pass nil for the legacy base profiles only.
func RenderCheckCommand(envPath string, values map[string]string) string {
	parts := RenderCheckArgs(envPath, values)
	quoted := make([]string, 0, len(parts)+1)
	quoted = append(quoted, "docker")
	for _, p := range parts {
		quoted = append(quoted, shellQuote(p))
	}
	return strings.Join(quoted, " ")
}

// RenderCheckArgs is RenderCheckCommand as an argv, for a caller that runs the
// chain rather than printing it — `vidra doctor` renders the SAME model the
// deploy will and reads the ports out of it. The returned slice starts at
// "compose" (the docker sub-command), so it is appended to whatever "docker"
// means on the host.
//
// tail replaces the trailing `config -q`, which is what lets doctor ask for
// `config --format json` without rebuilding the -f/--env-file/--profile chain
// beside the one that is already the deploy's definition of it. Pass nothing for
// the default.
//
// This is the ONLY builder of that chain in the codebase, deliberately: a second
// one is a second answer to "which overlays and profiles does this deployment
// use", and the first symptom of them disagreeing is a check that passes against
// a model nothing deploys.
func RenderCheckArgs(envPath string, values map[string]string, tail ...string) []string {
	args := []string{"compose", "-f", "docker-compose.yml", "-f", "docker-compose.prod.yml"}
	// Order matters: a compose overlay only wins over what an EARLIER file said,
	// so the external-service overlays go after docker-compose.prod.yml.
	for _, c := range externalOverlays {
		if isTrue(values[c.flag]) {
			args = append(args, "-f", c.overlay)
		}
	}
	args = append(args, "--env-file", envPath)
	for _, p := range EnabledProfiles(values) {
		args = append(args, "--profile", p)
	}
	if len(tail) == 0 {
		tail = []string{"config", "-q"}
	}
	return append(args, tail...)
}

// EnabledProfiles is the compose profile list the deploy will enable for an env
// file: the managed VIDRA_COMPOSE_PROFILES list (or the legacy core+frontend
// when the file predates the key), plus the operator's EXTRA_COMPOSE_PROFILES,
// deduplicated.
//
// Exported because it is also the answer to "is the RTMP edge supposed to be
// public here?" — `vidra doctor` reads the rendered port map against it, and a
// second opinion about which profiles are on would make that check wrong in the
// one direction that matters.
func EnabledProfiles(values map[string]string) []string { return checkProfiles(values) }

// checkProfiles is the profile list the deploy will enable: the managed list, or
// the legacy base when the file predates it, plus the operator's extras.
//
// Deduped because the two lists overlap by design — an operator who enabled ipfs
// through EXTRA_COMPOSE_PROFILES before this key existed keeps that line after a
// re-run puts `ipfs` in the managed list, and `--profile ipfs --profile ipfs` is
// noise in a command an operator is being asked to read and paste.
func checkProfiles(values map[string]string) []string {
	profiles := strings.Fields(values[profilesKey])
	if len(profiles) == 0 {
		profiles = append([]string(nil), baseProfiles...)
	}
	profiles = append(profiles, strings.Fields(values[extraProfilesKey])...)
	seen := make(map[string]bool, len(profiles))
	out := make([]string, 0, len(profiles))
	for _, p := range profiles {
		if seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// shellQuote makes a value safe to paste into a shell, and leaves the ordinary
// case (env/production.env) untouched so the printed command stays readable.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if !strings.ContainsAny(s, " \t\n\r\"'$&|;<>()[]{}*?!#~`\\") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
