// Package searchevents builds and durably enqueues the domain + behavioural
// events vidra-core emits to the vidra-search service (search-service W4,
// MASTER-PLAN §1.5 event schema, §2.2 enqueue seams).
//
// Emission is best-effort and NEVER on the request's critical path: every
// Enqueue* method persists a single row to the search_outbox table (drained
// asynchronously by runSearchOutboxWorker) and, on failure, logs + increments a
// metric rather than surfacing an error to the caller. A search event is
// non-authoritative — the periodic reconcile sweep is the backstop that repairs
// any dropped event — so a search hiccup can never fail a publish, an edit, a
// settings change, or a watch.
package searchevents

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// SchemaVersion is the event envelope schema version (MASTER-PLAN §1.5). It rides
// every enqueued event so the search service can evolve the shape compatibly.
const SchemaVersion = 1

// Event type identifiers (MASTER-PLAN §1.5). Domain events drive the search
// index; behavioural events feed ranking/aggregates. Unknown types are accepted
// and ignored by the search service (forward compatibility), so this set can grow
// without a coordinated deploy.
const (
	// Domain events.
	TypeVideoUpsert    = "video.upsert"
	TypeVideoSuppress  = "video.suppress"
	TypeVideoStats     = "video.stats"
	TypeChannelUpsert  = "channel.upsert"
	TypeChannelDelete  = "channel.delete"
	TypeUserSuppress   = "user.suppress"
	TypeUserHistoryDel = "user.history_deleted"
	TypeConfigUpdated  = "search.config_updated"
	TypeReconcileBegin = "reconcile.begin"
	TypeReconcilePage  = "reconcile.page"
	TypeReconcileEnd   = "reconcile.end"
	TypeVideoWatchProg = "video.watch_progress"

	// Behavioural events (also the POST /api/v1/search/events allowlist).
	TypeSearchSubmitted   = "search.submitted"
	TypeSuggestionsShown  = "search.suggestions_shown"
	TypeSuggestionClicked = "search.suggestion_selected"
	TypeResultClicked     = "search.result_clicked"
	TypeVideoImpression   = "video.impression"
	TypeVideoPlayStarted  = "video.play_started"
	TypeVideoCompleted    = "video.completed"
)

// Suppression reasons (MASTER-PLAN §1.5 video.suppress).
const (
	SuppressDeleted           = "deleted"
	SuppressBlocked           = "blocked"
	SuppressPrivate           = "private"
	SuppressQuarantined       = "quarantined"
	SuppressModerated         = "moderated"
	SuppressOwnerUnlisted     = "owner_unlisted"
	SuppressFederationBlocked = "federation_blocked"
)

// History-deletion scopes (MASTER-PLAN §1.5 user.history_deleted).
const (
	HistoryScopeWatch  = "watch"
	HistoryScopeSearch = "search"
	HistoryScopeAll    = "all"
)

// Envelope is one event as sent in the /internal/v1/events batch (MASTER-PLAN
// §1.4). event_id + occurred_at come from the outbox row (the DB assigns event_id
// so it is the stable cross-service idempotency key); type + payload are stored
// by the enqueuer.
type Envelope struct {
	EventID       uuid.UUID       `json:"event_id"`
	Type          string          `json:"type"`
	OccurredAt    time.Time       `json:"occurred_at"`
	SchemaVersion int             `json:"schema_version"`
	Payload       json.RawMessage `json:"payload"`
}

// EventBatch is the POST /internal/v1/events request body.
type EventBatch struct {
	Events []Envelope `json:"events"`
}

// FailedEvent is one per-event failure the search service reports back so the
// outbox can retry only those, not the whole batch (MASTER-PLAN §1.4).
type FailedEvent struct {
	EventID uuid.UUID `json:"event_id"`
	Code    string    `json:"code"`
	Message string    `json:"message"`
}

// EventBatchResult is the /internal/v1/events response.
type EventBatchResult struct {
	Accepted   int           `json:"accepted"`
	Duplicates int           `json:"duplicates"`
	Failed     []FailedEvent `json:"failed"`
}

// VideoDoc is the full search document for a local video (MASTER-PLAN §1.5
// video.upsert). Eligibility (public+published+owner-not-unlisted) is DERIVED in
// the search service from privacy/state, so the raw fields ride along.
type VideoDoc struct {
	ID              uuid.UUID  `json:"id"`
	Kind            string     `json:"kind"` // always "local" from core
	ChannelID       uuid.UUID  `json:"channel_id"`
	ChannelHandle   string     `json:"channel_handle"`
	ChannelName     string     `json:"channel_name"`
	OwnerID         uuid.UUID  `json:"owner_id"`
	Title           string     `json:"title"`
	Description     string     `json:"description"`
	Tags            []string   `json:"tags"`
	Category        *string    `json:"category,omitempty"`
	Language        *string    `json:"language,omitempty"`
	DurationSeconds *int32     `json:"duration_seconds,omitempty"`
	IsSensitive     bool       `json:"is_sensitive"`
	Privacy         string     `json:"privacy"`
	State           string     `json:"state"`
	PublishedAt     *time.Time `json:"published_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	Views           int64      `json:"views"`
	Likes           int64      `json:"likes"`
}

// videoUpsertPayload wraps a doc for the video.upsert event.
type videoUpsertPayload struct {
	Video VideoDoc `json:"video"`
}

// videoSuppressPayload is the video.suppress event body.
type videoSuppressPayload struct {
	VideoID uuid.UUID `json:"video_id"`
	Reason  string    `json:"reason"`
}

// videoStatsPayload is the (optional, reconcile-driven) video.stats event body.
type videoStatsPayload struct {
	VideoID uuid.UUID `json:"video_id"`
	Views   int64     `json:"views"`
	Likes   int64     `json:"likes"`
}

// channelDoc is the channel.upsert body.
type channelDoc struct {
	ID          uuid.UUID `json:"id"`
	Handle      string    `json:"handle"`
	DisplayName string    `json:"display_name"`
	OwnerID     uuid.UUID `json:"owner_id"`
}

type channelUpsertPayload struct {
	Channel channelDoc `json:"channel"`
}

type channelDeletePayload struct {
	ChannelID uuid.UUID `json:"channel_id"`
}

// userSuppressPayload is the user.suppress body (unlisted true suppresses the
// owner's docs; false restores them).
type userSuppressPayload struct {
	UserID   uuid.UUID `json:"user_id"`
	Unlisted bool      `json:"unlisted"`
}

type userHistoryDeletedPayload struct {
	UserID uuid.UUID `json:"user_id"`
	Scope  string    `json:"scope"`
}

// SearchConfig is the effective search-settings subset pushed on
// search.config_updated (MASTER-PLAN §1.5). trending_half_life_hours is
// deliberately omitted — core owns no such setting; the search service keeps its
// own default.
type SearchConfig struct {
	SearchMode                         string `json:"search_mode"`
	SuggestionsEnabled                 bool   `json:"suggestions_enabled"`
	PersonalizedSearchEnabled          bool   `json:"personalized_search_enabled"`
	PersonalizedRecommendationsEnabled bool   `json:"personalized_recommendations_enabled"`
	SearchHistoryEnabled               bool   `json:"search_history_enabled"`
	SearchEventRetentionDays           int64  `json:"search_event_retention_days"`
	MinimumQueryUserCount              int64  `json:"minimum_query_user_count"`
	HideSensitiveDefault               bool   `json:"hide_sensitive_default"`
}

type configUpdatedPayload struct {
	Settings SearchConfig `json:"settings"`
}

// WatchProgress is the video.watch_progress event body (throttled by the caller).
type WatchProgress struct {
	VideoID         uuid.UUID `json:"video_id"`
	UserID          *string   `json:"user_id,omitempty"`
	SessionID       *string   `json:"session_id,omitempty"`
	PositionSeconds int32     `json:"position_seconds"`
	DurationSeconds *int32    `json:"duration_seconds,omitempty"`
}

// reconcileBeginPayload / reconcilePagePayload / reconcileEndPayload frame a full
// index reconciliation sweep (MASTER-PLAN §1.5).
type reconcileBeginPayload struct {
	RunID uuid.UUID `json:"run_id"`
}

type reconcilePagePayload struct {
	RunID  uuid.UUID  `json:"run_id"`
	Videos []VideoDoc `json:"videos"`
}

type reconcileEndPayload struct {
	RunID uuid.UUID `json:"run_id"`
	Total int       `json:"total"`
}
