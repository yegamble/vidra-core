package domain

import (
	"time"

	"github.com/google/uuid"
)

// AbuseMessage represents a discussion message on an abuse report.
type AbuseMessage struct {
	ID            uuid.UUID `db:"id"              json:"id"`
	AbuseReportID uuid.UUID `db:"abuse_report_id" json:"abuseReportId"`
	SenderID      string    `db:"sender_id"       json:"senderId"`
	Message       string    `db:"message"         json:"message"`
	CreatedAt     time.Time `db:"created_at"      json:"createdAt"`
}
