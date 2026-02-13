package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAbuseReportStatus_Constants(t *testing.T) {
	tests := []struct {
		name     string
		status   AbuseReportStatus
		expected string
	}{
		{"pending", AbuseReportStatusPending, "pending"},
		{"accepted", AbuseReportStatusAccepted, "accepted"},
		{"rejected", AbuseReportStatusRejected, "rejected"},
		{"investigating", AbuseReportStatusInvestigating, "investigating"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, string(tt.status))
		})
	}
}

func TestReportedEntityType_Constants(t *testing.T) {
	tests := []struct {
		name       string
		entityType ReportedEntityType
		expected   string
	}{
		{"video", ReportedEntityVideo, "video"},
		{"comment", ReportedEntityComment, "comment"},
		{"user", ReportedEntityUser, "user"},
		{"channel", ReportedEntityChannel, "channel"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, string(tt.entityType))
		})
	}
}

func TestBlockType_Constants(t *testing.T) {
	tests := []struct {
		name      string
		blockType BlockType
		expected  string
	}{
		{"user", BlockTypeUser, "user"},
		{"domain", BlockTypeDomain, "domain"},
		{"ip", BlockTypeIP, "ip"},
		{"email", BlockTypeEmail, "email"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, string(tt.blockType))
		})
	}
}
