package searchevents

import (
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// docKindLocal is the only kind core ever emits: every video vidra-core owns is
// local. Remote/federated docs are the search service's own concern.
const docKindLocal = "local"

// videoDocFields is the shared field set of the two doc-source rows
// (GetVideoSearchDocRow and ListVideoSearchDocsPageRow), so both build an
// identical VideoDoc.
type videoDocFields struct {
	ID              uuid.UUID
	ChannelID       uuid.UUID
	ChannelHandle   string
	ChannelName     string
	OwnerID         uuid.UUID
	Title           string
	Description     string
	Tags            []string
	Category        *string
	Language        *string
	License         *string
	DurationSeconds *int32
	IsSensitive     bool
	Privacy         string
	State           string
	PublishAt       pgtype.Timestamptz
	CreatedAt       time.Time
	UpdatedAt       time.Time
	Views           int64
	Likes           int64
}

// buildVideoDoc assembles the wire VideoDoc from the shared fields, resolving the
// published_at timestamp. vidra has no published_at column — a video's
// publication is state='published' at its created_at — so published_at reports
// created_at for a published video, or the scheduled publish_at when one is set
// (documented deviation).
func buildVideoDoc(f videoDocFields) VideoDoc {
	tags := f.Tags
	if tags == nil {
		tags = []string{}
	}
	doc := VideoDoc{
		ID:              f.ID,
		Kind:            docKindLocal,
		ChannelID:       f.ChannelID,
		ChannelHandle:   f.ChannelHandle,
		ChannelName:     f.ChannelName,
		OwnerID:         f.OwnerID,
		Title:           f.Title,
		Description:     f.Description,
		Tags:            tags,
		Category:        f.Category,
		Language:        f.Language,
		License:         f.License,
		DurationSeconds: f.DurationSeconds,
		IsSensitive:     f.IsSensitive,
		Privacy:         f.Privacy,
		State:           f.State,
		CreatedAt:       f.CreatedAt,
		UpdatedAt:       f.UpdatedAt,
		Views:           f.Views,
		Likes:           f.Likes,
	}
	switch {
	case f.State == "published":
		t := f.CreatedAt
		doc.PublishedAt = &t
	case f.PublishAt.Valid:
		t := f.PublishAt.Time
		doc.PublishedAt = &t
	}
	return doc
}

// videoDocFromRow builds a VideoDoc from the single-video doc query.
func videoDocFromRow(r sqlcgen.GetVideoSearchDocRow) VideoDoc {
	return buildVideoDoc(videoDocFields{
		ID: r.ID, ChannelID: r.ChannelID, ChannelHandle: r.ChannelHandle, ChannelName: r.ChannelName,
		OwnerID: r.OwnerID, Title: r.Title, Description: r.Description, Tags: r.Tags,
		Category: r.Category, Language: r.Language, License: r.License, DurationSeconds: r.DurationSeconds,
		IsSensitive: r.IsSensitive, Privacy: r.Privacy, State: r.State, PublishAt: r.PublishAt,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, Views: r.Views, Likes: r.Likes,
	})
}

// videoDocFromPageRow builds a VideoDoc from a reconcile-page row.
func videoDocFromPageRow(r sqlcgen.ListVideoSearchDocsPageRow) VideoDoc {
	return buildVideoDoc(videoDocFields{
		ID: r.ID, ChannelID: r.ChannelID, ChannelHandle: r.ChannelHandle, ChannelName: r.ChannelName,
		OwnerID: r.OwnerID, Title: r.Title, Description: r.Description, Tags: r.Tags,
		Category: r.Category, Language: r.Language, License: r.License, DurationSeconds: r.DurationSeconds,
		IsSensitive: r.IsSensitive, Privacy: r.Privacy, State: r.State, PublishAt: r.PublishAt,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, Views: r.Views, Likes: r.Likes,
	})
}
