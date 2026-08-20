// Command vidra is the operator CLI for a Vidra deployment.
//
// It is the whole day-to-day surface: `setup` generates the production env file
// (phase-1 item 8), `doctor` checks a deployment (item 14), `deploy`,
// `rollback`, `backup`, `restore`, `release`, `logs`, `restart` and `status`
// (item 13) are the running-it half, and `update` (item 15) is the one that
// moves a deployment to a new release. Each is one file next to this, sharing
// the same argument/stream plumbing: adding a command is a table entry and a
// file, never a rewrite of how the CLI is invoked.
//
// THE FIVE DEPLOY COMMANDS ARE WRAPPERS, NOT RE-IMPLEMENTATIONS. deploy,
// rollback, backup, restore and release exec the deployment's own
// deploy/*.sh with the operator's terminal attached and return the script's exit
// code unchanged. Every gate — the embedded-migrator tag floor, the pre-deploy
// dump, the confirmations — stays in the scripts, in one copy, because a Go
// transcription of any of them would be a second opinion that has to be kept in
// step by hand. See passthrough.go.
//
// `update` is the one command that is NOT a wrapper, and it is worth saying why
// it is allowed not to be: choosing a release, refusing one whose migrations are
// behind the database, keeping more than one generation of the env file and
// flipping the tags back after a failed deploy are all things no script does at
// all. It still runs deploy/deploy.sh for the deploy itself, gates and prompts
// unchanged. See update.go.
//
// It is a separate binary from cmd/api on purpose: the api image runs a server
// (and its `migrate` subcommand, which must ship inside that image), while this
// runs on the host beside docker compose, and will be fetched by the installer
// before any image exists.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
)

// streams is the CLI's IO, injected so every command is testable without
// touching the process's stdin/stdout.
type streams struct {
	in  io.Reader
	out io.Writer
	err io.Writer
}

// command is one subcommand. run receives the arguments AFTER the command name.
type command struct {
	name    string
	summary string
	run     func(s streams, args []string) error
}

// commands is the dispatch table; order is the order `vidra help` lists them.
var commands = []command{
	{
		name:    "setup",
		summary: "generate the production env file (and --check an existing one)",
		run:     runSetup,
	},
	{
		name:    "doctor",
		summary: "check a deployment: compose, exposure, configuration, backups, reachability",
		run:     runDoctor,
	},
	{
		name:    "status",
		summary: "what is running and whether it answers: containers, api, search, frontend",
		run:     runStatus,
	},
	{
		name:    "logs",
		summary: "follow the deployment's logs (all services, or the ones you name)",
		run:     runLogs,
	},
	{
		name:    "update",
		summary: "move this deployment to the newest release: find it, pin it, deploy it, roll back if it fails",
		run:     runUpdate,
	},
	{
		name:    "deploy",
		summary: "deploy the tags pinned in the env file (deploy/deploy.sh)",
		run:     func(s streams, args []string) error { return runScript(deployScript, s, args) },
	},
	{
		name:    "rollback",
		summary: "roll the application back to a previously released tag (deploy/rollback.sh)",
		run:     func(s streams, args []string) error { return runScript(rollbackScript, s, args) },
	},
	{
		name:    "restart",
		summary: "restart one service that has wedged (no pull, no migration)",
		run:     runRestart,
	},
	{
		name:    "backup",
		summary: "dump the database now, and prune the old dumps (deploy/backup.sh)",
		run:     func(s streams, args []string) error { return runScript(backupScript, s, args) },
	},
	{
		name:    "restore",
		summary: "DESTRUCTIVE: restore a dump over the live database (deploy/restore.sh)",
		run:     func(s streams, args []string) error { return runScript(restoreScript, s, args) },
	},
	{
		name:    "release",
		summary: "cut a release in each component repo and wait for its image (deploy/release.sh)",
		run:     func(s streams, args []string) error { return runScript(releaseScript, s, args) },
	},
}

// errReported marks a failure the command has ALREADY printed in the shape the
// operator needs it (usage text, a per-variable problem list). main turns it
// into a non-zero exit without adding a second, redundant error line.
var errReported = errors.New("reported")

func main() {
	s := streams{in: os.Stdin, out: os.Stdout, err: os.Stderr}
	if err := run(s, os.Args[1:]); err != nil {
		// A wrapped script's own exit code, passed through untouched and
		// SILENTLY: deploy.sh already printed one clear sentence about why it
		// stopped, and a `vidra: exit status 3` under it would be a second,
		// emptier line. The code matters because `vidra deploy || rollback`,
		// cron and CI all read it. See exec.go.
		var exit *exitError
		if errors.As(err, &exit) {
			os.Exit(exit.code)
		}
		if !errors.Is(err, errReported) {
			fmt.Fprintln(s.err, "vidra: "+err.Error())
		}
		os.Exit(1)
	}
}

func run(s streams, args []string) error {
	if len(args) == 0 {
		usage(s.err)
		return errReported
	}
	switch args[0] {
	case "-h", "--help", "help":
		usage(s.out)
		return nil
	}
	for _, c := range commands {
		if c.name == args[0] {
			return c.run(s, args[1:])
		}
	}
	fmt.Fprintf(s.err, "vidra: unknown command %q\n\n", args[0])
	usage(s.err)
	return errReported
}

func usage(w io.Writer) {
	fmt.Fprint(w, "vidra — operator CLI for a Vidra deployment\n\nusage: vidra <command> [flags]\n\ncommands:\n")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, c := range commands {
		fmt.Fprintf(tw, "  %s\t%s\n", c.name, c.summary)
	}
	_ = tw.Flush()
	fmt.Fprint(w, "\nRun `vidra <command> -h` for a command's flags.\n")
}
