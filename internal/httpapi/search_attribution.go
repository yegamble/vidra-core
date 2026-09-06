package httpapi

import (
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

// --- the A13 opt-out ruling: opt-out means no attributed collection ---
//
// The three discovery controls (search_history_enabled,
// personalized_search_enabled, personalized_recommendations_enabled) all
// default TRUE — they are opt-OUT switches. Until this rule they gated only
// what vidra-search SERVED back: an opted-out user got no history page, no
// personal suggestions and an anonymous home rail, while every search and play
// they made still landed in query_log and behavior_events under their raw
// account UUID, still counted toward the instance-wide k-anonymity floors, and
// still keyed trending and co-visitation to their account. The owner ruled that
// the switch means collection too.
//
// The rule, in one paragraph. A signed-in caller's behavioural search events
// are ATTRIBUTED — they carry user_id — only while at least one of their three
// controls is still on; with all three off the event is built exactly as an
// anonymous visitor's would be: no user_id, no account-derived value under any
// key, and the day-scoped anonymous subject core already derives for anonymous
// callers alongside the client's session id, so vidra-search counts them ONCE
// in every k floor however many session ids they rotate through and never keys
// trending or co-visitation to the account. Each surviving control then feeds
// only its own store: allow_history gates the user's own search-history page,
// allow_personalization gates the watch projection personalization reads. The
// decision is taken per event at emit time, so it is forward-only — re-enabling
// resumes attribution from that moment and changes nothing already written.
//
// Two deliberate non-extensions, because both look like they belong here:
//
//   - The INSTANCE-level switches gate serving, not attribution. An operator
//     turning personalization off for everyone is not the user withdrawing
//     consent, and making it retract attribution would silently anonymize whole
//     instances on a settings toggle. They do gate the allow_* flags, exactly
//     as before.
//   - search_mode is not consulted either. A simple-mode instance serves no
//     personalization, but the projection is a durable store built over months;
//     dropping it on the operator's mode would leave an instance that flips to
//     advanced with nothing to personalize FROM, for users who never asked for
//     that. Mode gates serving (searchAdvanced, recsPersonalized); consent
//     gates collection.

// searchEventIdentity is the identity core stamps on one behavioural search
// event. It is the whole answer for a request: which fields the payload carries
// and which of the two durable per-user stores the event may feed.
//
// UserID is empty for an unattributed event and SubjectID is empty for an
// attributed one — they are mutually exclusive by construction, because a
// subject beside a known account id is both redundant and a leak of the
// derivation for a named user (see search_subject.go).
type searchEventIdentity struct {
	UserID    string
	SessionID string
	SubjectID string
	// AllowHistory gates search.user_search_history — the rows behind the
	// caller's own /settings/search page, and nothing else.
	AllowHistory bool
	// AllowPersonalization gates search.user_watch_projection — the durable
	// per-user watch vector every personalized generator keys off. It was
	// previously gated by allow_history, which crossed the streams: a user with
	// history ON and both personalization controls OFF had a projection built
	// for them that no feature they had enabled could ever read.
	AllowPersonalization bool
}

// Attributed reports whether the event carries the caller's account id.
func (i searchEventIdentity) Attributed() bool { return i.UserID != "" }

// searchEventIdentity resolves the rule above for the current request. It is
// the SINGLE decision point: every behavioural emit path in core goes through
// it (POST /search/events, the routed GET /videos/search emit, and the
// authenticated PUT /videos/{id}/watch-progress emit), because a rule applied
// on some paths is a rule applied on none — one browser search alone writes two
// query_log rows through two different paths.
func (s *Server) searchEventIdentity(c echo.Context) searchEventIdentity {
	userID, prefs, authed := s.searchUserPrefs(c)
	out := searchEventIdentity{SessionID: sessionIDFromRequest(c)}
	if !authed || !prefs.attributable() {
		// Anonymous, or signed in with every control off: identical shapes, and
		// deliberately so — "stored exactly as an anonymous caller's would be" is
		// the promise the settings page now makes.
		out.SubjectID = s.unattributedSearchSubject(c)
		return out
	}
	out.UserID = userID.String()
	out.AllowHistory = s.instanceSearchHistoryEnabled() && prefs.History
	out.AllowPersonalization = (s.instancePersonalizedSearch() && prefs.Personalized) ||
		(s.instancePersonalizedRecs() && prefs.PersonalizedRecs)
	return out
}

// attributable reports whether ANY of the caller's three discovery controls is
// still on. It is the consent test for writing their account id at all, and it
// is deliberately an OR: a user who keeps their search history but turns
// personalization off still needs attributed rows, because the history page is
// built from them.
func (p searchUserPref) attributable() bool {
	return p.History || p.Personalized || p.PersonalizedRecs
}

// searchServiceUserID is the account id core sends to vidra-search on a READ
// call (suggestions, search, recommendations), or nil when it must send none.
//
// A read stores no row, so this is not what the ruling turns on — but handing
// the service an account id in a request line beside the caller's query text,
// for a user who has asked not to be attributed, is collection by another name
// (an access log is a log of that pair). Every flag that would USE the id is
// already false for such a caller — personalized and include_history are both
// computed from the same prefs — so nothing is lost by withholding it; what
// changes is that advanced mode's experiment bucketing falls back to the
// session, which is the correct subject for someone who is not being attributed.
func searchServiceUserID(userID uuid.UUID, prefs searchUserPref, authed bool) *uuid.UUID {
	if !authed || !prefs.attributable() {
		return nil
	}
	return &userID
}
