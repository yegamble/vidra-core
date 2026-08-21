package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vidra/vidra-core/internal/preflight"
	"github.com/vidra/vidra-core/internal/setup"
)

// runSetup is `vidra setup`: generate (or re-generate, or check) the env file a
// production deployment runs on. The engine lives in internal/setup — this file
// is argument plumbing, prompts and printing, so the web wizard and the
// installer can reach the same behaviour without going through a terminal.
func runSetup(s streams, args []string) error {
	fs := flag.NewFlagSet("vidra setup", flag.ContinueOnError)
	fs.SetOutput(s.err)

	opt := registerSetupFlags(fs)

	usage := func(w io.Writer) {
		fmt.Fprint(w, `usage: vidra setup --template env/production.env.example [flags]
       vidra setup --check env/production.env

Generates the production env file from the deployment template, filling in the
answers below and minting every secret the template leaves blank. Comments and
ordering are preserved, so the generated file stays as self-documenting as the
template it came from, and it is written with mode 0600.

Re-running is safe. The file being written is ALWAYS read back first and every
value it sets is preserved, whether or not --from is given; --from adds a second
source for the keys it leaves blank, and never overrides it. A secret is only
ever replaced when --rotate names it, and rotating a *_KEK additionally needs
--yes-i-know because it orphans data already sealed in the database — that is a
separate answer from --yes, which only confirms the in-place rewrite.

Secrets do not have to appear on the command line: --s3-secret-key,
--smtp-password, --database-url and --redis-url accept @path (read the file), -
(read stdin, with --non-interactive) or a VIDRA_SETUP_* environment variable, and
the interactive prompts read them without echoing. A managed connection string
counts as a secret: it carries the password inside it.

Answers can come from a FILE as well as from argv: --answers <path> reads
"flag-name = value" lines (the flag names below, without their dashes; # comments
and blank lines ignored) and applies each one the command line did not already
set — argv always wins, so --domain other.example.org beside a file overrides it.
--answers implies nothing else: without --non-interactive, the questions it does
not answer are still asked, which makes a partial file a pre-seeded interview. A
secret's @path and $VIDRA_SETUP_* indirections work in there too; - (stdin) does
not, because stdin belongs to the terminal.

The optional components (--scan, --captions, --media, --otel, --ipfs) are compose
profiles, written to VIDRA_COMPOSE_PROFILES for the deploy scripts to enable. Not
passing one keeps whatever the env file already selects, so a re-run about
something else cannot silently switch a component off; --scan=false turns one off
deliberately.

The reverse proxy is generated too: deploy/Caddyfile.local is rendered from the
deploy/Caddyfile template with the instance's domain as its site address and the
--tls-mode answer as its certificate issuer, and the prod compose file bind-mounts
it. Pass --no-caddy when another proxy terminates TLS.

--tls-mode also chooses the deployment's TOPOLOGY, not just its issuer. external
means your own load balancer or nginx is the front door: the managed caddy is left
out of the compose profiles, no Caddyfile is written, and an example config for
that proxy is generated instead. plain-http means no TLS anywhere — a lab, a LAN
or an air-gapped install: caddy still runs, as an http site, the origin is http://
and the generated file carries VIDRA_ALLOW_PLAIN_HTTP=true, which is what lets the
api boot without Secure cookies. Nothing on the public internet should use it.

flags:
`)
		fs.SetOutput(w)
		fs.PrintDefaults()
		fs.SetOutput(s.err)
		fmt.Fprint(w, "\nrotatable secrets: "+strings.Join(setup.SecretVars(), ", ")+"\n")
	}
	// Nothing is printed by flag's own usage hook: WHERE the text goes is the
	// difference between help and a mistake, and only the code below knows which
	// happened. flag still prints its specific "flag provided but not defined"
	// line to s.err first, which is the part worth keeping.
	fs.Usage = func() {}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			// -h is a successful request for help, and it goes to STDOUT, so
			// `vidra setup -h | head` and `... && echo ok` both work.
			usage(s.out)
			return nil
		}
		usage(s.err)
		return errReported
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(s.err, "vidra setup: unexpected argument %q\n\n", fs.Arg(0))
		usage(s.err)
		return errReported
	}
	// AFTER the command line has been parsed, so "was this flag passed?" has an
	// answer: the file fills what argv left unset, and never the other way round.
	if opt.answersPath != "" {
		if err := applyAnswersFile(fs, opt.answersPath); err != nil {
			return err
		}
	}

	if opt.checkPath != "" {
		return checkFile(s, opt.checkPath)
	}
	if opt.template == "" {
		usage(s.err)
		return errors.New("setup: --template is required (the deployment template, env/production.env.example, is the input format)")
	}

	outPath, err := outputPath(opt.template, opt.output)
	if err != nil {
		return err
	}
	tmpl, err := readEnvFile(opt.template)
	if err != nil {
		return err
	}

	// Preservation sources, in precedence order. THE FILE BEING WRITTEN IS
	// ALWAYS ONE OF THEM: it holds the KEKs that seal data in the database, so
	// its values have to survive the run no matter which other file was named.
	// Deriving the source from --from instead is what let `--from staging.env
	// --output production.env` re-mint a live instance's KEKs — staging's values
	// landed and everything staging did not mention was generated fresh.
	var sources []*setup.EnvFile
	var sourcePaths []string
	_, statErr := os.Stat(outPath)
	outExists := statErr == nil
	if outExists {
		outFile, rerr := readEnvFile(outPath)
		if rerr != nil {
			return rerr
		}
		sources = append(sources, outFile)
		sourcePaths = append(sourcePaths, outPath)
	}
	// --from naming the output itself is not a second source, whatever it is
	// spelled like: env/production.env, ./env/production.env and an absolute path
	// are one file. Comparing the raw strings would both list the file twice and
	// let `--from ./production.env` walk past the intent gate below, which is the
	// one keystroke standing between a typo and a live deployment's env file.
	fromPath := opt.from
	if fromPath != "" && samePath(fromPath, outPath) {
		fromPath = ""
	}
	if fromPath != "" {
		fromFile, rerr := readEnvFile(fromPath)
		if rerr != nil {
			return rerr
		}
		sources = append(sources, fromFile)
		sourcePaths = append(sourcePaths, fromPath)
	}
	// The remaining gate is intent, not safety: rewriting the env file a live
	// deployment is running from is worth one deliberate keystroke, and --from
	// (or --yes) is that keystroke.
	if outExists && fromPath == "" && !opt.yes {
		return fmt.Errorf("setup: %s already exists and is the file a running deployment reads — re-run with --yes to rewrite it in place, "+
			"or with --from <other env file> to merge that file into it as well (either way every value %s already sets, including the KEKs, is preserved), "+
			"or pass a different --output", outPath, outPath)
	}
	existing := setup.MergeSources(sources...)

	stdinTaken := false
	s3Secret, err := readSecretFlag(s, "s3-secret-key", opt.s3SecretKey, opt.nonInteractive, &stdinTaken)
	if err != nil {
		return err
	}
	mailPassword, err := readSecretFlag(s, "smtp-password", opt.smtpPassword, opt.nonInteractive, &stdinTaken)
	if err != nil {
		return err
	}
	// A connection string is a secret like any other — postgres://user:PASSWORD@host
	// is the whole point of the managed-service path — so it gets the same @file /
	// stdin / environment indirections and stays out of argv.
	databaseConn, err := readSecretFlag(s, "database-url", opt.databaseURL, opt.nonInteractive, &stdinTaken)
	if err != nil {
		return err
	}
	redisConn, err := readSecretFlag(s, "redis-url", opt.redisURL, opt.nonInteractive, &stdinTaken)
	if err != nil {
		return err
	}

	answers := setup.Answers{
		Domain:         opt.domain,
		InstanceName:   opt.instanceName,
		TLSMode:        opt.tlsMode,
		AcmeEmail:      opt.acmeEmail,
		ReleaseTag:     opt.releaseTag,
		CoreTag:        opt.coreTag,
		UserTag:        opt.userTag,
		SearchTag:      opt.searchTag,
		StorageBackend: opt.storage,
		S3: setup.S3Answers{
			Endpoint:  opt.s3Endpoint,
			Region:    opt.s3Region,
			Bucket:    opt.s3Bucket,
			AccessKey: opt.s3AccessKey,
			SecretKey: s3Secret,
		},
		Database: setup.DatabaseAnswers{Mode: opt.database, URL: databaseConn},
		Redis:    setup.RedisAnswers{Mode: opt.redis, URL: redisConn},
	}
	// Giving the connection string IS the answer, the same way giving an SMTP host
	// turns mail on: nobody passes --database-url to keep using the bundled
	// Postgres, and requiring --database external beside it would make the
	// difference between the two a silently-ignored flag.
	if answers.Database.Mode == "" && answers.Database.URL != "" {
		answers.Database.Mode = "external"
	}
	if answers.Redis.Mode == "" && answers.Redis.URL != "" {
		answers.Redis.Mode = "external"
	}
	// SEEDED, then overlaid with the flags that were actually passed: the profile
	// list is authoritative in the engine (see setup.Answers.Features), so an
	// operator re-running setup to change the domain would otherwise lose the ipfs
	// profile they turned on last time.
	answers.Features = setup.FeaturesFromProfiles(effective(tmpl, existing, "VIDRA_COMPOSE_PROFILES"))
	opt.features.applyTo(&answers.Features)
	if opt.smtpHost != "" || opt.smtpFrom != "" || opt.smtpUsername != "" || mailPassword != "" || opt.smtpPort != "" {
		answers.Mail = &setup.MailAnswers{
			Host:     opt.smtpHost,
			Port:     opt.smtpPort,
			Username: opt.smtpUsername,
			Password: mailPassword,
			From:     opt.smtpFrom,
		}
	}
	if opt.registration != "" {
		reg, rerr := registrationAnswer(opt.registration)
		if rerr != nil {
			return rerr
		}
		answers.Registration = reg
	}
	if !opt.nonInteractive {
		if err := requireInteractiveStdin(s); err != nil {
			return err
		}
		if err := interview(s, tmpl, existing, &answers); err != nil {
			return err
		}
	}

	res, err := setup.Generate(setup.Request{
		Template:           tmpl,
		Existing:           existing,
		Answers:            answers,
		Rotate:             opt.rotate,
		ConfirmDestructive: opt.yesIKnow,
	})
	if err != nil {
		var invalid *setup.ValidationError
		if errors.As(err, &invalid) {
			fmt.Fprintf(s.err, "setup: refusing to write %s — the configuration it describes would not boot:\n\n", outPath)
			printIssues(s.err, invalid.Issues)
			return errReported
		}
		return err
	}
	// Rendered BEFORE anything is written: an answer the Caddyfile refuses (an
	// acme mode with no contact address, a template someone stripped the markers
	// out of) must stop the run, not leave a rewritten env file beside a proxy
	// config that was never generated.
	caddy, err := renderCaddyfile(opt.caddyTemplate, opt.noCaddy, res.Values)
	if err != nil {
		return err
	}
	nginx, err := renderNginxExample(res.Values)
	if err != nil {
		return err
	}
	if err := setup.WriteFile(outPath, res.Content); err != nil {
		return err
	}
	caddyPath := ""
	if caddy != nil {
		if err := setup.WriteCaddyfile(opt.caddyOut, caddy); err != nil {
			return err
		}
		caddyPath = opt.caddyOut
	}
	nginxPath := ""
	if nginx != nil {
		// Same 0644 as the Caddyfile: it is a proxy configuration an operator has
		// to be able to read, copy and diff, and it holds no secret.
		if err := setup.WriteCaddyfile(opt.nginxOut, nginx); err != nil {
			return err
		}
		nginxPath = opt.nginxOut
	}
	report(s, outPath, caddyPath, nginxPath, sourcePaths, res)
	return nil
}

// setupOptions is where every `vidra setup` flag lands. It exists so the flag
// set can be built TWICE from one definition — once for the real run, and once
// as the scratch copy applyAnswersFile validates the answers file against.
type setupOptions struct {
	checkPath      string
	template       string
	output         string
	from           string
	answersPath    string
	nonInteractive bool
	yes            bool
	yesIKnow       bool
	domain         string
	instanceName   string
	releaseTag     string
	tlsMode        string
	acmeEmail      string
	caddyTemplate  string
	caddyOut       string
	nginxOut       string
	noCaddy        bool
	coreTag        string
	userTag        string
	searchTag      string
	database       string
	databaseURL    string
	redis          string
	redisURL       string
	storage        string
	s3Endpoint     string
	s3Region       string
	s3Bucket       string
	s3AccessKey    string
	s3SecretKey    string
	smtpHost       string
	smtpPort       string
	smtpUsername   string
	smtpPassword   string
	smtpFrom       string
	registration   string
	rotate         stringList
	features       featureFlags
}

// registerSetupFlags defines the command's whole interface on fs. It is the ONE
// definition of it: a second, divergent list is how "every line is validated"
// stops being true.
func registerSetupFlags(fs *flag.FlagSet) *setupOptions {
	o := &setupOptions{}
	fs.StringVar(&o.checkPath, "check", "", "validate an existing env `file` and exit (no file is written)")
	fs.StringVar(&o.template, "template", "", "`path` to the deployment template, e.g. env/production.env.example (required)")
	fs.StringVar(&o.output, "output", "", "`path` to write (default: the template path without its .example suffix)")
	fs.StringVar(&o.from, "from", "", "extra env `file` to merge: it fills keys the output file leaves blank, and never overrides it")

	fs.StringVar(&o.answersPath, "answers", "", "`file` of \"flag-name = value\" lines to take answers from; anything also on the command line wins")
	fs.BoolVar(&o.nonInteractive, "non-interactive", false, "never prompt; take every answer from flags (for unattended installs)")
	fs.BoolVar(&o.yes, "yes", false, "rewrite an existing --output in place (every value it sets is still preserved)")
	// Deliberately NOT --yes: an operator confirming the routine in-place
	// rewrite has not agreed to orphan every secret a KEK seals, and one flag
	// for both would mean an everyday `--yes` silently pre-authorised a
	// `--rotate MFA_KEY_KEK` in the same command line. The spelling matches the
	// `api migrate force --yes-i-know` gate, which guards the same class of
	// unrecoverable action.
	fs.BoolVar(&o.yesIKnow, "yes-i-know", false, "confirm a DESTRUCTIVE *_KEK rotation: it orphans the data already sealed under that key")

	fs.StringVar(&o.domain, "domain", "", "public origin of the instance, e.g. video.example.org (https is assumed)")
	fs.StringVar(&o.instanceName, "instance-name", "", "public `name` of this instance (INSTANCE_NAME): served at /api/v1/instance, in NodeInfo, and as the TOTP issuer")
	fs.StringVar(&o.releaseTag, "release-tag", "", "image `tag` to deploy for all three services, e.g. v0.1.1")

	fs.StringVar(&o.tlsMode, "tls-mode", "", "TLS topology: `acme|acme-staging|internal|external|plain-http` (default acme; external = your own proxy terminates, plain-http = no TLS at all)")
	fs.StringVar(&o.acmeEmail, "acme-email", "", "contact `address` Let's Encrypt sends expiry notices to (optional; without it there is no warning before a failed renewal)")
	fs.StringVar(&o.caddyTemplate, "caddy-template", setup.CaddyTemplatePath, "`path` to the Caddyfile template the reverse-proxy config is rendered from")
	fs.StringVar(&o.caddyOut, "caddy-out", setup.CaddyOutputPath, "`path` to write the generated Caddyfile (the prod compose file bind-mounts it)")
	fs.StringVar(&o.nginxOut, "nginx-out", setup.NginxExampleOutputPath, "`path` to write the external-proxy example (--tls-mode external only; nothing mounts it)")
	fs.BoolVar(&o.noCaddy, "no-caddy", false, "do not generate a Caddyfile (another reverse proxy terminates TLS)")
	fs.StringVar(&o.coreTag, "core-tag", "", "override the api image `tag`")
	fs.StringVar(&o.userTag, "user-tag", "", "override the frontend image `tag`")
	fs.StringVar(&o.searchTag, "search-tag", "", "override the search image `tag`")

	fs.StringVar(&o.database, "database", "", "PostgreSQL: `local|external` — external needs --database-url and makes the deploy skip the bundled container")
	fs.StringVar(&o.databaseURL, "database-url", "", "managed PostgreSQL connection string: `@file`, - for stdin, or the value itself (also $VIDRA_SETUP_DATABASE_URL)")
	fs.StringVar(&o.redis, "redis", "", "Redis: `local|external` — external needs --redis-url and makes the deploy skip the bundled container")
	fs.StringVar(&o.redisURL, "redis-url", "", "managed Redis connection string: `@file`, - for stdin, or the value itself (also $VIDRA_SETUP_REDIS_URL)")

	fs.StringVar(&o.storage, "storage", "", "media storage backend: `local|s3`")
	fs.StringVar(&o.s3Endpoint, "s3-endpoint", "", "S3 endpoint `host` WITHOUT a scheme, e.g. nyc3.digitaloceanspaces.com")
	fs.StringVar(&o.s3Region, "s3-region", "", "S3 `region`, matching the endpoint")
	fs.StringVar(&o.s3Bucket, "s3-bucket", "", "S3 `bucket` (pre-create it)")
	fs.StringVar(&o.s3AccessKey, "s3-access-key", "", "S3 access `key`")
	fs.StringVar(&o.s3SecretKey, "s3-secret-key", "", "S3 secret key: `@file`, - for stdin, or the value itself (also $VIDRA_SETUP_S3_SECRET_KEY)")

	fs.StringVar(&o.smtpHost, "smtp-host", "", "SMTP relay `host` (enables mail)")
	fs.StringVar(&o.smtpPort, "smtp-port", "", "SMTP `port` (587 with STARTTLS is the usual answer)")
	fs.StringVar(&o.smtpUsername, "smtp-username", "", "SMTP `username`")
	fs.StringVar(&o.smtpPassword, "smtp-password", "", "SMTP password: `@file`, - for stdin, or the value itself (also $VIDRA_SETUP_SMTP_PASSWORD)")
	fs.StringVar(&o.smtpFrom, "smtp-from", "", "From `address` for outbound mail")

	fs.StringVar(&o.registration, "registration", "", "signup policy: `closed|open|approval`")
	fs.Var(&o.rotate, "rotate", "re-generate the secret in this `VAR` even though it already has a value (repeatable)")
	o.features.register(fs)
	return o
}

// renderCaddyfile renders the managed reverse-proxy config from the values the
// env file was just generated with, returning nil when there is nothing to
// generate.
//
// The RESOLVED values are the input, not the answers: a re-run that only changes
// the release tag passes no --domain and no --tls-mode, and it must still
// regenerate the same Caddyfile rather than none. res.Values is what the file on
// disk says after answer/existing/template precedence has been applied, which is
// exactly the deployment the proxy has to match.
func renderCaddyfile(templatePath string, skip bool, values map[string]string) ([]byte, error) {
	if skip {
		return nil, nil
	}
	// VIDRA_TLS_MODE=external is --no-caddy chosen in the env file instead of on
	// the command line: the deploy leaves the caddy service out of the compose
	// profiles entirely, so a Caddyfile generated here would be a file nothing
	// mounts, ordering a certificate for a hostname the operator's own terminator
	// already holds one for. renderNginxExample is what runs instead.
	if setup.SkipsManagedCaddy(values["VIDRA_TLS_MODE"]) {
		return nil, nil
	}
	// Only reachable with a template that does not define PUBLIC_BASE_URL at all
	// (Generate refuses a missing domain otherwise). There is no site address to
	// render, so there is no Caddyfile.
	if strings.TrimSpace(values["PUBLIC_BASE_URL"]) == "" {
		return nil, nil
	}
	b, err := os.ReadFile(templatePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("setup: the Caddyfile template %s does not exist. Run this from the deployment directory (the checkout that holds deploy/ and docker-compose.yml), point --caddy-template at the template, or pass --no-caddy if another reverse proxy terminates TLS", templatePath)
		}
		return nil, fmt.Errorf("setup: read %s: %w", templatePath, err)
	}
	return setup.RenderCaddyfile(b, setup.Answers{
		Domain:    values["PUBLIC_BASE_URL"],
		TLSMode:   values["VIDRA_TLS_MODE"],
		AcmeEmail: values["VIDRA_ACME_EMAIL"],
	})
}

// renderNginxExample is the other half of renderCaddyfile: the file an
// external-terminator deployment gets INSTEAD of a Caddyfile. Same input (the
// resolved values, not the answers), same nil-means-nothing-to-write contract.
//
// It runs even under --no-caddy, because the two flags mean different things:
// --no-caddy says "do not generate a Caddyfile", and an operator who has also
// said VIDRA_TLS_MODE=external has said their proxy is the front door — which is
// precisely who this example is for.
func renderNginxExample(values map[string]string) ([]byte, error) {
	if !setup.SkipsManagedCaddy(values["VIDRA_TLS_MODE"]) {
		return nil, nil
	}
	if strings.TrimSpace(values["PUBLIC_BASE_URL"]) == "" {
		return nil, nil
	}
	return setup.RenderNginxExternal(setup.Answers{Domain: values["PUBLIC_BASE_URL"]}, values)
}

// secretFlagEnv is every flag whose value is a SECRET, mapped to the environment
// variable it falls back to. One list, read by both halves of the contract:
// readSecretFlag resolves the fallback from it, and applyAnswersFile refuses the
// stdin indirection for exactly these flags. Two lists would drift, and the drift
// would show up as a secret that silently arrived from nowhere.
var secretFlagEnv = map[string]string{
	"s3-secret-key": "VIDRA_SETUP_S3_SECRET_KEY",
	"smtp-password": "VIDRA_SETUP_SMTP_PASSWORD",
	"database-url":  "VIDRA_SETUP_DATABASE_URL",
	"redis-url":     "VIDRA_SETUP_REDIS_URL",
}

// applyAnswersFile fills in the flags an answers file names and the command line
// did not.
//
// It exists because the flags ARE the unattended interface and a real install
// needs a dozen of them: kept in a file they can be reviewed, diffed, put in
// configuration management and re-run, instead of living in one enormous shell
// line that has to be retyped correctly at 3am. The format is the flag names
// themselves — `domain = video.example.org` — so there is no second vocabulary
// to learn or to keep in step with `-h`.
//
// Two rules make it safe to reach for:
//
//   - ARGV ALWAYS WINS. The file is applied through fs.Set only for names
//     fs.Visit did not report, so `--instance-name Other` beside a file that says
//     something else is an override, not a conflict. A file that could overrule
//     the command line would make the command line unreadable.
//   - IT IMPLIES NOTHING ELSE. Not --non-interactive, not --yes. A partial file
//     is therefore a PRE-SEEDED INTERVIEW: what it answers is not asked, what it
//     leaves out still is.
//
// Every line is validated whether or not it is applied, and it is a SCRATCH flag
// set — the same command, built by the same constructor — that makes that true.
// Validating by applying to the real one could only ever check the lines argv did
// not override, so `--scan` on the command line beside `scan = maybe` in the file
// used to hide the typo until the run where the operator stopped passing --scan.
// A typo on a line argv happens to override is still a typo, and finding it now
// costs one run.
func applyAnswersFile(fs *flag.FlagSet, path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("setup: read --answers %s: %w", path, err)
	}
	onArgv := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { onArgv[f.Name] = true })
	// Nothing reads what lands in it, and nothing prints from it: it exists to
	// run each value through the same flag.Value.Set the real run would.
	scratch := flag.NewFlagSet("vidra setup", flag.ContinueOnError)
	scratch.SetOutput(io.Discard)
	registerSetupFlags(scratch)

	for i, raw := range strings.Split(string(b), "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		lineNo := i + 1
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("setup: %s:%d: %q is not an answer — every line is `flag-name = value`, a # comment, or blank", path, lineNo, line)
		}
		// Leading dashes are tolerated, not required: an operator copying a line
		// out of `-h` has written the right answer, and refusing it would be
		// pedantry with a re-run attached.
		name = strings.TrimLeft(strings.TrimSpace(name), "-")
		value = strings.TrimSpace(value)
		switch {
		case name == "answers":
			return fmt.Errorf("setup: %s:%d: an answers file cannot set --answers — the file being read is already the answer to that question", path, lineNo)
		case fs.Lookup(name) == nil:
			return fmt.Errorf("setup: %s:%d: %q is not an answer this command knows — the names here are the flag names without their dashes, and `vidra setup -h` lists every one", path, lineNo, name)
		case value == "-" && secretFlagEnv[name] != "":
			// stdin belongs to the terminal running the install (and only ONE
			// flag may ever read it — see readSecretFlag). A file that could
			// claim it would make that rule unenforceable from the command line,
			// which is where it is enforced.
			return fmt.Errorf("setup: %s:%d: `%s = -` reads the value from stdin, which belongs to the terminal and can only be claimed on the command line — write `%s = @<path>` to read a file, or set $%s",
				path, lineNo, name, name, secretFlagEnv[name])
		}
		if err := scratch.Set(name, value); err != nil {
			return fmt.Errorf("setup: %s:%d: --%s %q: %w", path, lineNo, name, value, err)
		}
		if onArgv[name] {
			continue
		}
		if err := fs.Set(name, value); err != nil {
			return fmt.Errorf("setup: %s:%d: --%s %q: %w", path, lineNo, name, value, err)
		}
	}
	return nil
}

// requireInteractiveStdin refuses an interview that has nobody to ask.
//
// `curl … | sh` is the first install everyone tries, and with stdin held by the
// pipe the interview used to print its first question, read EOF, and die with
// "no answer for ..." — halfway through a run the operator had already watched
// start, having written nothing. The mode is knowable up front, so it is
// answered up front, with the two ways out named.
//
// The test is deliberately ONE-SIDED: only a stdin that is an *os.File AND says
// it is not a character device is refused. Anything whose file-ness is not
// positively known — a test's strings.Reader, an in-process pipe, a platform
// whose Stat says something else — is left exactly as it was, because "not
// provably a terminal" must never become "refuse the install".
func requireInteractiveStdin(s streams) error {
	f, ok := s.in.(*os.File)
	if !ok || isTerminal(f) {
		return nil
	}
	return errors.New("setup: stdin is not a terminal, so the interview has nobody to ask — this is what a piped install (`curl … | sh`) hits, and every question would fail at once. " +
		"Either run `vidra setup` from a real terminal, or pass --non-interactive with the answers as flags (or in an --answers file)")
}

// isTerminal reports whether f is a real terminal. It is the stdlib-only test —
// a character device — and it is the ONE primitive both the echo-disabling
// secret prompt and the refusal above are built on, so the two can never
// disagree about what a terminal is.
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// readSecretFlag resolves a secret without requiring it on the command line.
// argv is world-readable on a normal Linux box (`ps aux`, /proc/<pid>/cmdline)
// and ends up in the shell history of the operator running the install, so
// --smtp-password=hunter2 leaks the credential to every local account and to
// whoever reads ~/.bash_history next. The indirections cost one character each:
//
//	@path  read the value from a file (one line; the trailing newline is dropped)
//	-      read the value from stdin (needs --non-interactive: the interview
//	       reads stdin too, and consuming it would eat the answers)
//	unset  fall back to $envName, which is at least not in argv
//
// A literal value still works — it is the wrong default, not a forbidden one.
//
// The environment variable is looked up in secretFlagEnv rather than passed in,
// so "which flags are secrets" is one list this file and applyAnswersFile share.
func readSecretFlag(s streams, flagName, value string, nonInteractive bool, stdinTaken *bool) (string, error) {
	switch {
	case value == "":
		v, ok := os.LookupEnv(secretFlagEnv[flagName])
		if !ok {
			return "", nil
		}
		return strings.TrimRight(v, "\r\n"), nil
	case value == "-":
		if !nonInteractive {
			return "", fmt.Errorf("setup: --%s - reads the value from stdin, which the interview also reads — add --non-interactive, or pass @<file>", flagName)
		}
		if *stdinTaken {
			return "", fmt.Errorf("setup: only one flag can read stdin; --%s - is the second — pass the other as @<file>", flagName)
		}
		*stdinTaken = true
		b, err := io.ReadAll(s.in)
		if err != nil {
			return "", fmt.Errorf("setup: read --%s from stdin: %w", flagName, err)
		}
		return strings.TrimRight(string(b), "\r\n"), nil
	case strings.HasPrefix(value, "@"):
		path := value[1:]
		b, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("setup: read --%s from %s: %w", flagName, path, err)
		}
		return strings.TrimRight(string(b), "\r\n"), nil
	default:
		return value, nil
	}
}

// checkFile is `vidra setup --check`: run the api's boot validation over an env
// file that already exists and report per-variable. It is the precursor to
// `vidra doctor` (phase-1 item 14), which grows the host-level checks around
// this one.
func checkFile(s streams, path string) error {
	f, err := readEnvFile(path)
	if err != nil {
		return err
	}
	vars := f.Values()
	issues := setup.Check(vars)
	warnings := setup.Warnings(vars)
	if len(issues) == 0 {
		fmt.Fprintf(s.out, "✓ %s: %d variables checked, no problems\n", path, len(vars))
		printWarnings(s.out, warnings)
		return nil
	}
	fmt.Fprintf(s.out, "%s: %d variables checked\n\n", path, len(vars))
	printIssues(s.out, issues)
	printWarnings(s.out, warnings)
	return errReported
}

// printWarnings reports what the file would still do wrong after every problem
// above is fixed — a value compose rewrites on the way to the container. It does
// not change the exit code: the file boots.
func printWarnings(w io.Writer, warnings []string) {
	for _, warning := range warnings {
		fmt.Fprintf(w, "  ⚠ %s\n", warning)
	}
}

// printIssues renders one line per problem, attributed to the variable an
// operator has to fix (phase-1 item 14's output contract: never a raw Go error).
func printIssues(w io.Writer, issues []setup.Issue) {
	for _, is := range issues {
		if is.Var != "" {
			fmt.Fprintf(w, "  ✗ %s: %s\n", is.Var, is.Msg)
			continue
		}
		fmt.Fprintf(w, "  ✗ %s\n", is.Msg)
	}
	fmt.Fprintf(w, "\n%d problem(s)\n", len(issues))
}

func registrationAnswer(v string) (*setup.RegistrationAnswers, error) {
	switch v {
	case "closed":
		return &setup.RegistrationAnswers{Enabled: false}, nil
	case "open":
		return &setup.RegistrationAnswers{Enabled: true}, nil
	case "approval":
		return &setup.RegistrationAnswers{Enabled: true, RequireApproval: true}, nil
	default:
		return nil, fmt.Errorf("setup: unknown --registration %q (want closed|open|approval)", v)
	}
}

// outputPath defaults the destination to the template path without its .example
// suffix — env/production.env.example generates env/production.env, which is the
// path the Makefile, deploy.sh and the runbook all expect.
func outputPath(template, output string) (string, error) {
	if output != "" {
		return output, nil
	}
	if trimmed := strings.TrimSuffix(template, ".example"); trimmed != template {
		return trimmed, nil
	}
	return "", fmt.Errorf("setup: --output is required when the template path (%s) does not end in .example", template)
}

// samePath reports whether two path arguments name the same file. os.SameFile is
// asked first because it is the only answer that sees through a symlink or a
// bind mount — `--from /etc/vidra/production.env` really can be the output path
// under another name. When one of them does not exist yet (the ordinary first
// install), the comparison falls back to the resolved absolute paths, which
// still catches the everyday case: the same file spelled relatively.
func samePath(a, b string) bool {
	if a == b {
		return true
	}
	if fa, err := os.Stat(a); err == nil {
		if fb, err := os.Stat(b); err == nil {
			return os.SameFile(fa, fb)
		}
	}
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	return errA == nil && errB == nil && absA == absB
}

func readEnvFile(path string) (*setup.EnvFile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("setup: read %s: %w", path, err)
	}
	f, err := setup.ParseEnvFile(b)
	if err != nil {
		return nil, err
	}
	return f, nil
}

// interview fills in the answers that were not passed as flags. It is
// deliberately plain — one question per line, current value in brackets, empty
// input keeps it — because the wizard UX belongs to the web flow (phase-1 item
// 9); this path exists so an operator on a fresh box with a shell can finish.
func interview(s streams, tmpl, existing *setup.EnvFile, a *setup.Answers) error {
	r := bufio.NewReader(s.in)
	fmt.Fprint(s.out, "Answer the questions below; press enter to keep the value in brackets.\n\n")

	// TLS belongs with the domain: it is the same answer seen from the edge, and
	// the mode decides whether an ACME order goes out the moment the stack comes
	// up. Asking it here is also what keeps the operator who has no DNS yet from
	// finding out by burning a Let's Encrypt rate limit.
	//
	// It is asked BEFORE the domain, and that order is load-bearing now that
	// plain-http exists: the mode decides which SCHEME a domain answer is
	// normalised to, so asking for the domain first would reject `video.lan` from
	// the one operator whose deployment is allowed to use it.
	if a.TLSMode == "" {
		def := effective(tmpl, existing, "VIDRA_TLS_MODE")
		if def == "" {
			def = setup.TLSModeACME
		}
		// plain-http is worded so nobody arrives at it by accident: it is last, it
		// says what it costs, and it names the audience.
		v, err := askValid(s, r, "TLS (acme = Let's Encrypt, acme-staging = untrusted rehearsal, internal = private CA, external = your own proxy terminates, plain-http = NO TLS, LAN/lab only — logins travel in clear)", def, func(in string) error {
			_, err := setup.NormalizeTLSMode(in)
			return err
		})
		if err != nil {
			return err
		}
		a.TLSMode = v
	}
	if a.Domain == "" {
		// Deliberately no default from the template: its PUBLIC_BASE_URL is an
		// example, and accepting it would generate a file for example.com.
		def, _ := existingValue(existing, "PUBLIC_BASE_URL")
		label := "Public domain of this instance (e.g. video.example.org)"
		if a.TLSMode == setup.TLSModePlainHTTP {
			// No PORT in the example, deliberately: the managed caddy publishes 80
			// and 443 only, so an operator who copied the shape would deploy an
			// instance that comes up healthy and answers nowhere.
			label = "Address of this instance (e.g. video.lan or 192.168.1.10 — it will be an http:// origin)"
		}
		v, err := askValid(s, r, label, def, func(in string) error {
			_, err := setup.NormalizeOriginForMode(in, a.TLSMode)
			return err
		})
		if err != nil {
			return err
		}
		a.Domain = v
	}
	// One line about whether that domain points here YET. It is feedback, never a
	// gate: see reportDomainDNS. plain-http instances are LAN/lab by definition —
	// the name may resolve only on an internal resolver, or be an IP — and
	// external ones point at somebody else's terminator, so neither is asked
	// about: a ⚠ that is expected teaches an operator to ignore the line.
	if a.Domain != "" && a.TLSMode != setup.TLSModePlainHTTP && a.TLSMode != setup.TLSModeExternal {
		reportDomainDNS(s, a.Domain)
	}
	if a.AcmeEmail == "" && (a.TLSMode == setup.TLSModeACME || a.TLSMode == setup.TLSModeACMEStaging) {
		// Optional, and the prompt says so: an operator with no address to give
		// presses enter and the install continues with the ⚠ the report prints.
		v, err := askValid(s, r, "Contact address for Let's Encrypt expiry notices (optional, enter to skip)",
			effective(tmpl, existing, "VIDRA_ACME_EMAIL"), setup.CheckAcmeEmail)
		if err != nil {
			return err
		}
		a.AcmeEmail = v
	}
	if a.InstanceName == "" {
		// Asked because nothing else can catch it: the template's "Example Video"
		// is a plausible value rather than a <...> placeholder, so Check waves it
		// through and it ships — to /api/v1/instance, to NodeInfo, and into every
		// user's authenticator app as the TOTP issuer label.
		v, err := ask(s, r, "Name of this instance (shown publicly, and as the TOTP issuer)", effective(tmpl, existing, "INSTANCE_NAME"))
		if err != nil {
			return err
		}
		a.InstanceName = v
	}
	if a.ReleaseTag == "" && a.CoreTag == "" && a.UserTag == "" && a.SearchTag == "" {
		v, err := ask(s, r, "Release tag to deploy", effective(tmpl, existing, "VIDRA_CORE_TAG"))
		if err != nil {
			return err
		}
		a.ReleaseTag = v
	}
	if a.StorageBackend == "" {
		def := effective(tmpl, existing, "STORAGE_BACKEND")
		// A DEFAULT THAT CANNOT PASS Check IS NOT A DEFAULT. The shipped template
		// says s3 next to <your Spaces access key> placeholders, so an operator
		// pressing enter through the interview chose a backend with no
		// credentials — and the run then refused to write anything, after every
		// other question had been answered. The template's answer is still the
		// RECOMMENDATION, so it stands the moment there are real keys behind it;
		// what changes is which one is offered to somebody who has not got them.
		if def == "s3" && !s3CredentialsAnswered(tmpl, existing) {
			def = "local"
		}
		v, err := ask(s, r, "Media storage backend (local|s3)", def)
		if err != nil {
			return err
		}
		a.StorageBackend = v
	}
	if a.StorageBackend == "s3" {
		for _, q := range []struct {
			label  string
			key    string
			field  *string
			secret bool
		}{
			{label: "S3 endpoint host (no scheme)", key: "STORAGE_S3_ENDPOINT", field: &a.S3.Endpoint},
			{label: "S3 region", key: "STORAGE_S3_REGION", field: &a.S3.Region},
			{label: "S3 bucket", key: "STORAGE_S3_BUCKET", field: &a.S3.Bucket},
			{label: "S3 access key", key: "STORAGE_S3_ACCESS_KEY", field: &a.S3.AccessKey},
			{label: "S3 secret key", key: "STORAGE_S3_SECRET_KEY", field: &a.S3.SecretKey, secret: true},
		} {
			if *q.field != "" {
				continue
			}
			v, err := askMaybeSecret(s, r, q.label, effective(tmpl, existing, q.key), q.secret)
			if err != nil {
				return err
			}
			*q.field = v
		}
	}
	// The datastores come before the optional components: they decide whether the
	// bundled containers start at all, and an external answer with no connection
	// string is the one combination the engine refuses to write.
	for _, q := range []struct {
		label, urlLabel string
		flagKey, urlKey string
		mode, url       *string
	}{
		{"Use an external/managed PostgreSQL instead of the bundled one", "Managed PostgreSQL connection string",
			"VIDRA_EXTERNAL_POSTGRES", "DATABASE_URL", &a.Database.Mode, &a.Database.URL},
		{"Use an external/managed Redis instead of the bundled one", "Managed Redis connection string",
			"VIDRA_EXTERNAL_REDIS", "REDIS_URL", &a.Redis.Mode, &a.Redis.URL},
	} {
		if *q.mode != "" {
			continue
		}
		external, err := askYesNo(s, r, q.label, boolValue(effective(tmpl, existing, q.flagKey), false))
		if err != nil {
			return err
		}
		*q.mode = "local"
		if !external {
			continue
		}
		*q.mode = "external"
		if *q.url != "" {
			continue
		}
		// The connection string carries the password: hidden input, and the
		// current one offered masked so enter keeps it without printing it.
		v, err := askMaybeSecret(s, r, q.urlLabel, effective(tmpl, existing, q.urlKey), true)
		if err != nil {
			return err
		}
		*q.url = v
	}
	// The optional components sit behind ONE gate, like SMTP: they are off on a
	// normal install, and an operator pressing enter through a re-run should not
	// be walked through five questions to keep what is already running — a.Features
	// arrives seeded from the env file's profile list, so declining changes
	// nothing. Saying yes is how a component gets turned off again, since each
	// question defaults to what is running today.
	if on, err := askYesNo(s, r, "Configure optional components (virus scanning, captions, live streaming, tracing, IPFS)", a.Features != (setup.FeatureAnswers{})); err != nil {
		return err
	} else if on {
		for _, q := range []struct {
			label string
			field *bool
		}{
			{"Run the bundled ClamAV upload scanner (profile scan; MALWARE_SCAN_ENABLED stays a separate setting)", &a.Features.Scan},
			{"Run the bundled Whisper caption worker (profile captions)", &a.Features.Captions},
			{"Run the bundled RTMP live-ingest server (profile media)", &a.Features.Media},
			{"Run the bundled OpenTelemetry collector and Jaeger (profile otel)", &a.Features.Otel},
			{"Run the bundled IPFS node (profile ipfs)", &a.Features.IPFS},
		} {
			v, err := askYesNo(s, r, q.label, *q.field)
			if err != nil {
				return err
			}
			*q.field = v
		}
	}
	if a.Mail == nil {
		// Already-working SMTP defaults to yes, so an operator pressing enter
		// through a re-run is not offered "no" for something that is on. The
		// TEMPLATE's MAIL_ENABLED=true does not count: it ships with a blank
		// SMTP_HOST, which is a question rather than a configuration.
		configured := boolValue(effective(tmpl, existing, "MAIL_ENABLED"), false) && effective(tmpl, existing, "SMTP_HOST") != ""
		on, err := askYesNo(s, r, "Configure SMTP now (password reset and email verification need it)", configured)
		if err != nil {
			return err
		}
		if on {
			m := &setup.MailAnswers{}
			for _, q := range []struct {
				label  string
				key    string
				def    string
				field  *string
				secret bool
			}{
				{label: "SMTP host", key: "SMTP_HOST", field: &m.Host},
				{label: "SMTP port", key: "SMTP_PORT", def: "587", field: &m.Port},
				{label: "SMTP username", key: "SMTP_USERNAME", field: &m.Username},
				{label: "SMTP password", key: "SMTP_PASSWORD", field: &m.Password, secret: true},
				{label: "From address", key: "SMTP_FROM", field: &m.From},
			} {
				def := effective(tmpl, existing, q.key)
				if def == "" {
					def = q.def
				}
				v, err := askMaybeSecret(s, r, q.label, def, q.secret)
				if err != nil {
					return err
				}
				*q.field = v
			}
			a.Mail = m
		}
	}
	if a.Registration == nil {
		// Seeded from the current policy, like every other prompt: the interview
		// runs on re-deploys too, and a hard-coded "closed" default silently
		// closed signups on an open instance whose operator pressed enter. The
		// TEMPLATE's value is the seed on a first install, where closed is the
		// safe boot anyway — bring the stack up, claim the owner account with the
		// one-time token, then re-run with registration open.
		open, err := askYesNo(s, r, "Open registration to the public now", boolValue(effective(tmpl, existing, "REGISTRATION_ENABLED"), false))
		if err != nil {
			return err
		}
		approval := boolValue(effective(tmpl, existing, "REGISTRATION_REQUIRE_APPROVAL"), false)
		reg := &setup.RegistrationAnswers{Enabled: open, RequireApproval: approval}
		if open {
			reg.RequireApproval, err = askYesNo(s, r, "Require an admin to approve each signup", approval)
			if err != nil {
				return err
			}
		}
		a.Registration = reg
	}
	fmt.Fprintln(s.out)
	return nil
}

// askMaybeSecret asks for a value, hiding the typing when it is a secret AND the
// terminal can be told to stop echoing. A shoulder-surfed SMTP password is the
// small half of the problem: the value also lands in a terminal scrollback, in
// `screen`/`tmux` capture files and in whatever records the install session.
//
// When echo cannot be disabled — piped stdin, a CI job, no stty — the question
// is asked anyway and SAYS SO, because refusing would break unattended installs
// and silently echoing would be the bug this fixes. The current value is offered
// as a default like everywhere else; for a secret it is shown masked, so an
// operator can press enter to keep it without it being displayed.
func askMaybeSecret(s streams, r *bufio.Reader, label, def string, secret bool) (string, error) {
	if !secret {
		return ask(s, r, label, def)
	}
	restore, hidden := disableEcho(s)
	if !hidden {
		return askWithShownDefault(s, r, label+" (input is echoed)", def, mask(def))
	}
	defer restore()
	v, err := askWithShownDefault(s, r, label+" (input is hidden)", def, mask(def))
	// The operator's own newline was not echoed either, so the next prompt would
	// otherwise land on the same line.
	fmt.Fprintln(s.out)
	return v, err
}

// mask renders a value's PRESENCE without its content, and deliberately not as
// something an operator could mistake for a value to type back.
func mask(v string) string {
	if v == "" {
		return ""
	}
	return "set; enter keeps it"
}

// disableEcho turns terminal echo off, returning a restore func and whether it
// worked. stty is used rather than a termios binding to keep this a stdlib-only
// binary: the CLI is fetched by the installer onto a bare host, and a new module
// dependency for one ioctl is not worth it. Anything that is not a real
// terminal — a pipe, a test's strings.Reader, Windows — fails the check and is
// handled by the caller.
func disableEcho(s streams) (func(), bool) {
	f, ok := s.in.(*os.File)
	if !ok || !isTerminal(f) {
		return nil, false
	}
	if err := stty(f, "-echo"); err != nil {
		return nil, false
	}
	return func() { _ = stty(f, "echo") }, true
}

func stty(tty *os.File, arg string) error {
	cmd := exec.Command("stty", arg)
	cmd.Stdin = tty
	return cmd.Run()
}

func ask(s streams, r *bufio.Reader, label, def string) (string, error) {
	return askWithShownDefault(s, r, label, def, def)
}

// askValid asks until the answer is one the ENGINE will accept, using the
// engine's own validator rather than a friendlier second opinion.
//
// A mistyped domain used to cost the whole interview: it was discovered by
// Generate, after every other question had been answered, and the run exited
// having written nothing and with no way back to the answers. Here it costs one
// line. There is no attempt limit — the loop ends when the answer is valid or
// when stdin does, which is the same EOF error every other prompt returns.
func askValid(s streams, r *bufio.Reader, label, def string, valid func(string) error) (string, error) {
	for {
		v, err := ask(s, r, label, def)
		if err != nil {
			return "", err
		}
		if verr := valid(v); verr != nil {
			// The engine's message, minus its package prefix: it is already
			// addressed to an operator and says what to write instead.
			fmt.Fprintf(s.out, "  ✗ %s\n", strings.TrimPrefix(verr.Error(), "setup: "))
			continue
		}
		return v, nil
	}
}

// domainCheckTimeout bounds the whole DNS feedback. Short on purpose: an
// operator is watching a prompt, and this is a nicety, not a dependency.
const domainCheckTimeout = 5 * time.Second

// checkDomain is preflight's domain check, indirected so the tests can answer
// without a nameserver or an HTTP round trip — a suite that reaches the network
// to test the network is a suite nobody runs (preflight's own doctrine).
var checkDomain = preflight.CheckDomain

// reportDomainDNS prints ONE line about whether the domain resolves here yet.
//
// It is FEEDBACK, NEVER A GATE, and the distinction is the whole design. DNS
// that does not point here yet is a completely ordinary state of a fresh
// install — it is what VIDRA_TLS_MODE=internal exists for, and the operator may
// well be about to create the record. Refusing here would refuse the install
// that the next question already has an answer for. And a check that cannot
// COMPLETE (no outbound HTTPS, so no public IP to compare against) is not a
// check that failed, so it reads as ⚠ and not as ✗ (internal/preflight's
// package doc).
func reportDomainDNS(s streams, domain string) {
	ctx, cancel := context.WithTimeout(context.Background(), domainCheckTimeout)
	defer cancel()
	res := checkDomain(ctx, preflight.DomainRequest{
		Domain: domain,
		// Half the budget per endpoint, so both can be asked inside it.
		PublicIP: preflight.PublicIPOptions{Timeout: domainCheckTimeout / 2},
	})
	if res.Status == preflight.StatusOK {
		fmt.Fprintf(s.out, "  ✓ %s\n", res.Message)
		return
	}
	fmt.Fprintf(s.out, "  ⚠ %s — not a blocker: TLS mode %s issues from Caddy's own CA until DNS points here\n", res.Message, setup.TLSModeInternal)
}

// askWithShownDefault separates the default's VALUE from how it is displayed, so
// a secret can be kept by pressing enter without being printed.
func askWithShownDefault(s streams, r *bufio.Reader, label, def, shown string) (string, error) {
	if shown != "" {
		fmt.Fprintf(s.out, "%s [%s]: ", label, shown)
	} else {
		fmt.Fprintf(s.out, "%s: ", label)
	}
	line, err := r.ReadString('\n')
	if err != nil && strings.TrimSpace(line) == "" {
		return "", fmt.Errorf("setup: no answer for %q (stdin ended) — for an unattended install pass --non-interactive with the answers as flags", label)
	}
	if v := strings.TrimSpace(line); v != "" {
		return v, nil
	}
	return def, nil
}

func askYesNo(s streams, r *bufio.Reader, label string, def bool) (bool, error) {
	hint := "y/N"
	if def {
		hint = "Y/n"
	}
	fmt.Fprintf(s.out, "%s? [%s]: ", label, hint)
	line, err := r.ReadString('\n')
	if err != nil && strings.TrimSpace(line) == "" {
		return false, fmt.Errorf("setup: no answer for %q (stdin ended) — for an unattended install pass --non-interactive with the answers as flags", label)
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "":
		return def, nil
	case "y", "yes":
		return true, nil
	case "n", "no":
		return false, nil
	default:
		return false, fmt.Errorf("setup: answer %q for %q is neither yes nor no", strings.TrimSpace(line), label)
	}
}

// boolValue reads an env-file boolean, falling back to def for anything that is
// not one — a prompt's default is never worth failing an install over.
func boolValue(v string, def bool) bool {
	b, err := strconv.ParseBool(strings.TrimSpace(v))
	if err != nil {
		return def
	}
	return b
}

// s3CredentialsAnswered and effective are the ENGINE's rules, called rather than
// restated. They used to live here, back when the interview was the only front
// end that needed them; `vidra setup --web` is the second, and a seed rule with
// two implementations is one where the wizard proposes a default the interview
// refuses (or the other way round). See setup.S3CredentialsAnswered and
// setup.Effective for what each one decides and why.
func s3CredentialsAnswered(tmpl, existing *setup.EnvFile) bool {
	return setup.S3CredentialsAnswered(tmpl, existing)
}

func effective(tmpl, existing *setup.EnvFile, key string) string {
	return setup.Effective(tmpl, existing, key)
}

func existingValue(existing *setup.EnvFile, key string) (string, bool) {
	if existing == nil {
		return "", false
	}
	v, ok := existing.Value(key)
	if !ok || strings.TrimSpace(v) == "" || setup.IsPlaceholder(v) {
		return "", false
	}
	return strings.TrimSpace(v), true
}

// report prints what happened, by variable NAME only — a generated secret's
// value belongs in the file and nowhere else, least of all a terminal scrollback
// or a CI log.
func report(s streams, path, caddyPath, nginxPath string, sources []string, res *setup.Result) {
	fmt.Fprintf(s.out, "✓ wrote %s (mode 0600)\n", path)
	if caddyPath != "" {
		mode := strings.TrimSpace(res.Values["VIDRA_TLS_MODE"])
		if mode == "" {
			mode = setup.TLSModeACME
		}
		fmt.Fprintf(s.out, "✓ wrote %s (mode 0644, TLS %s) — the caddy service bind-mounts it; a running deployment needs `caddy reload` to pick it up\n", caddyPath, mode)
	}
	if nginxPath != "" {
		// Said in the same breath as the file, because the ONE thing that goes
		// wrong here is an operator treating a .example as a deployed file and
		// wondering why editing it changes nothing.
		fmt.Fprintf(s.out, "✓ wrote %s (mode 0644) — an EXAMPLE for your own proxy, mounted by nothing. Copy it into that proxy's configuration, fill in the two TLS lines, and reload it; the managed caddy does not run in VIDRA_TLS_MODE=%s\n",
			nginxPath, setup.TLSModeExternal)
	}
	if len(sources) > 0 {
		// Named, and in precedence order, because "which file won" is the
		// question an operator merging two env files actually has.
		fmt.Fprintf(s.out, "  preserved values from (first wins): %s\n", strings.Join(sources, ", "))
	}
	for _, line := range []struct {
		label string
		vars  []string
	}{
		{"generated secrets", res.Generated},
		{"preserved secrets", res.Preserved},
		{"rotated secrets", res.Rotated},
		{"carried over from the previous file", res.Carried},
	} {
		if len(line.vars) == 0 {
			continue
		}
		vars := append([]string(nil), line.vars...)
		sort.Strings(vars)
		fmt.Fprintf(s.out, "  %s: %s\n", line.label, strings.Join(vars, ", "))
	}
	for _, w := range res.Warnings {
		fmt.Fprintf(s.out, "  ⚠ %s\n", w)
	}
	fmt.Fprintf(s.out, "\nNext, render the production compose chain with it (from the deployment directory):\n  %s\n", setup.RenderCheckCommand(path, res.Values))

	// The admin account is NOT in this file and cannot be. The api mints a
	// one-time owner-claim token at boot and prints it to its own log, once; until
	// it is redeemed every signup path answers 403 owner_claim_required. So the
	// operator who has just generated an env file is two commands away from an
	// instance they cannot log into, and this is the only place that tells them
	// which two. `./deploy/compose.sh logs api` and never a bare `docker compose
	// logs api`: on a deployment host the bare form silently picks up
	// docker-compose.override.yml's dev defaults and addresses a different
	// project than the deploy scripts do.
	origin := strings.TrimSpace(res.Values["PUBLIC_BASE_URL"])
	if origin == "" {
		origin = "https://<your domain>"
	}
	fmt.Fprintf(s.out, `
Then, once the stack is up, claim the admin account. The api mints a one-time
owner-claim token at boot and prints it in its own log:
  ./deploy/compose.sh logs api
  %s/setup/claim
Every api restart mints a fresh token and invalidates the previous one, so take
the newest line in the log.
`, origin)
}

// featureFlags is the optional-component block of the command line: one flag per
// compose profile the engine knows how to enable.
type featureFlags struct {
	scan     optionalBool
	captions optionalBool
	media    optionalBool
	otel     optionalBool
	ipfs     optionalBool
}

func (f *featureFlags) register(fs *flag.FlagSet) {
	for _, e := range f.entries() {
		fs.Var(e.flag, e.name, e.usage)
	}
}

// applyTo overlays the flags that were PASSED onto answers already seeded from
// the env file, leaving the rest alone.
func (f *featureFlags) applyTo(a *setup.FeatureAnswers) {
	f.scan.applyTo(&a.Scan)
	f.captions.applyTo(&a.Captions)
	f.media.applyTo(&a.Media)
	f.otel.applyTo(&a.Otel)
	f.ipfs.applyTo(&a.IPFS)
}

func (f *featureFlags) entries() []struct {
	name, usage string
	flag        *optionalBool
} {
	return []struct {
		name, usage string
		flag        *optionalBool
	}{
		{"scan", "run the bundled ClamAV upload scanner (compose profile scan; MALWARE_SCAN_ENABLED is a separate setting)", &f.scan},
		{"captions", "run the bundled Whisper caption worker (compose profile captions)", &f.captions},
		{"media", "run the bundled RTMP live-ingest server (compose profile media)", &f.media},
		{"otel", "run the bundled OpenTelemetry collector and Jaeger (compose profile otel)", &f.otel},
		{"ipfs", "run the bundled IPFS node (compose profile ipfs)", &f.ipfs},
	}
}

// optionalBool is a bool flag that remembers whether it was passed at all. The
// distinction is the whole preservation story for the profile list: a plain bool
// cannot say "leave it as it is", so `vidra setup --domain video.example.org` on
// an instance running ClamAV would answer "scanning: no" without the operator
// ever being asked. Unset means keep the env file's profile list; --scan and
// --scan=false are the two ways to change it.
type optionalBool struct {
	set   bool
	value bool
}

func (o *optionalBool) String() string {
	if o == nil || !o.set {
		return "false"
	}
	return strconv.FormatBool(o.value)
}

func (o *optionalBool) Set(v string) error {
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fmt.Errorf("%q is neither true nor false", v)
	}
	o.set, o.value = true, b
	return nil
}

// IsBoolFlag lets the flag package accept the bare `--scan` form (and keeps
// `--scan=false` working); without it, `--scan` would swallow the NEXT argument.
func (o *optionalBool) IsBoolFlag() bool { return true }

func (o *optionalBool) applyTo(dst *bool) {
	if o.set {
		*dst = o.value
	}
}

// stringList collects a repeatable flag.
type stringList []string

func (l *stringList) String() string { return strings.Join(*l, ",") }

func (l *stringList) Set(v string) error {
	*l = append(*l, v)
	return nil
}
