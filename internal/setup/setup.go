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
//  3. THE PRODUCT IS VALIDATED BY THE BOOT ENGINE. Generation ends by running
//     the api's own config validation (config.CheckEnv) over the file it just
//     rendered, and refuses to hand back a file that would not boot. There is
//     no second validation library to drift.
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

	// StorageBackend is "local" or "s3" ("" keeps the template's choice).
	StorageBackend string
	// S3 is consulted only for StorageBackend == "s3".
	S3 S3Answers

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
// non-destructive.
type Request struct {
	Template *EnvFile
	Existing *EnvFile
	Answers  Answers

	// Rotate names variables whose secret must be re-generated even though a
	// value already exists. Every entry must be in the secret manifest.
	Rotate []string
	// ConfirmDestructive authorises rotating a *_KEK. Without it, a KEK in
	// Rotate is an error that spells out what the rotation would destroy.
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
			if _, isSecret := secretManifest[key]; isSecret {
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
	// FEDERATION_KEY_KEK the operator uncommented, a managed DATABASE_URL, an
	// EXTRA_COMPOSE_PROFILES line. Dropping them would silently change the
	// deployment, so they are carried into their own block.
	for _, key := range existingKeys(req.Existing) {
		if req.Template.Has(key) {
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
	res.Warnings = append(res.Warnings, releaseTagWarnings(req, answers)...)

	content := req.Template.Render(res.Values, res.Carried)
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
// <...> placeholders first, then the api's own boot validation.
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
			return nil, fmt.Errorf("setup: rotating %s is DESTRUCTIVE: %s. Re-run with --yes if that is really what you want, and take a database dump first", key, spec.why)
		}
		out[key] = true
	}
	return out, nil
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
// https://host[:port]. https is not negotiable here: in production the api pins
// Secure cookies and both apps emit HSTS, so a plain-http origin silently breaks
// login (deliberate plain-HTTP and external-terminator modes are phase-1 item
// 12, and they need code, not an env value).
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
	return "https://" + u.Host, nil
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

// releaseTagWarnings flags image pins left at the template's example tag: the
// file renders and deploys, it just deploys the wrong release.
func releaseTagWarnings(req Request, answers map[string]string) []string {
	var stale []string
	for _, k := range releaseTagKeys {
		if !req.Template.Has(k) || answers[k] != "" {
			continue
		}
		if _, ok := existingValue(req.Existing, k); ok {
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
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("setup: chmod %s: %w", tmpName, err)
	}
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return fmt.Errorf("setup: write %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("setup: close %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("setup: install %s: %w", path, err)
	}
	return nil
}

// RenderCheckCommand returns the command that proves the OTHER half: compose
// substitution, the required-variable (`:?`) assertions in the prod overlay, and
// the profile set. It mirrors the meta repo's `make prod-config` /
// deploy/deploy.sh step 0 exactly, and must be run from the deployment
// directory. This package never shells out to docker itself — the engine has to
// work on a machine where the daemon is not up yet.
func RenderCheckCommand(envPath string) string {
	return "docker compose -f docker-compose.yml -f docker-compose.prod.yml --env-file " +
		envPath + " --profile core --profile frontend config -q"
}
