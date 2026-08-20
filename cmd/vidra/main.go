// Command vidra is the operator CLI for a Vidra deployment.
//
// It starts with one subcommand — `vidra setup`, which generates the production
// env file (phase-1 item 8) — and is structured for the rest: `doctor`,
// `status`, `logs`, `deploy` and friends land as further entries in the
// dispatch table below (phase-1 items 13-15), each one file next to this,
// sharing the same argument/stream plumbing. The point of shipping the table
// now is that adding a command is a table entry and a file, never a rewrite of
// how the CLI is invoked.
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
}

// errReported marks a failure the command has ALREADY printed in the shape the
// operator needs it (usage text, a per-variable problem list). main turns it
// into a non-zero exit without adding a second, redundant error line.
var errReported = errors.New("reported")

func main() {
	s := streams{in: os.Stdin, out: os.Stdout, err: os.Stderr}
	if err := run(s, os.Args[1:]); err != nil {
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
