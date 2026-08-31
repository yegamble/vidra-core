package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/searchclient"
)

// searchModerationGateway is the suggestion-ban subset of the vidra-search
// client. It is declared apart from searchGateway on purpose: the read gateway
// is on every viewer's render path and is faked in the routing truth table,
// while this is a rare operator write. Keeping them separate means wiring or
// faking one never widens the other.
type searchModerationGateway interface {
	BanSuggestion(ctx context.Context, query string) (searchclient.SuggestionBan, error)
	UnbanSuggestion(ctx context.Context, query string) error
	ListSuggestionBans(ctx context.Context, limit, offset int) (searchclient.SuggestionBanList, error)
}

// searchModerationGate returns the gateway to use, or the error the handler must
// surface before touching vidra-search. It follows searchHistoryGate's split
// exactly: the admin toggle being off is a deliberate operator decision and
// answers 403 feature_disabled, while an unwired or unhealthy service is a 503
// search_unavailable. The distinction is load-bearing — a moderator must be able
// to tell "this instance does not use smart search" from "the ban did not land".
//
// A ban that cannot reach the service must NEVER answer 200: a moderator who
// believes an abusive string is out of autosuggest, while it is still being
// suggested, is the failure this whole surface exists to prevent.
func (s *Server) searchModerationGate() (searchModerationGateway, error) {
	if !s.searchServiceEnabled() {
		return nil, &FeatureDisabledError{Feature: "search_service"}
	}
	if !s.searchEnabled() || !s.searchClient.Healthy() {
		return nil, &SearchUnavailableError{}
	}
	gw, ok := s.searchClient.(searchModerationGateway)
	if !ok {
		// A client too old to carry the moderation calls is an honest outage of
		// this surface, not a silent success.
		return nil, &SearchUnavailableError{}
	}
	return gw, nil
}

// suggestionBanView is one reviewable ban. It carries the aggregate counts and
// the first/last-seen window because that is the evidence a second operator
// judges someone else's ban on; it carries no per-viewer state, because a ban is
// global and vidra-search stores none.
type suggestionBanView struct {
	NormalizedQuery string `json:"normalized_query"`
	Query           string `json:"query"`
	TotalCount      int64  `json:"total_count"`
	DistinctUsers   int    `json:"distinct_users"`
	FirstSeen       string `json:"first_seen"`
	LastSeen        string `json:"last_seen"`
}

type suggestionBanListResponse struct {
	Entries []suggestionBanView `json:"entries"`
	Limit   int                 `json:"limit"`
	Offset  int                 `json:"offset"`
}

type suggestionBanResponse struct {
	NormalizedQuery string `json:"normalized_query"`
	Banned          bool   `json:"banned"`
}

// queryFingerprint is the stable audit handle for a banned query. The plaintext
// is deliberately absent from audit_log: a search query is user-authored free
// text that can contain anything a visitor typed, and free-form content never
// enters the security ledger (same rule as a moderator's report prose). The
// digest still lets the ledger be read as pairs — this unban reverses that ban —
// while the reviewable plaintext lives in the ban list, which is the surface an
// operator actually reverses a ban from.
//
// Ban fingerprints the key the SERVICE reports it moved; unban fingerprints the
// key core was asked to lift. Those agree for every unban issued from the ban
// list (the list returns normalized keys), and differ only if an operator
// hand-types an un-normalized display form at the unban route — core does not
// duplicate vidra-search's normalizer.
func queryFingerprint(normalizedQuery string) string {
	sum := sha256.Sum256([]byte(normalizedQuery))
	return hex.EncodeToString(sum[:])
}

// suggestionBanTarget reads the query path segment. Echo has already decoded it;
// a segment that is blank (or only whitespace) is a client error, not a call.
func suggestionBanTarget(c echo.Context) (string, error) {
	q := strings.TrimSpace(c.Param("query"))
	if q == "" {
		return "", &ValidationError{Fields: []FieldError{{
			Field: "query", Message: "must not be empty",
		}}}
	}
	return q, nil
}

// handleListSuggestionBans serves GET /admin/search/suggestion-bans: the queries
// currently suppressed from instance-wide autosuggest. Moderator/admin. A read
// is not a moderation action and writes no audit event.
func (s *Server) handleListSuggestionBans(c echo.Context) error {
	gw, err := s.searchModerationGate()
	if err != nil {
		return err
	}
	page := parsePage(c, 20, 100)
	out, err := gw.ListSuggestionBans(c.Request().Context(), page.Limit, page.Offset)
	if err != nil {
		return &SearchUnavailableError{}
	}
	entries := make([]suggestionBanView, 0, len(out.Entries))
	for _, e := range out.Entries {
		entries = append(entries, suggestionBanView{
			NormalizedQuery: e.NormalizedQuery,
			Query:           e.Query,
			TotalCount:      e.TotalCount,
			DistinctUsers:   e.DistinctUsers,
			FirstSeen:       e.FirstSeen.UTC().Format(time.RFC3339),
			LastSeen:        e.LastSeen.UTC().Format(time.RFC3339),
		})
	}
	return c.JSON(http.StatusOK, suggestionBanListResponse{
		Entries: entries, Limit: out.Limit, Offset: out.Offset,
	})
}

// handleBanSuggestion serves PUT /admin/search/suggestion-bans/{query}: suppress
// a query from instance-wide autosuggest. Moderator/admin — this is the same
// lever class as blocking a video, and gating it like an instance setting would
// hand the person on shift a 403 at the moment they need it.
//
// PUT, not POST: banning the same query twice is one end state, so a client that
// retries a timed-out ban cannot double-apply anything.
func (s *Server) handleBanSuggestion(c echo.Context) error {
	actorID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	query, err := suggestionBanTarget(c)
	if err != nil {
		return err
	}
	gw, gerr := s.searchModerationGate()
	if gerr != nil {
		s.auditSuggestionBan(c, observability.ActionSearchSuggestionBan,
			observability.ResultFailure, actorID.String(), query, gerr)
		return gerr
	}
	out, err := gw.BanSuggestion(c.Request().Context(), query)
	if err != nil {
		s.auditSuggestionBan(c, observability.ActionSearchSuggestionBan,
			observability.ResultFailure, actorID.String(), query, err)
		return suggestionBanUpstreamError(err)
	}
	// Fingerprint the key the SERVICE moved, not the operator's input.
	s.auditSuggestionBan(c, observability.ActionSearchSuggestionBan,
		observability.ResultSuccess, actorID.String(), out.NormalizedQuery, nil)
	return c.JSON(http.StatusOK, suggestionBanResponse{
		NormalizedQuery: out.NormalizedQuery, Banned: out.Banned,
	})
}

// handleUnbanSuggestion serves DELETE /admin/search/suggestion-bans/{query}:
// lift a ban. Idempotent. It does not re-suggest the query — vidra-search
// re-earns suggestibility from real distinct-user counts, so an unban can never
// promote a string that never cleared the aggregation threshold.
func (s *Server) handleUnbanSuggestion(c echo.Context) error {
	actorID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	query, err := suggestionBanTarget(c)
	if err != nil {
		return err
	}
	gw, gerr := s.searchModerationGate()
	if gerr != nil {
		s.auditSuggestionBan(c, observability.ActionSearchSuggestionUnban,
			observability.ResultFailure, actorID.String(), query, gerr)
		return gerr
	}
	if err := gw.UnbanSuggestion(c.Request().Context(), query); err != nil {
		s.auditSuggestionBan(c, observability.ActionSearchSuggestionUnban,
			observability.ResultFailure, actorID.String(), query, err)
		return suggestionBanUpstreamError(err)
	}
	s.auditSuggestionBan(c, observability.ActionSearchSuggestionUnban,
		observability.ResultSuccess, actorID.String(), query, nil)
	return c.NoContent(http.StatusNoContent)
}

// auditSuggestionBan records one ban/unban attempt. Failures are recorded too:
// after an outage an operator has to be able to see that a ban was ATTEMPTED and
// did not land, which is precisely the state a silent 503 would hide.
func (s *Server) auditSuggestionBan(c echo.Context, action, result, actorID, normalizedQuery string, cause error) {
	reason := ""
	if cause != nil {
		reason = suggestionBanFailureReason(cause)
	}
	s.auditEvent(c, audit.Event{
		Action: action, Result: result, ActorID: actorID, Reason: reason,
		ResourceType: "search_query", ResourceID: queryFingerprint(normalizedQuery),
	})
}

// suggestionBanFailureReason maps a failure to a stable classification code. It
// never carries an upstream body or the query.
func suggestionBanFailureReason(err error) string {
	var fd *FeatureDisabledError
	if errors.As(err, &fd) {
		return "feature_disabled"
	}
	var he *searchclient.HTTPError
	if errors.As(err, &he) {
		return "search_rejected"
	}
	return "search_unavailable"
}

// suggestionBanUpstreamError maps a client failure to the response. A 4xx means
// the service is up and rejected the request (it normalized the query away to
// nothing) — that is the caller's error, 422, not a fake outage. Everything else
// is an honest 503.
func suggestionBanUpstreamError(err error) error {
	var he *searchclient.HTTPError
	if errors.As(err, &he) && he.Status == http.StatusUnprocessableEntity {
		return &ValidationError{Fields: []FieldError{{
			Field: "query", Message: "is empty after normalization",
		}}}
	}
	return &SearchUnavailableError{}
}
