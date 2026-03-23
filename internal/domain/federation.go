package domain

import (
	"encoding/json"
	"time"
)

type FederationJobStatus string

const (
	FedJobPending    FederationJobStatus = "pending"
	FedJobProcessing FederationJobStatus = "processing"
	FedJobCompleted  FederationJobStatus = "completed"
	FedJobFailed     FederationJobStatus = "failed"
)

type FederationJob struct {
	ID            string              `json:"id" db:"id"`
	JobType       string              `json:"job_type" db:"job_type"`
	Payload       json.RawMessage     `json:"payload" db:"payload"`
	Status        FederationJobStatus `json:"status" db:"status"`
	Attempts      int                 `json:"attempts" db:"attempts"`
	MaxAttempts   int                 `json:"max_attempts" db:"max_attempts"`
	NextAttemptAt time.Time           `json:"next_attempt_at" db:"next_attempt_at"`
	LastError     *string             `json:"last_error,omitempty" db:"last_error"`
	CreatedAt     time.Time           `json:"created_at" db:"created_at"`
	UpdatedAt     time.Time           `json:"updated_at" db:"updated_at"`
}

type FederatedPost struct {
	ID                         string          `json:"id" db:"id"`
	ActorDID                   string          `json:"actor_did" db:"actor_did"`
	ActorHandle                *string         `json:"actor_handle,omitempty" db:"actor_handle"`
	URI                        string          `json:"uri" db:"uri"`
	CID                        *string         `json:"cid,omitempty" db:"cid"`
	Text                       *string         `json:"text,omitempty" db:"text"`
	CreatedAt                  *time.Time      `json:"created_at,omitempty" db:"created_at"`
	IndexedAt                  *time.Time      `json:"indexed_at,omitempty" db:"indexed_at"`
	EmbedType                  *string         `json:"embed_type,omitempty" db:"embed_type"`
	EmbedURL                   *string         `json:"embed_url,omitempty" db:"embed_url"`
	EmbedTitle                 *string         `json:"embed_title,omitempty" db:"embed_title"`
	EmbedDescription           *string         `json:"embed_description,omitempty" db:"embed_description"`
	Labels                     json.RawMessage `json:"labels,omitempty" db:"labels"`
	Raw                        json.RawMessage `json:"raw,omitempty" db:"raw"`
	InsertedAt                 time.Time       `json:"inserted_at" db:"inserted_at"`
	UpdatedAt                  time.Time       `json:"updated_at" db:"updated_at"`
	ContentHash                *string         `json:"content_hash,omitempty" db:"content_hash"`
	DuplicateOf                *string         `json:"duplicate_of,omitempty" db:"duplicate_of"`
	ConflictResolutionStrategy *string         `json:"conflict_resolution_strategy,omitempty" db:"conflict_resolution_strategy"`
	VersionNumber              int             `json:"version_number" db:"version_number"`
	IsCanonical                bool            `json:"is_canonical" db:"is_canonical"`
}

type FederatedTimeline struct {
	Total    int             `json:"total"`
	Page     int             `json:"page"`
	PageSize int             `json:"pageSize"`
	Data     []FederatedPost `json:"data"`
}
