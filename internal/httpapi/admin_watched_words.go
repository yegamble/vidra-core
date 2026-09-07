package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/watchword"
)

const maxWatchedWordLen = 100

// maxWatchedWordMatchNoteLen bounds the moderator's triage note, matching the
// report queue's note cap.
const maxWatchedWordMatchNoteLen = 2000

// watchedWordView is the projection of a watched word.
type watchedWordView struct {
	ID                string    `json:"id"`
	Word              string    `json:"word"`
	CreatedByUsername string    `json:"created_by_username,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
}

// watchedWordListResponse is the paginated watched-words list.
type watchedWordListResponse struct {
	Words []watchedWordView `json:"words"`
	pageMeta
}

// createWatchedWordRequest is the POST /admin/watched-words body.
type createWatchedWordRequest struct {
	Word string `json:"word"`
}

func (r createWatchedWordRequest) Validate() []FieldError {
	word := strings.TrimSpace(r.Word)
	switch {
	case word == "":
		return []FieldError{{Field: "word", Message: "is required"}}
	case len(word) > maxWatchedWordLen:
		return []FieldError{{Field: "word", Message: "must be at most 100 characters"}}
	}
	return nil
}

// handleListWatchedWords returns the watched-words list, newest first. Behind
// requireRole(admin, moderator). Pagination via ?limit (1–100, default 20)/?offset.
func (s *Server) handleListWatchedWords(c echo.Context) error {
	page := parsePage(c, defaultVideoFeedLimit, maxVideoFeedLimit)
	items, total, err := s.watchwordsvc.List(c.Request().Context(), page.Limit32(), page.Offset32())
	if err != nil {
		return err
	}
	views := make([]watchedWordView, 0, len(items))
	for _, it := range items {
		views = append(views, watchedWordView{
			ID:                it.ID.String(),
			Word:              it.Word,
			CreatedByUsername: it.CreatedByUsername,
			CreatedAt:         it.CreatedAt,
		})
	}
	return c.JSON(http.StatusOK, watchedWordListResponse{Words: views, pageMeta: page.meta(total)})
}

// handleAddWatchedWord adds a term to the watched-words list. Behind
// requireRole(admin, moderator). A duplicate (case-insensitive) → 409.
func (s *Server) handleAddWatchedWord(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	var in createWatchedWordRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	word, err := s.watchwordsvc.Add(c.Request().Context(), strings.TrimSpace(in.Word), userID)
	if err != nil {
		if errors.Is(err, watchword.ErrAlreadyExists) {
			return echo.NewHTTPError(http.StatusConflict, "watched word already exists")
		}
		return err
	}
	return c.JSON(http.StatusCreated, watchedWordView{
		ID: word.ID.String(), Word: word.Word, CreatedAt: word.CreatedAt,
	})
}

// handleDeleteWatchedWord removes a term from the watched-words list. Behind
// requireRole(admin, moderator). Idempotent (an unknown id still succeeds).
func (s *Server) handleDeleteWatchedWord(c echo.Context) error {
	id, err := pathUUID(c, "id", "watched word not found")
	if err != nil {
		return err
	}
	if err := s.watchwordsvc.Delete(c.Request().Context(), id); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

// watchedWordMatchView is flagged content for the moderation review queue: the
// matched term plus the target's context. Type is "comment" (comment_id/
// comment_body set; video_id is the video the comment is on) or "video"
// (comment fields omitted; video_id/video_title are the flagged video — §12).
//
// MatchedText is the SNAPSHOT captured when the flag was raised, which is what
// a moderator reviews: comment_body/video_title are the LIVE target and may
// no longer contain the term (target_status says which). match_offset/
// match_length locate the term inside the snapshot in RUNES; -1 means it could
// not be located and the client falls back to searching for `word`.
type watchedWordMatchView struct {
	ID                 string     `json:"id"`
	Word               string     `json:"word"`
	Type               string     `json:"type"`
	CommentID          string     `json:"comment_id,omitempty"`
	CommentBody        string     `json:"comment_body,omitempty"`
	VideoID            string     `json:"video_id"`
	VideoTitle         string     `json:"video_title"`
	AuthorUsername     string     `json:"author_username"`
	CreatedAt          time.Time  `json:"created_at"`
	MatchedText        string     `json:"matched_text"`
	MatchOffset        int32      `json:"match_offset"`
	MatchLength        int32      `json:"match_length"`
	SnapshotBackfilled bool       `json:"snapshot_backfilled"`
	TermActive         bool       `json:"term_active"`
	TargetStatus       string     `json:"target_status"`
	Status             string     `json:"status"`
	ModeratorNote      string     `json:"moderator_note"`
	ResolvedAt         *time.Time `json:"resolved_at,omitempty"`
	ResolvedByUsername string     `json:"resolved_by_username,omitempty"`
}

// watchedWordMatchListResponse is the paginated flagged-content queue.
type watchedWordMatchListResponse struct {
	Matches []watchedWordMatchView `json:"matches"`
	pageMeta
}

// watchedWordMatchStatuses are the accepted ?status values for the queue.
var watchedWordMatchStatuses = []string{
	watchword.StatusOpen, watchword.StatusResolved, watchword.StatusDismissed, statusFilterAll,
}

// handleListWatchedWordMatches returns content flagged by the watched-words
// list — comments and videos (§12) — newest match first, each with a type
// badge, the flag-time snapshot and its triage state. Behind
// requireRole(admin, moderator). ?status is open|resolved|dismissed|all and
// defaults to OPEN — the queue is a work list, and before triage existed it
// could only ever grow. Pagination via ?limit (1–100, default 20)/?offset.
func (s *Server) handleListWatchedWordMatches(c echo.Context) error {
	status, err := parseEnumParam(c, "status", watchedWordMatchStatuses, watchword.StatusOpen)
	if err != nil {
		return err
	}
	page := parsePage(c, defaultVideoFeedLimit, maxVideoFeedLimit)
	items, total, err := s.watchwordsvc.ListMatches(c.Request().Context(), listFilterValue(status), page.Limit32(), page.Offset32())
	if err != nil {
		return err
	}
	views := make([]watchedWordMatchView, 0, len(items))
	for _, m := range items {
		view := watchedWordMatchView{
			ID:                 m.ID.String(),
			Word:               m.Word,
			Type:               m.Type,
			VideoID:            m.VideoID.String(),
			VideoTitle:         m.VideoTitle,
			AuthorUsername:     m.AuthorUsername,
			CreatedAt:          m.CreatedAt,
			MatchedText:        m.MatchedText,
			MatchOffset:        m.MatchOffset,
			MatchLength:        m.MatchLength,
			SnapshotBackfilled: m.SnapshotBackfilled,
			TermActive:         m.TermActive,
			TargetStatus:       m.TargetStatus,
			Status:             m.Status,
			ModeratorNote:      m.ModeratorNote,
			ResolvedAt:         m.ResolvedAt,
			ResolvedByUsername: m.ResolvedByUsername,
		}
		if m.Type == watchword.MatchTargetComment {
			view.CommentID = m.CommentID.String()
			view.CommentBody = m.CommentBody
		}
		views = append(views, view)
	}
	return c.JSON(http.StatusOK, watchedWordMatchListResponse{Matches: views, pageMeta: page.meta(total)})
}

// resolveWatchedWordMatchRequest is the body for triaging a match.
type resolveWatchedWordMatchRequest struct {
	Status string `json:"status"`
	Note   string `json:"note"`
}

func (r resolveWatchedWordMatchRequest) Validate() []FieldError {
	var fes []FieldError
	if !watchword.ValidOutcome(r.Status) {
		fes = append(fes, FieldError{Field: "status", Message: "must be 'resolved' or 'dismissed'"})
	}
	if len(r.Note) > maxWatchedWordMatchNoteLen {
		fes = append(fes, FieldError{Field: "note", Message: "must be at most 2000 characters"})
	}
	return fes
}

// handleResolveWatchedWordMatch triages one flagged item — "resolved" (acted on)
// or "dismissed" (false positive) — with an optional moderator note. Behind
// requireRole(admin, moderator). Idempotent like the report queue's resolve: a
// repeat overwrites the outcome and the note and still answers 204, so a retry
// is never an error. An unknown id → 404. Emits an audit event carrying the
// OUTCOME and whether a note was supplied — never the note itself, which is
// moderator prose and belongs on the domain row (audit_log's metadata allowlist
// rejects the whole event when prose reaches it; 0130 learned this).
func (s *Server) handleResolveWatchedWordMatch(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	id, err := pathUUID(c, "id", "flagged item not found")
	if err != nil {
		return err
	}
	var in resolveWatchedWordMatchRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	note := strings.TrimSpace(in.Note)
	if err := s.watchwordsvc.Resolve(c.Request().Context(), userID, id, in.Status, note); err != nil {
		if errors.Is(err, watchword.ErrMatchNotFound) {
			s.audit(c, observability.ActionWatchedWordMatchResolve, observability.ResultFailure, userID.String(), "not_found")
			return echo.NewHTTPError(http.StatusNotFound, "flagged item not found")
		}
		return err
	}
	s.auditEvent(c, audit.Event{
		Action: observability.ActionWatchedWordMatchResolve, Result: observability.ResultSuccess,
		ActorID: userID.String(), Reason: "moderator_triaged",
		ResourceType: "watched_word_match", ResourceID: id.String(),
		Metadata: []audit.MetadataField{
			{Key: "outcome", Value: in.Status},
			{Key: "reason_provided", Value: strconv.FormatBool(note != "")},
		},
	})
	return c.NoContent(http.StatusNoContent)
}
