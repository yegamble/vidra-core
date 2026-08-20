package main

import (
	"bufio"
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

	"github.com/vidra/vidra-core/internal/setup"
)

// runSetup is `vidra setup`: generate (or re-generate, or check) the env file a
// production deployment runs on. The engine lives in internal/setup — this file
// is argument plumbing, prompts and printing, so the web wizard and the
// installer can reach the same behaviour without going through a terminal.
func runSetup(s streams, args []string) error {
	fs := flag.NewFlagSet("vidra setup", flag.ContinueOnError)
	fs.SetOutput(s.err)

	var (
		checkPath = fs.String("check", "", "validate an existing env `file` and exit (no file is written)")
		template  = fs.String("template", "", "`path` to the deployment template, e.g. env/production.env.example (required)")
		output    = fs.String("output", "", "`path` to write (default: the template path without its .example suffix)")
		from      = fs.String("from", "", "extra env `file` to merge: it fills keys the output file leaves blank, and never overrides it")

		nonInteractive = fs.Bool("non-interactive", false, "never prompt; take every answer from flags (for unattended installs)")
		yes            = fs.Bool("yes", false, "rewrite an existing --output in place (every value it sets is still preserved)")
		// Deliberately NOT --yes: an operator confirming the routine in-place
		// rewrite has not agreed to orphan every secret a KEK seals, and one flag
		// for both would mean an everyday `--yes` silently pre-authorised a
		// `--rotate MFA_KEY_KEK` in the same command line. The spelling matches the
		// `api migrate force --yes-i-know` gate, which guards the same class of
		// unrecoverable action.
		yesIKnow = fs.Bool("yes-i-know", false, "confirm a DESTRUCTIVE *_KEK rotation: it orphans the data already sealed under that key")

		domain     = fs.String("domain", "", "public origin of the instance, e.g. video.example.org (https is assumed)")
		releaseTag = fs.String("release-tag", "", "image `tag` to deploy for all three services, e.g. v0.1.1")
		coreTag    = fs.String("core-tag", "", "override the api image `tag`")
		userTag    = fs.String("user-tag", "", "override the frontend image `tag`")
		searchTag  = fs.String("search-tag", "", "override the search image `tag`")

		storage     = fs.String("storage", "", "media storage backend: `local|s3`")
		s3Endpoint  = fs.String("s3-endpoint", "", "S3 endpoint `host` WITHOUT a scheme, e.g. nyc3.digitaloceanspaces.com")
		s3Region    = fs.String("s3-region", "", "S3 `region`, matching the endpoint")
		s3Bucket    = fs.String("s3-bucket", "", "S3 `bucket` (pre-create it)")
		s3AccessKey = fs.String("s3-access-key", "", "S3 access `key`")
		s3SecretKey = fs.String("s3-secret-key", "", "S3 secret key: `@file`, - for stdin, or the value itself (also $VIDRA_SETUP_S3_SECRET_KEY)")

		smtpHost     = fs.String("smtp-host", "", "SMTP relay `host` (enables mail)")
		smtpPort     = fs.String("smtp-port", "", "SMTP `port` (587 with STARTTLS is the usual answer)")
		smtpUsername = fs.String("smtp-username", "", "SMTP `username`")
		smtpPassword = fs.String("smtp-password", "", "SMTP password: `@file`, - for stdin, or the value itself (also $VIDRA_SETUP_SMTP_PASSWORD)")
		smtpFrom     = fs.String("smtp-from", "", "From `address` for outbound mail")

		registration = fs.String("registration", "", "signup policy: `closed|open|approval`")
	)
	var rotate stringList
	fs.Var(&rotate, "rotate", "re-generate the secret in this `VAR` even though it already has a value (repeatable)")

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

Secrets do not have to appear on the command line: --s3-secret-key and
--smtp-password accept @path (read the file), - (read stdin, with
--non-interactive) or a VIDRA_SETUP_* environment variable, and the interactive
prompts read them without echoing.

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

	if *checkPath != "" {
		return checkFile(s, *checkPath)
	}
	if *template == "" {
		usage(s.err)
		return errors.New("setup: --template is required (the deployment template, env/production.env.example, is the input format)")
	}

	outPath, err := outputPath(*template, *output)
	if err != nil {
		return err
	}
	tmpl, err := readEnvFile(*template)
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
	fromPath := *from
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
	if outExists && fromPath == "" && !*yes {
		return fmt.Errorf("setup: %s already exists and is the file a running deployment reads — re-run with --yes to rewrite it in place, "+
			"or with --from <other env file> to merge that file into it as well (either way every value %s already sets, including the KEKs, is preserved), "+
			"or pass a different --output", outPath, outPath)
	}
	existing := setup.MergeSources(sources...)

	stdinTaken := false
	s3Secret, err := readSecretFlag(s, "s3-secret-key", "VIDRA_SETUP_S3_SECRET_KEY", *s3SecretKey, *nonInteractive, &stdinTaken)
	if err != nil {
		return err
	}
	mailPassword, err := readSecretFlag(s, "smtp-password", "VIDRA_SETUP_SMTP_PASSWORD", *smtpPassword, *nonInteractive, &stdinTaken)
	if err != nil {
		return err
	}

	answers := setup.Answers{
		Domain:         *domain,
		ReleaseTag:     *releaseTag,
		CoreTag:        *coreTag,
		UserTag:        *userTag,
		SearchTag:      *searchTag,
		StorageBackend: *storage,
		S3: setup.S3Answers{
			Endpoint:  *s3Endpoint,
			Region:    *s3Region,
			Bucket:    *s3Bucket,
			AccessKey: *s3AccessKey,
			SecretKey: s3Secret,
		},
	}
	if *smtpHost != "" || *smtpFrom != "" || *smtpUsername != "" || mailPassword != "" || *smtpPort != "" {
		answers.Mail = &setup.MailAnswers{
			Host:     *smtpHost,
			Port:     *smtpPort,
			Username: *smtpUsername,
			Password: mailPassword,
			From:     *smtpFrom,
		}
	}
	if *registration != "" {
		reg, rerr := registrationAnswer(*registration)
		if rerr != nil {
			return rerr
		}
		answers.Registration = reg
	}
	if !*nonInteractive {
		if err := interview(s, tmpl, existing, &answers); err != nil {
			return err
		}
	}

	res, err := setup.Generate(setup.Request{
		Template:           tmpl,
		Existing:           existing,
		Answers:            answers,
		Rotate:             rotate,
		ConfirmDestructive: *yesIKnow,
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
	if err := setup.WriteFile(outPath, res.Content); err != nil {
		return err
	}
	report(s, outPath, sourcePaths, res)
	return nil
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
func readSecretFlag(s streams, flagName, envName, value string, nonInteractive bool, stdinTaken *bool) (string, error) {
	switch {
	case value == "":
		v, ok := os.LookupEnv(envName)
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

	if a.Domain == "" {
		// Deliberately no default from the template: its PUBLIC_BASE_URL is an
		// example, and accepting it would generate a file for example.com.
		def, _ := existingValue(existing, "PUBLIC_BASE_URL")
		v, err := ask(s, r, "Public domain of this instance (e.g. video.example.org)", def)
		if err != nil {
			return err
		}
		a.Domain = v
	}
	if a.ReleaseTag == "" && a.CoreTag == "" && a.UserTag == "" && a.SearchTag == "" {
		v, err := ask(s, r, "Release tag to deploy", effective(tmpl, existing, "VIDRA_CORE_TAG"))
		if err != nil {
			return err
		}
		a.ReleaseTag = v
	}
	if a.StorageBackend == "" {
		v, err := ask(s, r, "Media storage backend (local|s3)", effective(tmpl, existing, "STORAGE_BACKEND"))
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
	if !ok {
		return nil, false
	}
	info, err := f.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
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

// effective is the value an operator would see today: the existing file's, else
// the template's — and never a placeholder, which is a question, not an answer.
func effective(tmpl, existing *setup.EnvFile, key string) string {
	if v, ok := existingValue(existing, key); ok {
		return v
	}
	if tmpl != nil {
		if v, ok := tmpl.Value(key); ok && !setup.IsPlaceholder(v) {
			return strings.TrimSpace(v)
		}
	}
	return ""
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
func report(s streams, path string, sources []string, res *setup.Result) {
	fmt.Fprintf(s.out, "✓ wrote %s (mode 0600)\n", path)
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
}

// stringList collects a repeatable flag.
type stringList []string

func (l *stringList) String() string { return strings.Join(*l, ",") }

func (l *stringList) Set(v string) error {
	*l = append(*l, v)
	return nil
}
