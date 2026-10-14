package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/watchword"
)

// Named watched-word lists (0172), at two scopes with ONE implementation:
// /admin/watched-word-lists (instance lists, moderators/admins) and
// /me/watched-word-lists (a creator's own lists, applied to comments on their
// own videos). The scope is the owner passed down: uuid.Nil = instance. A list
// in the other scope is a 404 — the per-account surface is owner-only by
// construction, not by a check that could be forgotten.

type watchedWordListView struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Action    string    `json:"action"`
	IsDefault bool      `json:"is_default"`
	WordCount int64     `json:"word_count"`
	Words     []string  `json:"words,omitempty"` // detail/create/update only; never for the default list
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type watchedWordListsPage struct {
	Lists []watchedWordListView `json:"lists"`
	pageMeta
}

func newWatchedWordListView(l watchword.List) watchedWordListView {
	return watchedWordListView{ID: l.ID.String(), Name: l.Name, Action: l.Action, IsDefault: l.IsDefault,
		WordCount: l.WordCount, Words: l.Words, CreatedAt: l.CreatedAt, UpdatedAt: l.UpdatedAt}
}

func validateWordListFields(name *string, action *string, words []string) []FieldError {
	var out []FieldError
	if name != nil {
		if n := strings.TrimSpace(*name); n == "" {
			out = append(out, FieldError{Field: "name", Message: "is required"})
		} else if utf8.RuneCountInString(n) > 100 {
			out = append(out, FieldError{Field: "name", Message: "must be at most 100 characters"})
		} else if strings.EqualFold(n, watchword.TagExternalLink) {
			// The built-in automatic tag's name: a list so named would be indistinguishable from it.
			out = append(out, FieldError{Field: "name", Message: "is reserved"})
		}
	}
	if action != nil && *action != watchword.ActionFlag && *action != watchword.ActionHold {
		out = append(out, FieldError{Field: "action", Message: "must be flag or hold"})
	}
	if len(words) > watchword.MaxWordsPerList {
		out = append(out, FieldError{Field: "words", Message: "at most 500 words per list"})
	}
	for _, w := range words {
		if utf8.RuneCountInString(strings.TrimSpace(w)) > maxWatchedWordLen {
			out = append(out, FieldError{Field: "words", Message: "each word must be at most 100 characters"})
			break
		}
	}
	return out
}

type createWordListRequest struct {
	Name   string   `json:"name"`
	Action string   `json:"action"`
	Words  []string `json:"words"`
}

func (r createWordListRequest) Validate() []FieldError {
	var action *string
	if r.Action != "" {
		action = &r.Action
	}
	return validateWordListFields(&r.Name, action, r.Words)
}

// updateWordListRequest: absent fields are left alone; "words" REPLACES the
// list's terms when present (an empty array clears them).
type updateWordListRequest struct {
	Name   *string   `json:"name"`
	Action *string   `json:"action"`
	Words  *[]string `json:"words"`
}

func (r updateWordListRequest) Validate() []FieldError {
	var words []string
	if r.Words != nil {
		words = *r.Words
	}
	return validateWordListFields(r.Name, r.Action, words)
}

// wordListError maps service errors onto HTTP.
func wordListError(err error) error {
	switch {
	case errors.Is(err, watchword.ErrListNotFound):
		return echo.NewHTTPError(http.StatusNotFound, "watched-word list not found")
	case errors.Is(err, watchword.ErrListNameTaken):
		return echo.NewHTTPError(http.StatusConflict, "a list with that name already exists")
	case errors.Is(err, watchword.ErrDefaultList):
		return echo.NewHTTPError(http.StatusConflict, "the default list cannot be deleted, and its words are managed one at a time")
	case errors.Is(err, watchword.ErrTooManyLists):
		return echo.NewHTTPError(http.StatusConflict, "list limit reached")
	}
	return err
}

func (s *Server) listWordLists(c echo.Context, owner uuid.UUID) error {
	page := parsePage(c, defaultVideoFeedLimit, maxVideoFeedLimit)
	items, total, err := s.watchwordsvc.ListLists(c.Request().Context(), owner, page.Limit32(), page.Offset32())
	if err != nil {
		return err
	}
	views := make([]watchedWordListView, 0, len(items))
	for _, it := range items {
		views = append(views, newWatchedWordListView(it))
	}
	return c.JSON(http.StatusOK, watchedWordListsPage{Lists: views, pageMeta: page.meta(total)})
}

func (s *Server) getWordList(c echo.Context, owner uuid.UUID) error {
	id, err := pathUUID(c, "id", "watched-word list not found")
	if err != nil {
		return err
	}
	l, err := s.watchwordsvc.GetList(c.Request().Context(), owner, id)
	if err != nil {
		return wordListError(err)
	}
	return c.JSON(http.StatusOK, newWatchedWordListView(l))
}

func (s *Server) createWordList(c echo.Context, owner uuid.UUID) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	var in createWordListRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	l, err := s.watchwordsvc.CreateList(c.Request().Context(), owner, userID, in.Name, in.Action, in.Words)
	if err != nil {
		return wordListError(err)
	}
	if owner == uuid.Nil {
		s.auditWordList(c, observability.ActionAdminWatchedWordListCreate, userID, l.ID)
	}
	return c.JSON(http.StatusCreated, newWatchedWordListView(l))
}

func (s *Server) updateWordList(c echo.Context, owner uuid.UUID) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	id, err := pathUUID(c, "id", "watched-word list not found")
	if err != nil {
		return err
	}
	var in updateWordListRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	var words []string
	if in.Words != nil {
		words = *in.Words
	}
	l, err := s.watchwordsvc.UpdateList(c.Request().Context(), owner, userID, id, in.Name, in.Action, words, in.Words != nil)
	if err != nil {
		return wordListError(err)
	}
	if owner == uuid.Nil {
		s.auditWordList(c, observability.ActionAdminWatchedWordListUpdate, userID, l.ID)
	}
	return c.JSON(http.StatusOK, newWatchedWordListView(l))
}

func (s *Server) deleteWordList(c echo.Context, owner uuid.UUID) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	id, err := pathUUID(c, "id", "watched-word list not found")
	if err != nil {
		return err
	}
	if err := s.watchwordsvc.DeleteList(c.Request().Context(), owner, id); err != nil {
		return wordListError(err)
	}
	if owner == uuid.Nil {
		s.auditWordList(c, observability.ActionAdminWatchedWordListDelete, userID, id)
	}
	return c.NoContent(http.StatusNoContent)
}

func (s *Server) auditWordList(c echo.Context, action string, actor, listID uuid.UUID) {
	s.auditEvent(c, audit.Event{
		Action: action, Result: observability.ResultSuccess,
		ActorID: actor.String(), ResourceType: "watched_word_list", ResourceID: listID.String(),
	})
}

// Instance scope (requireRole admin|moderator at the route).
func (s *Server) handleAdminListWordLists(c echo.Context) error { return s.listWordLists(c, uuid.Nil) }
func (s *Server) handleAdminGetWordList(c echo.Context) error   { return s.getWordList(c, uuid.Nil) }
func (s *Server) handleAdminCreateWordList(c echo.Context) error {
	return s.createWordList(c, uuid.Nil)
}
func (s *Server) handleAdminUpdateWordList(c echo.Context) error {
	return s.updateWordList(c, uuid.Nil)
}
func (s *Server) handleAdminDeleteWordList(c echo.Context) error {
	return s.deleteWordList(c, uuid.Nil)
}

// Account scope: the owner is always the authenticated caller.
func (s *Server) handleMyListWordLists(c echo.Context) error {
	uid, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	return s.listWordLists(c, uid)
}
func (s *Server) handleMyGetWordList(c echo.Context) error {
	uid, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	return s.getWordList(c, uid)
}
func (s *Server) handleMyCreateWordList(c echo.Context) error {
	uid, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	return s.createWordList(c, uid)
}
func (s *Server) handleMyUpdateWordList(c echo.Context) error {
	uid, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	return s.updateWordList(c, uid)
}
func (s *Server) handleMyDeleteWordList(c echo.Context) error {
	uid, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	return s.deleteWordList(c, uid)
}
