package domain

import (
	"time"

	"github.com/google/uuid"
)

// LiveStreamSession records one broadcast session for a live stream.
type LiveStreamSession struct {
	ID           uuid.UUID  `db:"id"            json:"id"`
	StreamID     uuid.UUID  `db:"stream_id"     json:"streamId"`
	StartedAt    time.Time  `db:"started_at"    json:"startedAt"`
	EndedAt      *time.Time `db:"ended_at"      json:"endedAt,omitempty"`
	PeakViewers  *int       `db:"peak_viewers"  json:"peakViewers,omitempty"`
	TotalSeconds *int       `db:"total_seconds" json:"totalSeconds,omitempty"`
	AvgViewers   *int       `db:"avg_viewers"   json:"avgViewers,omitempty"`
}
