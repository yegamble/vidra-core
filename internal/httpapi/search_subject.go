package httpapi

import (
	"strings"
	"time"

	"github.com/labstack/echo/v4"
)

// searchSubjectDomainSeparation binds the anonymous search-subject key to this
// single purpose. It MUST differ from every other pseudonym label (notably
// internal/qoe's viewer digest, keyed from the same JWT secret): sharing a label
// would let anyone holding both a qoe_events dump and vidra-search's query log
// join a viewer's playback telemetry to their searches. "/v1" is the label's
// version — changing it makes every existing subject incomparable.
const searchSubjectDomainSeparation = "vidra/search-anon-subject/v1"

// anonSearchSubject returns the aggregation subject for an ANONYMOUS behavioural
// search event, or "" when there is none to derive.
//
// Why it exists: vidra-search's k-anonymity floor — the gate deciding whether a
// query becomes instance-wide autosuggest — counts distinct user_ids plus, for
// rows with no user_id, distinct session_ids. session_id comes from the client's
// X-Vidra-Session header and is validated for UUID SHAPE only, so a client mints
// unlimited well-formed ids and clears the default floor of 3 by rotating the
// header. This subject is derived from the connecting address instead, which the
// client does not choose: c.RealIP() is the IPExtractor installed in New(),
// walking X-Forwarded-For from the RIGHT to the nearest UNTRUSTED hop, so an
// invented header from a direct client is ignored.
//
// The limitation, plainly: this raises the cost of clearing the floor from one
// request to N distinct addresses; it does not close the hole — a proxy pool
// still yields N subjects. Its FAILURE MODE is why it is worth having: a NAT,
// CGNAT or campus egress collapses many real people into one subject, which
// UNDER-counts and yields FEWER suggestions, never more.
//
// It is an ADDITIONAL field, never a replacement for session_id, which carries
// within-session correlation (co-visitation, query reformulation) — collapsing
// everyone behind one NAT into one shared session would splice unrelated
// people's browsing into a single chain.
//
// Authenticated callers get "": user_id is already the trustworthy subject, and
// an address-derived value beside a known account id is both redundant and a
// leak. The address is never stored or logged; only the day-scoped pseudonym
// leaves this function, and "subject_id" is on the observability sensitive-key
// denylist so it cannot be used as a structured-log key either.
func (s *Server) anonSearchSubject(c echo.Context) string {
	if _, _, authed := principalFromContext(c); authed {
		return ""
	}
	// No address to derive from (an unusual transport, a malformed RemoteAddr) is
	// no subject, not a digest of the empty string — otherwise every such request
	// would silently share one subject, which is the wrong kind of collapse.
	ip := strings.TrimSpace(c.RealIP())
	if ip == "" {
		return ""
	}
	return s.searchSubjects.Of(time.Now(), "ip:"+ip)
}
