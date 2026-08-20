package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/vidra/vidra-core/internal/setup"
)

// DefaultTimeout bounds each individual check. It is generous enough for a
// cold `docker compose config` on a small droplet and short enough that a whole
// run stays something an operator waits for rather than backgrounds.
const DefaultTimeout = 20 * time.Second

// DefaultEnvFile is the deployment's env file, relative to the meta repo root.
// It is the path the Makefile, deploy.sh and the runbook all use.
const DefaultEnvFile = "env/production.env"

// Options is one doctor run.
type Options struct {
	// Root is the meta repo root — the directory holding docker-compose.yml,
	// deploy/ and env/. Empty means the working directory.
	Root string
	// EnvFile is the deployment's env file, absolute or relative to Root.
	// Empty means DefaultEnvFile.
	EnvFile string
	// Timeout bounds EACH check; zero means DefaultTimeout.
	Timeout time.Duration
	// Host and Prober are the injected outside world; nil means the real one.
	Host   Host
	Prober Prober
}

// state is the shared, lazily-computed context every check reads. The expensive
// facts — the rendered compose model, the running containers, the daemon's own
// configuration — are memoised, because they cost a subprocess each and up to
// four checks want the same one.
//
// Nothing here fails a run. A fact that could not be gathered is remembered as
// the REASON it could not, and each check turns that into its own ⚠ with its own
// wording, because "the daemon is not running" means something different to the
// port audit than it does to the ffmpeg probe.
type state struct {
	opt     Options
	root    string // absolute
	envPath string // absolute
	envRel  string // as handed to compose: relative to root when it is under it

	// vars is the deployment's env file, parsed. envErr is why it is not.
	vars   map[string]string
	envErr error

	modelOnce sync.Once
	model     *composeModel
	modelErr  string // an operator-facing reason, never a Go error

	psOnce sync.Once
	ps     []container
	psErr  string

	infoOnce sync.Once
	info     dockerInfo
	infoErr  string
}

func newState(opt Options) (*state, error) {
	if opt.Host == nil {
		opt.Host = RealHost{}
	}
	if opt.Prober == nil {
		opt.Prober = RealProber{}
	}
	if opt.Timeout <= 0 {
		opt.Timeout = DefaultTimeout
	}
	root := opt.Root
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("doctor: resolve --repo %q: %w", opt.Root, err)
	}
	envFile := opt.EnvFile
	if strings.TrimSpace(envFile) == "" {
		envFile = DefaultEnvFile
	}
	envPath := envFile
	if !filepath.IsAbs(envPath) {
		envPath = filepath.Join(root, envPath)
	}
	st := &state{opt: opt, root: root, envPath: envPath, envRel: composePath(root, envPath), vars: map[string]string{}}
	st.loadEnv()
	return st, nil
}

// composePath is how a path is spelled ON the compose command line: relative to
// the deployment directory when it is inside it, absolute otherwise. Compose
// accepts both; the relative form is what the runbook prints, so a command
// doctor quotes back is a command an operator recognises.
func composePath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return path
	}
	return rel
}

func (s *state) loadEnv() {
	b, err := s.opt.Host.ReadFile(s.envPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s.envErr = fmt.Errorf("%s does not exist", s.envRel)
			return
		}
		s.envErr = fmt.Errorf("%s could not be read", s.envRel)
		return
	}
	f, err := setup.ParseEnvFile(b)
	if err != nil {
		// ParseEnvFile's own message is operator-facing (a duplicate assignment,
		// with both line numbers), so it is the exception that is quoted as-is.
		s.envErr = errors.New(strings.TrimPrefix(err.Error(), "setup: "))
		return
	}
	s.vars = f.Values()
}

// value is a trimmed env-file value.
func (s *state) value(key string) string { return strings.TrimSpace(s.vars[key]) }

// path joins a repo-relative path onto the root.
func (s *state) path(rel ...string) string {
	return filepath.Join(append([]string{s.root}, rel...)...)
}

// ---------------------------------------------------------------------------
// The rendered compose model.

// composeModel is the subset of `docker compose config --format json` doctor
// reads. Everything else in that document is ignored on purpose: a model type
// that mirrored the whole schema would break on the next Compose release for
// fields no check looks at.
type composeModel struct {
	Name     string                    `json:"name"`
	Services map[string]composeService `json:"services"`
}

type composeService struct {
	Image    string          `json:"image"`
	Profiles []string        `json:"profiles"`
	Ports    []composePort   `json:"ports"`
	Logging  *composeLogging `json:"logging"`
}

type composePort struct {
	Mode      string `json:"mode"`
	Target    int    `json:"target"`
	Published string `json:"published"`
	Protocol  string `json:"protocol"`
	HostIP    string `json:"host_ip"`
}

// composeLogging's options are decoded as `any` because compose renders them as
// whatever the file wrote — "10m" is a string, but a bare 3 is a number — and a
// map[string]string would fail the WHOLE model decode over a value no check
// compares to anything.
type composeLogging struct {
	Driver  string         `json:"driver"`
	Options map[string]any `json:"options"`
}

func (l *composeLogging) option(name string) string {
	if l == nil {
		return ""
	}
	v, ok := l.Options[name]
	if !ok {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(v))
}

// composeModelFor renders the deployment's own compose chain and decodes it.
//
// The chain is setup.RenderCheckArgs — the SAME builder that produces the
// command `vidra setup` prints and deploy.sh runs — with `config -q` swapped for
// `config --format json`. Rebuilding the -f/--env-file/--profile list here would
// be a second opinion about which overlays and profiles this deployment uses,
// and the first symptom of the two disagreeing would be a port audit that passes
// against a model nothing deploys.
func (s *state) composeModel(ctx context.Context) (*composeModel, string) {
	s.modelOnce.Do(func() {
		if s.envErr != nil {
			s.modelErr = fmt.Sprintf("the env file could not be read (%s), and the compose chain is built from it", s.envErr)
			return
		}
		args := setup.RenderCheckArgs(s.envRel, s.vars, "config", "--format", "json")
		out, err := s.opt.Host.Run(ctx, s.root, "docker", args...)
		if err != nil {
			s.modelErr = "docker is not on this host's PATH, so the compose model could not be rendered"
			return
		}
		if out.ExitCode != 0 {
			s.modelErr = "the compose chain does not render: " + firstLine(out.Stderr)
			return
		}
		var m composeModel
		if err := json.Unmarshal([]byte(out.Stdout), &m); err != nil {
			s.modelErr = "the rendered compose model could not be read as JSON (this Compose may be too old for `config --format json`)"
			return
		}
		s.model = &m
	})
	return s.model, s.modelErr
}

// composeArgs is the deploy's compose chain with a different tail — for the
// `exec` probes, and for quoting the right invocation back at an operator.
func (s *state) composeArgs(tail ...string) []string {
	return setup.RenderCheckArgs(s.envRel, s.vars, tail...)
}

// composeCommand is composeArgs as a pasteable command line.
func (s *state) composeCommand(tail ...string) string {
	return "docker " + strings.Join(s.composeArgs(tail...), " ")
}

// ---------------------------------------------------------------------------
// The running containers.

// container is one running compose container, as `docker ps` labels describe it.
type container struct {
	Name        string
	Service     string
	Project     string
	ConfigFiles []string
	State       string
	Health      string
}

// psFormat asks docker ps for exactly the four labels the checks need, tab
// separated. The compose CLI's own `compose ps --format json` is not used: it
// does not expose com.docker.compose.project.config_files, which is the whole
// evidence for the dev-override check.
const psFormat = `{{.Names}}	{{.Label "com.docker.compose.project"}}	{{.Label "com.docker.compose.service"}}	{{.Label "com.docker.compose.project.config_files"}}	{{.State}}	{{.Status}}`

func (s *state) containers(ctx context.Context) ([]container, string) {
	s.psOnce.Do(func() {
		out, err := s.opt.Host.Run(ctx, s.root, "docker", "ps", "--filter", "label=com.docker.compose.project", "--format", psFormat)
		if err != nil {
			s.psErr = "docker is not on this host's PATH"
			return
		}
		if out.ExitCode != 0 {
			s.psErr = "the docker daemon could not be asked what is running: " + firstLine(out.Stderr)
			return
		}
		for _, line := range strings.Split(strings.TrimSpace(out.Stdout), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			f := strings.Split(line, "\t")
			for len(f) < 6 {
				f = append(f, "")
			}
			s.ps = append(s.ps, container{
				Name:        f[0],
				Project:     f[1],
				Service:     f[2],
				ConfigFiles: splitConfigFiles(f[3]),
				State:       f[4],
				Health:      f[5],
			})
		}
	})
	return s.ps, s.psErr
}

// splitConfigFiles reads the com.docker.compose.project.config_files label,
// which is a comma-separated list of the -f files the project was brought up
// with.
func splitConfigFiles(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// service finds a running container by its compose service name.
func serviceContainer(list []container, name string) (container, bool) {
	for _, c := range list {
		if c.Service == name {
			return c, true
		}
	}
	return container{}, false
}

// ---------------------------------------------------------------------------
// The daemon.

// dockerInfo is the subset of `docker info` doctor reads: where the daemon keeps
// its data (which is the filesystem every named volume lives on, including the
// transcode scratch) and what it logs with by default.
type dockerInfo struct {
	DockerRootDir string `json:"DockerRootDir"`
	LoggingDriver string `json:"LoggingDriver"`
}

func (s *state) dockerInfo(ctx context.Context) (dockerInfo, string) {
	s.infoOnce.Do(func() {
		out, err := s.opt.Host.Run(ctx, s.root, "docker", "info", "--format", "{{json .}}")
		if err != nil {
			s.infoErr = "docker is not on this host's PATH"
			return
		}
		if out.ExitCode != 0 {
			s.infoErr = "the docker daemon did not answer: " + firstLine(out.Stderr)
			return
		}
		if err := json.Unmarshal([]byte(out.Stdout), &s.info); err != nil {
			s.infoErr = "the daemon's `docker info` output could not be read as JSON"
		}
	})
	return s.info, s.infoErr
}

// ---------------------------------------------------------------------------

// firstLine reduces a subprocess's stderr to the one sentence worth printing.
// Compose puts the actionable part first ("required variable X is missing"), and
// the rest is a stack of file paths an operator does not need on a ⚠ line.
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return truncate(line, 220)
		}
	}
	return "no output"
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// addDSNParams adds query parameters to a postgres DSN without disturbing the
// ones already there. It is how the ledger probes get a connect timeout (and the
// search ledger its table name) onto an operator's own connection string.
func addDSNParams(dsn string, params map[string]string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(dsn))
	if err != nil {
		return "", errors.New("the connection string is not a URL")
	}
	q := u.Query()
	for k, v := range params {
		if q.Get(k) == "" {
			q.Set(k, v)
		}
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}
