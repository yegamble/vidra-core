package setupweb

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// shell is the whole user interface: one hand-written, dependency-free HTML
// document with its CSS and JS inline.
//
// INLINE IS THE POINT, not a shortcut. A wizard that fetched a stylesheet, a
// font or a framework would be a wizard that needs the internet at the exact
// moment an operator is configuring a host that may not have it yet — and every
// external reference is another origin the Content-Security-Policy above would
// have to allow. One document also means one token-gated route: a subresource
// cannot carry the X-Setup-Token header, so anything served separately would
// either need the token in its URL or need to be served without one.
//
//go:embed ui/index.html
var shell []byte

// routes is the wizard's whole surface. Everything below it is already past the
// guard, so a handler here may assume the operator's own browser (or their curl)
// is on the other end.
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	// {$} rather than a bare "/": ServeMux's rooted-subtree pattern would match
	// every path this server does not define, and the shell answering /api/state
	// with a page of HTML is a worse answer than 404.
	mux.HandleFunc("GET /{$}", s.handleShell)
	mux.HandleFunc("GET /api/state", s.handleState)
	mux.HandleFunc("POST /api/validate", s.handleValidate)
	mux.HandleFunc("POST /api/check-domain", s.handleCheckDomain)
	mux.HandleFunc("POST /api/doctor", s.handleDoctor)
	mux.HandleFunc("POST /api/review", s.handleReview)
	mux.HandleFunc("POST /api/apply", s.handleApply)
	mux.HandleFunc("POST /api/finish", s.handleFinish)
	return mux
}

func (s *Server) handleShell(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(shell)
}

// handleFinish is the Success step's shutdown. The response is written and
// flushed BEFORE the shutdown starts, so the page gets its confirmation rather
// than a dropped connection it would have to guess about.
func (s *Server) handleFinish(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status": "finished",
		"detail": "the wizard has stopped. This page is now inert; the terminal that started it has printed what to do next.",
	})
	flush(w)
	s.Shutdown(ReasonFinished)
}

// writeJSON is every handler's reply. The status code is explicit at each call
// site because several of them are refusals, and a refusal that arrives as 200
// with an error field inside it is one a page can forget to read.
func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

// writeError is a refusal in the shape the page renders as a role="alert".
func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// decode reads a JSON request body, bounded.
//
// The bound is not a micro-optimisation: this server is a credential, and a
// handler that will allocate whatever it is sent is a way to take the host down
// while the operator is watching a progress bar. A megabyte is orders of
// magnitude more than the largest form here.
func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("the request body is not the document this endpoint expects: %s", err))
		return false
	}
	return true
}

// flush pushes what has been written so far at the client. It is a no-op on a
// ResponseWriter that cannot do it, which is only ever a test's.
func flush(w http.ResponseWriter) {
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}
