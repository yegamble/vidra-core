package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/jmoiron/sqlx"

	"vidra-core/internal/domain"
	"vidra-core/internal/port"
)

type watchedWordsRepository struct {
	db *sqlx.DB
}

// NewWatchedWordsRepository creates a new WatchedWordsRepository.
func NewWatchedWordsRepository(db *sqlx.DB) port.WatchedWordsRepository {
	return &watchedWordsRepository{db: db}
}

func (r *watchedWordsRepository) ListByAccount(ctx context.Context, accountName *string) ([]*domain.WatchedWordList, error) {
	var lists []*domain.WatchedWordList
	var err error

	if accountName == nil {
		err = r.db.SelectContext(ctx, &lists,
			`SELECT id, account_name, list_name, words, created_at, updated_at
			 FROM watched_word_lists
			 WHERE account_name IS NULL
			 ORDER BY created_at DESC`)
	} else {
		err = r.db.SelectContext(ctx, &lists,
			`SELECT id, account_name, list_name, words, created_at, updated_at
			 FROM watched_word_lists
			 WHERE account_name = $1
			 ORDER BY created_at DESC`, *accountName)
	}

	if err != nil {
		return nil, fmt.Errorf("list watched word lists: %w", err)
	}

	for _, list := range lists {
		if err := json.Unmarshal([]byte(list.WordsJSON), &list.Words); err != nil {
			return nil, fmt.Errorf("unmarshal words for list %d: %w", list.ID, err)
		}
	}

	return lists, nil
}

func (r *watchedWordsRepository) GetByID(ctx context.Context, id int64) (*domain.WatchedWordList, error) {
	var list domain.WatchedWordList
	err := r.db.GetContext(ctx, &list,
		`SELECT id, account_name, list_name, words, created_at, updated_at
		 FROM watched_word_lists
		 WHERE id = $1`, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.ErrWatchedWordListNotFound
		}
		return nil, fmt.Errorf("get watched word list: %w", err)
	}

	if err := json.Unmarshal([]byte(list.WordsJSON), &list.Words); err != nil {
		return nil, fmt.Errorf("unmarshal words: %w", err)
	}

	return &list, nil
}

func (r *watchedWordsRepository) Create(ctx context.Context, list *domain.WatchedWordList) error {
	wordsJSON, err := json.Marshal(list.Words)
	if err != nil {
		return fmt.Errorf("marshal words: %w", err)
	}

	err = r.db.QueryRowContext(ctx,
		`INSERT INTO watched_word_lists (account_name, list_name, words)
		 VALUES ($1, $2, $3)
		 RETURNING id, created_at, updated_at`,
		list.AccountName, list.ListName, string(wordsJSON),
	).Scan(&list.ID, &list.CreatedAt, &list.UpdatedAt)

	if err != nil {
		return fmt.Errorf("create watched word list: %w", err)
	}

	list.WordsJSON = string(wordsJSON)
	return nil
}

func (r *watchedWordsRepository) Update(ctx context.Context, list *domain.WatchedWordList) error {
	wordsJSON, err := json.Marshal(list.Words)
	if err != nil {
		return fmt.Errorf("marshal words: %w", err)
	}

	result, err := r.db.ExecContext(ctx,
		`UPDATE watched_word_lists
		 SET list_name = $1, words = $2
		 WHERE id = $3`,
		list.ListName, string(wordsJSON), list.ID)
	if err != nil {
		return fmt.Errorf("update watched word list: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return domain.ErrWatchedWordListNotFound
	}

	list.WordsJSON = string(wordsJSON)
	return nil
}

func (r *watchedWordsRepository) Delete(ctx context.Context, id int64) error {
	result, err := r.db.ExecContext(ctx,
		`DELETE FROM watched_word_lists WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete watched word list: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return domain.ErrWatchedWordListNotFound
	}

	return nil
}
