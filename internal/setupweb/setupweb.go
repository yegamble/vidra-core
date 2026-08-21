// Package setupweb serves `vidra setup --web`: the nine-step install wizard
// (docs/productionization phase-1 item 9), hosted by the operator's own copy of
// the vidra binary on loopback.
//
// WHY THE HOST BINARY AND NOT A CONTAINER OR THE FRONTEND. The wizard's whole
// job is to produce the file the stack has not got yet, so it cannot live inside
// the stack: the api container has no docker socket to deploy through, no
// deployment tree to write into, no env file to read — and if it failed to boot
// for want of that env file it could not report so. Serving it from the frontend
// image has the same problem one layer out, plus a worse one: browser JS would
// have to aim at the instance's public origin, which is exactly the origin that
// does not answer yet, behind CORS rules (singleOriginKeys) the file being
// generated is what configures. The vidra binary, in contrast, is already on the
// host, already reads the template, already writes the env file and already
// knows how to exec deploy.sh. So the wizard is a local server, and the remote
// operator reaches it the way they reach any other localhost service: an SSH
// tunnel.
//
// WHAT THIS PACKAGE IS ALLOWED TO DECIDE, AND WHAT IT IS NOT. Every answer flows
// through internal/setup — NormalizeTLSMode, NormalizeOriginForMode and
// CheckAcmeEmail for the per-field checks, Generate + Check + Warnings for
// Review, the same Generate result written through the same setup.WriteFile for
// Apply. There is no second validation here and there must never be one: a
// wizard with a friendlier opinion about what a domain is, is a wizard that
// accepts answers the engine then refuses, after every other question has been
// answered. What this package owns is CONTAINMENT (see guard.go), the shape of
// the wire payloads, and the order the steps are asked in.
//
// IT IS A CREDENTIAL. A server that writes a 0600 env file full of freshly
// minted secrets and then execs a deploy is, for as long as it is running, a
// root-equivalent handle on the deployment with no login of its own. Everything
// in guard.go follows from that sentence, and so does the lifetime below: it
// exits on SIGINT, on the Success step's Finish, and on an idle timeout, because
// a localhost server holding a file-writing token must not still be there
// tomorrow.
package setupweb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

// DefaultListen is where the wizard binds when --listen is not given. The port
// is deliberately not one anything else on a Vidra host uses (8080 is the api,
// 3000 the frontend, 8443/80/443 the edge), so the default works on a box that
// is already serving.
const DefaultListen = "127.0.0.1:8321"

// DefaultIdleTimeout is how long the wizard waits for a browser that has stopped
// asking before it shuts itself down. Long enough that an operator can go and
// create a DNS record mid-wizard and come back; short enough that a terminal
// left open overnight is not still offering the deployment to whoever gets a
// shell on that host next.
const DefaultIdleTimeout = 30 * time.Minute

// Options is one wizard run. The paths are fixed HERE, from the command line,
// and are never taken from the wire: a request cannot name a file, which is what
// makes "path traversal on something the server writes" un-askable rather than
// defended against.
type Options struct {
	// Listen is the bind address, host:port. It must be a loopback host — see
	// ParseListen, which is the refusal, not a warning.
	Listen string

	// TemplatePath, OutputPath, CaddyOutPath and NginxOutPath are the four files
	// the wizard reads and writes, spelled exactly as `vidra setup` spells them
	// so a wizard run and a terminal run produce the same tree.
	TemplatePath string
	OutputPath   string
	CaddyOutPath string
	NginxOutPath string

	// IdleTimeout overrides DefaultIdleTimeout; zero means the default. A
	// negative value disables the timer, which is for tests only — nothing on the
	// command line can reach it.
	IdleTimeout time.Duration

	// KeepAlive overrides defaultKeepAlive, the interval at which a silent deploy
	// still heartbeats the install stream; zero means the default. It exists as an
	// option so a test can shrink it without mutating a shared global, which would
	// race a parallel test streaming its own install.
	KeepAlive time.Duration

	// Rand is the entropy behind the one-time token; nil means crypto/rand.
	Rand io.Reader

	// RenderProxy renders the reverse-proxy configs the run would write, from the
	// RESOLVED values of the generated file. Injected rather than reimplemented:
	// which of the two files a deployment gets (and whether --no-caddy suppressed
	// the first) is a decision `vidra setup` already makes, and a second copy of
	// it here would be the drift that leaves a deployment with a Caddyfile
	// nothing mounts. Either return may be nil, meaning "no such file".
	RenderProxy func(values map[string]string) (caddy, nginx []byte, err error)

	// CheckDomain is the non-blocking DNS report behind the Domain step. It is
	// preflight's, injected so this package's tests need no nameserver.
	CheckDomain func(ctx context.Context, domain string) DomainReport

	// Doctor runs the host-side checks the System Check step renders.
	Doctor func(ctx context.Context) (DoctorReport, error)

	// Install runs the deployment's own deploy.sh with stdin closed, copying its
	// combined output to w as it arrives, and returns the script's exit code. The
	// error return is for a script that could not be STARTED; a failed deploy is
	// a non-zero code, not an error.
	Install func(ctx context.Context, w io.Writer) (int, error)

	// Status is the post-install probe behind the Success step.
	Status func(ctx context.Context) []StatusLine

	// DeployCommand is what an operator types to run the install themselves,
	// verbatim, for the "I'll run it myself" path.
	DeployCommand string
}

// Server is one wizard. It is created (and its token minted) before anything
// binds, so a caller can print the opening URL and the tunnel instruction in one
// block and only then start serving.
type Server struct {
	opt      Options
	token    string
	handler  http.Handler
	listener net.Listener

	// addr is the bind address. It is under the mutex because Listen replaces it
	// with the one the OS granted, and the CLI reads it from another goroutine to
	// print the URL.
	addr string

	// idle is the inactivity timer. It is reset by every request that gets past
	// the guard — an unauthenticated probe must not be able to keep the wizard
	// alive, which is why the reset lives at the END of the guard and not in the
	// mux.
	mu   sync.Mutex
	idle *time.Timer

	// installing is held for the duration of a deploy. Two installs at once would
	// be two `docker compose up` runs racing over the same project.
	installing bool

	closeOnce sync.Once
	done      chan struct{}
	reason    string
}

// New validates the options and mints the one-time token. It binds nothing: a
// caller that cannot bind should say so after it has already decided the run is
// legitimate, and a caller writing a test should not have to.
func New(opt Options) (*Server, error) {
	if opt.Listen == "" {
		opt.Listen = DefaultListen
	}
	addr, err := ParseListen(opt.Listen)
	if err != nil {
		return nil, err
	}
	if opt.IdleTimeout == 0 {
		opt.IdleTimeout = DefaultIdleTimeout
	}
	token, err := mintToken(opt.Rand)
	if err != nil {
		return nil, err
	}
	s := &Server{opt: opt, token: token, addr: addr, done: make(chan struct{})}
	s.handler = s.guard(s.routes())
	return s, nil
}

// Token is the one-time credential. It is printed ONCE, in the opening URL, and
// is the only thing standing between this server and anything else that can talk
// to loopback on this host.
func (s *Server) Token() string { return s.token }

// Addr is the address the wizard is on: the one ParseListen vetted before
// Listen, and the one the OS actually granted after it — they differ when the
// operator asked for port 0.
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addr
}

// URL is the opening link: the shell's address with the token in it. This is the
// one place the token is ever rendered, and it is only correct once Listen has
// returned — which is why the CLI binds before it prints.
func (s *Server) URL() string { return "http://" + s.Addr() + "/?t=" + s.token }

// Handler is the whole server as an http.Handler, guard included. It exists for
// the tests: every refusal in guard.go is a property of THIS handler, so a test
// that reached past it would be testing a server nobody runs.
func (s *Server) Handler() http.Handler { return s.handler }

// Done closes when the wizard has decided to stop — Finish, the idle timeout, or
// a Shutdown from a signal handler.
func (s *Server) Done() <-chan struct{} { return s.done }

// Reason says why it stopped, for the line the CLI prints on the way out. It is
// only meaningful once Done is closed.
func (s *Server) Reason() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reason
}

// Shutdown stops the wizard with a stated reason. It is safe to call more than
// once and from any goroutine: SIGINT, the Finish handler and the idle timer all
// race for it by design.
func (s *Server) Shutdown(reason string) {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.reason = reason
		if s.idle != nil {
			s.idle.Stop()
		}
		s.mu.Unlock()
		close(s.done)
	})
}

// The three ways a wizard ends, spelled once so the CLI's parting line and the
// tests agree about them.
const (
	ReasonFinished = "the wizard reported it was finished"
	ReasonIdle     = "no browser has spoken to it for " // + the timeout
	ReasonSignal   = "interrupted"
)

// Listen binds the loopback port and nothing else. It is separate from Run so
// the CLI can do the three things in the order an operator needs them: bind (so
// "that port is taken" is reported while they are still looking at the command),
// print the opening URL and the tunnel instruction (which needs the port that
// was actually granted), and only then start answering requests.
func (s *Server) Listen() error {
	if s.listener != nil {
		return nil
	}
	ln, err := net.Listen("tcp", s.Addr())
	if err != nil {
		return fmt.Errorf("setup: the wizard could not bind %s: %w. Another `vidra setup --web` may still be running on this host; pass --listen 127.0.0.1:<other port> to use a different one", s.Addr(), err)
	}
	s.listener = ln
	// The bound address is not always the requested one: --listen 127.0.0.1:0 is
	// how a test (and an operator with a busy host) asks the OS to pick, and the
	// URL has to name the port that was actually taken.
	s.mu.Lock()
	s.addr = ln.Addr().String()
	s.mu.Unlock()
	return nil
}

// Run serves until the wizard is done, then drains in-flight requests. It binds
// first if the caller has not already.
func (s *Server) Run(ctx context.Context) error {
	if err := s.Listen(); err != nil {
		return err
	}
	ln := s.listener
	s.startIdleTimer()

	srv := &http.Server{
		Handler: s.handler,
		// A generous read header timeout and NO write timeout: the install step
		// streams a deploy that legitimately takes minutes, and a write deadline
		// would cut the operator off mid-migration with nothing but a broken
		// stream to explain it.
		ReadHeaderTimeout: 20 * time.Second,
	}
	errc := make(chan error, 1)
	go func() {
		err := srv.Serve(ln)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errc <- err
	}()

	select {
	case err := <-errc:
		s.Shutdown("the server stopped on its own")
		return err
	case <-ctx.Done():
		s.Shutdown(ReasonSignal)
	case <-s.done:
	}
	// Bounded: Finish is answered before the shutdown starts, so the only thing
	// left to drain is a browser that has already been told to stop.
	shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutCtx)
	<-errc
	return nil
}

// startIdleTimer arms the inactivity shutdown. A non-positive timeout disables
// it outright, which is a test affordance: nothing on the command line can
// produce one.
//
// A RUNNING INSTALL IS NEVER IDLE. A deploy's pre-deploy pg_dump can be silent
// for well past the idle window on a large database, and the browser watching an
// SSE stream sends nothing while it waits — so a naive timer would fire mid-dump,
// tear the server down, orphan the deploy child (it outlives the request by
// design) and drop the stream with no `done` event, leaving an operator staring
// at a frozen progress log for a deploy that is still running underneath. So the
// timer re-arms instead of shutting down while s.installing is set; the install
// keepalive (see sseWriter) is what keeps the timer honest even when the deploy
// itself is silent.
func (s *Server) startIdleTimer() {
	if s.opt.IdleTimeout <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.idle = time.AfterFunc(s.opt.IdleTimeout, s.onIdle)
}

// onIdle is the timer's callback. It shuts the wizard down UNLESS a deploy is in
// flight, in which case it re-arms and waits — an install is activity even when
// it is producing no output.
func (s *Server) onIdle() {
	s.mu.Lock()
	if s.installing {
		if s.idle != nil {
			s.idle.Reset(s.opt.IdleTimeout)
		}
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	s.Shutdown(ReasonIdle + s.opt.IdleTimeout.String())
}

// touch resets the inactivity timer. Called from the guard, once a request has
// proved it holds the token.
func (s *Server) touch() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.idle != nil {
		s.idle.Reset(s.opt.IdleTimeout)
	}
}
