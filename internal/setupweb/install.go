package setupweb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/vidra/vidra-core/internal/setup"
)

// installHint is what the page says under a deploy that failed. It is the same
// next step the CLI would give: the deploy scripts print one clear sentence
// about why they stopped, and `vidra doctor` is what turns that into a list of
// things to fix.
const installHint = "The deploy stopped. The last lines above are its own explanation — read them first. `vidra doctor` checks the whole deployment (compose version, exposure, configuration, backups, reachability) and is the next command to run; `vidra logs <service>` holds why a container stopped."

// handleInstall runs the deployment's own deploy.sh and streams it to the
// browser.
//
// IT RE-IMPLEMENTS NO GATE. Every refusal, dump and probe stays in the script,
// in one copy — the same rule cmd/vidra's five wrapper commands follow, and for
// the same reason: a Go transcription of MIN_EMBEDDED_MIGRATE_TAG or the
// pre-deploy dump would be a second opinion that has to be kept in step by hand,
// and the first symptom of it drifting is a deploy the wizard allows and
// deploy.sh would have refused.
//
// STDIN IS CLOSED, and that is a decision rather than an omission. There is
// nobody at a terminal here: a script that blocked on a prompt would hang the
// wizard with a progress spinner and no explanation. With stdin at EOF a `read`
// fails immediately and the script dies with its own message — loud, and in the
// stream the operator is already watching. (deploy.sh does not prompt; the
// scripts that do, restore.sh and release.sh, are not run from here.)
//
// THE PROCESS OUTLIVES THE REQUEST. The exec context is deliberately NOT the
// request's: a browser tab closed halfway through must not kill a deploy between
// the core migration and the search one. The stream stops; the deploy finishes.
func (s *Server) handleInstall(w http.ResponseWriter, _ *http.Request) {
	if s.opt.Install == nil {
		writeError(w, http.StatusNotImplemented, "this build cannot run the deploy for you — run it yourself with "+s.opt.DeployCommand)
		return
	}
	// One at a time. Two deploys racing over the same compose project is not a
	// state anything downstream is written to survive, and the second one would
	// arrive from a stray double-click.
	s.mu.Lock()
	if s.installing {
		s.mu.Unlock()
		writeError(w, http.StatusConflict, "a deploy is already running. Watch the stream that is already open — starting a second one would run two `docker compose up` against the same project at once.")
		return
	}
	s.installing = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.installing = false
		s.mu.Unlock()
	}()

	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	// A reverse proxy has no business being in front of a loopback wizard, but
	// one buffering this stream would turn a live install into a blank screen
	// followed by ten minutes of output at once, so it is asked not to.
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flush(w)

	stream := &sseWriter{w: w, touch: s.touch}
	code, err := s.opt.Install(context.Background(), stream)
	stream.close()

	ev := InstallEvent{ExitCode: code}
	switch {
	case err != nil:
		// The script could not be STARTED — a missing deploy/deploy.sh, no bash.
		// That is not a failed deploy, and saying so is the difference between an
		// operator reading a log that does not exist and one fixing their --repo.
		ev.ExitCode, ev.Error = 1, err.Error()
		ev.Hint = "Nothing was deployed. " + installHint
	case code != 0:
		ev.Hint = installHint
	}
	stream.event("done", ev)
}

// sseWriter turns a subprocess's byte stream into one server-sent event per
// LINE.
//
// Per line, rather than per chunk, because a chunk boundary is wherever the pipe
// happened to fill: an operator watching a deploy would otherwise see half a
// sentence arrive, then the rest of it three seconds later attached to the front
// of the next one. The payload is JSON-encoded, which is also what keeps a line
// containing a newline (or a lone \r from a progress bar) from breaking the
// event framing.
type sseWriter struct {
	w     http.ResponseWriter
	buf   bytes.Buffer
	touch func()
}

func (s *sseWriter) Write(p []byte) (int, error) {
	s.buf.Write(p)
	for {
		line, err := s.buf.ReadString('\n')
		if err != nil {
			// An incomplete line: put it back and wait for the rest.
			s.buf.Reset()
			s.buf.WriteString(line)
			break
		}
		s.emit(strings.TrimRight(line, "\r\n"))
	}
	// A deploy is minutes of silence punctuated by output, and it must not be
	// mistaken for a browser that has gone away: every line the operator sees is
	// also proof they are still there.
	if s.touch != nil {
		s.touch()
	}
	// The subprocess's writes always "succeed" from here: a browser that has
	// closed the tab must not make os/exec's copier abandon a deploy that is
	// still running (see handleInstall).
	return len(p), nil
}

// close flushes a final line with no newline after it — which is what a script
// that died mid-sentence leaves behind, and exactly the line worth reading.
func (s *sseWriter) close() {
	if rest := strings.TrimRight(s.buf.String(), "\r\n"); rest != "" {
		s.emit(rest)
	}
	s.buf.Reset()
}

func (s *sseWriter) emit(text string) { s.event("line", InstallEvent{Text: text}) }

func (s *sseWriter) event(name string, payload any) {
	b, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", name, b)
	flush(s.w)
}

// handleStatus is the Success step's probe: what is running and whether it
// answers, in `vidra status`'s own words.
//
// This is where the api's 15-second database/Redis fail-fast surfaces as wizard
// feedback instead of as a crash loop an operator discovers from `docker ps`
// twenty minutes later.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	out := StatusResponse{
		// The two halves of the owner-claim handoff, verbatim from the terminal's
		// report(). The admin account is NOT in the env file and cannot be: the api
		// mints a one-time token at boot and prints it in its own log, and until it
		// is redeemed every signup path answers 403 owner_claim_required.
		//
		// `./deploy/compose.sh logs api` and never a bare `docker compose logs
		// api`: on a deployment host the bare form silently picks up
		// docker-compose.override.yml's dev defaults and addresses a different
		// project than the deploy scripts do.
		LogsCommand: "./deploy/compose.sh logs api",
		ClaimURL:    "https://<your domain>/setup/claim",
	}
	// The origin comes from the file that was just written, not from the form:
	// the Success step is about the deployment that EXISTS, and after Apply that
	// is what the env file says.
	if src, err := s.load(); err == nil && src.existing != nil {
		if origin := setup.Effective(nil, src.existing, "PUBLIC_BASE_URL"); origin != "" {
			out.ClaimURL = strings.TrimSuffix(origin, "/") + "/setup/claim"
		}
	}
	if s.opt.Status != nil {
		out.Lines = s.opt.Status(r.Context())
	}
	writeJSON(w, http.StatusOK, out)
}
