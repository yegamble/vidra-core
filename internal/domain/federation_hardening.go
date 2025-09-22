package domain

import (
	"encoding/json"
	"time"
)

// DeadLetterJob represents a failed federation job in the DLQ
type DeadLetterJob struct {
	ID            string          `json:"id" db:"id"`
	OriginalJobID *string         `json:"original_job_id,omitempty" db:"original_job_id"`
	JobType       string          `json:"job_type" db:"job_type"`
	Payload       json.RawMessage `json:"payload" db:"payload"`
	ErrorMessage  *string         `json:"error_message,omitempty" db:"error_message"`
	ErrorCount    int             `json:"error_count" db:"error_count"`
	LastErrorAt   time.Time       `json:"last_error_at" db:"last_error_at"`
	CreatedAt     time.Time       `json:"created_at" db:"created_at"`
	CanRetry      bool            `json:"can_retry" db:"can_retry"`
	Metadata      json.RawMessage `json:"metadata,omitempty" db:"metadata"`
}

// BlockSeverity represents the severity of a block
type BlockSeverity string

const (
	BlockSeverityBlock      BlockSeverity = "block"
	BlockSeverityShadowban  BlockSeverity = "shadowban"
	BlockSeverityQuarantine BlockSeverity = "quarantine"
	BlockSeverityMute       BlockSeverity = "mute"
)

// InstanceBlock represents a blocked federation instance
type InstanceBlock struct {
	ID             string          `json:"id" db:"id"`
	InstanceDomain string          `json:"instance_domain" db:"instance_domain"`
	Reason         *string         `json:"reason,omitempty" db:"reason"`
	Severity       BlockSeverity   `json:"severity" db:"severity"`
	BlockedBy      *string         `json:"blocked_by,omitempty" db:"blocked_by"`
	CreatedAt      time.Time       `json:"created_at" db:"created_at"`
	ExpiresAt      *time.Time      `json:"expires_at,omitempty" db:"expires_at"`
	Metadata       json.RawMessage `json:"metadata,omitempty" db:"metadata"`
}

// ActorBlock represents a blocked federation actor
type ActorBlock struct {
	ID          string          `json:"id" db:"id"`
	ActorDID    *string         `json:"actor_did,omitempty" db:"actor_did"`
	ActorHandle *string         `json:"actor_handle,omitempty" db:"actor_handle"`
	Reason      *string         `json:"reason,omitempty" db:"reason"`
	Severity    BlockSeverity   `json:"severity" db:"severity"`
	BlockedBy   *string         `json:"blocked_by,omitempty" db:"blocked_by"`
	CreatedAt   time.Time       `json:"created_at" db:"created_at"`
	ExpiresAt   *time.Time      `json:"expires_at,omitempty" db:"expires_at"`
	Metadata    json.RawMessage `json:"metadata,omitempty" db:"metadata"`
}

// FederationMetric represents a federation health metric
type FederationMetric struct {
	ID             string          `json:"id" db:"id"`
	MetricType     string          `json:"metric_type" db:"metric_type"`
	MetricValue    float64         `json:"metric_value" db:"metric_value"`
	InstanceDomain *string         `json:"instance_domain,omitempty" db:"instance_domain"`
	ActorDID       *string         `json:"actor_did,omitempty" db:"actor_did"`
	JobType        *string         `json:"job_type,omitempty" db:"job_type"`
	Timestamp      time.Time       `json:"timestamp" db:"timestamp"`
	Metadata       json.RawMessage `json:"metadata,omitempty" db:"metadata"`
}

// MetricType constants
const (
	MetricTypeJobSuccess      = "job_success"
	MetricTypeJobFailure      = "job_failure"
	MetricTypeJobLatency      = "job_latency_ms"
	MetricTypeIngestRate      = "ingest_rate"
	MetricTypeIngestLatency   = "ingest_latency_ms"
	MetricTypeQueueDepth      = "queue_depth"
	MetricTypeDLQSize         = "dlq_size"
	MetricTypeRateLimitHit    = "rate_limit_hit"
	MetricTypeSignatureReject = "signature_reject"
	MetricTypeBlockedRequest  = "blocked_request"
	MetricTypeAbuseReport     = "abuse_report"
)

// IdempotencyRecord represents an idempotency key for deduplication
type IdempotencyRecord struct {
	IdempotencyKey string          `json:"idempotency_key" db:"idempotency_key"`
	OperationType  string          `json:"operation_type" db:"operation_type"`
	Payload        json.RawMessage `json:"payload,omitempty" db:"payload"`
	Result         json.RawMessage `json:"result,omitempty" db:"result"`
	Status         string          `json:"status" db:"status"`
	CreatedAt      time.Time       `json:"created_at" db:"created_at"`
	ExpiresAt      time.Time       `json:"expires_at" db:"expires_at"`
}

// IdempotencyStatus constants
const (
	IdempotencyStatusPending = "pending"
	IdempotencyStatusSuccess = "success"
	IdempotencyStatusFailed  = "failed"
)

// RequestSignature represents a cached request signature for replay prevention
type RequestSignature struct {
	SignatureHash  string    `json:"signature_hash" db:"signature_hash"`
	InstanceDomain string    `json:"instance_domain" db:"instance_domain"`
	RequestPath    *string   `json:"request_path,omitempty" db:"request_path"`
	ReceivedAt     time.Time `json:"received_at" db:"received_at"`
	ExpiresAt      time.Time `json:"expires_at" db:"expires_at"`
}

// AbuseReport represents an abuse report for federated content
type AbuseReport struct {
	ID                 string          `json:"id" db:"id"`
	ReporterDID        *string         `json:"reporter_did,omitempty" db:"reporter_did"`
	ReportedContentURI *string         `json:"reported_content_uri,omitempty" db:"reported_content_uri"`
	ReportedActorDID   *string         `json:"reported_actor_did,omitempty" db:"reported_actor_did"`
	ReportType         string          `json:"report_type" db:"report_type"`
	Description        *string         `json:"description,omitempty" db:"description"`
	Evidence           json.RawMessage `json:"evidence,omitempty" db:"evidence"`
	Status             string          `json:"status" db:"status"`
	Resolution         *string         `json:"resolution,omitempty" db:"resolution"`
	ResolvedBy         *string         `json:"resolved_by,omitempty" db:"resolved_by"`
	ResolvedAt         *time.Time      `json:"resolved_at,omitempty" db:"resolved_at"`
	CreatedAt          time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at" db:"updated_at"`
}

// AbuseReportType constants
const (
	AbuseReportTypeSpam          = "spam"
	AbuseReportTypeHarassment    = "harassment"
	AbuseReportTypeIllegal       = "illegal"
	AbuseReportTypeImpersonation = "impersonation"
	AbuseReportTypeViolence      = "violence"
	AbuseReportTypeNSFW          = "nsfw"
	AbuseReportTypeOther         = "other"
)

// AbuseReportStatus constants
const (
	AbuseReportStatusPending   = "pending"
	AbuseReportStatusReviewing = "reviewing"
	AbuseReportStatusResolved  = "resolved"
	AbuseReportStatusRejected  = "rejected"
)

// RateLimitEntry represents rate limiting state for an instance or actor
type RateLimitEntry struct {
	ID           string     `json:"id" db:"id"`
	RequestCount int        `json:"request_count" db:"request_count"`
	WindowStart  time.Time  `json:"window_start" db:"window_start"`
	LastRequest  time.Time  `json:"last_request" db:"last_request"`
	IsBlocked    bool       `json:"is_blocked" db:"is_blocked"`
	BlockedUntil *time.Time `json:"blocked_until,omitempty" db:"blocked_until"`
}

// FederationHealthSummary represents aggregated health metrics
type FederationHealthSummary struct {
	Hour        time.Time `json:"hour" db:"hour"`
	MetricType  string    `json:"metric_type" db:"metric_type"`
	EventCount  int64     `json:"event_count" db:"event_count"`
	AvgValue    float64   `json:"avg_value" db:"avg_value"`
	MinValue    float64   `json:"min_value" db:"min_value"`
	MaxValue    float64   `json:"max_value" db:"max_value"`
	MedianValue float64   `json:"median_value" db:"median_value"`
	P95Value    float64   `json:"p95_value" db:"p95_value"`
}

// BackoffConfig represents exponential backoff configuration
type BackoffConfig struct {
	InitialDelay time.Duration `json:"initial_delay"`
	MaxDelay     time.Duration `json:"max_delay"`
	Multiplier   float64       `json:"multiplier"`
	MaxRetries   int           `json:"max_retries"`
}

// FederationSecurityConfig represents security configuration
type FederationSecurityConfig struct {
	MaxRequestSize         int64         `json:"max_request_size"`
	SignatureWindowSeconds int           `json:"signature_window_seconds"`
	RateLimitRequests      int           `json:"rate_limit_requests"`
	RateLimitWindow        time.Duration `json:"rate_limit_window"`
	EnableAbuseReporting   bool          `json:"enable_abuse_reporting"`
	MetricsEnabled         bool          `json:"metrics_enabled"`
}
