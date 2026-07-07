package video

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Chapter is one seek-bar chapter: a start offset (whole seconds from the video
// start) and a short title. The custom player renders chapters as tick markers
// and a "current chapter" label.
type Chapter struct {
	StartSeconds int32
	Title        string
}

// Chapter limits (CORE-15 / W1.C1 contract). A video may have at most
// MaxChapters chapters, each titled 1..MaxChapterTitleLen characters after trim.
const (
	MaxChapters        = 100
	MaxChapterTitleLen = 120
)

// ChapterValidationError is a 400-class failure validating a chapters replace:
// bad ordering, an out-of-bounds start, a bad title length, or too many rows.
// The HTTP layer maps it to 400 (not the 422 field-error path) because the
// contract specifies 400 for every chapters violation.
type ChapterValidationError struct{ Message string }

func (e *ChapterValidationError) Error() string { return e.Message }

// ListChapters returns a video's chapters in playback order (ascending start),
// empty when none. It performs no visibility check — the HTTP layer gates the
// read exactly like GET /videos/{id}.
func (s *Service) ListChapters(ctx context.Context, videoID uuid.UUID) ([]Chapter, error) {
	rows, err := s.repo.ListVideoChapters(ctx, videoID)
	if err != nil {
		return nil, err
	}
	out := make([]Chapter, 0, len(rows))
	for _, r := range rows {
		out = append(out, Chapter{StartSeconds: r.StartSeconds, Title: r.Title})
	}
	return out, nil
}

// HasChapters reports whether a video has at least one chapter (drives the
// detail has_chapters flag). Best-effort: a lookup error reports false rather
// than failing the read, matching HasThumbnail/HasStoryboard.
func (s *Service) HasChapters(ctx context.Context, videoID uuid.UUID) bool {
	ok, err := s.repo.VideoHasChapters(ctx, videoID)
	if err != nil {
		return false
	}
	return ok
}

// ReplaceChapters replaces a video's whole chapter set atomically-at-the-set
// level: the input is validated in full BEFORE any write, so a rejected request
// (ErrForbidden, ErrNotFound, or *ChapterValidationError) leaves the stored
// chapters untouched. An empty (or nil) input clears all chapters. Only the
// owner may write: a non-owner gets ErrForbidden, an unknown id ErrNotFound
// (the HTTP layer renders both as 404). Returns the stored set in playback order.
func (s *Service) ReplaceChapters(ctx context.Context, ownerID, videoID uuid.UUID, chapters []Chapter) ([]Chapter, error) {
	v, err := s.GetByID(ctx, videoID)
	if err != nil {
		return nil, err // ErrNotFound
	}
	if v.OwnerID != ownerID {
		return nil, ErrForbidden
	}

	// Validate against the probed duration when one was recorded.
	var duration *int32
	if md, ok, mErr := s.GetMetadata(ctx, videoID); mErr == nil && ok {
		duration = md.DurationSeconds
	}
	normalized, err := validateChapters(chapters, duration)
	if err != nil {
		return nil, err // *ChapterValidationError — nothing written
	}

	if err := s.repo.DeleteVideoChapters(ctx, videoID); err != nil {
		return nil, err
	}
	if len(normalized) > 0 {
		starts := make([]int32, len(normalized))
		titles := make([]string, len(normalized))
		for i, ch := range normalized {
			starts[i] = ch.StartSeconds
			titles[i] = ch.Title
		}
		if err := s.repo.InsertVideoChapters(ctx, sqlcgen.InsertVideoChaptersParams{
			VideoID:      videoID,
			StartSeconds: starts,
			Title:        titles,
		}); err != nil {
			return nil, err
		}
	}
	return normalized, nil
}

// validateChapters enforces the CORE-15 contract on a full chapter set and
// returns the normalized (trimmed-title) set. Rules: at most MaxChapters entries;
// each start_seconds >= 0 and strictly increasing (which implies unique); each
// title 1..MaxChapterTitleLen characters after trimming; and, when the probe
// recorded a positive duration, every start < duration. Any violation is a
// *ChapterValidationError (400). A nil/empty set is valid (clears all).
func validateChapters(chapters []Chapter, durationSeconds *int32) ([]Chapter, error) {
	if len(chapters) > MaxChapters {
		return nil, &ChapterValidationError{Message: fmt.Sprintf("at most %d chapters are allowed", MaxChapters)}
	}
	out := make([]Chapter, 0, len(chapters))
	prev := int32(-1)
	for i, ch := range chapters {
		if ch.StartSeconds < 0 {
			return nil, &ChapterValidationError{Message: fmt.Sprintf("chapter %d: start_seconds must be >= 0", i)}
		}
		if ch.StartSeconds <= prev {
			return nil, &ChapterValidationError{Message: fmt.Sprintf("chapter %d: start_seconds must strictly increase", i)}
		}
		if durationSeconds != nil && *durationSeconds > 0 && ch.StartSeconds >= *durationSeconds {
			return nil, &ChapterValidationError{Message: fmt.Sprintf("chapter %d: start_seconds must be less than the video duration (%ds)", i, *durationSeconds)}
		}
		title := strings.TrimSpace(ch.Title)
		if n := len([]rune(title)); n < 1 || n > MaxChapterTitleLen {
			return nil, &ChapterValidationError{Message: fmt.Sprintf("chapter %d: title must be 1 to %d characters", i, MaxChapterTitleLen)}
		}
		out = append(out, Chapter{StartSeconds: ch.StartSeconds, Title: title})
		prev = ch.StartSeconds
	}
	return out, nil
}
