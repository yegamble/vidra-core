package domain

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestBlockSeverityConstants(t *testing.T) {
	tests := []struct {
		name     string
		severity BlockSeverity
		expected string
	}{
		{"Block", BlockSeverityBlock, "block"},
		{"Shadowban", BlockSeverityShadowban, "shadowban"},
		{"Quarantine", BlockSeverityQuarantine, "quarantine"},
		{"Mute", BlockSeverityMute, "mute"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, BlockSeverity(tt.expected), tt.severity)
			assert.Equal(t, tt.expected, string(tt.severity))
		})
	}
}

func TestMetricTypeConstants(t *testing.T) {
	tests := []struct {
		name     string
		constant string
		expected string
	}{
		{"JobSuccess", MetricTypeJobSuccess, "job_success"},
		{"JobFailure", MetricTypeJobFailure, "job_failure"},
		{"JobLatency", MetricTypeJobLatency, "job_latency_ms"},
		{"IngestRate", MetricTypeIngestRate, "ingest_rate"},
		{"IngestLatency", MetricTypeIngestLatency, "ingest_latency_ms"},
		{"QueueDepth", MetricTypeQueueDepth, "queue_depth"},
		{"DLQSize", MetricTypeDLQSize, "dlq_size"},
		{"RateLimitHit", MetricTypeRateLimitHit, "rate_limit_hit"},
		{"SignatureReject", MetricTypeSignatureReject, "signature_reject"},
		{"BlockedRequest", MetricTypeBlockedRequest, "blocked_request"},
		{"AbuseReport", MetricTypeAbuseReport, "abuse_report"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.constant)
		})
	}
}

func TestIdempotencyStatusConstants(t *testing.T) {
	tests := []struct {
		name     string
		constant string
		expected string
	}{
		{"Pending", IdempotencyStatusPending, "pending"},
		{"Success", IdempotencyStatusSuccess, "success"},
		{"Failed", IdempotencyStatusFailed, "failed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.constant)
		})
	}
}

func TestFederationAbuseReportTypeConstants(t *testing.T) {
	tests := []struct {
		name     string
		constant string
		expected string
	}{
		{"Spam", FederationAbuseReportTypeSpam, "spam"},
		{"Harassment", FederationAbuseReportTypeHarassment, "harassment"},
		{"Illegal", FederationAbuseReportTypeIllegal, "illegal"},
		{"Impersonation", FederationAbuseReportTypeImpersonation, "impersonation"},
		{"Violence", FederationAbuseReportTypeViolence, "violence"},
		{"NSFW", FederationAbuseReportTypeNSFW, "nsfw"},
		{"Other", FederationAbuseReportTypeOther, "other"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.constant)
		})
	}
}

func TestFederationAbuseReportStatusConstants(t *testing.T) {
	tests := []struct {
		name     string
		constant string
		expected string
	}{
		{"Pending", FederationAbuseReportStatusPending, "pending"},
		{"Reviewing", FederationAbuseReportStatusReviewing, "reviewing"},
		{"Resolved", FederationAbuseReportStatusResolved, "resolved"},
		{"Rejected", FederationAbuseReportStatusRejected, "rejected"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.constant)
		})
	}
}

func TestDeadLetterJobJSONRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	origJobID := "orig-job-123"
	errMsg := "max retries exceeded"

	dlj := DeadLetterJob{
		ID:            "dlj-456",
		OriginalJobID: &origJobID,
		JobType:       "deliver_activity",
		Payload:       json.RawMessage(`{"target":"https://remote.example/inbox"}`),
		ErrorMessage:  &errMsg,
		ErrorCount:    5,
		LastErrorAt:   now,
		CreatedAt:     now,
		CanRetry:      true,
		Metadata:      json.RawMessage(`{"instance":"remote.example"}`),
	}

	data, err := json.Marshal(dlj)
	assert.NoError(t, err)

	var decoded DeadLetterJob
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, dlj.ID, decoded.ID)
	assert.NotNil(t, decoded.OriginalJobID)
	assert.Equal(t, origJobID, *decoded.OriginalJobID)
	assert.Equal(t, dlj.JobType, decoded.JobType)
	assert.NotNil(t, decoded.ErrorMessage)
	assert.Equal(t, errMsg, *decoded.ErrorMessage)
	assert.Equal(t, 5, decoded.ErrorCount)
	assert.True(t, decoded.CanRetry)
}

func TestInstanceBlockJSONRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	reason := "Spam origin"
	blockedBy := "admin-user-1"
	expires := now.Add(30 * 24 * time.Hour)

	block := InstanceBlock{
		ID:             "block-123",
		InstanceDomain: "spam.example.com",
		Reason:         &reason,
		Severity:       BlockSeverityBlock,
		BlockedBy:      &blockedBy,
		CreatedAt:      now,
		ExpiresAt:      &expires,
		Metadata:       json.RawMessage(`{"auto_detected":true}`),
	}

	data, err := json.Marshal(block)
	assert.NoError(t, err)

	var decoded InstanceBlock
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, block.ID, decoded.ID)
	assert.Equal(t, "spam.example.com", decoded.InstanceDomain)
	assert.NotNil(t, decoded.Reason)
	assert.Equal(t, reason, *decoded.Reason)
	assert.Equal(t, BlockSeverityBlock, decoded.Severity)
	assert.NotNil(t, decoded.BlockedBy)
	assert.Equal(t, blockedBy, *decoded.BlockedBy)
	assert.NotNil(t, decoded.ExpiresAt)
}

func TestFederationAbuseReportJSONRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	reporterDID := "did:plc:reporter"
	contentURI := "at://did:plc:bad/post/1"
	description := "This is spam content"

	report := FederationAbuseReport{
		ID:                 "report-789",
		ReporterDID:        &reporterDID,
		ReportedContentURI: &contentURI,
		ReportType:         FederationAbuseReportTypeSpam,
		Description:        &description,
		Evidence:           json.RawMessage(`{"screenshots":["url1","url2"]}`),
		Status:             FederationAbuseReportStatusPending,
		CreatedAt:          now,
		UpdatedAt:          now,
	}

	data, err := json.Marshal(report)
	assert.NoError(t, err)

	var decoded FederationAbuseReport
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, report.ID, decoded.ID)
	assert.NotNil(t, decoded.ReporterDID)
	assert.Equal(t, reporterDID, *decoded.ReporterDID)
	assert.NotNil(t, decoded.ReportedContentURI)
	assert.Equal(t, contentURI, *decoded.ReportedContentURI)
	assert.Equal(t, FederationAbuseReportTypeSpam, decoded.ReportType)
	assert.NotNil(t, decoded.Description)
	assert.Equal(t, description, *decoded.Description)
	assert.Equal(t, FederationAbuseReportStatusPending, decoded.Status)
	assert.Nil(t, decoded.Resolution)
	assert.Nil(t, decoded.ResolvedBy)
	assert.Nil(t, decoded.ResolvedAt)
}

func TestIdempotencyRecordJSONRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	record := IdempotencyRecord{
		IdempotencyKey: "key-abc-123",
		OperationType:  "create_post",
		Payload:        json.RawMessage(`{"text":"hello"}`),
		Result:         json.RawMessage(`{"id":"post-1"}`),
		Status:         IdempotencyStatusSuccess,
		CreatedAt:      now,
		ExpiresAt:      now.Add(24 * time.Hour),
	}

	data, err := json.Marshal(record)
	assert.NoError(t, err)

	var decoded IdempotencyRecord
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, record.IdempotencyKey, decoded.IdempotencyKey)
	assert.Equal(t, record.OperationType, decoded.OperationType)
	assert.Equal(t, IdempotencyStatusSuccess, decoded.Status)
}

func TestFederationHealthSummaryJSON(t *testing.T) {
	now := time.Now().Truncate(time.Hour)
	summary := FederationHealthSummary{
		Hour:        now,
		MetricType:  MetricTypeJobLatency,
		EventCount:  1000,
		AvgValue:    150.5,
		MinValue:    10.0,
		MaxValue:    5000.0,
		MedianValue: 120.0,
		P95Value:    450.0,
	}

	data, err := json.Marshal(summary)
	assert.NoError(t, err)

	var decoded FederationHealthSummary
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, summary.MetricType, decoded.MetricType)
	assert.Equal(t, summary.EventCount, decoded.EventCount)
	assert.Equal(t, summary.AvgValue, decoded.AvgValue)
	assert.Equal(t, summary.MinValue, decoded.MinValue)
	assert.Equal(t, summary.MaxValue, decoded.MaxValue)
	assert.Equal(t, summary.MedianValue, decoded.MedianValue)
	assert.Equal(t, summary.P95Value, decoded.P95Value)
}

func TestBackoffConfigJSON(t *testing.T) {
	config := BackoffConfig{
		InitialDelay: 1 * time.Second,
		MaxDelay:     5 * time.Minute,
		Multiplier:   2.0,
		MaxRetries:   10,
	}

	data, err := json.Marshal(config)
	assert.NoError(t, err)

	var decoded BackoffConfig
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, config.InitialDelay, decoded.InitialDelay)
	assert.Equal(t, config.MaxDelay, decoded.MaxDelay)
	assert.Equal(t, config.Multiplier, decoded.Multiplier)
	assert.Equal(t, config.MaxRetries, decoded.MaxRetries)
}

func TestRateLimitEntryJSON(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	blockedUntil := now.Add(1 * time.Hour)
	entry := RateLimitEntry{
		ID:           "rate-limit-1",
		RequestCount: 100,
		WindowStart:  now,
		LastRequest:  now,
		IsBlocked:    true,
		BlockedUntil: &blockedUntil,
	}

	data, err := json.Marshal(entry)
	assert.NoError(t, err)

	var decoded RateLimitEntry
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, entry.ID, decoded.ID)
	assert.Equal(t, entry.RequestCount, decoded.RequestCount)
	assert.True(t, decoded.IsBlocked)
	assert.NotNil(t, decoded.BlockedUntil)
}
