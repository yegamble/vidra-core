// Package watchword implements the moderation "watched words" list for vidra-core:
// an instance-wide set of terms a moderator/admin maintains. This package owns the
// list management (add / list / delete); the matching/flagging effect on content
// (comments, videos) is applied by those surfaces in a later slice. It is
// HTTP-agnostic and testable without a server.
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
	DeleteWatchedWord(ctx context.Context, id uuid.UUID) (int64, error)
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

// List returns the watched words, newest first. The caller clamps limit/offset.
func (s *Service) List(ctx context.Context, limit, offset int32) ([]WatchedWord, error) {
	rows, err := s.repo.ListWatchedWords(ctx, sqlcgen.ListWatchedWordsParams{
		ResultLimit:  limit,
		ResultOffset: offset,
	})
	if err != nil {
		return nil, err
	}
	items := make([]WatchedWord, 0, len(rows))
	for _, r := range rows {
		item := WatchedWord{ID: r.ID, Word: r.Word, CreatedAt: r.CreatedAt}
		if r.CreatedByUsername != nil {
			item.CreatedByUsername = *r.CreatedByUsername
		}
		items = append(items, item)
	}
	return items, nil
}

// Delete removes a term from the list (idempotent: removing an absent term is a
// no-op).
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := s.repo.DeleteWatchedWord(ctx, id)
	return err
}

// sqlState returns the SQLSTATE code of a PostgreSQL error, "" otherwise.
func sqlState(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}
