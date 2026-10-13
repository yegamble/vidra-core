package watchword

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/pgconv"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Named lists (migration 0172). A list is either an INSTANCE list (owner
// uuid.Nil, managed by moderators) or a creator's own list (owner = the
// creator, applied only to comments on that creator's videos). Every method
// takes the scope's owner and never reaches across it: a list in the other
// scope reads as absent, which is what makes the per-account surface owner-only.
const (
	// MaxWordsPerList bounds one list so the replace-all write stays a
	// single small statement. The default list is exempt on the flat endpoints.
	MaxWordsPerList = 500
	// MaxListsPerAccount bounds a creator's own lists.
	MaxListsPerAccount = 20
)

var (
	ErrListNotFound  = errors.New("watchword: list not found")
	ErrListNameTaken = errors.New("watchword: list name already used")
	ErrDefaultList   = errors.New("watchword: the default list cannot be deleted or have its words replaced here")
	ErrTooManyLists  = errors.New("watchword: too many lists")
	ErrTooManyWords  = errors.New("watchword: too many words in one list")
	ErrBadAction     = errors.New("watchword: action must be flag or hold")
)

// WordListRepository is the list-management data access.
type WordListRepository interface {
	CreateWatchedWordList(ctx context.Context, arg sqlcgen.CreateWatchedWordListParams) (sqlcgen.WatchedWordList, error)
	GetWatchedWordList(ctx context.Context, arg sqlcgen.GetWatchedWordListParams) (sqlcgen.GetWatchedWordListRow, error)
	ListWatchedWordLists(ctx context.Context, arg sqlcgen.ListWatchedWordListsParams) ([]sqlcgen.ListWatchedWordListsRow, error)
	CountWatchedWordLists(ctx context.Context, ownerID pgtype.UUID) (int64, error)
	CountOwnedWatchedWordLists(ctx context.Context, ownerID pgtype.UUID) (int64, error)
	UpdateWatchedWordList(ctx context.Context, arg sqlcgen.UpdateWatchedWordListParams) (sqlcgen.WatchedWordList, error)
	DeleteWatchedWordList(ctx context.Context, arg sqlcgen.DeleteWatchedWordListParams) (int64, error)
	ListWatchedWordListWords(ctx context.Context, listID pgtype.UUID) ([]string, error)
	ReplaceWatchedWordListWords(ctx context.Context, arg sqlcgen.ReplaceWatchedWordListWordsParams) error
}

// List is a named list. Words is filled by Get/Create/Update only; List
// (the page) reports WordCount.
type List struct {
	ID        uuid.UUID
	Name      string
	Action    string
	IsDefault bool
	WordCount int64
	Words     []string
	CreatedAt time.Time
	UpdatedAt time.Time
}

func scope(owner uuid.UUID) pgtype.UUID {
	if owner == uuid.Nil {
		return pgtype.UUID{}
	}
	return pgconv.UUID(owner)
}

func validAction(a string) bool { return a == ActionFlag || a == ActionHold }

func cleanWords(words []string) ([]string, error) {
	out := make([]string, 0, len(words))
	for _, w := range words {
		if w = strings.TrimSpace(w); w != "" {
			out = append(out, w)
		}
	}
	if len(out) > MaxWordsPerList {
		return nil, ErrTooManyWords
	}
	return out, nil
}

// ListLists returns one page of a scope's lists (default first) and the total.
func (s *Service) ListLists(ctx context.Context, owner uuid.UUID, limit, offset int32) ([]List, int64, error) {
	rows, err := s.repo.ListWatchedWordLists(ctx, sqlcgen.ListWatchedWordListsParams{OwnerID: scope(owner), ResultLimit: limit, ResultOffset: offset})
	if err != nil {
		return nil, 0, err
	}
	total, err := s.repo.CountWatchedWordLists(ctx, scope(owner))
	if err != nil {
		return nil, 0, err
	}
	out := make([]List, 0, len(rows))
	for _, r := range rows {
		out = append(out, List{ID: r.ID, Name: r.Name, Action: r.Action, IsDefault: r.IsDefault, WordCount: r.WordCount, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt})
	}
	return out, total, nil
}

// GetList returns one list WITH its words, or ErrListNotFound (absent, or in
// the other scope).
func (s *Service) GetList(ctx context.Context, owner, id uuid.UUID) (List, error) {
	r, err := s.repo.GetWatchedWordList(ctx, sqlcgen.GetWatchedWordListParams{ID: id, OwnerID: scope(owner)})
	if errors.Is(err, pgx.ErrNoRows) {
		return List{}, ErrListNotFound
	}
	if err != nil {
		return List{}, err
	}
	var words []string // the default list can be huge: the flat endpoints page it
	if !r.IsDefault {
		if words, err = s.repo.ListWatchedWordListWords(ctx, pgconv.UUID(id)); err != nil {
			return List{}, err
		}
	}
	return List{ID: r.ID, Name: r.Name, Action: r.Action, IsDefault: r.IsDefault, WordCount: r.WordCount,
		Words: words, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}, nil
}

// CreateList makes a list holding words. A creator is capped at
// MaxListsPerAccount; a name already used in the scope → ErrListNameTaken.
func (s *Service) CreateList(ctx context.Context, owner, actor uuid.UUID, name, action string, words []string) (List, error) {
	if action == "" {
		action = ActionFlag
	}
	if !validAction(action) {
		return List{}, ErrBadAction
	}
	words, err := cleanWords(words)
	if err != nil {
		return List{}, err
	}
	if owner != uuid.Nil {
		n, err := s.repo.CountOwnedWatchedWordLists(ctx, pgconv.UUID(owner))
		if err != nil {
			return List{}, err
		}
		if n >= MaxListsPerAccount {
			return List{}, ErrTooManyLists
		}
	}
	row, err := s.repo.CreateWatchedWordList(ctx, sqlcgen.CreateWatchedWordListParams{OwnerID: scope(owner), Name: strings.TrimSpace(name), Action: action})
	if pgconv.IsUniqueViolation(err) {
		return List{}, ErrListNameTaken
	}
	if err != nil {
		return List{}, err
	}
	if err := s.repo.ReplaceWatchedWordListWords(ctx, sqlcgen.ReplaceWatchedWordListWordsParams{ListID: pgconv.UUID(row.ID), CreatedBy: pgconv.UUID(actor), Words: words}); err != nil {
		// Do not leave a half-made list behind; the caller can simply retry.
		_, _ = s.repo.DeleteWatchedWordList(ctx, sqlcgen.DeleteWatchedWordListParams{ID: row.ID, OwnerID: scope(owner)})
		return List{}, err
	}
	return s.GetList(ctx, owner, row.ID)
}

// UpdateList changes a list's name/action and, when words != nil, REPLACES its
// terms. Nil fields are left as they are. Replacing the default list's words
// is refused (it can hold an unbounded legacy list; use the per-word endpoints).
func (s *Service) UpdateList(ctx context.Context, owner, actor, id uuid.UUID, name, action *string, words []string, replaceWords bool) (List, error) {
	cur, err := s.GetList(ctx, owner, id)
	if err != nil {
		return List{}, err
	}
	newName, newAction := cur.Name, cur.Action
	if name != nil {
		newName = strings.TrimSpace(*name)
	}
	if action != nil {
		newAction = *action
	}
	if !validAction(newAction) {
		return List{}, ErrBadAction
	}
	if replaceWords && cur.IsDefault {
		return List{}, ErrDefaultList
	}
	var clean []string
	if replaceWords {
		if clean, err = cleanWords(words); err != nil {
			return List{}, err
		}
	}
	if _, err := s.repo.UpdateWatchedWordList(ctx, sqlcgen.UpdateWatchedWordListParams{ID: id, OwnerID: scope(owner), Name: newName, Action: newAction}); err != nil {
		if pgconv.IsUniqueViolation(err) {
			return List{}, ErrListNameTaken
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return List{}, ErrListNotFound
		}
		return List{}, err
	}
	if replaceWords {
		if err := s.repo.ReplaceWatchedWordListWords(ctx, sqlcgen.ReplaceWatchedWordListWordsParams{ListID: pgconv.UUID(id), CreatedBy: pgconv.UUID(actor), Words: clean}); err != nil {
			return List{}, err
		}
	}
	return s.GetList(ctx, owner, id)
}

// DeleteList removes a list and its terms (matches keep their snapshot). The
// default list → ErrDefaultList; absent/other scope → ErrListNotFound.
func (s *Service) DeleteList(ctx context.Context, owner, id uuid.UUID) error {
	cur, err := s.GetList(ctx, owner, id)
	if err != nil {
		return err
	}
	if cur.IsDefault {
		return ErrDefaultList
	}
	n, err := s.repo.DeleteWatchedWordList(ctx, sqlcgen.DeleteWatchedWordListParams{ID: id, OwnerID: scope(owner)})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrListNotFound
	}
	return nil
}
