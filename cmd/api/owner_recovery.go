package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/config"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/store"
)

// `owner-recovery` is the "lost the owner's password, no working mail" exit:
//
//	docker compose run --rm api owner-recovery [--remove-mfa]
//
// Authority is shell access to the host. OUTPUT CONTRACT (the host verb parses
// it): stdout is EXACTLY one line, the link; every human sentence goes to
// stderr. Failures exit 1 with a one-line stderr message and an EMPTY stdout, so
// a script never captures a half-result as a link. Do not log through slog
// here: its default writes stdout.
const ownerRecoveryUsage = "owner-recovery [--remove-mfa]"

// ownerRecoveryDeps lets the output contract be tested without a database.
type ownerRecoveryDeps struct {
	issue   func(ctx context.Context, removeMFA bool) (auth.OwnerRecovery, error)
	record  func(ctx context.Context, ev audit.Event) error
	baseURL string
}

// openOwnerRecovery loads the FULL config (a link built on a development-default
// origin points nowhere) and opens the database.
func openOwnerRecovery(ctx context.Context) (ownerRecoveryDeps, func(), error) {
	cfg, err := config.Load()
	if err != nil {
		return ownerRecoveryDeps{}, nil, fmt.Errorf("configuration is not usable: %w", err)
	}
	db, err := store.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return ownerRecoveryDeps{}, nil, fmt.Errorf("the database is unreachable: %w", err)
	}
	q := db.Queries()
	// No mailer (this is the no-mail path) and no MFA cipher (removal deletes
	// rows, it never opens a secret).
	svc := auth.NewService(q,
		auth.NewTokenIssuer(cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTAudience, cfg.JWTAccessTTL),
		cfg.JWTRefreshTTL,
		auth.WithMFA(q, nil, cfg.TOTPIssuer))
	return ownerRecoveryDeps{
		issue:   svc.IssueOwnerRecovery,
		record:  audit.NewService(q).Record,
		baseURL: cfg.PublicBaseURL,
	}, db.Close, nil
}

// runOwnerRecovery executes the subcommand and returns the process exit code.
func runOwnerRecovery(args []string, stdout, stderr io.Writer, open func(context.Context) (ownerRecoveryDeps, func(), error)) int {
	fs := flag.NewFlagSet("owner-recovery", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "usage: api %s\n\n", ownerRecoveryUsage)
		fs.PrintDefaults()
	}
	removeMFA := fs.Bool("remove-mfa", false, "also remove the owner's authenticator and recovery codes, and sign the owner out everywhere")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "owner-recovery takes no positional arguments (got %q)\nusage: api %s\n", fs.Arg(0), ownerRecoveryUsage)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	deps, closeFn, err := open(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "owner-recovery: %v\n", err)
		return 1
	}
	if closeFn != nil {
		defer closeFn()
	}
	return execOwnerRecovery(ctx, *removeMFA, deps, stdout, stderr)
}

func execOwnerRecovery(ctx context.Context, removeMFA bool, deps ownerRecoveryDeps, stdout, stderr io.Writer) int {
	// A link to a guessed host looks redeemable and is not: fail before minting.
	base := strings.TrimRight(strings.TrimSpace(deps.baseURL), "/")
	if base == "" {
		fmt.Fprintln(stderr, "owner-recovery: PUBLIC_BASE_URL is not set, so there is no origin to build the link on")
		return 1
	}

	rec, err := deps.issue(ctx, removeMFA)
	if err != nil {
		fmt.Fprintf(stderr, "owner-recovery: %v\n", ownerRecoveryMessage(err))
		return 1
	}

	// FAIL-CLOSED: the audit row is written BEFORE the link is shown, and a failed
	// write withholds it (the unprinted token expires unspent) — a credential
	// that leaves no trace is worse than one never issued.
	ev := audit.Event{
		Action: observability.ActionOwnerRecovery, Result: observability.ResultSuccess,
		Actor:  audit.ActorSnapshot{Kind: "system"},
		Reason: "host operator (CLI)", ResourceType: "user", ResourceID: rec.OwnerID.String(),
	}
	ev.Metadata = []audit.MetadataField{{Key: "mode", Value: map[bool]string{false: "password_only", true: "password_and_mfa"}[removeMFA]}}
	if rec.MFARemoved {
		ev.Changes = []audit.Change{{Field: "mfa_enabled", Before: "true", After: "false"}}
	}
	if err := deps.record(ctx, ev); err != nil {
		fmt.Fprintln(stderr, "owner-recovery: the recovery was issued but could not be written to the audit log, so the link is withheld (it expires unused); fix the database and re-run")
		return 1
	}

	fmt.Fprintf(stderr, "Link expires %s. Single-use; anyone with this link can set the owner's password.\n", rec.ExpiresAt.UTC().Format(time.RFC3339))
	switch {
	case rec.MFARemoved:
		fmt.Fprintln(stderr, "The owner's two-factor authentication was REMOVED and all their sessions signed out.")
	case removeMFA:
		fmt.Fprintln(stderr, "The owner had no two-factor authentication enabled; nothing was removed.")
	default:
		fmt.Fprintln(stderr, "Without --remove-mfa the owner still needs their authenticator to log in.")
	}
	fmt.Fprintf(stdout, "%s/reset-password/confirm?token=%s\n", base, url.QueryEscape(rec.Token))
	return 0
}

// ownerRecoveryMessage keeps the one-line stderr promise.
func ownerRecoveryMessage(err error) string {
	return strings.ReplaceAll(strings.TrimPrefix(err.Error(), "auth: "), "\n", " ")
}
