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

// --- effective search config accessors (search-service W4) ---
//
// searchEnabled reports whether the vidra-search client is wired (SEARCH_SERVICE_
// URL set). When false every search surface degrades to local behaviour.
func (s *Server) searchEnabled() bool { return s.searchClient != nil }

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
	limit := clampInt(queryInt(c, "limit", 10), 1, 20)
	resp := suggestionsResponse{Query: q, Suggestions: []suggestionView{}}

	if q == "" || !s.searchEnabled() || !s.instanceSuggestionsEnabled() {
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
		HideSensitive:  s.hideSensitiveVideos(),
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
func (s *Server) hydrateRankedRecs(ctx context.Context, items []searchclient.RecItem, viewerID uuid.UUID, authed bool, limit int) ([]recItemView, bool) {
	ids := make([]uuid.UUID, 0, len(items))
	reasonByID := make(map[uuid.UUID]string, len(items))
	for _, it := range items {
		ids = append(ids, it.VideoID)
		reasonByID[it.VideoID] = it.Reason
	}
	feed, err := s.videosvc.HydrateByIDs(ctx, ids, viewerID, authed, s.hideSensitiveVideos())
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
	limit := clampInt(queryInt(c, "limit", 20), 1, 50)
	ctx := c.Request().Context()
	viewerID, _, authed := principalFromContext(c)
	userID, prefs, _ := s.searchUserPrefs(c)
	personalized := s.instancePersonalizedRecs() && authed && prefs.PersonalizedRecs

	if s.searchEnabled() {
		var uid *uuid.UUID
		if authed {
			uid = &userID
		}
		out, err := s.searchClient.RecommendationsHome(ctx, searchclient.RecsParams{
			UserID:        uid,
			SessionID:     sessionIDFromRequest(c),
			Limit:         overfetchCount(0, limit),
			Personalized:  personalized,
			HideSensitive: s.hideSensitiveVideos(),
		})
		if err == nil {
			if views, ok := s.hydrateRankedRecs(ctx, out.Items, viewerID, authed, limit); ok && len(views) > 0 {
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
	items, err := s.videosvc.ListPublic(ctx, "trending", "local",
		video.FeedFilter{HideSensitive: s.hideSensitiveVideos()}, viewerID, authed, int32(limit), 0)
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
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	limit := clampInt(queryInt(c, "limit", 12), 1, 50)
	ctx := c.Request().Context()
	viewerID, _, authed := principalFromContext(c)
	userID, prefs, _ := s.searchUserPrefs(c)
	personalized := s.instancePersonalizedRecs() && authed && prefs.PersonalizedRecs

	if s.searchEnabled() {
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
			HideSensitive: s.hideSensitiveVideos(),
		})
		if err == nil {
			if views, ok := s.hydrateRankedRecs(ctx, out.Items, viewerID, authed, limit); ok && len(views) > 0 {
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
	items, err := s.videosvc.RelatedFallback(ctx, id, v.ChannelID, v.Category, viewerID, authed, s.hideSensitiveVideos(), int32(limit))
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

// searchEventsRequest is the POST /search/events body: a batch of raw event
// objects, each carrying a "type" plus its behavioural fields.
type searchEventsRequest struct {
	Events []map[string]json.RawMessage `json:"events"`
}

// handleSearchEvents accepts a batch of behavioural search events, validates the
// type allowlist + batch cap, enriches each with the caller's user_id / session /
// allow_history flag, and enqueues them to the outbox. Always 202 on success;
// never blocks on the search service. optionalAuth, rate-limited.
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
		if authed {
			payload["user_id"], _ = json.Marshal(id.String())
		}
		if session != "" {
			payload["session_id"], _ = json.Marshal(session)
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

// handleGetSearchHistory proxies the caller's stored search history from
// vidra-search. requireAuth. When the service is disabled/unreachable it answers
// 503 search_unavailable (an honest failure, not a fake empty history).
func (s *Server) handleGetSearchHistory(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	if !s.searchEnabled() {
		return &SearchUnavailableError{}
	}
	limit := clampInt(queryInt(c, "limit", 20), 1, 100)
	offset := queryInt(c, "offset", 0)
	if offset < 0 {
		offset = 0
	}
	out, err := s.searchClient.GetUserHistory(c.Request().Context(), searchclient.HistoryParams{
		UserID: userID, Limit: limit, Offset: offset,
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
// vidra-search. requireAuth. 503 when the service is down (must not silently
// fail).
func (s *Server) handleClearSearchHistory(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	if !s.searchEnabled() {
		return &SearchUnavailableError{}
	}
	if err := s.searchClient.DeleteUserHistory(c.Request().Context(), userID); err != nil {
		return &SearchUnavailableError{}
	}
	return c.NoContent(http.StatusNoContent)
}

// handleDeleteSearchHistoryQuery removes a single normalized query from the
// caller's history. requireAuth. 503 when the service is down.
func (s *Server) handleDeleteSearchHistoryQuery(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	if !s.searchEnabled() {
		return &SearchUnavailableError{}
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

// searchViaService routes a public video search through vidra-search when it is
// wired: it computes the effective flags + mode, over-fetches a ranked id list,
// hydrates under the canonical predicate, and slices the [offset,offset+limit)
// window. ok is false on ANY error (the caller falls back to local SQL). The
// public response contract is unchanged — this is a ranking swap only.
func (s *Server) searchViaService(c echo.Context, q string, filter video.FeedFilter, limit, offset int, viewerID uuid.UUID, authed bool) ([]videoView, bool) {
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
		UserID:        uid,
		SessionID:     sessionIDFromRequest(c),
		Personalized:  personalized,
		HideSensitive: filter.HideSensitive,
		Mode:          s.searchMode(),
	})
	if err != nil {
		return nil, false
	}
	ids := make([]uuid.UUID, 0, len(out.IDs))
	for _, x := range out.IDs {
		ids = append(ids, x.VideoID)
	}
	feed, err := s.videosvc.HydrateByIDs(ctx, ids, viewerID, authed, filter.HideSensitive)
	if err != nil {
		return nil, false
	}
	if offset >= len(feed) {
		return []videoView{}, true
	}
	end := offset + limit
	if end > len(feed) {
		end = len(feed)
	}
	views := make([]videoView, 0, end-offset)
	for _, it := range feed[offset:end] {
		views = append(views, feedItemView(it))
	}
	return views, true
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
