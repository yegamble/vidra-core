package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/watchword"
)

const maxWatchedWordLen = 100

// watchedWordView is the projection of a watched word.
type watchedWordView struct {
	ID                string    `json:"id"`
	Word              string    `json:"word"`
	CreatedByUsername string    `json:"created_by_username,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
}

// watchedWordListResponse is the paginated watched-words list.
type watchedWordListResponse struct {
	Words  []watchedWordView `json:"words"`
	Limit  int               `json:"limit"`
	Offset int               `json:"offset"`
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
	limit := clampInt(queryInt(c, "limit", defaultVideoFeedLimit), 1, maxVideoFeedLimit)
	offset := queryInt(c, "offset", 0)
	if offset < 0 {
		offset = 0
	}
	items, err := s.watchwordsvc.List(c.Request().Context(), int32(limit), int32(offset))
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
	return c.JSON(http.StatusOK, watchedWordListResponse{Words: views, Limit: limit, Offset: offset})
}

// handleAddWatchedWord adds a term to the watched-words list. Behind
// requireRole(admin, moderator). A duplicate (case-insensitive) → 409.
func (s *Server) handleAddWatchedWord(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
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
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "watched word not found")
	}
	if err := s.watchwordsvc.Delete(c.Request().Context(), id); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}
