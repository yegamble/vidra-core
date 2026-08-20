package main

import (
	"fmt"
	"io"
)

// The five commands that WRAP a deployment script: deploy, rollback, backup,
// restore and release. Each one resolves deploy/<name>.sh under --repo, execs it
// with ENV_FILE in its environment and the operator's terminal attached, and
// returns its exit code unchanged.
//
// WHAT THIS FILE DELIBERATELY DOES NOT DO, and why every line of it is argument
// plumbing:
//
//   - It re-implements no gate. MIN_EMBEDDED_MIGRATE_TAG, the Caddyfile.local
//     precondition, the pre-deploy dump, the external-Postgres refusals — all of
//     it stays in the scripts, in one copy. A Go transcription of any of them is
//     a SECOND opinion that has to be kept in step by hand, and the first symptom
//     of it drifting is a deploy that vidra allows and deploy.sh would have
//     refused (or worse, the reverse).
//
//   - It adds no prompt. restore.sh and release.sh each own exactly one
//     confirmation. A wrapper that asked again would train operators to type
//     "yes" twice without reading either, which is the failure mode confirmations
//     exist to prevent.
//
//   - It parses none of the script's own arguments. See parseWrapperFlags.
//
// The value it does add is small and real: one command name to remember instead
// of a path, --repo so it works from anywhere, ENV_FILE handled the way the
// scripts document, and a --help that says what the script will do before it
// does it.

// script is one wrapped deployment script.
type script struct {
	// name is both the vidra subcommand and deploy/<name>.sh.
	name string
	// shape is the usage line's argument shape, cribbed from the script's own
	// header so `vidra restore -h` and `deploy/restore.sh -h` agree.
	shape string
	// about is what the script does, in the two or three sentences an operator
	// wants BEFORE running it.
	about string
}

var (
	deployScript = script{
		name:  "deploy",
		shape: "vidra deploy [-C <deployment directory>] [--env <env file>]",
		about: `Runs the deployment's own deploy/deploy.sh: a pre-deploy database dump, a pull
of the pinned images, the core and search migrations as two separate exit-code
gated steps, ` + "`up -d --no-build`" + `, then /readyz, frontend and TLS-edge probes.
Each step is gated on the one before it, and a failure stops there.

It takes no arguments of its own. What it deploys is the VIDRA_CORE_TAG /
VIDRA_USER_TAG / VIDRA_SEARCH_TAG values in the env file — edit those first.`,
	}
	rollbackScript = script{
		name:  "rollback",
		shape: "vidra rollback [-C <dir>] [--env <file>] <tag> | --core <tag> [--user <tag>] [--search <tag>]",
		about: `Runs deploy/rollback.sh: rewrites the image tags in the env file (keeping a .bak
of the previous values), pulls, restarts and re-probes. About a minute, and no
schema change — it does not touch the database, which is only safe because
schema changes stay backward-compatible for one release. Rolling back ACROSS an
incompatible one needs a restore; deploy/README.md has that sequence.`,
	}
	backupScript = script{
		name:  "backup",
		shape: "vidra backup [-C <deployment directory>] [--env <env file>]",
		about: `Runs deploy/backup.sh: one custom-format pg_dump of the whole database into
backups/, then prunes to 14 daily + 8 weekly. One dump covers vidra-search too —
it lives in its own schema in the same database. Safe to run while the stack is
serving; it does not back up media.

It refuses outright when VIDRA_EXTERNAL_POSTGRES is true: there is no bundled
container to dump, and the managed provider's own backups are the answer.`,
	}
	restoreScript = script{
		name:  "restore",
		shape: "vidra restore [-C <dir>] [--env <file>] [--yes] <dump file>",
		about: `Runs deploy/restore.sh. DESTRUCTIVE: it drops and recreates the database from a
pg_dump archive, then re-runs both migrators. Everything written since the dump
is gone — accounts, videos, comments, moderation decisions.

The confirmation is the script's and there is exactly one: pass --yes, or set
RESTORE_CONFIRM=vidra in the environment. Both reach it unchanged, and vidra
adds no second prompt of its own.`,
	}
	releaseScript = script{
		name:  "release",
		shape: "vidra release [-C <dir>] [--env <file>] [--yes] <tag> [repo…]",
		about: `Runs deploy/release.sh: cuts a GitHub release in each component repo and waits
for GHCR to actually carry the image, in a fixed order, with every pre-flight
check done before the first release is created (a release is outward-facing and
cannot be un-announced).

It deploys nothing. Bump the VIDRA_*_TAG values and run ` + "`vidra deploy`" + ` when you
want the release live. Its confirmation prompt reads your terminal directly, so
--yes is the only way to script it.`,
	}
)

// runScript is every passthrough command's body.
func runScript(sc script, s streams, args []string) error {
	f, err := parseWrapperFlags(sc.name, args)
	if err != nil {
		fmt.Fprintf(s.err, "vidra: %s\n\n", err)
		scriptUsage(s.err, sc)
		return errReported
	}
	if f.help {
		scriptUsage(s.out, sc)
		return nil
	}
	dep, path, err := resolve(sc.name, f, sc.name+".sh")
	if err != nil {
		return err
	}
	// f.rest verbatim: the script's own flags, in the script's own order.
	return theRunner.Passthrough(dep.bash(path, f.rest...), s)
}

func scriptUsage(w io.Writer, sc script) {
	fmt.Fprintf(w, `usage: %s

%s

This is a wrapper and nothing more. Every check, refusal, prompt and exit code
belongs to deploy/%s.sh — vidra re-implements none of them and passes the
script's exit status through unchanged, so `+"`vidra %s && …`"+` reads exactly as
`+"`./deploy/%s.sh && …`"+` does.

Arguments vidra does not recognise, and everything after the first of them, are
handed to the script verbatim. Use -- to end vidra's flags explicitly.

flags:
  -C, --repo <dir>   the deployment directory: the checkout holding
                     docker-compose.yml, deploy/ and env/ (default ".")
      --env <file>   the deployment's env file, exported to the script as
                     ENV_FILE (default %s). An ENV_FILE already
                     exported in your environment is left alone unless you pass
                     this flag.
  -h, --help         this text
`, sc.shape, sc.about, sc.name, sc.name, sc.name, defaultEnvFile)
}
