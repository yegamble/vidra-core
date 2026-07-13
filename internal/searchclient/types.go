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
type SearchResponse struct {
	Query        string     `json:"query"`
	IDs          []ScoredID `json:"ids"`
	ModelVersion string     `json:"model_version"`
	Experiment   string     `json:"experiment,omitempty"`
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
