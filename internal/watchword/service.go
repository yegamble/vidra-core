// Package watchword implements the moderation "watched words" list for vidra-core:
// an instance-wide set of terms a moderator/admin maintains, plus the matching/
// flagging of content against it — comment bodies on post/edit and video
// title+description on create/edit (§12). Flagging only records matches for the
// moderator review queue; it never blocks or hides the content itself (the §11
// quarantine pipeline is the hold mechanism for videos). It is HTTP-agnostic and
// testable without a server.
package watchword

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// ErrAlreadyExists means the term is already on the list (case-insensitive).
var ErrAlreadyExists = errors.New("watchword: already exists")

// Repository is the data access the watched-words service needs. *sqlcgen.Queries
// satisfies it directly; tests substitute an in-memory fake.
type Repository interface {
	CreateWatchedWord(ctx context.Context, arg sqlcgen.CreateWatchedWordParams) (sqlcgen.WatchedWord, error)
	ListWatchedWords(ctx context.Context, arg sqlcgen.ListWatchedWordsParams) ([]sqlcgen.ListWatchedWordsRow, error)
	CountWatchedWords(ctx context.Context) (int64, error)
	DeleteWatchedWord(ctx context.Context, id uuid.UUID) (int64, error)
	MatchWatchedWords(ctx context.Context, text string) ([]sqlcgen.MatchWatchedWordsRow, error)
	RecordWatchedWordMatch(ctx context.Context, arg sqlcgen.RecordWatchedWordMatchParams) error
	RecordWatchedWordVideoMatch(ctx context.Context, arg sqlcgen.RecordWatchedWordVideoMatchParams) error
	ListWatchedWordMatches(ctx context.Context, arg sqlcgen.ListWatchedWordMatchesParams) ([]sqlcgen.ListWatchedWordMatchesRow, error)
	CountWatchedWordMatches(ctx context.Context) (int64, error)
}

// Service holds the watched-words application logic.
type Service struct {
	repo Repository
}

// NewService builds the watched-words service.
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// WatchedWord is a term on the list, with who added it and when.
type WatchedWord struct {
	ID                uuid.UUID
	Word              string
	CreatedByUsername string
	CreatedAt         time.Time
}

// Add appends a term to the watched-words list. A duplicate (case-insensitive) →
// ErrAlreadyExists.
func (s *Service) Add(ctx context.Context, word string, createdBy uuid.UUID) (WatchedWord, error) {
	row, err := s.repo.CreateWatchedWord(ctx, sqlcgen.CreateWatchedWordParams{
		Word:      word,
		CreatedBy: pgtype.UUID{Bytes: createdBy, Valid: true},
	})
	if sqlState(err) == "23505" { // unique violation: term already on the list
		return WatchedWord{}, ErrAlreadyExists
	}
	if err != nil {
		return WatchedWord{}, err
	}
	return WatchedWord{ID: row.ID, Word: row.Word, CreatedAt: row.CreatedAt}, nil
}

// List returns the watched words, newest first, with the total. The caller
// clamps limit/offset.
func (s *Service) List(ctx context.Context, limit, offset int32) ([]WatchedWord, int64, error) {
	rows, err := s.repo.ListWatchedWords(ctx, sqlcgen.ListWatchedWordsParams{
		ResultLimit:  limit,
		ResultOffset: offset,
	})
	if err != nil {
		return nil, 0, err
	}
	total, err := s.repo.CountWatchedWords(ctx)
	if err != nil {
		return nil, 0, err
	}
	items := make([]WatchedWord, 0, len(rows))
	for _, r := range rows {
		item := WatchedWord{ID: r.ID, Word: r.Word, CreatedAt: r.CreatedAt}
		if r.CreatedByUsername != nil {
			item.CreatedByUsername = *r.CreatedByUsername
		}
		items = append(items, item)
	}
	return items, total, nil
}

// Delete removes a term from the list (idempotent: removing an absent term is a
// no-op).
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := s.repo.DeleteWatchedWord(ctx, id)
	return err
}

// FlagComment checks a comment's body against the watched-words list and records
// a match row for each term found (idempotent per word+comment). It returns the
// number of terms matched. Best-effort: callers invoke it as a side effect of
// posting a comment and must not fail the comment on error.
func (s *Service) FlagComment(ctx context.Context, commentID uuid.UUID, body string) (int, error) {
	matches, err := s.repo.MatchWatchedWords(ctx, body)
	if err != nil {
		return 0, err
	}
	for _, m := range matches {
		if err := s.repo.RecordWatchedWordMatch(ctx, sqlcgen.RecordWatchedWordMatchParams{
			WatchedWordID: m.ID,
			CommentID:     pgtype.UUID{Bytes: commentID, Valid: true},
		}); err != nil {
			return 0, err
		}
	}
	return len(matches), nil
}

// FlagVideo checks a video's title+description against the watched-words list
// and records a match row for each term found (idempotent per word+video). It
// returns the number of terms matched. Best-effort: callers invoke it as a side
// effect of creating/editing a video and must not fail the write on error. No
// auto-hold: the §11 quarantine pipeline is the hold mechanism for videos.
func (s *Service) FlagVideo(ctx context.Context, videoID uuid.UUID, text string) (int, error) {
	matches, err := s.repo.MatchWatchedWords(ctx, text)
	if err != nil {
		return 0, err
	}
	for _, m := range matches {
		if err := s.repo.RecordWatchedWordVideoMatch(ctx, sqlcgen.RecordWatchedWordVideoMatchParams{
			WatchedWordID: m.ID,
			VideoID:       pgtype.UUID{Bytes: videoID, Valid: true},
		}); err != nil {
			return 0, err
		}
	}
	return len(matches), nil
}

// Match target discriminators for the review queue.
const (
	MatchTargetComment = "comment"
	MatchTargetVideo   = "video"
)

// Match is flagged content for the moderation review queue: the matched term
// plus the target's context. Type says what was flagged — a comment (CommentID/
// CommentBody set; VideoID is the video the comment is on) or a video
// (CommentID/CommentBody empty; VideoID/VideoTitle are the flagged video). The
// author is the comment's author or the video's owner respectively.
type Match struct {
	ID             uuid.UUID
	Word           string
	Type           string
	CommentID      uuid.UUID
	CommentBody    string
	VideoID        uuid.UUID
	VideoTitle     string
	AuthorUsername string
	CreatedAt      time.Time
}

// ListMatches returns flagged comments and videos, newest match first, with the
// total. The caller clamps limit/offset.
func (s *Service) ListMatches(ctx context.Context, limit, offset int32) ([]Match, int64, error) {
	rows, err := s.repo.ListWatchedWordMatches(ctx, sqlcgen.ListWatchedWordMatchesParams{
		ResultLimit:  limit,
		ResultOffset: offset,
	})
	if err != nil {
		return nil, 0, err
	}
	total, err := s.repo.CountWatchedWordMatches(ctx)
	if err != nil {
		return nil, 0, err
	}
	out := make([]Match, 0, len(rows))
	for _, r := range rows {
		m := Match{
			ID: r.ID, Word: r.Word, Type: MatchTargetVideo,
			VideoID: r.VideoID, AuthorUsername: r.AuthorUsername, CreatedAt: r.CreatedAt,
		}
		if r.VideoTitle != nil {
			m.VideoTitle = *r.VideoTitle
		}
		if r.CommentID.Valid {
			m.Type = MatchTargetComment
			m.CommentID = uuid.UUID(r.CommentID.Bytes)
			if r.CommentBody != nil {
				m.CommentBody = *r.CommentBody
			}
		}
		out = append(out, m)
	}
	return out, total, nil
}

// sqlState returns the SQLSTATE code of a PostgreSQL error, "" otherwise.
func sqlState(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}
