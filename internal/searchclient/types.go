package searchclient

import (
	"time"

	"github.com/google/uuid"
)

// SuggestParams are the GET /internal/v1/suggestions inputs. The personalization/
// history flags are computed IN CORE per request (instance setting AND user pref
// AND signed-in) and passed here — the search service receives flags, never
// policy (MASTER-PLAN §0.5).
type SuggestParams struct {
	Query          string
	Limit          int
	UserID         *uuid.UUID
	SessionID      string
	Personalized   bool
	IncludeHistory bool
	HideSensitive  bool
	Lang           string
}

// Suggestion is one autocomplete row (MASTER-PLAN §1.4/§2.7).
type Suggestion struct {
	Text          string  `json:"text"`
	Type          string  `json:"type"` // query|video|channel|tag|history
	VideoID       *string `json:"video_id,omitempty"`
	ChannelHandle *string `json:"channel_handle,omitempty"`
	IsPersonal    bool    `json:"is_personal"`
}

// SuggestionsResponse is the GET /internal/v1/suggestions body.
type SuggestionsResponse struct {
	Query           string       `json:"query"`
	NormalizedQuery string       `json:"normalized_query"`
	Suggestions     []Suggestion `json:"suggestions"`
	ModelVersion    string       `json:"model_version"`
}

// SearchParams are the GET /internal/v1/search inputs.
type SearchParams struct {
	Query         string
	Limit         int
	Offset        int
	Tag           string
	Category      string
	Language      string
	License       string
	UserID        *uuid.UUID
	SessionID     string
	Personalized  bool
	HideSensitive bool
	Mode          string // simple|advanced
}

// ScoredID is one ranked video id (visibility-safe: id + score only).
type ScoredID struct {
	VideoID uuid.UUID `json:"video_id"`
	Score   float64   `json:"score"`
}

// SearchResponse is the GET /internal/v1/search body.
//
// Total/TotalIsLowerBound/HasMore are the service's paging facts. All three are
// POINTERS, and that is load-bearing: they were added to vidra-search after this
// client shipped, so a deployed search service WILL omit them until both sides
// are released together. A nil field means "the service did not say", which is
// not the same claim as zero or false — a decoded `HasMore=false` on a service
// that never sent the field would stop a client paging through a list it had
// only just started. Every consumer must treat nil as unknown.
//
// See the Response doc comment in vidra-search's internal/search for what the
// three mean when they ARE present: Total counts documents matching the query
// and the request's filters; TotalIsLowerBound marks it "at least this many"
// (advanced mode recalls a capped window, so a hit ceiling makes the count a
// floor); HasMore is exact and is the field to drive "fetch another page".
type SearchResponse struct {
	Query        string     `json:"query"`
	IDs          []ScoredID `json:"ids"`
	ModelVersion string     `json:"model_version"`
	Experiment   string     `json:"experiment,omitempty"`
	// Total is the service's hit count; nil when it was not reported.
	Total *int64 `json:"total,omitempty"`
	// TotalIsLowerBound qualifies Total as a floor rather than an exact count.
	TotalIsLowerBound *bool `json:"total_is_lower_bound,omitempty"`
	// HasMore reports whether a further page would return results.
	HasMore *bool `json:"has_more,omitempty"`
}

// RecsParams are the recommendation-endpoint inputs (home + related share it).
type RecsParams struct {
	VideoID       *uuid.UUID // set for /related, nil for /home
	UserID        *uuid.UUID
	SessionID     string
	Limit         int
	Personalized  bool
	HideSensitive bool
	Lang          string
}

// RecItem is one ranked recommendation (id + score + reason).
type RecItem struct {
	VideoID uuid.UUID `json:"video_id"`
	Score   float64   `json:"score"`
	Reason  string    `json:"reason"` // subscribed|trending|co_watch|similar|fresh|popular
}

// RecommendationsResponse is the recommendation-endpoint body.
type RecommendationsResponse struct {
	Items        []RecItem `json:"items"`
	ModelVersion string    `json:"model_version"`
}

// HistoryParams are the user search-history inputs.
type HistoryParams struct {
	UserID uuid.UUID
	Limit  int
	Offset int
}

// HistoryEntry is one stored search-history row.
type HistoryEntry struct {
	Query           string    `json:"query"`
	NormalizedQuery string    `json:"normalized_query"`
	LastUsedAt      time.Time `json:"last_used_at"`
	UseCount        int       `json:"use_count"`
}

// HistoryResponse is the GET user search-history body.
type HistoryResponse struct {
	Entries []HistoryEntry `json:"entries"`
	Limit   int            `json:"limit"`
	Offset  int            `json:"offset"`
}

// --- suggestion moderation (vidra-search PR #30) ---

// SuggestionBan is the PUT /internal/v1/suggestions/bans/{q} body. NormalizedQuery
// is the aggregate key the SERVICE actually moved, not the string core sent: the
// service normalizes the path segment, so echoing its answer back is what lets a
// later unban target the same row.
type SuggestionBan struct {
	NormalizedQuery string `json:"normalized_query"`
	Banned          bool   `json:"banned"`
}

// SuggestionBanEntry is one row of the reviewable ban list. The counts and the
// first/last-seen window are the evidence a second operator judges a ban on —
// they are aggregate facts about the query string, never per-viewer state.
type SuggestionBanEntry struct {
	NormalizedQuery string    `json:"normalized_query"`
	Query           string    `json:"query"`
	TotalCount      int64     `json:"total_count"`
	DistinctUsers   int       `json:"distinct_users"`
	FirstSeen       time.Time `json:"first_seen"`
	LastSeen        time.Time `json:"last_seen"`
}

// SuggestionBanList is the GET /internal/v1/suggestions/bans body.
type SuggestionBanList struct {
	Entries []SuggestionBanEntry `json:"entries"`
	Limit   int                  `json:"limit"`
	Offset  int                  `json:"offset"`
}
