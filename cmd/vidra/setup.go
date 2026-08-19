package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
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
		from      = fs.String("from", "", "existing env `file` to merge: every value it sets is preserved")

		nonInteractive = fs.Bool("non-interactive", false, "never prompt; take every answer from flags (for unattended installs)")
		yes            = fs.Bool("yes", false, "confirm destructive actions (required to rotate a *_KEK)")

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
		s3SecretKey = fs.String("s3-secret-key", "", "S3 secret `key`")

		smtpHost     = fs.String("smtp-host", "", "SMTP relay `host` (enables mail)")
		smtpPort     = fs.String("smtp-port", "", "SMTP `port` (587 with STARTTLS is the usual answer)")
		smtpUsername = fs.String("smtp-username", "", "SMTP `username`")
		smtpPassword = fs.String("smtp-password", "", "SMTP `password`")
		smtpFrom     = fs.String("smtp-from", "", "From `address` for outbound mail")

		registration = fs.String("registration", "", "signup policy: `closed|open|approval`")
	)
	var rotate stringList
	fs.Var(&rotate, "rotate", "re-generate the secret in this `VAR` even though it already has a value (repeatable)")

	fs.Usage = func() {
		fmt.Fprint(s.err, `usage: vidra setup --template env/production.env.example [flags]
       vidra setup --check env/production.env

Generates the production env file from the deployment template, filling in the
answers below and minting every secret the template leaves blank. Comments and
ordering are preserved, so the generated file stays as self-documenting as the
template it came from, and it is written with mode 0600.

Re-running is safe: pass --from <existing file> and every value it already sets
is preserved. A secret is only ever replaced when --rotate names it, and
rotating a *_KEK additionally needs --yes because it orphans data already sealed
in the database.

flags:
`)
		fs.PrintDefaults()
		fmt.Fprint(s.err, "\nrotatable secrets: "+strings.Join(setup.SecretVars(), ", ")+"\n")
	}
	if err := fs.Parse(args); err != nil {
		return errReported
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(s.err, "vidra setup: unexpected argument %q\n\n", fs.Arg(0))
		fs.Usage()
		return errReported
	}

	if *checkPath != "" {
		return checkFile(s, *checkPath)
	}
	if *template == "" {
		fs.Usage()
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

	fromPath := *from
	if fromPath == "" {
		// Refuse rather than overwrite. The existing file holds the KEKs that
		// seal data in the database; regenerating it from scratch would mint new
		// ones, and the operator would find out when TOTP stopped working.
		if _, statErr := os.Stat(outPath); statErr == nil {
			return fmt.Errorf("setup: %s already exists — re-run with --from %s to merge into it (every existing value, including the KEKs, is preserved), or pass a different --output", outPath, outPath)
		}
	}
	var existing *setup.EnvFile
	if fromPath != "" {
		if existing, err = readEnvFile(fromPath); err != nil {
			return err
		}
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
			SecretKey: *s3SecretKey,
		},
	}
	if *smtpHost != "" || *smtpFrom != "" || *smtpUsername != "" || *smtpPassword != "" || *smtpPort != "" {
		answers.Mail = &setup.MailAnswers{
			Host:     *smtpHost,
			Port:     *smtpPort,
			Username: *smtpUsername,
			Password: *smtpPassword,
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
		ConfirmDestructive: *yes,
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
	report(s, outPath, res)
	return nil
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
	if len(issues) == 0 {
		fmt.Fprintf(s.out, "✓ %s: %d variables checked, no problems\n", path, len(vars))
		return nil
	}
	fmt.Fprintf(s.out, "%s: %d variables checked\n\n", path, len(vars))
	printIssues(s.out, issues)
	return errReported
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
			label string
			key   string
			field *string
		}{
			{"S3 endpoint host (no scheme)", "STORAGE_S3_ENDPOINT", &a.S3.Endpoint},
			{"S3 region", "STORAGE_S3_REGION", &a.S3.Region},
			{"S3 bucket", "STORAGE_S3_BUCKET", &a.S3.Bucket},
			{"S3 access key", "STORAGE_S3_ACCESS_KEY", &a.S3.AccessKey},
			{"S3 secret key (input is echoed)", "STORAGE_S3_SECRET_KEY", &a.S3.SecretKey},
		} {
			if *q.field != "" {
				continue
			}
			v, err := ask(s, r, q.label, effective(tmpl, existing, q.key))
			if err != nil {
				return err
			}
			*q.field = v
		}
	}
	if a.Mail == nil {
		on, err := askYesNo(s, r, "Configure SMTP now (password reset and email verification need it)", false)
		if err != nil {
			return err
		}
		if on {
			m := &setup.MailAnswers{}
			for _, q := range []struct {
				label string
				key   string
				def   string
				field *string
			}{
				{"SMTP host", "SMTP_HOST", "", &m.Host},
				{"SMTP port", "SMTP_PORT", "587", &m.Port},
				{"SMTP username", "SMTP_USERNAME", "", &m.Username},
				{"SMTP password (input is echoed)", "SMTP_PASSWORD", "", &m.Password},
				{"From address", "SMTP_FROM", "", &m.From},
			} {
				def := effective(tmpl, existing, q.key)
				if def == "" {
					def = q.def
				}
				v, err := ask(s, r, q.label, def)
				if err != nil {
					return err
				}
				*q.field = v
			}
			a.Mail = m
		}
	}
	if a.Registration == nil {
		// The safe first boot is closed: bring the stack up, claim the owner
		// account with the one-time token, then re-run with registration open.
		open, err := askYesNo(s, r, "Open registration to the public now", false)
		if err != nil {
			return err
		}
		reg := &setup.RegistrationAnswers{Enabled: open}
		if open {
			approval, err := askYesNo(s, r, "Require an admin to approve each signup", false)
			if err != nil {
				return err
			}
			reg.RequireApproval = approval
		}
		a.Registration = reg
	}
	fmt.Fprintln(s.out)
	return nil
}

func ask(s streams, r *bufio.Reader, label, def string) (string, error) {
	if def != "" {
		fmt.Fprintf(s.out, "%s [%s]: ", label, def)
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
func report(s streams, path string, res *setup.Result) {
	fmt.Fprintf(s.out, "✓ wrote %s (mode 0600)\n", path)
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
	fmt.Fprintf(s.out, "\nNext, render the production compose chain with it (from the deployment directory):\n  %s\n", setup.RenderCheckCommand(path))
}

// stringList collects a repeatable flag.
type stringList []string

func (l *stringList) String() string { return strings.Join(*l, ",") }

func (l *stringList) Set(v string) error {
	*l = append(*l, v)
	return nil
}
