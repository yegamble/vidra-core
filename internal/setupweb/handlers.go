package setupweb

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/vidra/vidra-core/internal/setup"
)

// domainCheckTimeout bounds the whole DNS report. Short on purpose, and the same
// budget the terminal interview uses: an operator is watching a form field, and
// this is a nicety rather than a dependency.
const domainCheckTimeout = 5 * time.Second

// sources is the template and the file being written, read fresh on every
// request.
//
// FRESH, not cached, and that is deliberate. A wizard is open for minutes while
// an operator creates a DNS record in another tab; the deployment tree can
// change under it (a colleague running `vidra setup` in a terminal, a restored
// backup), and a cached template would generate from a file that no longer
// exists. Re-reading costs two small files per request and removes a whole class
// of "the wizard wrote something the tree does not agree with".
type sources struct {
	tmpl      *setup.EnvFile
	existing  *setup.EnvFile
	outExists bool
}

// load reads them, applying runSetup's preservation rule EXACTLY: the file being
// written is always a preservation source when it exists, because it holds the
// KEKs that seal data in the database. There is no --from here — the wizard has
// no second-source question — so it is that file or nothing.
//
// The nil-vs-empty distinction is load-bearing and is left to MergeSources: nil
// means NO FILE, which is the only state in which Generate will mint a blank
// KEK. A truncated production.env parses to zero assignments and must still
// count as a deployment that exists.
func (s *Server) load() (sources, error) {
	var out sources
	tmplBytes, err := os.ReadFile(s.opt.TemplatePath)
	if err != nil {
		return out, fmt.Errorf("the deployment template %s could not be read (%s). Run the wizard from the deployment directory — the checkout that holds docker-compose.yml, deploy/ and env/", s.opt.TemplatePath, reason(err))
	}
	out.tmpl, err = setup.ParseEnvFile(tmplBytes)
	if err != nil {
		return out, errors.New(strings.TrimPrefix(err.Error(), "setup: "))
	}
	outBytes, err := os.ReadFile(s.opt.OutputPath)
	switch {
	case err == nil:
		out.outExists = true
		f, perr := setup.ParseEnvFile(outBytes)
		if perr != nil {
			return out, fmt.Errorf("%s already exists but could not be parsed: %s. Fix or move it before generating over the top of it — every value it sets, including the KEKs, is supposed to be preserved, and a file this cannot read is a file whose secrets would be lost", s.opt.OutputPath, strings.TrimPrefix(perr.Error(), "setup: "))
		}
		out.existing = setup.MergeSources(f)
	case errors.Is(err, os.ErrNotExist):
		// A first install. out.existing stays nil, which is what tells Generate
		// there is no database whose rows a freshly minted KEK could orphan.
	default:
		return out, fmt.Errorf("%s exists but could not be read (%s)", s.opt.OutputPath, reason(err))
	}
	return out, nil
}

// reason reduces a filesystem error to the half an operator can act on.
func reason(err error) string {
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "it does not exist"
	case errors.Is(err, os.ErrPermission):
		return "permission denied"
	default:
		return "it could not be read"
	}
}

// handleState is the Welcome step: what this deployment looks like right now,
// and what every form field should open with.
func (s *Server) handleState(w http.ResponseWriter, _ *http.Request) {
	src, err := s.load()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, StateResponse{
		Template:      s.opt.TemplatePath,
		Output:        s.opt.OutputPath,
		OutputExists:  src.outExists,
		Seed:          SeedFor(src.tmpl, src.existing),
		Secrets:       secretNames(src.existing),
		DeployCommand: s.opt.DeployCommand,
	})
}

// handleValidate is one field, through the ENGINE's own validator and nothing
// else.
//
// This endpoint is the whole of ruling "one engine, no second validation" made
// concrete. A wizard that checked a domain with its own regex would accept
// answers Generate then refuses — after every other question had been answered,
// which is the failure the terminal's askValid was written to prevent. So the
// field list below is short by design: it is exactly the answers internal/setup
// exports a validator for, and a field that is not in it is a 400 rather than an
// invitation to write one here.
func (s *Server) handleValidate(w http.ResponseWriter, r *http.Request) {
	var req ValidateRequest
	if !decode(w, r, &req) {
		return
	}
	var (
		normalized string
		err        error
	)
	switch req.Field {
	case "tls_mode":
		normalized, err = setup.NormalizeTLSMode(req.Value)
	case "domain":
		// The MODE is the context, and the order is load-bearing: it decides which
		// scheme a bare host is normalised to, so plain-http's video.lan and
		// acme's video.example.org are validated by the same function with
		// different answers. This is why the wizard asks for the mode first.
		normalized, err = setup.NormalizeOriginForMode(req.Value, req.Context.TLSMode)
	case "acme_email":
		normalized = strings.TrimSpace(req.Value)
		err = setup.CheckAcmeEmail(req.Value)
	default:
		writeError(w, http.StatusBadRequest, fmt.Sprintf("%q is not a field this wizard validates one at a time. The engine exports a validator for the TLS mode, the domain and the ACME contact address; everything else is checked over the whole answer set at the Review step, which is where its problems belong", req.Field))
		return
	}
	if err != nil {
		// The engine's own message, minus its package prefix — the same reduction
		// the terminal's askValid prints. It is already addressed to an operator
		// and says what to write instead, so replacing it with a friendlier
		// sentence would be replacing the accurate one.
		writeJSON(w, http.StatusOK, ValidateResponse{Error: strings.TrimPrefix(err.Error(), "setup: ")})
		return
	}
	writeJSON(w, http.StatusOK, ValidateResponse{OK: true, Normalized: normalized})
}

// checkDomainRequest is the DNS feedback's input. The TLS mode is part of it
// because the SKIP is a rule, not a UI choice — see below.
type checkDomainRequest struct {
	Domain  string `json:"domain"`
	TLSMode string `json:"tls_mode"`
}

// handleCheckDomain is ONE line about whether the domain points here yet.
//
// It is FEEDBACK, NEVER A GATE, exactly as the terminal's reportDomainDNS is:
// DNS that does not point here yet is a completely ordinary state of a fresh
// install, and refusing it would refuse the install the next step already has an
// answer for. The response therefore has no "ok" field to gate on — a caller
// gets a status and a sentence, and the wizard prints them.
//
// The skip is here rather than in the page's JavaScript because it is the
// terminal's rule and there should be one copy of it: plain-http instances are
// LAN/lab by definition (the name may resolve only on an internal resolver, or
// be an IP) and external ones point at somebody else's terminator, so a ⚠ for
// either is a warning that is EXPECTED — and an expected warning teaches an
// operator to ignore the line.
func (s *Server) handleCheckDomain(w http.ResponseWriter, r *http.Request) {
	var req checkDomainRequest
	if !decode(w, r, &req) {
		return
	}
	switch strings.TrimSpace(req.TLSMode) {
	case setup.TLSModePlainHTTP:
		writeJSON(w, http.StatusOK, DomainReport{
			Status:  "skipped",
			Message: "no DNS check: a plain-http instance is a LAN or lab deployment, so its name may resolve only on an internal resolver — or be an address rather than a name.",
		})
		return
	case setup.TLSModeExternal:
		writeJSON(w, http.StatusOK, DomainReport{
			Status:  "skipped",
			Message: "no DNS check: with an external terminator the domain points at your own proxy, not at this host, so the two are not supposed to match.",
		})
		return
	}
	if strings.TrimSpace(req.Domain) == "" {
		writeJSON(w, http.StatusOK, DomainReport{Status: "skipped", Message: "no domain to check yet."})
		return
	}
	if s.opt.CheckDomain == nil {
		writeJSON(w, http.StatusOK, DomainReport{Status: "warn", Message: "this build cannot check DNS from here."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), domainCheckTimeout)
	defer cancel()
	writeJSON(w, http.StatusOK, s.opt.CheckDomain(ctx, strings.TrimSpace(req.Domain)))
}

// handleDoctor is the System Check step: the host-side facts, run fresh.
//
// It is NOT A GATE, and the page says so in the same words `vidra doctor` does:
// a ✗ here blocks nothing, because half of what doctor checks is about a
// deployment that does not exist yet on a first install. What it is for is the
// two findings that would otherwise be discovered by a failed deploy — a Compose
// older than 2.24, a disk with no room for the images.
func (s *Server) handleDoctor(w http.ResponseWriter, r *http.Request) {
	if s.opt.Doctor == nil {
		writeError(w, http.StatusNotImplemented, "this build has no host checks wired in")
		return
	}
	report, err := s.opt.Doctor(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, report)
}
