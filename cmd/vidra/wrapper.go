package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vidra/vidra-core/internal/doctor"
	"github.com/vidra/vidra-core/internal/setup"
)

// This file is the plumbing every command that RUNS one of the deployment's own
// scripts shares: which arguments belong to vidra, where the script is, and what
// environment it gets. The commands themselves are one file each.

// defaultEnvFile is the deployment's env file. It is doctor's constant rather
// than a second copy of the string: `vidra doctor`, `vidra deploy` and
// deploy/lib.sh must default to the SAME file, or a check and a deploy describe
// different deployments.
const defaultEnvFile = doctor.DefaultEnvFile

// wrapperFlags are the arguments `vidra` consumes before a wrapped script's own
// arguments begin.
type wrapperFlags struct {
	// repo is -C/--repo: the deployment directory.
	repo string
	// envFile is --env, and explicit records whether the OPERATOR said it. The
	// difference is the whole ENV_FILE precedence rule below.
	envFile  string
	explicit bool
	help     bool
	// rest is everything from the first argument vidra did not recognise,
	// verbatim.
	rest []string
}

// parseWrapperFlags reads the LEADING vidra flags off a command line and stops
// at the first thing it does not recognise.
//
// It is a hand-written loop and not a flag.FlagSet, and that is the point. A
// FlagSet over the whole argv would parse the SCRIPT's flags too: restore.sh's
// `--yes` would become "flag provided but not defined", rollback.sh's `--core
// v0.2.0` would be eaten, and release.sh's repo list would land in fs.Args() in
// an order this code chose rather than the one the operator typed. The rule an
// operator can hold in their head is "vidra's flags come first, everything after
// the first one it does not know belongs to the script" — and `--` ends vidra's
// half explicitly when the script's own first argument would otherwise look like
// one of ours.
func parseWrapperFlags(command string, args []string) (wrapperFlags, error) {
	f := wrapperFlags{repo: ".", envFile: defaultEnvFile}
	i := 0
loop:
	for i < len(args) {
		name, value, hasValue := splitFlag(args[i])
		switch name {
		case "-C", "--repo", "-repo", "--env", "-env":
			if !hasValue {
				if i+1 >= len(args) {
					return f, fmt.Errorf("%s: %s needs a value", command, name)
				}
				value = args[i+1]
				i++
			}
			if name == "--env" || name == "-env" {
				f.envFile, f.explicit = value, true
			} else {
				f.repo = value
			}
			i++
		case "-h", "--help", "-help":
			f.help = true
			i++
		case "--":
			// The explicit end of vidra's flags: `vidra restore -- --env` hands
			// --env to the script. Consumed, not forwarded.
			i++
			break loop
		default:
			break loop
		}
	}
	f.rest = args[i:]
	if f.repo == "" {
		return f, fmt.Errorf("%s: -C/--repo needs a directory", command)
	}
	if f.envFile == "" {
		return f, fmt.Errorf("%s: --env needs a file", command)
	}
	return f, nil
}

// splitFlag splits --flag=value. A bare word is returned as its own name with no
// value, which is what makes the loop above stop on it.
func splitFlag(arg string) (name, value string, hasValue bool) {
	if !strings.HasPrefix(arg, "-") {
		return arg, "", false
	}
	if eq := strings.IndexByte(arg, '='); eq > 0 {
		return arg[:eq], arg[eq+1:], true
	}
	return arg, "", false
}

// deployment is a resolved deployment directory: the absolute root, and the
// absolute path of the script about to be run.
type deployment struct {
	root string
	// envFile is as the OPERATOR spelled it — relative stays relative, because
	// that is what ENV_FILE hands to the scripts and what their messages quote
	// back. envPath is the same thing resolved, for reading it here.
	envFile string
	envPath string
	env     []string
}

// resolve turns the parsed flags into a deployment and the absolute path of
// deploy/<script>.sh inside it.
//
// A missing script is the ONE usage error these commands have, and it is worth
// its own sentence: it means --repo does not point at a deployment, which is the
// same assumption `vidra setup` and `vidra doctor` make and the same mistake
// (running from a component checkout instead of the meta repo) they are pointed
// at with -C.
func resolve(command string, f wrapperFlags, script string) (deployment, string, error) {
	root, err := filepath.Abs(f.repo)
	if err != nil {
		return deployment{}, "", fmt.Errorf("%s: %s is not a usable path for -C/--repo", command, f.repo)
	}
	path := filepath.Join(root, "deploy", script)
	if _, err := os.Stat(path); err != nil {
		return deployment{}, "", fmt.Errorf(
			"%s: %s does not exist — -C/--repo must name the deployment directory, the checkout that holds docker-compose.yml, deploy/ and env/ (the same directory `vidra setup` writes into and `vidra doctor` reads)",
			command, path)
	}
	envPath := f.envFile
	if !filepath.IsAbs(envPath) {
		envPath = filepath.Join(root, envPath)
	}
	return deployment{
		root:    root,
		envFile: f.envFile,
		envPath: envPath,
		env:     childEnv(theRunner.Environ(), f),
	}, path, nil
}

// childEnv is the environment a wrapped script runs with: this process's, plus
// ENV_FILE.
//
// The precedence is deploy/lib.sh's, unchanged. Every script reads
// `ENV_FILE="${ENV_FILE:-env/production.env}"` from its own environment, and
// env_get then lets a real environment variable beat the file for any key — so
// `POSTGRES_DB=other vidra backup` works exactly as `POSTGRES_DB=other
// ./deploy/backup.sh` does, for free, because the whole environment is
// inherited.
//
// The one rule vidra adds: an ENV_FILE the OPERATOR already exported is never
// overwritten by our DEFAULT. `ENV_FILE=env/staging.env vidra deploy` has to
// deploy staging, not silently deploy production because a flag has a default
// value. An explicit --env does win — that is the operator saying it twice, more
// recently.
func childEnv(base []string, f wrapperFlags) []string {
	const key = "ENV_FILE="
	out := make([]string, 0, len(base)+1)
	at, inherited := -1, ""
	for _, kv := range base {
		if strings.HasPrefix(kv, key) {
			at, inherited = len(out), strings.TrimSpace(strings.TrimPrefix(kv, key))
		}
		out = append(out, kv)
	}
	// An exported-but-EMPTY ENV_FILE counts as unset, because that is how the
	// scripts read it: `${ENV_FILE:-env/production.env}` falls back on empty as
	// well as on absent.
	if inherited != "" && !f.explicit {
		return out
	}
	if at >= 0 {
		out[at] = key + f.envFile
		return out
	}
	return append(out, key+f.envFile)
}

// values reads the deployment's env file. The error is operator-facing: this is
// the file the operator edits, and "open /x/y: no such file or directory" is not
// a sentence about their deployment.
func (d deployment) values() (map[string]string, error) {
	b, err := os.ReadFile(d.envPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%s does not exist (cp env/production.env.example env/production.env, or run `vidra setup`)", d.envFile)
		}
		return nil, fmt.Errorf("%s could not be read", d.envFile)
	}
	f, err := setup.ParseEnvFile(b)
	if err != nil {
		// ParseEnvFile's message is already operator-facing (a duplicate
		// assignment, with both line numbers), so it is quoted rather than
		// replaced.
		return nil, errors.New(strings.TrimPrefix(err.Error(), "setup: "))
	}
	return f.Values(), nil
}

// bash builds the spec for running one of the deployment's scripts.
func (d deployment) bash(path string, args ...string) execSpec {
	return execSpec{
		Path: "bash",
		Args: append([]string{path}, args...),
		Dir:  d.root,
		Env:  d.env,
	}
}

// composeCommandLine is the compose.sh invocation as something an operator can
// paste, for the findings that suggest running it by hand. The ENV_FILE prefix
// is shown only when it is not the default, so the ordinary case stays readable.
func composeCommandLine(dep deployment, args ...string) string {
	prefix := ""
	if dep.envFile != defaultEnvFile {
		prefix = "ENV_FILE=" + dep.envFile + " "
	}
	return prefix + "./deploy/compose.sh " + strings.Join(args, " ")
}

// envGet mirrors deploy/lib.sh's env_get, and exists so `vidra status` dials the
// port the deployment actually publishes.
//
// The precedence is the shell's, in the same order: a real PROCESS environment
// variable wins, then the env file, then the default. Reversing those two is the
// bug this comment exists to prevent — `HTTP_PORT=8088 ./deploy/compose.sh up`
// is a documented one-off, and a status command that then probed 8080 would
// report the stack down while it served happily.
func envGet(processEnv, values map[string]string, key, def string) string {
	if v := strings.TrimSpace(processEnv[key]); v != "" {
		return v
	}
	if v := strings.TrimSpace(values[key]); v != "" {
		return unquote(v)
	}
	return def
}

// unquote strips one layer of matching quotes, as lib.sh's env_get does. The env
// file is handed to compose verbatim, so this is only ever applied to values
// this CLI reads for itself.
func unquote(v string) string {
	if len(v) >= 2 {
		if (v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'') {
			return v[1 : len(v)-1]
		}
	}
	return v
}

// environMap turns a KEY=VALUE list into a map for envGet.
func environMap(environ []string) map[string]string {
	out := make(map[string]string, len(environ))
	for _, kv := range environ {
		if eq := strings.IndexByte(kv, '='); eq > 0 {
			out[kv[:eq]] = kv[eq+1:]
		}
	}
	return out
}

// firstLine reduces a subprocess's output to the one sentence worth printing —
// the same reduction internal/doctor makes, and for the same reason: the
// actionable part comes first and the rest is a stack of paths.
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return truncate(line, 220)
		}
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
