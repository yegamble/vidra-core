package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/instancesettings"
	"github.com/vidra/vidra-core/internal/searchclient"
	"github.com/vidra/vidra-core/internal/searchevents"
	"github.com/vidra/vidra-core/internal/video"
)

// searchGateway is the subset of the vidra-search client the HTTP handlers use,
// plus Healthy() — the active-health signal the W9 routing policy consults. The
// concrete *searchclient.Client satisfies it; unit tests inject a fake with a
// programmable health flag and a per-method call log so the routing truth table
// can be asserted without a live service.
type searchGateway interface {
	Healthy() bool
	// Ready is the on-demand probe behind the admin status page's search
	// component — the service's answer now, not the prober's cached flag.
	Ready(ctx context.Context) error
	Suggestions(ctx context.Context, p searchclient.SuggestParams) (searchclient.SuggestionsResponse, error)
	Search(ctx context.Context, p searchclient.SearchParams) (searchclient.SearchResponse, error)
	RecommendationsHome(ctx context.Context, p searchclient.RecsParams) (searchclient.RecommendationsResponse, error)
	RecommendationsRelated(ctx context.Context, p searchclient.RecsParams) (searchclient.RecommendationsResponse, error)
	GetUserHistory(ctx context.Context, p searchclient.HistoryParams) (searchclient.HistoryResponse, error)
	DeleteUserHistory(ctx context.Context, userID uuid.UUID) error
	DeleteUserHistoryQuery(ctx context.Context, userID uuid.UUID, normalizedQuery string) error
	DeleteUser(ctx context.Context, userID uuid.UUID) error
}

// --- effective search config accessors (search-service W4) ---
//
// searchEnabled reports whether the vidra-search client is wired (SEARCH_SERVICE_
// URL set). When false every search surface degrades to local behaviour.
func (s *Server) searchEnabled() bool { return s.searchClient != nil }

// searchServiceEnabled is the admin runtime toggle (search_service_enabled,
// default true; search-service W9). When an admin flips it off, every gateway
// surface takes its local backup path WITHOUT calling vidra-search — even if the
// service is wired and healthy.
func (s *Server) searchServiceEnabled() bool {
	return s.settingBool(instancesettings.KeySearchServiceEnabled, true)
}

// useSearchService reports whether a gateway surface (search, suggestions, home/
// related recs) should route to vidra-search RIGHT NOW: the client is wired, the
// admin toggle is on, and the client reports Healthy() (active prober up + no
// breaker open). When false the caller takes its backup path with no per-request
// timeout latency. The search-history proxy does NOT use this helper — it must
// distinguish admin-off (403) from service-down (503).
func (s *Server) useSearchService() bool {
	return s.searchEnabled() && s.searchServiceEnabled() && s.searchClient.Healthy()
}

// searchMode is the effective ranking family (simple|advanced).
func (s *Server) searchMode() string {
	return s.settingString(instancesettings.KeySearchMode, instancesettings.DefaultSearchMode)
}

// searchAdvanced reports whether advanced-mode signals (incl. personalization)
// are in play.
func (s *Server) searchAdvanced() bool {
	return s.searchMode() == "advanced"
}

func (s *Server) instanceSuggestionsEnabled() bool {
	return s.settingBool(instancesettings.KeySearchSuggestionsEnabled, true)
}

func (s *Server) instancePersonalizedSearch() bool {
	return s.settingBool(instancesettings.KeyPersonalizedSearchEnabled, true)
}

func (s *Server) instancePersonalizedRecs() bool {
	return s.settingBool(instancesettings.KeyPersonalizedRecommendationsEnabled, true)
}

func (s *Server) instanceSearchHistoryEnabled() bool {
	return s.settingBool(instancesettings.KeySearchHistoryEnabled, true)
}

// searchUserPref is the caller's resolved search preferences (the user half of
// the two-factor personalization gate).
type searchUserPref struct {
	History          bool
	Personalized     bool
	PersonalizedRecs bool
}

// searchUserPrefs resolves the caller's id + prefs. ok is false for anonymous
// callers; an authenticated caller whose row cannot be loaded returns ok=true
// with all-false prefs (fail closed — never personalize on unknown state).
func (s *Server) searchUserPrefs(c echo.Context) (uuid.UUID, searchUserPref, bool) {
	id, _, authed := principalFromContext(c)
	if !authed {
		return uuid.Nil, searchUserPref{}, false
	}
	var prefs searchUserPref
	if s.authsvc != nil {
		if u, err := s.authsvc.UserByID(c.Request().Context(), id); err == nil {
			prefs = searchUserPref{
				History:          u.SearchHistoryEnabled,
				Personalized:     u.PersonalizedSearchEnabled,
				PersonalizedRecs: u.PersonalizedRecommendationsEnabled,
			}
		}
	}
	return id, prefs, true
}

// sessionIDFromRequest returns the sanitized X-Vidra-Session id (a UUID) or "".
// A non-UUID value is dropped so a client cannot inject an arbitrary session key.
func sessionIDFromRequest(c echo.Context) string {
	raw := strings.TrimSpace(c.Request().Header.Get("X-Vidra-Session"))
	if raw == "" {
		return ""
	}
	if _, err := uuid.Parse(raw); err != nil {
		return ""
	}
	return raw
}

// overfetchCount is the visibility-safe over-fetch (MASTER-PLAN §0.2): request
// min(200, (offset+limit)*2 + 10) ids so per-viewer filtering still leaves a full
// page after the canonical predicate drops some.
func overfetchCount(offset, limit int) int {
	n := (offset+limit)*2 + 10
	if n > 200 {
		return 200
	}
	return n
}

// --- GET /api/v1/search/suggestions ---

// suggestionView is one autocomplete row in the public response (§2.7).
type suggestionView struct {
	Text          string  `json:"text"`
	Type          string  `json:"type"`
	VideoID       *string `json:"video_id,omitempty"`
	ChannelHandle *string `json:"channel_handle,omitempty"`
	IsPersonal    bool    `json:"is_personal"`
}

// suggestionsResponse is the GET /search/suggestions body.
type suggestionsResponse struct {
	Query       string           `json:"query"`
	Suggestions []suggestionView `json:"suggestions"`
}

// handleSearchSuggestions proxies autocomplete to vidra-search with core-computed
// personalization/history flags. optionalAuth. Disabled/off/empty-query/timeout/
// error all degrade to a 200 {query, suggestions:[]} — a suggestion box never
// errors.
func (s *Server) handleSearchSuggestions(c echo.Context) error {
	q := strings.TrimSpace(c.QueryParam("q"))
	limit := parseLimit(c, 10, 20)
	resp := suggestionsResponse{Query: q, Suggestions: []suggestionView{}}

	// Backup path (empty list, never an error) when the query is empty, smart
	// search is off/down (admin toggle or health), or suggestions are disabled —
	// all WITHOUT calling the service (W9). Health is folded in via useSearchService.
	if q == "" || !s.useSearchService() || !s.instanceSuggestionsEnabled() {
		return c.JSON(http.StatusOK, resp)
	}
	userID, prefs, authed := s.searchUserPrefs(c)
	personalized := s.searchAdvanced() && s.instancePersonalizedSearch() && authed && prefs.Personalized
	includeHistory := s.instanceSearchHistoryEnabled() && authed && prefs.History
	var uid *uuid.UUID
	if authed {
		uid = &userID
	}
	out, err := s.searchClient.Suggestions(c.Request().Context(), searchclient.SuggestParams{
		Query:          q,
		Limit:          limit,
		UserID:         uid,
		SessionID:      sessionIDFromRequest(c),
		Personalized:   personalized,
		IncludeHistory: includeHistory,
		HideSensitive:  s.effectiveHideSensitive(c),
	})
	if err != nil {
		return c.JSON(http.StatusOK, resp) // silent degrade to empty
	}
	views := make([]suggestionView, 0, len(out.Suggestions))
	for _, sg := range out.Suggestions {
		views = append(views, suggestionView{
			Text:          sg.Text,
			Type:          sg.Type,
			VideoID:       sg.VideoID,
			ChannelHandle: sg.ChannelHandle,
			IsPersonal:    sg.IsPersonal,
		})
	}
	resp.Suggestions = views
	return c.JSON(http.StatusOK, resp)
}

// --- recommendations (home + related) ---

// recItemView is one recommendation card: the existing videoView plus a reason.
type recItemView struct {
	videoView
	Reason string `json:"reason,omitempty"`
}

// recommendationsResponse is the home/related recommendation body.
type recommendationsResponse struct {
	Items        []recItemView `json:"items"`
	Personalized bool          `json:"personalized"`
	Source       string        `json:"source"` // search | fallback
}

// hydrateRankedRecs hydrates a ranked id list to recommendation cards under the
// canonical predicate, preserving search order and capping at limit. Returns the
// cards and whether hydration succeeded.
func (s *Server) hydrateRankedRecs(ctx context.Context, items []searchclient.RecItem, viewerID uuid.UUID, authed bool, limit int, hideSensitive bool) ([]recItemView, bool) {
	ids := make([]uuid.UUID, 0, len(items))
	reasonByID := make(map[uuid.UUID]string, len(items))
	for _, it := range items {
		ids = append(ids, it.VideoID)
		reasonByID[it.VideoID] = it.Reason
	}
	feed, err := s.videosvc.HydrateByIDs(ctx, ids, viewerID, authed, hideSensitive)
	if err != nil {
		return nil, false
	}
	views := make([]videoView, 0, len(feed))
	for _, it := range feed {
		views = append(views, feedItemView(it))
	}
	s.attachIPFSPinned(ctx, views)
	out := make([]recItemView, 0, len(views))
	for i := range views {
		id, _ := uuid.Parse(views[i].ID)
		out = append(out, recItemView{videoView: views[i], Reason: reasonByID[id]})
		if len(out) >= limit {
			break
		}
	}
	return out, true
}

// handleHomeRecommendations returns the "for you"/trending home rail. optionalAuth.
// Tries vidra-search (personalized when the instance + user allow it and the
// caller is signed in); on disable/error/empty falls back to the trending feed.
func (s *Server) handleHomeRecommendations(c echo.Context) error {
	limit := parseLimit(c, 20, 50)
	ctx := c.Request().Context()
	viewerID, _, authed := principalFromContext(c)
	userID, prefs, _ := s.searchUserPrefs(c)
	personalized := s.instancePersonalizedRecs() && authed && prefs.PersonalizedRecs
	hideSensitive := s.effectiveHideSensitive(c)

	if s.useSearchService() {
		var uid *uuid.UUID
		if authed {
			uid = &userID
		}
		out, err := s.searchClient.RecommendationsHome(ctx, searchclient.RecsParams{
			UserID:        uid,
			SessionID:     sessionIDFromRequest(c),
			Limit:         overfetchCount(0, limit),
			Personalized:  personalized,
			HideSensitive: hideSensitive,
		})
		if err == nil {
			if views, ok := s.hydrateRankedRecs(ctx, out.Items, viewerID, authed, limit, hideSensitive); ok && len(views) > 0 {
				return c.JSON(http.StatusOK, recommendationsResponse{Items: views, Personalized: personalized, Source: "search"})
			}
		}
	}
	return s.homeRecommendationsFallback(c, limit, viewerID, authed)
}

// homeRecommendationsFallback serves the trending feed when vidra-search is
// unavailable or returns nothing.
func (s *Server) homeRecommendationsFallback(c echo.Context, limit int, viewerID uuid.UUID, authed bool) error {
	ctx := c.Request().Context()
	items, _, err := s.videosvc.ListPublic(ctx, "trending", "local",
		video.FeedFilter{HideSensitive: s.effectiveHideSensitive(c)}, viewerID, authed, int32(limit), 0)
	if err != nil {
		return err
	}
	views := make([]videoView, 0, len(items))
	for _, it := range items {
		views = append(views, feedItemView(it))
	}
	s.attachIPFSPinned(ctx, views)
	out := make([]recItemView, 0, len(views))
	for i := range views {
		out = append(out, recItemView{videoView: views[i], Reason: "trending"})
	}
	return c.JSON(http.StatusOK, recommendationsResponse{Items: out, Personalized: false, Source: "fallback"})
}

// handleVideoRecommendations returns the related-videos rail for a watch page.
// optionalAuth. Tries vidra-search; on disable/error/empty falls back to the
// server-side same-channel + same-category heuristic.
func (s *Server) handleVideoRecommendations(c echo.Context) error {
	id, err := pathUUID(c, "id", "video not found")
	if err != nil {
		return err
	}
	limit := parseLimit(c, 12, 50)
	ctx := c.Request().Context()
	viewerID, _, authed := principalFromContext(c)
	userID, prefs, _ := s.searchUserPrefs(c)
	personalized := s.instancePersonalizedRecs() && authed && prefs.PersonalizedRecs
	hideSensitive := s.effectiveHideSensitive(c)

	if s.useSearchService() {
		var uid *uuid.UUID
		if authed {
			uid = &userID
		}
		out, err := s.searchClient.RecommendationsRelated(ctx, searchclient.RecsParams{
			VideoID:       &id,
			UserID:        uid,
			SessionID:     sessionIDFromRequest(c),
			Limit:         overfetchCount(0, limit),
			Personalized:  personalized,
			HideSensitive: hideSensitive,
		})
		if err == nil {
			if views, ok := s.hydrateRankedRecs(ctx, out.Items, viewerID, authed, limit, hideSensitive); ok && len(views) > 0 {
				return c.JSON(http.StatusOK, recommendationsResponse{Items: views, Personalized: personalized, Source: "search"})
			}
		}
	}
	return s.videoRecommendationsFallback(c, id, limit, viewerID, authed)
}

// videoRecommendationsFallback serves the same-channel + same-category related
// heuristic (server-side version of the frontend's current logic).
func (s *Server) videoRecommendationsFallback(c echo.Context, id uuid.UUID, limit int, viewerID uuid.UUID, authed bool) error {
	ctx := c.Request().Context()
	v, err := s.videosvc.GetByID(ctx, id)
	if err != nil {
		// Unknown/invisible source video: an empty rail, not an error.
		return c.JSON(http.StatusOK, recommendationsResponse{Items: []recItemView{}, Personalized: false, Source: "fallback"})
	}
	items, err := s.videosvc.RelatedFallback(ctx, id, v.ChannelID, v.Category, viewerID, authed, s.effectiveHideSensitive(c), int32(limit))
	if err != nil {
		return err
	}
	views := make([]videoView, 0, len(items))
	for _, it := range items {
		views = append(views, feedItemView(it))
	}
	s.attachIPFSPinned(ctx, views)
	out := make([]recItemView, 0, len(views))
	for i := range views {
		out = append(out, recItemView{videoView: views[i], Reason: "related"})
	}
	return c.JSON(http.StatusOK, recommendationsResponse{Items: out, Personalized: false, Source: "fallback"})
}

// --- POST /api/v1/search/events ---

// searchEventAllowlist is the set of behavioural event types the public endpoint
// accepts (MASTER-PLAN §2.7).
var searchEventAllowlist = map[string]bool{
	searchevents.TypeSuggestionsShown:  true,
	searchevents.TypeSuggestionClicked: true,
	searchevents.TypeSearchSubmitted:   true,
	searchevents.TypeResultClicked:     true,
	searchevents.TypeVideoImpression:   true,
	searchevents.TypeVideoPlayStarted:  true,
	searchevents.TypeVideoCompleted:    true,
}

// maxSearchEventsPerRequest caps a single behavioural-event batch (§2.7).
const maxSearchEventsPerRequest = 20

// serverDerivedSearchEventFields are the outbox-payload keys POST /search/events
// derives from the request context and NEVER from the body. They are stripped
// from every client event BEFORE the server sets its own values.
//
// Strip-then-set, not conditional-overwrite: an overwrite only fires when the
// server has a value to write, so a forged field survived whenever it did not
// (anonymous caller → no user_id to overwrite; no X-Vidra-Session header → no
// session_id to overwrite). That mattered because vidra-search writes
// payload.user_id into search.query_log ungated, and the k-anonymity floor that
// decides whether a query becomes instance-wide autosuggest counts
// count(DISTINCT ql.user_id) — 20 fabricated ids in one unauthenticated batch
// cleared the default floor of 3. sessionIDFromRequest also validates the UUID
// shape of the HEADER only, so a body-supplied session_id was never validated at
// all. Do not "simplify" this back into four conditional assignments.
//
// subject_id is the anonymous half of the same problem: rotating X-Vidra-Session
// mints unlimited well-formed session ids, which the floor counts for rows with
// no user_id. It is derived from the connecting address instead — see
// anonSearchSubject in search_subject.go for the derivation and for the NAT
// limitation it does not close.
var serverDerivedSearchEventFields = []string{"user_id", "session_id", "subject_id", "allow_history"}

// searchEventsRequest is the POST /search/events body: a batch of raw event
// objects, each carrying a "type" plus its behavioural fields.
type searchEventsRequest struct {
	Events []map[string]json.RawMessage `json:"events"`
}

// handleSearchEvents accepts a batch of behavioural search events, validates the
// type allowlist + batch cap, replaces the server-derived identity fields with
// the caller's own user_id / session / subject / allow_history, and enqueues them
// to the outbox. Always 202 on success; never blocks on the search service.
// optionalAuth, rate-limited.
func (s *Server) handleSearchEvents(c echo.Context) error {
	var in searchEventsRequest
	if err := c.Bind(&in); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "malformed or invalid request body")
	}
	if len(in.Events) == 0 {
		return c.NoContent(http.StatusAccepted)
	}
	if len(in.Events) > maxSearchEventsPerRequest {
		return &ValidationError{Fields: []FieldError{{Field: "events", Message: "at most 20 events per request"}}}
	}
	// Validate every type before enqueuing any (all-or-nothing on validation).
	types := make([]string, len(in.Events))
	var fes []FieldError
	for i, ev := range in.Events {
		var t string
		if raw, ok := ev["type"]; ok {
			_ = json.Unmarshal(raw, &t)
		}
		if !searchEventAllowlist[t] {
			fes = append(fes, FieldError{Field: "events", Message: "unsupported event type"})
			continue
		}
		types[i] = t
	}
	if len(fes) > 0 {
		return &ValidationError{Fields: fes}
	}

	id, _, authed := principalFromContext(c)
	session := sessionIDFromRequest(c)
	// Anonymous callers only: an authenticated caller's user_id is already the
	// trustworthy subject, so a second, address-derived one would be redundant
	// AND would leak that derivation for a known account.
	subject := s.anonSearchSubject(c)
	_, prefs, _ := s.searchUserPrefs(c)
	allowHistory := s.instanceSearchHistoryEnabled() && authed && prefs.History

	for i, ev := range in.Events {
		payload := make(map[string]json.RawMessage, len(ev)+3)
		for k, v := range ev {
			if k == "type" {
				continue
			}
			payload[k] = v
		}
		// Identity is the server's to decide; drop any client-supplied copy first.
		for _, k := range serverDerivedSearchEventFields {
			delete(payload, k)
		}
		if authed {
			payload["user_id"], _ = json.Marshal(id.String())
		}
		if session != "" {
			payload["session_id"], _ = json.Marshal(session)
		}
		// subject_id sits ALONGSIDE session_id, never replacing it: session_id
		// carries within-session correlation (co-visitation, reformulation), and
		// collapsing every visitor behind one NAT into one shared session would
		// splice unrelated people's browsing into a single chain.
		if subject != "" {
			payload["subject_id"], _ = json.Marshal(subject)
		}
		payload["allow_history"], _ = json.Marshal(allowHistory)
		raw, err := json.Marshal(payload)
		if err != nil {
			continue
		}
		s.searchEvents.EnqueueBehavioral(c.Request().Context(), types[i], raw)
	}
	return c.NoContent(http.StatusAccepted)
}

// --- search-history proxy (GET/DELETE /api/v1/me/search-history) ---

// searchHistoryEntryView is one stored search-history row.
type searchHistoryEntryView struct {
	Query           string `json:"query"`
	NormalizedQuery string `json:"normalized_query"`
	LastUsedAt      string `json:"last_used_at"`
	UseCount        int    `json:"use_count"`
}

// searchHistoryResponse is the GET /me/search-history body.
type searchHistoryResponse struct {
	Entries []searchHistoryEntryView `json:"entries"`
	Limit   int                      `json:"limit"`
	Offset  int                      `json:"offset"`
}

// searchHistoryGate returns the error a search-history proxy handler must surface
// before touching vidra-search, or nil when it may proceed (search-service W9).
// The admin toggle being off is a 403 feature_disabled (the operator deliberately
// turned smart search off — this is a feature-disabled state, distinct from an
// outage); a service that is unwired or currently unhealthy is a 503
// search_unavailable (an honest "down", never a fake empty history / silent no-op).
func (s *Server) searchHistoryGate() error {
	if !s.searchServiceEnabled() {
		return &FeatureDisabledError{Feature: "search_service"}
	}
	if !s.searchEnabled() || !s.searchClient.Healthy() {
		return &SearchUnavailableError{}
	}
	return nil
}

// handleGetSearchHistory proxies the caller's stored search history from
// vidra-search. requireAuth. Admin-off → 403 feature_disabled; unwired/unreachable
// → 503 search_unavailable (an honest failure, not a fake empty history).
func (s *Server) handleGetSearchHistory(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	if err := s.searchHistoryGate(); err != nil {
		return err
	}
	page := parsePage(c, 20, 100)
	out, err := s.searchClient.GetUserHistory(c.Request().Context(), searchclient.HistoryParams{
		UserID: userID, Limit: page.Limit, Offset: page.Offset,
	})
	if err != nil {
		return &SearchUnavailableError{}
	}
	entries := make([]searchHistoryEntryView, 0, len(out.Entries))
	for _, e := range out.Entries {
		entries = append(entries, searchHistoryEntryView{
			Query:           e.Query,
			NormalizedQuery: e.NormalizedQuery,
			LastUsedAt:      e.LastUsedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
			UseCount:        e.UseCount,
		})
	}
	return c.JSON(http.StatusOK, searchHistoryResponse{Entries: entries, Limit: out.Limit, Offset: out.Offset})
}

// handleClearSearchHistory clears the caller's entire search history in
// vidra-search. requireAuth. Admin-off → 403 feature_disabled; service down → 503
// search_unavailable (must not silently fail).
func (s *Server) handleClearSearchHistory(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	if err := s.searchHistoryGate(); err != nil {
		return err
	}
	if err := s.searchClient.DeleteUserHistory(c.Request().Context(), userID); err != nil {
		return &SearchUnavailableError{}
	}
	return c.NoContent(http.StatusNoContent)
}

// handleDeleteSearchHistoryQuery removes a single normalized query from the
// caller's history. requireAuth. Admin-off → 403 feature_disabled; service down
// → 503 search_unavailable.
func (s *Server) handleDeleteSearchHistoryQuery(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	if err := s.searchHistoryGate(); err != nil {
		return err
	}
	query := strings.TrimSpace(c.Param("query"))
	if query == "" {
		return echo.NewHTTPError(http.StatusNotFound, "history entry not found")
	}
	if err := s.searchClient.DeleteUserHistoryQuery(c.Request().Context(), userID, query); err != nil {
		return &SearchUnavailableError{}
	}
	return c.NoContent(http.StatusNoContent)
}

// --- handleSearchVideos service routing (search-service W4) ---

// searchServicePaging is vidra-search's own view of the result set, carried out
// of searchViaService so the handler can pass it through. Every field is a
// pointer and the zero value is "nothing known", which is exactly what the
// local SQL path and a search service too old to report them both mean.
type searchServicePaging struct {
	Total             *int64
	TotalIsLowerBound *bool
	HasMore           *bool
}

// searchViaService routes a public video search through vidra-search when it is
// wired: it computes the effective flags + mode, over-fetches a ranked id list,
// hydrates under the canonical predicate, and slices the [offset,offset+limit)
// window. ok is false on ANY error (the caller falls back to local SQL). The
// public response contract is additive — this is still a ranking swap, now with
// the service's paging facts passed through when it reports them.
//
// The caller has already established (searchServiceCanRank) that this request is
// one the service can answer: relevance order, and only the
// tag/category/language/license facets it accepts. Nothing here re-checks that, and nothing here post-filters —
// a filter the service did not apply would produce a page that disagreed with the
// total, so such requests never reach this function.
func (s *Server) searchViaService(c echo.Context, q string, filter video.SearchFilter, limit, offset int, viewerID uuid.UUID, authed bool) ([]videoView, searchServicePaging, bool) {
	ctx := c.Request().Context()
	userID, prefs, _ := s.searchUserPrefs(c)
	personalized := s.searchAdvanced() && s.instancePersonalizedSearch() && authed && prefs.Personalized
	var uid *uuid.UUID
	if authed {
		uid = &userID
	}
	out, err := s.searchClient.Search(ctx, searchclient.SearchParams{
		Query:         q,
		Limit:         overfetchCount(offset, limit),
		Offset:        0,
		Tag:           filter.Tag,
		Category:      filter.Category,
		Language:      filter.Language,
		License:       filter.License,
		UserID:        uid,
		SessionID:     sessionIDFromRequest(c),
		Personalized:  personalized,
		HideSensitive: filter.HideSensitive,
		Mode:          s.searchMode(),
	})
	if err != nil {
		return nil, searchServicePaging{}, false
	}
	// Passed through as received: a field the service omitted stays nil, so
	// "unknown" survives the hop instead of collapsing into 0/false.
	paging := searchServicePaging{
		Total:             out.Total,
		TotalIsLowerBound: out.TotalIsLowerBound,
		HasMore:           out.HasMore,
	}
	ids := make([]uuid.UUID, 0, len(out.IDs))
	for _, x := range out.IDs {
		ids = append(ids, x.VideoID)
	}
	feed, err := s.videosvc.HydrateByIDs(ctx, ids, viewerID, authed, filter.HideSensitive)
	if err != nil {
		return nil, searchServicePaging{}, false
	}
	if offset >= len(feed) {
		return []videoView{}, paging, true
	}
	end := offset + limit
	if end > len(feed) {
		end = len(feed)
	}
	views := make([]videoView, 0, end-offset)
	for _, it := range feed[offset:end] {
		views = append(views, feedItemView(it))
	}
	return views, paging, true
}

// searchConfigSnapshot builds the effective search-settings subset pushed to the
// search service on search.config_updated (search-service W4).
func (s *Server) searchConfigSnapshot() searchevents.SearchConfig {
	return searchevents.SearchConfig{
		SearchMode:                         s.searchMode(),
		SuggestionsEnabled:                 s.instanceSuggestionsEnabled(),
		PersonalizedSearchEnabled:          s.instancePersonalizedSearch(),
		PersonalizedRecommendationsEnabled: s.instancePersonalizedRecs(),
		SearchHistoryEnabled:               s.instanceSearchHistoryEnabled(),
		SearchEventRetentionDays:           s.settingInt(instancesettings.KeySearchEventRetentionDays, 90),
		MinimumQueryUserCount:              s.settingInt(instancesettings.KeySearchMinQueryUserCount, 3),
		HideSensitiveDefault:               s.hideSensitiveVideos(),
	}
}

// emitSearchConfigChangedIfNeeded enqueues a search.config_updated when any of
// the changed setting keys is a search key (search-service W4).
func (s *Server) emitSearchConfigChangedIfNeeded(ctx context.Context, changed []string) {
	for _, k := range changed {
		if instancesettings.IsSearchSettingKey(k) {
			s.searchEvents.EnqueueConfigUpdated(ctx, s.searchConfigSnapshot())
			return
		}
	}
}

// purgeUserFromSearch removes an account's search-side data on hard delete
// (search-service W4): it enqueues the durable user.suppress + user.history_deleted
// events AND issues a best-effort DIRECT DELETE /internal/v1/users/{id}. The
// events guarantee the privacy-critical outcome even if the direct call is lost.
func (s *Server) purgeUserFromSearch(ctx context.Context, userID uuid.UUID) {
	s.searchEvents.EnqueueUserPurge(ctx, userID)
	if s.searchEnabled() {
		if err := s.searchClient.DeleteUser(ctx, userID); err != nil {
			s.logger.WarnContext(ctx, "search user purge direct delete failed", "user_id", userID, "error", err)
		}
	}
}

// emitSearchSubmitted enqueues a search.submitted behavioural event (best-effort;
// the outbox delivers it asynchronously). No-op when the enqueuer is unwired.
func (s *Server) emitSearchSubmitted(c echo.Context, query string, resultsCount int, source string) {
	if s.searchEvents == nil {
		return
	}
	id, _, authed := principalFromContext(c)
	payload := map[string]any{
		"query":         query,
		"results_count": resultsCount,
		"source":        source,
	}
	if authed {
		payload["user_id"] = id.String()
	}
	if sid := sessionIDFromRequest(c); sid != "" {
		payload["session_id"] = sid
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	s.searchEvents.EnqueueBehavioral(c.Request().Context(), searchevents.TypeSearchSubmitted, raw)
}
