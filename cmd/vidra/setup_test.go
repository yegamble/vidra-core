package main

import (
	"bytes"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A minimal template in the deployment format: comments, a blank secret, a
// <generate: ...> placeholder, a commented-out optional key. The FORMAT is
// pinned by internal/setup's fixture and golden; this one only has to exercise
// the command's plumbing.
const cliTemplate = `# ==============================================================================
# VIDRA — PRODUCTION ENVIRONMENT TEMPLATE (cli test fixture)
# ==============================================================================

VIDRA_ENV=production
VIDRA_CORE_TAG=v0.1.0
VIDRA_USER_TAG=v0.1.0
VIDRA_SEARCH_TAG=v0.1.0
PUBLIC_BASE_URL=https://example.com

#   generate: openssl rand -base64 48
JWT_SECRET=
# DESTRUCTIVE TO ROTATE.
#   generate: openssl rand -base64 32
MFA_KEY_KEK=<generate: openssl rand -base64 32>
#   generate: openssl rand -hex 32
POSTGRES_PASSWORD=<generate: openssl rand -hex 32>
REDIS_PASSWORD=

VIDRA_COMPOSE_PROFILES=core frontend
VIDRA_EXTERNAL_POSTGRES=false
VIDRA_EXTERNAL_REDIS=false

STORAGE_BACKEND=local
STORAGE_S3_ENDPOINT=
STORAGE_S3_REGION=
STORAGE_S3_BUCKET=
# SECRETS — never commit a real value.
STORAGE_S3_ACCESS_KEY=
STORAGE_S3_SECRET_KEY=
STORAGE_S3_USE_SSL=true
MAIL_ENABLED=false
REGISTRATION_ENABLED=false
REGISTRATION_REQUIRE_APPROVAL=false
# FEDERATION_KEY_KEK=
`

// A minimal MARKED Caddyfile in the deployment format. The real one is 240 lines
// of routing this command never touches; what it has to exercise here is the
// plumbing — the two markers, a comment that keeps example.com and a site line
// that must not.
const cliCaddyfile = `# Reference reverse proxy (cli test fixture). Replace example.com with your domain.
{
	# email ops@example.com
	# vidra:global-options
}

example.com {
	# vidra:tls

	reverse_proxy api:8080
}
`

type harness struct {
	dir           string
	template      string
	output        string
	caddyTemplate string
	caddyOut      string
	out           bytes.Buffer
	err           bytes.Buffer
	stdin         string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{dir: t.TempDir()}
	h.template = filepath.Join(h.dir, "production.env.example")
	h.output = filepath.Join(h.dir, "production.env")
	h.caddyTemplate = filepath.Join(h.dir, "Caddyfile")
	h.caddyOut = filepath.Join(h.dir, "Caddyfile.local")
	for path, content := range map[string]string{h.template: cliTemplate, h.caddyTemplate: cliCaddyfile} {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return h
}

func (h *harness) run(args ...string) error {
	h.out.Reset()
	h.err.Reset()
	return run(streams{in: strings.NewReader(h.stdin), out: &h.out, err: &h.err}, args)
}

func (h *harness) setupArgs(extra ...string) []string {
	return append(append([]string{
		"setup", "--template", h.template, "--non-interactive",
		"--domain", "video.example.org", "--release-tag", "v0.1.1", "--storage", "local",
		"--acme-email", "ops@example.org",
	}, h.caddyArgs()...), extra...)
}

// caddyArgs points the Caddyfile generation at the fixture instead of the
// deployment's deploy/Caddyfile, which does not exist under `go test`.
func (h *harness) caddyArgs() []string {
	return []string{"--caddy-template", h.caddyTemplate, "--caddy-out", h.caddyOut}
}

func (h *harness) readCaddyfile(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(h.caddyOut)
	if err != nil {
		t.Fatalf("read generated Caddyfile: %v", err)
	}
	return string(b)
}

func (h *harness) readOutput(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(h.output)
	if err != nil {
		t.Fatalf("read generated file: %v", err)
	}
	return string(b)
}

func TestSetupWritesAPrivateFileAndPrintsTheRenderCheck(t *testing.T) {
	h := newHarness(t)
	if err := h.run(h.setupArgs()...); err != nil {
		t.Fatalf("setup: %v (stderr: %s)", err, h.err.String())
	}

	info, err := os.Stat(h.output)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}

	got := h.readOutput(t)
	for _, want := range []string{"PUBLIC_BASE_URL=https://video.example.org", "VIDRA_CORE_TAG=v0.1.1"} {
		if !strings.Contains(got, want) {
			t.Errorf("generated file is missing %q", want)
		}
	}
	if strings.Contains(got, "<generate:") {
		t.Error("a placeholder survived into the generated file")
	}

	stdout := h.out.String()
	if !strings.Contains(stdout, "docker compose -f docker-compose.yml -f docker-compose.prod.yml") || !strings.Contains(stdout, "config -q") {
		t.Errorf("the render-check command was not printed:\n%s", stdout)
	}
	// The summary names secrets; it must never print one.
	if !strings.Contains(stdout, "generated secrets") {
		t.Errorf("no secret summary:\n%s", stdout)
	}
	for _, line := range strings.Split(stdout, "\n") {
		if strings.Contains(line, "generated secrets") && strings.Contains(line, "=") {
			t.Errorf("a secret value may have been printed: %q", line)
		}
	}
}

// Rewriting the env file a deployment is running from needs one deliberate
// keystroke — --yes, or a --from that makes the merge explicit. The refusal names
// both, and says nothing is lost either way.
func TestSetupRefusesToOverwriteWithoutIntent(t *testing.T) {
	h := newHarness(t)
	if err := h.run(h.setupArgs()...); err != nil {
		t.Fatalf("first setup: %v", err)
	}
	before := h.readOutput(t)

	err := h.run(h.setupArgs()...)
	if err == nil {
		t.Fatal("an existing output file was rewritten with no intent flag")
	}
	for _, want := range []string{"--yes", "--from", "--output"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not offer %q", err, want)
		}
	}
	if h.readOutput(t) != before {
		t.Fatal("the refused run still rewrote the file")
	}

	// --yes rewrites it in place, and the rewrite is byte-identical: the file
	// itself was the preservation source.
	if err := h.run(h.setupArgs("--yes")...); err != nil {
		t.Fatalf("--yes rewrite: %v (stderr: %s)", err, h.err.String())
	}
	if got := h.readOutput(t); got != before {
		t.Errorf("the in-place rewrite changed the file\n--- got ---\n%s--- want ---\n%s", got, before)
	}
	if !strings.Contains(h.out.String(), "preserved secrets") {
		t.Errorf("the in-place rewrite did not report preserved secrets:\n%s", h.out.String())
	}
}

// A --from merge keeps every value the output already had — it is the first
// preservation source — and adds only what --from brings that the output leaves
// blank or does not have at all.
func TestSetupMergeKeepsEveryExistingValue(t *testing.T) {
	h := newHarness(t)
	if err := h.run(h.setupArgs()...); err != nil {
		t.Fatalf("first setup: %v", err)
	}
	first := h.readOutput(t)

	extra := filepath.Join(h.dir, "extra.env")
	if err := os.WriteFile(extra, []byte("PUBLIC_BASE_URL=https://other.example.org\nINSTANCE_NAME=Vidra\n"), 0o600); err != nil {
		t.Fatalf("write extra env: %v", err)
	}
	if err := h.run(h.setupArgs("--from", extra)...); err != nil {
		t.Fatalf("merge setup: %v (stderr: %s)", err, h.err.String())
	}
	got := h.readOutput(t)
	for _, key := range []string{"JWT_SECRET", "MFA_KEY_KEK", "POSTGRES_PASSWORD", "REDIS_PASSWORD", "PUBLIC_BASE_URL"} {
		if valueOf(t, got, key) != valueOf(t, first, key) {
			t.Errorf("%s was replaced by the --from file's value", key)
		}
	}
	if !strings.Contains(got, "INSTANCE_NAME=Vidra") {
		t.Errorf("the --from file's extra key was dropped:\n%s", got)
	}
	if !strings.Contains(h.out.String(), "preserved secrets") {
		t.Errorf("the merge did not report preserved secrets:\n%s", h.out.String())
	}
}

// --from pointing at the OUTPUT is not a second source and not a second
// keystroke, however it is spelled. Comparing the raw strings let
// `--from ./production.env` list the same file twice AND stand in for --yes, so
// a mistyped path silently rewrote the file the gate exists to protect.
func TestSetupFromTheOutputItselfIsNotIntent(t *testing.T) {
	h := newHarness(t)
	if err := h.run(h.setupArgs()...); err != nil {
		t.Fatalf("first setup: %v", err)
	}
	before := h.readOutput(t)

	for _, spelling := range []string{
		h.output,
		h.dir + "/./production.env",      // the same file: os.SameFile sees it
		h.dir + "/sub/../production.env", // "sub" does not exist: only the resolved path sees it
	} {
		err := h.run(h.setupArgs("--from", spelling)...)
		if err == nil {
			t.Fatalf("--from %s rewrote the output with no --yes", spelling)
		}
		if !strings.Contains(err.Error(), "--yes") {
			t.Errorf("refusal for --from %s does not ask for --yes: %v", spelling, err)
		}
		if h.readOutput(t) != before {
			t.Fatalf("--from %s still rewrote the file", spelling)
		}
	}

	// With the intent flag it goes through, and names the file ONCE.
	if err := h.run(h.setupArgs("--yes", "--from", h.output)...); err != nil {
		t.Fatalf("--yes with --from the output: %v (stderr: %s)", err, h.err.String())
	}
	if got := h.readOutput(t); got != before {
		t.Errorf("the rewrite changed the file\n--- got ---\n%s--- want ---\n%s", got, before)
	}
	for _, line := range strings.Split(h.out.String(), "\n") {
		if strings.Contains(line, "preserved values from") && strings.Count(line, h.output) != 1 {
			t.Errorf("the output file is listed as two preservation sources: %q", line)
		}
	}
}

// THE reproducer: `--from staging.env --output production.env` used to treat only
// staging as the preservation source, so production's KEKs were re-minted and its
// secrets replaced by staging's. The file being written is now always a source,
// and it outranks --from.
func TestSetupFromADifferentFilePreservesTheFileBeingWritten(t *testing.T) {
	h := newHarness(t)
	if err := h.run(h.setupArgs()...); err != nil {
		t.Fatalf("first setup: %v", err)
	}
	live := h.readOutput(t)

	// A staging env file: same shape, different secrets, and one key the live
	// file leaves blank.
	staging := filepath.Join(h.dir, "staging.env")
	stagingBody := strings.Join([]string{
		"VIDRA_ENV=production",
		"PUBLIC_BASE_URL=https://staging.example.org",
		"JWT_SECRET=" + strings.Repeat("S", 48),
		"MFA_KEY_KEK=" + base64.StdEncoding.EncodeToString([]byte(strings.Repeat("s", 32))),
		"POSTGRES_PASSWORD=" + strings.Repeat("f", 64),
		"REDIS_PASSWORD=" + strings.Repeat("e", 64),
		"EXTRA_COMPOSE_PROFILES=ipfs",
	}, "\n") + "\n"
	if err := os.WriteFile(staging, []byte(stagingBody), 0o600); err != nil {
		t.Fatalf("write staging env: %v", err)
	}

	if err := h.run(h.setupArgs("--from", staging)...); err != nil {
		t.Fatalf("merge from a different file: %v (stderr: %s)", err, h.err.String())
	}
	got := h.readOutput(t)

	for _, key := range []string{"JWT_SECRET", "MFA_KEY_KEK", "POSTGRES_PASSWORD", "REDIS_PASSWORD"} {
		if valueOf(t, got, key) != valueOf(t, live, key) {
			t.Errorf("%s was replaced by the --from file's value — the live secret is gone", key)
		}
	}
	// staging's domain does not silently re-point the instance either: the
	// answered domain wins, and the live file's value is what --from could not
	// override.
	if v := valueOf(t, got, "PUBLIC_BASE_URL"); v != "https://video.example.org" {
		t.Errorf("PUBLIC_BASE_URL = %q, want the answered domain", v)
	}
	// A key only --from has still comes across: that is what --from is for.
	if !strings.Contains(got, "EXTRA_COMPOSE_PROFILES=ipfs") {
		t.Errorf("the --from file's extra key was dropped:\n%s", got)
	}
	// And with an extra profile in the file, the printed render-check has to
	// include it — deploy.sh will.
	if !strings.Contains(h.out.String(), "--profile ipfs") {
		t.Errorf("the render-check command omitted the env file's extra profile:\n%s", h.out.String())
	}
	if !strings.Contains(h.out.String(), h.output) || !strings.Contains(h.out.String(), staging) {
		t.Errorf("the report did not name both preservation sources:\n%s", h.out.String())
	}
}

// A KEK blanked out in the live file (a truncated restore, a hand-edited env) is
// refused, not silently re-minted: the destructive gate cannot depend on whether
// the old value happens to still be there.
func TestSetupRefusesToMintAKEKOverAnExistingFile(t *testing.T) {
	h := newHarness(t)
	if err := h.run(h.setupArgs()...); err != nil {
		t.Fatalf("first setup: %v", err)
	}
	before := h.readOutput(t)
	blanked := strings.Replace(before, "MFA_KEY_KEK="+valueOf(t, before, "MFA_KEY_KEK"), "MFA_KEY_KEK=", 1)
	if blanked == before {
		t.Fatal("failed to blank MFA_KEY_KEK in the fixture")
	}
	if err := os.WriteFile(h.output, []byte(blanked), 0o600); err != nil {
		t.Fatalf("write blanked file: %v", err)
	}

	err := h.run(h.setupArgs("--yes")...)
	if err == nil {
		t.Fatal("a blank KEK was minted over an existing env file")
	}
	for _, want := range []string{"MFA_KEY_KEK", "DESTRUCTIVE", "--rotate MFA_KEY_KEK --yes-i-know"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not mention %q", err, want)
		}
	}
	if h.readOutput(t) != blanked {
		t.Error("the refused run still rewrote the file")
	}

	// The named escape hatch works.
	if err := h.run(h.setupArgs("--rotate", "MFA_KEY_KEK", "--yes", "--yes-i-know")...); err != nil {
		t.Fatalf("explicit rotation of a blank KEK: %v (stderr: %s)", err, h.err.String())
	}
	if v := valueOf(t, h.readOutput(t), "MFA_KEY_KEK"); v == "" {
		t.Error("MFA_KEY_KEK is still blank after an explicit rotation")
	}
}

// Rotating a KEK needs its OWN confirmation. --yes is the everyday answer to
// "rewrite the file in place", typed on every re-deploy; letting it double as the
// destructive one meant a `--rotate MFA_KEY_KEK` sitting in the same command line
// (a copied runbook line, a stale shell history entry) was pre-authorised, and
// every enrolled TOTP secret went with it.
func TestSetupRotateKEKRequiresItsOwnConfirmation(t *testing.T) {
	h := newHarness(t)
	if err := h.run(h.setupArgs()...); err != nil {
		t.Fatalf("first setup: %v", err)
	}
	before := h.readOutput(t)

	for _, args := range [][]string{
		{"--yes", "--rotate", "MFA_KEY_KEK"},                     // the routine intent flag is not enough
		{"--from", h.output, "--yes", "--rotate", "MFA_KEY_KEK"}, // nor is naming a --from
	} {
		err := h.run(h.setupArgs(args...)...)
		if err == nil || !strings.Contains(err.Error(), "--yes-i-know") {
			t.Fatalf("run %v: err = %v, want the destructive-rotation refusal", args, err)
		}
		if h.readOutput(t) != before {
			t.Fatal("the refused rotation still rewrote the file")
		}
	}

	if err := h.run(h.setupArgs("--yes", "--rotate", "MFA_KEY_KEK", "--yes-i-know")...); err != nil {
		t.Fatalf("confirmed rotation: %v (stderr: %s)", err, h.err.String())
	}
	after := h.readOutput(t)
	if kek(t, after) == kek(t, before) {
		t.Error("MFA_KEY_KEK was not rotated")
	}
	if jwt(t, after) != jwt(t, before) {
		t.Error("JWT_SECRET changed while only MFA_KEY_KEK was rotated")
	}
}

// --check is the doctor precursor: it names every variable an operator has to
// fix and exits non-zero, and it never writes anything.
func TestSetupCheckReportsBadVariablesByName(t *testing.T) {
	h := newHarness(t)
	bad := filepath.Join(h.dir, "broken.env")
	if err := os.WriteFile(bad, []byte(strings.Join([]string{
		"VIDRA_ENV=production",
		"HTTP_PORT=not-a-number",
		"HTTP_REQUEST_TIMEOUT=soon",
		"STORAGE_S3_ACCESS_KEY=<your Spaces access key>",
		"VIDRA_CORE_TAG=v0.1.1",
	}, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	err := h.run("setup", "--check", bad)
	if err == nil {
		t.Fatal("--check exited zero on a broken file")
	}
	out := h.out.String()
	for _, want := range []string{"HTTP_PORT", "HTTP_REQUEST_TIMEOUT", "STORAGE_S3_ACCESS_KEY"} {
		if !strings.Contains(out, want) {
			t.Errorf("--check did not name %s:\n%s", want, out)
		}
	}
	if strings.Contains(out, "VIDRA_CORE_TAG") {
		t.Errorf("--check complained about a compose-only key:\n%s", out)
	}

	// And a file this command generated passes.
	if err := h.run(h.setupArgs()...); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := h.run("setup", "--check", h.output); err != nil {
		t.Fatalf("--check on a generated file: %v (%s)", err, h.out.String())
	}
	if !strings.Contains(h.out.String(), "no problems") {
		t.Errorf("unexpected --check output:\n%s", h.out.String())
	}
}

// The generated file is refused, not written, when it would not boot.
func TestSetupRefusesToWriteAnInvalidFile(t *testing.T) {
	h := newHarness(t)
	err := h.run("setup", "--template", h.template, "--non-interactive",
		"--domain", "video.example.org", "--storage", "s3")
	if err == nil {
		t.Fatal("an s3 backend with no credentials was accepted")
	}
	if _, statErr := os.Stat(h.output); !errors.Is(statErr, os.ErrNotExist) {
		t.Error("a file was written despite the validation failure")
	}
	if !strings.Contains(h.err.String(), "STORAGE_S3_ENDPOINT") && !strings.Contains(h.err.String(), "STORAGE_S3_BUCKET") {
		t.Errorf("the failure did not name the missing S3 variables:\n%s", h.err.String())
	}
}

// --check must never green-light a file that omits VIDRA_ENV: in development
// mode a blank JWT_SECRET falls back to the dev constant and a wildcard CORS
// origin is allowed, so the file "passes" and cannot be deployed. The missing
// line and the production findings come back in one pass.
func TestSetupCheckNeverValidatesInDevelopmentMode(t *testing.T) {
	h := newHarness(t)
	bad := filepath.Join(h.dir, "no-env.env")
	if err := os.WriteFile(bad, []byte("JWT_SECRET=\nCORS_ALLOWED_ORIGINS=*\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := h.run("setup", "--check", bad); err == nil {
		t.Fatal("--check exited zero on a file with no VIDRA_ENV, a blank JWT_SECRET and wildcard CORS")
	}
	out := h.out.String()
	if !strings.Contains(out, "VIDRA_ENV") {
		t.Errorf("--check did not report the missing VIDRA_ENV:\n%s", out)
	}
	if !strings.Contains(out, "JWT_SECRET") && !strings.Contains(out, "CORS_ALLOWED_ORIGINS") {
		t.Errorf("--check skipped the production-only rules:\n%s", out)
	}
}

// Secrets must not have to appear in argv, where `ps` and the shell history can
// read them.
func TestSetupSecretsCanAvoidArgv(t *testing.T) {
	s3Args := func(h *harness, extra ...string) []string {
		return append(append([]string{
			"setup", "--template", h.template, "--non-interactive", "--domain", "video.example.org",
			"--release-tag", "v0.1.1", "--storage", "s3",
			"--s3-endpoint", "fra1.example.net", "--s3-region", "fra1", "--s3-bucket", "media",
			"--s3-access-key", "AKIA", "--acme-email", "ops@example.org",
		}, h.caddyArgs()...), extra...)
	}

	t.Run("@file", func(t *testing.T) {
		h := newHarness(t)
		keyFile := filepath.Join(h.dir, "s3.secret")
		// A trailing newline is what an editor or `openssl > file` leaves; it is
		// not part of the secret.
		if err := os.WriteFile(keyFile, []byte("sup3r s3cret\n"), 0o600); err != nil {
			t.Fatalf("write secret file: %v", err)
		}
		if err := h.run(s3Args(h, "--s3-secret-key", "@"+keyFile)...); err != nil {
			t.Fatalf("setup: %v (stderr: %s)", err, h.err.String())
		}
		if got := valueOf(t, h.readOutput(t), "STORAGE_S3_SECRET_KEY"); got != "sup3r s3cret" {
			t.Errorf("STORAGE_S3_SECRET_KEY = %q, want the file's contents without the trailing newline", got)
		}
	})

	t.Run("environment", func(t *testing.T) {
		h := newHarness(t)
		t.Setenv("VIDRA_SETUP_S3_SECRET_KEY", "from-the-environment")
		if err := h.run(s3Args(h)...); err != nil {
			t.Fatalf("setup: %v (stderr: %s)", err, h.err.String())
		}
		if got := valueOf(t, h.readOutput(t), "STORAGE_S3_SECRET_KEY"); got != "from-the-environment" {
			t.Errorf("STORAGE_S3_SECRET_KEY = %q, want the VIDRA_SETUP_* fallback", got)
		}
	})

	t.Run("stdin", func(t *testing.T) {
		h := newHarness(t)
		h.stdin = "piped-secret\n"
		if err := h.run(s3Args(h, "--s3-secret-key", "-")...); err != nil {
			t.Fatalf("setup: %v (stderr: %s)", err, h.err.String())
		}
		if got := valueOf(t, h.readOutput(t), "STORAGE_S3_SECRET_KEY"); got != "piped-secret" {
			t.Errorf("STORAGE_S3_SECRET_KEY = %q, want the piped value", got)
		}
	})

	// A managed connection string is a secret too: postgres://user:PASSWORD@host
	// in argv is the same leak as the S3 key.
	t.Run("a connection string takes the same indirections", func(t *testing.T) {
		h := newHarness(t)
		dsnFile := filepath.Join(h.dir, "db.url")
		if err := os.WriteFile(dsnFile, []byte(managedDSN+"\n"), 0o600); err != nil {
			t.Fatalf("write dsn file: %v", err)
		}
		if err := h.run(h.setupArgs("--database-url", "@"+dsnFile)...); err != nil {
			t.Fatalf("setup: %v (stderr: %s)", err, h.err.String())
		}
		got := h.readOutput(t)
		if v := valueOf(t, got, "DATABASE_URL"); v != managedDSN {
			t.Errorf("DATABASE_URL = %q, want the file's contents without the trailing newline", v)
		}
		// Passing the connection string IS the answer, like an SMTP host is.
		if v := valueOf(t, got, "VIDRA_EXTERNAL_POSTGRES"); v != "true" {
			t.Errorf("VIDRA_EXTERNAL_POSTGRES = %q, want --database-url to have selected the external database", v)
		}
	})

	t.Run("stdin needs non-interactive", func(t *testing.T) {
		h := newHarness(t)
		err := h.run("setup", "--template", h.template, "--domain", "video.example.org", "--s3-secret-key", "-")
		if err == nil || !strings.Contains(err.Error(), "--non-interactive") {
			t.Fatalf("err = %v, want a refusal explaining the stdin clash with the interview", err)
		}
	})

	t.Run("only one flag may read stdin", func(t *testing.T) {
		h := newHarness(t)
		h.stdin = "x\n"
		err := h.run("setup", "--template", h.template, "--non-interactive", "--domain", "video.example.org",
			"--s3-secret-key", "-", "--smtp-password", "-")
		if err == nil || !strings.Contains(err.Error(), "one flag can read stdin") {
			t.Fatalf("err = %v, want the second stdin reader refused", err)
		}
	})

	t.Run("a missing @file is an error, not an empty secret", func(t *testing.T) {
		h := newHarness(t)
		err := h.run("setup", "--template", h.template, "--non-interactive", "--domain", "video.example.org",
			"--smtp-host", "smtp.example.net", "--smtp-from", "a@example.net",
			"--smtp-password", "@"+filepath.Join(h.dir, "nope"))
		if err == nil || !strings.Contains(err.Error(), "smtp-password") {
			t.Fatalf("err = %v, want the unreadable secret file named", err)
		}
	})
}

// A value with a newline in it would add LINES to the env file, truncating the
// secret and injecting an assignment. Refuse, naming the variable, and write
// nothing.
func TestSetupRefusesAValueWithALineBreak(t *testing.T) {
	h := newHarness(t)
	err := h.run("setup", "--template", h.template, "--non-interactive", "--domain", "video.example.org",
		"--release-tag", "v0.1.1", "--storage", "s3",
		"--s3-endpoint", "fra1.example.net", "--s3-region", "fra1", "--s3-bucket", "media",
		"--s3-access-key", "AKIA", "--s3-secret-key", "shhh\nREGISTRATION_ENABLED=true")
	if err == nil {
		t.Fatal("a value containing a newline was accepted")
	}
	if !strings.Contains(h.err.String(), "STORAGE_S3_SECRET_KEY") {
		t.Errorf("the offending variable was not named:\n%s", h.err.String())
	}
	if _, statErr := os.Stat(h.output); !errors.Is(statErr, os.ErrNotExist) {
		t.Error("a file was written despite the rejected value")
	}
}

// `vidra setup -h` is a successful request for help: it prints to stdout and
// exits zero, so `vidra setup -h | head` and `... && echo ok` both work.
func TestSetupHelpSucceedsOnStdout(t *testing.T) {
	for _, flagName := range []string{"-h", "--help", "-help"} {
		t.Run(flagName, func(t *testing.T) {
			h := newHarness(t)
			if err := h.run("setup", flagName); err != nil {
				t.Fatalf("`setup %s` = %v, want success", flagName, err)
			}
			if !strings.Contains(h.out.String(), "usage: vidra setup") || !strings.Contains(h.out.String(), "--rotate") {
				t.Errorf("help was not printed to stdout:\n%s", h.out.String())
			}
			if h.err.Len() != 0 {
				t.Errorf("help wrote to stderr:\n%s", h.err.String())
			}
		})
	}
}

func TestSetupInteractiveAnswersTheMinimalQuestions(t *testing.T) {
	h := newHarness(t)
	// domain, TLS mode, ACME contact, release tag, storage, external Postgres?,
	// external Redis?, optional components?, SMTP?, open registration?
	h.stdin = "video.example.org\nacme\nops@example.org\nv0.1.1\nlocal\nn\nn\nn\nn\nn\n"
	if err := h.run(append([]string{"setup", "--template", h.template}, h.caddyArgs()...)...); err != nil {
		t.Fatalf("interactive setup: %v (stderr: %s)", err, h.err.String())
	}
	got := h.readOutput(t)
	for _, want := range []string{"PUBLIC_BASE_URL=https://video.example.org", "VIDRA_CORE_TAG=v0.1.1", "STORAGE_BACKEND=local", "REGISTRATION_ENABLED=false"} {
		if !strings.Contains(got, want) {
			t.Errorf("generated file is missing %q", want)
		}
	}
	if !strings.Contains(h.out.String(), "Public domain of this instance") {
		t.Errorf("the domain was not asked for:\n%s", h.out.String())
	}
}

// An interactive RE-RUN must not change policy the operator did not answer. The
// registration prompts defaulted to false regardless of the current file, so an
// operator pressing enter through a re-deploy closed signups on an open
// instance — and the prompt did not even say so.
func TestSetupInteractiveReRunKeepsTheRegistrationPolicy(t *testing.T) {
	h := newHarness(t)
	// Start from an instance with registration open and approval required.
	if err := h.run(h.setupArgs("--registration", "approval")...); err != nil {
		t.Fatalf("first setup: %v (stderr: %s)", err, h.err.String())
	}
	before := h.readOutput(t)
	if valueOf(t, before, "REGISTRATION_ENABLED") != "true" || valueOf(t, before, "REGISTRATION_REQUIRE_APPROVAL") != "true" {
		t.Fatalf("fixture is not open+approval:\n%s", before)
	}

	// Re-run interactively, pressing enter at every question: domain, TLS mode,
	// ACME contact, release tag, storage, external Postgres?, external Redis?,
	// optional components?, SMTP?, open registration?, approval?
	h.stdin = "\n\n\n\n\n\n\n\n\n\n\n"
	if err := h.run(append([]string{"setup", "--template", h.template, "--yes"}, h.caddyArgs()...)...); err != nil {
		t.Fatalf("interactive re-run: %v (stderr: %s)", err, h.err.String())
	}
	after := h.readOutput(t)
	for _, k := range []string{"REGISTRATION_ENABLED", "REGISTRATION_REQUIRE_APPROVAL"} {
		if valueOf(t, after, k) != "true" {
			t.Errorf("%s = %q after pressing enter through the interview, want the existing true", k, valueOf(t, after, k))
		}
	}
	// The prompts have to SHOW the current policy as the default, or "enter"
	// is a guess.
	if !strings.Contains(h.out.String(), "Open registration to the public now? [Y/n]") {
		t.Errorf("the registration prompt did not offer the current policy as its default:\n%s", h.out.String())
	}
	if valueOf(t, after, "MFA_KEY_KEK") != valueOf(t, before, "MFA_KEY_KEK") {
		t.Error("the interactive re-run re-minted MFA_KEY_KEK")
	}
}

// An interactive secret prompt must never print the current secret as its
// default, and when the terminal cannot be told to stop echoing it has to SAY
// the input is visible rather than pretend otherwise. (Piped stdin, as here, is
// exactly that case: there is no terminal to turn echo off on.)
func TestSetupInteractiveSecretPromptDoesNotEchoTheCurrentValue(t *testing.T) {
	h := newHarness(t)
	if err := h.run(h.setupArgs("--storage", "s3", "--s3-endpoint", "fra1.example.net",
		"--s3-region", "fra1", "--s3-bucket", "media", "--s3-access-key", "AKIA",
		"--s3-secret-key", "the-live-secret")...); err != nil {
		t.Fatalf("first setup: %v (stderr: %s)", err, h.err.String())
	}

	// domain, TLS mode, ACME contact, tag, storage, endpoint, region, bucket,
	// access key, secret, external Postgres?, external Redis?, optional
	// components?, SMTP?, reg?
	h.stdin = "\n\n\n\n\n\n\n\n\n\n\n\n\nn\n\n"
	if err := h.run(append([]string{"setup", "--template", h.template, "--yes"}, h.caddyArgs()...)...); err != nil {
		t.Fatalf("interactive re-run: %v (stderr: %s)", err, h.err.String())
	}
	prompts := h.out.String()
	if strings.Contains(prompts, "the-live-secret") {
		t.Errorf("the current secret was printed as a prompt default:\n%s", prompts)
	}
	if !strings.Contains(prompts, "S3 secret key (input is echoed) [set; enter keeps it]") {
		t.Errorf("the secret prompt did not mask its default (or claimed hidden input on a pipe):\n%s", prompts)
	}
	// Pressing enter kept it.
	if got := valueOf(t, h.readOutput(t), "STORAGE_S3_SECRET_KEY"); got != "the-live-secret" {
		t.Errorf("STORAGE_S3_SECRET_KEY = %q, want the existing secret kept", got)
	}
}

// The release-tag warning exists for the file that deploys the template's
// example tag — including when the operator got there by accepting the prompt's
// default, which is where it went missing.
func TestSetupInteractiveWarnsWhenTheTemplateTagIsAccepted(t *testing.T) {
	h := newHarness(t)
	// domain, TLS mode, ACME contact, release tag (enter = the template's
	// v0.1.0), storage, external Postgres?, external Redis?, optional
	// components?, SMTP?, reg?
	h.stdin = "video.example.org\nacme\nops@example.org\n\nlocal\nn\nn\nn\nn\nn\n"
	if err := h.run(append([]string{"setup", "--template", h.template}, h.caddyArgs()...)...); err != nil {
		t.Fatalf("interactive setup: %v (stderr: %s)", err, h.err.String())
	}
	if valueOf(t, h.readOutput(t), "VIDRA_CORE_TAG") != "v0.1.0" {
		t.Fatalf("the template's tag was not the accepted default:\n%s", h.readOutput(t))
	}
	if !strings.Contains(h.out.String(), "template's example tag") {
		t.Errorf("accepting the template's example tag did not warn:\n%s", h.out.String())
	}
}

const managedDSN = "postgresql://doadmin:pw@db.example.net:25060/defaultdb?sslmode=require"

// The component answers have to reach BOTH halves of the deployment: the env
// file the scripts read, and the render-check command the operator is told to
// run before deploying.
func TestSetupComponentFlagsSelectProfilesAndOverlays(t *testing.T) {
	h := newHarness(t)
	// `--scan` immediately before another flag: a bool flag that swallowed the
	// next argument would silently drop --ipfs.
	if err := h.run(h.setupArgs("--scan", "--ipfs", "--database", "external", "--database-url", managedDSN)...); err != nil {
		t.Fatalf("setup: %v (stderr: %s)", err, h.err.String())
	}
	got := h.readOutput(t)
	for _, tc := range []struct{ key, want string }{
		{"VIDRA_COMPOSE_PROFILES", "core frontend scan ipfs"},
		{"VIDRA_EXTERNAL_POSTGRES", "true"},
		{"VIDRA_EXTERNAL_REDIS", "false"},
		{"DATABASE_URL", managedDSN},
	} {
		if v := valueOf(t, got, tc.key); v != tc.want {
			t.Errorf("%s = %q, want %q", tc.key, v, tc.want)
		}
	}
	stdout := h.out.String()
	for _, want := range []string{"-f docker-compose.external-postgres.yml", "--profile scan", "--profile ipfs"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the printed render check is missing %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "docker-compose.external-redis.yml") {
		t.Errorf("the redis overlay was added for a bundled Redis:\n%s", stdout)
	}
}

// A re-run about something else must not turn a component off. The flags are
// tri-state for this one reason: `vidra setup --yes` to change a tag would
// otherwise answer "no" to five questions nobody asked.
func TestSetupKeepsTheProfileListOnAReRun(t *testing.T) {
	h := newHarness(t)
	if err := h.run(h.setupArgs("--ipfs")...); err != nil {
		t.Fatalf("first setup: %v (stderr: %s)", err, h.err.String())
	}
	if v := valueOf(t, h.readOutput(t), "VIDRA_COMPOSE_PROFILES"); v != "core frontend ipfs" {
		t.Fatalf("VIDRA_COMPOSE_PROFILES = %q after --ipfs", v)
	}

	if err := h.run(h.setupArgs("--yes", "--release-tag", "v0.2.0")...); err != nil {
		t.Fatalf("re-run: %v (stderr: %s)", err, h.err.String())
	}
	after := h.readOutput(t)
	if v := valueOf(t, after, "VIDRA_COMPOSE_PROFILES"); v != "core frontend ipfs" {
		t.Errorf("VIDRA_COMPOSE_PROFILES = %q after a re-run that never mentioned ipfs, want it kept", v)
	}
	if v := valueOf(t, after, "VIDRA_CORE_TAG"); v != "v0.2.0" {
		t.Errorf("VIDRA_CORE_TAG = %q, want the re-run's answer to have landed", v)
	}

	// Turning it off is deliberate and explicit.
	if err := h.run(h.setupArgs("--yes", "--ipfs=false")...); err != nil {
		t.Fatalf("third setup: %v (stderr: %s)", err, h.err.String())
	}
	if v := valueOf(t, h.readOutput(t), "VIDRA_COMPOSE_PROFILES"); v != "core frontend" {
		t.Errorf("VIDRA_COMPOSE_PROFILES = %q after --ipfs=false, want the profile gone", v)
	}
}

// A managed connection string carries the password inside it, so the interview
// treats it as the secret it is.
func TestSetupInteractiveAsksForAManagedDatabase(t *testing.T) {
	h := newHarness(t)
	// domain, TLS mode, ACME contact, tag, storage, external Postgres? y, its
	// DSN, external Redis? n, optional components? n, SMTP? n, registration? n
	h.stdin = "video.example.org\nacme\nops@example.org\nv0.1.1\nlocal\ny\n" + managedDSN + "\nn\nn\nn\nn\n"
	if err := h.run(append([]string{"setup", "--template", h.template}, h.caddyArgs()...)...); err != nil {
		t.Fatalf("interactive setup: %v (stderr: %s)", err, h.err.String())
	}
	got := h.readOutput(t)
	if v := valueOf(t, got, "VIDRA_EXTERNAL_POSTGRES"); v != "true" {
		t.Errorf("VIDRA_EXTERNAL_POSTGRES = %q, want the answered external database", v)
	}
	if v := valueOf(t, got, "DATABASE_URL"); v != managedDSN {
		t.Errorf("DATABASE_URL = %q, want the answered connection string", v)
	}
	if v := valueOf(t, got, "VIDRA_EXTERNAL_REDIS"); v != "false" {
		t.Errorf("VIDRA_EXTERNAL_REDIS = %q, want the bundled Redis kept", v)
	}
	// The prompt is a secret prompt (piped stdin cannot hide the echo, so it says
	// so rather than pretending), and the answer must not be echoed back in the
	// summary.
	if !strings.Contains(h.out.String(), "Managed PostgreSQL connection string (input is echoed)") {
		t.Errorf("the DSN was not asked for as a secret:\n%s", h.out.String())
	}
}

// The second file every deployment needs. It is generated from the same answers
// as the env file, in the same run, so a deployment cannot end up with an env
// file naming one domain and a reverse proxy holding a certificate for another.
func TestSetupWritesTheManagedCaddyfile(t *testing.T) {
	h := newHarness(t)
	if err := h.run(h.setupArgs()...); err != nil {
		t.Fatalf("setup: %v (stderr: %s)", err, h.err.String())
	}

	info, err := os.Stat(h.caddyOut)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	// 0644, not 0600: it carries no secret, and it is bind-mounted into a
	// container that does not run as the user who generated it.
	if info.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v, want 0644", info.Mode().Perm())
	}

	got := h.readCaddyfile(t)
	for _, want := range []string{"GENERATED by `vidra setup`", "video.example.org {", "email ops@example.org"} {
		if !strings.Contains(got, want) {
			t.Errorf("the generated Caddyfile is missing %q:\n%s", want, got)
		}
	}
	for _, line := range strings.Split(got, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "#") && strings.Contains(line, "example.com") {
			t.Errorf("a non-comment line still names example.com, which deploy.sh refuses to deploy: %q", line)
		}
	}
	// The answers are also recorded in the env file, so a re-run (and doctor)
	// can see what the proxy config was generated with.
	env := h.readOutput(t)
	if v := valueOf(t, env, "VIDRA_TLS_MODE"); v != "acme" {
		t.Errorf("VIDRA_TLS_MODE = %q, want the default acme", v)
	}
	if v := valueOf(t, env, "VIDRA_ACME_EMAIL"); v != "ops@example.org" {
		t.Errorf("VIDRA_ACME_EMAIL = %q, want the answered contact address", v)
	}
	if !strings.Contains(h.out.String(), h.caddyOut) {
		t.Errorf("the summary does not mention the generated Caddyfile:\n%s", h.out.String())
	}
}

// The bring-up mode: no ACME order, so no contact address is required and the
// site issues from Caddy's local CA. The env file says so too, and says loudly
// that this is not a certificate a browser will accept.
func TestSetupCaddyfileFollowsTheTLSMode(t *testing.T) {
	h := newHarness(t)
	args := append([]string{
		"setup", "--template", h.template, "--non-interactive",
		"--domain", "video.example.org", "--release-tag", "v0.1.1", "--storage", "local",
		"--tls-mode", "internal",
	}, h.caddyArgs()...)
	if err := h.run(args...); err != nil {
		t.Fatalf("setup: %v (stderr: %s)", err, h.err.String())
	}
	got := h.readCaddyfile(t)
	if !strings.Contains(got, "tls internal") {
		t.Errorf("internal mode did not reach the Caddyfile:\n%s", got)
	}
	if strings.Contains(got, "\temail ") {
		t.Errorf("an ACME contact was injected in internal mode:\n%s", got)
	}
	if v := valueOf(t, h.readOutput(t), "VIDRA_TLS_MODE"); v != "internal" {
		t.Errorf("VIDRA_TLS_MODE = %q, want internal", v)
	}
	if !strings.Contains(h.out.String(), "publicly trusted") {
		t.Errorf("a bring-up mode was not warned about:\n%s", h.out.String())
	}
}

// An acme mode with no contact address is refused, and refused BEFORE anything
// is written: a rewritten env file beside a Caddyfile that was never regenerated
// is the state an operator cannot reason about.
func TestSetupAcmeNeedsAContactAddress(t *testing.T) {
	h := newHarness(t)
	args := append([]string{
		"setup", "--template", h.template, "--non-interactive",
		"--domain", "video.example.org", "--release-tag", "v0.1.1", "--storage", "local",
	}, h.caddyArgs()...)
	err := h.run(args...)
	if err == nil || !strings.Contains(err.Error(), "--acme-email") {
		t.Fatalf("err = %v, want a refusal naming --acme-email", err)
	}
	if _, statErr := os.Stat(h.output); statErr == nil {
		t.Error("the env file was written even though the run failed")
	}
}

// --no-caddy is the external-proxy answer: nothing is generated, and the env
// file is written exactly as before.
func TestSetupNoCaddySkipsTheProxyConfig(t *testing.T) {
	h := newHarness(t)
	if err := h.run("setup", "--template", h.template, "--non-interactive",
		"--domain", "video.example.org", "--release-tag", "v0.1.1", "--storage", "local",
		"--no-caddy"); err != nil {
		t.Fatalf("setup: %v (stderr: %s)", err, h.err.String())
	}
	if _, err := os.Stat(h.caddyOut); err == nil {
		t.Error("--no-caddy generated a Caddyfile anyway")
	}
	if strings.Contains(h.out.String(), "Caddyfile") {
		t.Errorf("--no-caddy still reported a Caddyfile:\n%s", h.out.String())
	}
	// No template was needed either — the point of the flag is a host that has
	// no deploy/Caddyfile at all.
	if got := h.readOutput(t); !strings.Contains(got, "PUBLIC_BASE_URL=https://video.example.org") {
		t.Errorf("the env file was not generated:\n%s", got)
	}
}

// The default template path is relative to the deployment directory, so running
// setup somewhere else has to say which file it wanted and how to proceed
// without it.
func TestSetupMissingCaddyTemplateNamesThePath(t *testing.T) {
	h := newHarness(t)
	missing := filepath.Join(h.dir, "nowhere", "Caddyfile")
	err := h.run("setup", "--template", h.template, "--non-interactive",
		"--domain", "video.example.org", "--release-tag", "v0.1.1", "--storage", "local",
		"--acme-email", "ops@example.org", "--caddy-template", missing)
	if err == nil {
		t.Fatal("a missing Caddyfile template was not reported")
	}
	for _, want := range []string{missing, "--no-caddy"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
	}
}

// An unattended install must fail loudly rather than hang or invent an answer.
func TestSetupNonInteractiveNeedsADomain(t *testing.T) {
	h := newHarness(t)
	err := h.run("setup", "--template", h.template, "--non-interactive", "--release-tag", "v0.1.1")
	if err == nil || !strings.Contains(err.Error(), "domain is required") {
		t.Fatalf("err = %v, want the domain requirement", err)
	}
}

func TestOutputPathDefault(t *testing.T) {
	got, err := outputPath("env/production.env.example", "")
	if err != nil || got != "env/production.env" {
		t.Errorf("outputPath = %q, %v", got, err)
	}
	if got, err := outputPath("env/production.env.example", "/tmp/other.env"); err != nil || got != "/tmp/other.env" {
		t.Errorf("explicit --output = %q, %v", got, err)
	}
	if _, err := outputPath("env/whatever", ""); err == nil {
		t.Error("a template without a .example suffix should require --output")
	}
}

// The name here has to be one the dispatch table does NOT carry — it used to be
// "doctor", which is now a real subcommand and would have run it against the
// working directory instead.
func TestUnknownCommandPrintsUsage(t *testing.T) {
	var out, errBuf bytes.Buffer
	err := run(streams{in: strings.NewReader(""), out: &out, err: &errBuf}, []string{"deploy"})
	if !errors.Is(err, errReported) {
		t.Fatalf("err = %v, want errReported", err)
	}
	if !strings.Contains(errBuf.String(), "unknown command") || !strings.Contains(errBuf.String(), "setup") {
		t.Errorf("usage not printed:\n%s", errBuf.String())
	}
	// The table's other entries are listed too, so an operator who typed the
	// wrong name can see the right one.
	if !strings.Contains(errBuf.String(), "doctor") {
		t.Errorf("the usage list is missing a registered command:\n%s", errBuf.String())
	}
}

func kek(t *testing.T, file string) string { return valueOf(t, file, "MFA_KEY_KEK") }
func jwt(t *testing.T, file string) string { return valueOf(t, file, "JWT_SECRET") }

func valueOf(t *testing.T, file, key string) string {
	t.Helper()
	for _, line := range strings.Split(file, "\n") {
		if strings.HasPrefix(line, key+"=") {
			return strings.TrimPrefix(line, key+"=")
		}
	}
	t.Fatalf("%s not found in the generated file", key)
	return ""
}
