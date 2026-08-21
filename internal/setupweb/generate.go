package setupweb

import (
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/vidra/vidra-core/internal/setup"
)

// plan is one generation, rendered but not written: the env file's bytes, both
// possible proxy configs, and the list of files that would land.
//
// It exists because Review and Apply must be THE SAME RUN, differing only in
// whether anything reaches the disk. A Review that computed one thing and an
// Apply that computed another would make the review step a decoration — and it
// is the step where an operator agrees to overwrite a live deployment's env
// file.
type plan struct {
	res   *setup.Result
	caddy []byte
	nginx []byte
	files []FileJSON
}

// writeMu serialises the whole write half. Two browsers (or a browser and a
// curl) applying at once would interleave two generations over the same three
// paths, and the file that survived would be neither.
var writeMu sync.Mutex

// build resolves a form all the way to "here is what would be written", and
// returns the payload the page renders either way.
//
// The RENDER ORDER is the terminal's, deliberately: both proxy configs are
// produced BEFORE anything is written, so an answer the Caddyfile refuses (an
// acme mode with no contact address, a template someone stripped the markers out
// of) stops the run rather than leaving a rewritten env file beside a proxy
// config that was never generated. Apply below writes only what build has
// already produced in full.
func (s *Server) build(src sources, form Form) (*plan, ReviewResponse) {
	out := ReviewResponse{OverwriteRequired: src.outExists}

	res, err := setup.Generate(setup.Request{
		Template: src.tmpl,
		Existing: src.existing,
		Answers:  BuildAnswers(form, src.tmpl, src.existing),
	})
	if err != nil {
		var invalid *setup.ValidationError
		if errors.As(err, &invalid) {
			// EVERY problem, not the first: an operator fixing a generated file
			// sees all of them at once, which is what the engine's two-pass
			// reporting shape is for and what makes a wizard's review step worth
			// looking at.
			for _, is := range invalid.Issues {
				out.Issues = append(out.Issues, IssueJSON{Var: is.Var, Msg: is.Msg})
			}
			return nil, out
		}
		out.Error = strings.TrimPrefix(err.Error(), "setup: ")
		return nil, out
	}

	p := &plan{res: res}
	if s.opt.RenderProxy != nil {
		p.caddy, p.nginx, err = s.opt.RenderProxy(res.Values)
		if err != nil {
			out.Error = strings.TrimPrefix(err.Error(), "setup: ")
			return nil, out
		}
	}

	mode := strings.TrimSpace(res.Values["VIDRA_TLS_MODE"])
	if mode == "" {
		mode = setup.TLSModeACME
	}
	p.files = []FileJSON{{
		Path: s.opt.OutputPath,
		Mode: "0600",
		What: "the deployment's environment file. Every secret this instance has lives in it, which is why it is written 0600 and why re-running preserves every value it already sets.",
	}}
	if p.caddy != nil {
		p.files = append(p.files, FileJSON{
			Path: s.opt.CaddyOutPath,
			Mode: "0644",
			What: "the managed reverse proxy's configuration, TLS " + mode + ". The caddy service bind-mounts it; a deployment that is already running needs a `caddy reload` to pick it up.",
		})
	}
	if p.nginx != nil {
		// Said in the same breath as the file, because the ONE thing that goes
		// wrong here is an operator treating a .example as a deployed file and
		// wondering why editing it changes nothing.
		p.files = append(p.files, FileJSON{
			Path: s.opt.NginxOutPath,
			Mode: "0644",
			What: "an EXAMPLE for your own proxy, mounted by nothing. Copy it into that proxy's configuration, fill in the two TLS lines and reload it — the managed caddy does not run in VIDRA_TLS_MODE=" + setup.TLSModeExternal + ".",
		})
	}

	out.OK = true
	out.Warnings = res.Warnings
	out.Secrets = SecretsJSON{
		Generated: sorted(res.Generated),
		Preserved: sorted(res.Preserved),
		Rotated:   sorted(res.Rotated),
		Carried:   sorted(res.Carried),
	}
	out.Files = p.files
	out.Profiles = strings.Fields(res.Values["VIDRA_COMPOSE_PROFILES"])
	out.Origin = strings.TrimSpace(res.Values["PUBLIC_BASE_URL"])
	return p, out
}

func sorted(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// handleReview is the dry run. It writes nothing, and it is the step where the
// operator agrees to the overwrite — while looking at what the overwrite would
// change, rather than at a flag on a command line.
func (s *Server) handleReview(w http.ResponseWriter, r *http.Request) {
	var form Form
	if !decode(w, r, &form) {
		return
	}
	src, err := s.load()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_, out := s.build(src, form)
	// 200 either way: a configuration with problems is a successful REVIEW —
	// listing them is the endpoint's job. The page gates on ok, not on the status
	// code.
	writeJSON(w, http.StatusOK, out)
}

// handleApply writes the files.
//
// THE ORDER IS THE TERMINAL'S, and so are the modes: env at 0600 (it holds every
// secret), then the proxy config at 0644 (it holds none, and is bind-mounted
// into a container that does not run as the user who generated it). Both proxy
// renders have already happened inside build, so nothing here can leave a
// half-written tree.
//
// Secrets are never re-minted, whatever the operator ticks: the file being
// overwritten is ALWAYS a preservation source (see load), so the KEKs that seal
// data in the database survive a re-run by construction rather than by
// remembering to ask.
func (s *Server) handleApply(w http.ResponseWriter, r *http.Request) {
	var req ApplyRequest
	if !decode(w, r, &req) {
		return
	}
	writeMu.Lock()
	defer writeMu.Unlock()

	src, err := s.load()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// The gate is INTENT, not safety — every value the file sets is preserved
	// either way — and it is the wizard's spelling of `vidra setup --yes`.
	// Enforced here and not only in the page, so a caller driving this API with
	// curl cannot rewrite a live deployment's env file by leaving a field out.
	if src.outExists && !req.Overwrite {
		writeError(w, http.StatusConflict, s.opt.OutputPath+" already exists and is the file a running deployment reads. Go back to Review and confirm the overwrite — every value it already sets, including the KEKs that seal data in the database, is preserved either way.")
		return
	}
	p, out := s.build(src, req.Form)
	if p == nil {
		// A configuration that would not boot. 422 rather than 200: nothing was
		// written, and a page that treated this as success would show a Success
		// step for a deployment that does not exist.
		writeJSON(w, http.StatusUnprocessableEntity, ApplyResponse{ReviewResponse: out})
		return
	}

	if err := setup.WriteFile(s.opt.OutputPath, p.res.Content); err != nil {
		writeError(w, http.StatusInternalServerError, strings.TrimPrefix(err.Error(), "setup: "))
		return
	}
	written := []FileJSON{p.files[0]}
	for _, f := range []struct {
		content []byte
		file    FileJSON
	}{
		{p.caddy, fileFor(p.files, s.opt.CaddyOutPath)},
		{p.nginx, fileFor(p.files, s.opt.NginxOutPath)},
	} {
		if f.content == nil {
			continue
		}
		if err := setup.WriteCaddyfile(f.file.Path, f.content); err != nil {
			// The env file IS on disk. Saying so is the difference between an
			// operator re-running this (harmless — every value is preserved) and
			// one hunting for a file that is already there.
			writeError(w, http.StatusInternalServerError,
				s.opt.OutputPath+" was written, but "+f.file.Path+" could not be: "+strings.TrimPrefix(err.Error(), "setup: "))
			return
		}
		written = append(written, f.file)
	}

	origin := out.Origin
	if origin == "" {
		origin = "https://<your domain>"
	}
	writeJSON(w, http.StatusOK, ApplyResponse{
		ReviewResponse: out,
		Written:        written,
		// The admin account is NOT in the file and cannot be: the api mints a
		// one-time owner-claim token at boot and prints it to its own log, and
		// until it is redeemed every signup path answers 403. Same handoff the
		// terminal's report() prints, in the same words.
		ClaimURL: strings.TrimSuffix(origin, "/") + "/setup/claim",
	})
}

// fileFor finds a planned file by path. The plan is three entries long; a map
// would be more machinery than the lookup is worth, and the list order is what
// the page renders.
func fileFor(files []FileJSON, path string) FileJSON {
	for _, f := range files {
		if f.Path == path {
			return f
		}
	}
	return FileJSON{Path: path}
}
