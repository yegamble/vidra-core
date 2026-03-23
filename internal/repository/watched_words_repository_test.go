package repository

import (
	"context"
	"database/sql"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vidra-core/internal/domain"
)

func setupWatchedWordsMock(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	return sqlx.NewDb(db, "postgres"), mock
}

func TestWatchedWordsRepository_ListByAccount_ServerLevel(t *testing.T) {
	db, mock := setupWatchedWordsMock(t)
	repo := NewWatchedWordsRepository(db)

	now := time.Now()
	rows := sqlmock.NewRows([]string{"id", "account_name", "list_name", "words", "created_at", "updated_at"}).
		AddRow(1, nil, "profanity", `["bad","word"]`, now, now)

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, account_name, list_name, words, created_at, updated_at
			 FROM watched_word_lists
			 WHERE account_name IS NULL
			 ORDER BY created_at DESC`)).
		WillReturnRows(rows)

	lists, err := repo.ListByAccount(context.Background(), nil)
	require.NoError(t, err)
	require.Len(t, lists, 1)
	assert.Equal(t, "profanity", lists[0].ListName)
	assert.Equal(t, []string{"bad", "word"}, lists[0].Words)
	assert.Nil(t, lists[0].AccountName)
}

func TestWatchedWordsRepository_ListByAccount_AccountLevel(t *testing.T) {
	db, mock := setupWatchedWordsMock(t)
	repo := NewWatchedWordsRepository(db)

	acct := "testuser"
	now := time.Now()
	rows := sqlmock.NewRows([]string{"id", "account_name", "list_name", "words", "created_at", "updated_at"}).
		AddRow(2, acct, "spam", `["buy","now"]`, now, now)

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, account_name, list_name, words, created_at, updated_at
			 FROM watched_word_lists
			 WHERE account_name = $1
			 ORDER BY created_at DESC`)).
		WithArgs(acct).
		WillReturnRows(rows)

	lists, err := repo.ListByAccount(context.Background(), &acct)
	require.NoError(t, err)
	require.Len(t, lists, 1)
	assert.Equal(t, "spam", lists[0].ListName)
	assert.Equal(t, []string{"buy", "now"}, lists[0].Words)
}

func TestWatchedWordsRepository_GetByID_Found(t *testing.T) {
	db, mock := setupWatchedWordsMock(t)
	repo := NewWatchedWordsRepository(db)

	now := time.Now()
	rows := sqlmock.NewRows([]string{"id", "account_name", "list_name", "words", "created_at", "updated_at"}).
		AddRow(1, nil, "profanity", `["bad"]`, now, now)

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, account_name, list_name, words, created_at, updated_at
		 FROM watched_word_lists
		 WHERE id = $1`)).
		WithArgs(int64(1)).
		WillReturnRows(rows)

	list, err := repo.GetByID(context.Background(), 1)
	require.NoError(t, err)
	assert.Equal(t, "profanity", list.ListName)
	assert.Equal(t, []string{"bad"}, list.Words)
}

func TestWatchedWordsRepository_GetByID_NotFound(t *testing.T) {
	db, mock := setupWatchedWordsMock(t)
	repo := NewWatchedWordsRepository(db)

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, account_name, list_name, words, created_at, updated_at
		 FROM watched_word_lists
		 WHERE id = $1`)).
		WithArgs(int64(999)).
		WillReturnError(sql.ErrNoRows)

	_, err := repo.GetByID(context.Background(), 999)
	assert.ErrorIs(t, err, domain.ErrWatchedWordListNotFound)
}

func TestWatchedWordsRepository_Create(t *testing.T) {
	db, mock := setupWatchedWordsMock(t)
	repo := NewWatchedWordsRepository(db)

	now := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO watched_word_lists (account_name, list_name, words)
		 VALUES ($1, $2, $3)
		 RETURNING id, created_at, updated_at`)).
		WithArgs(nil, "test", `["word1","word2"]`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).
			AddRow(1, now, now))

	list := &domain.WatchedWordList{
		ListName: "test",
		Words:    []string{"word1", "word2"},
	}

	err := repo.Create(context.Background(), list)
	require.NoError(t, err)
	assert.Equal(t, int64(1), list.ID)
}

func TestWatchedWordsRepository_Update_Found(t *testing.T) {
	db, mock := setupWatchedWordsMock(t)
	repo := NewWatchedWordsRepository(db)

	mock.ExpectExec(regexp.QuoteMeta(`UPDATE watched_word_lists
		 SET list_name = $1, words = $2
		 WHERE id = $3`)).
		WithArgs("updated", `["new"]`, int64(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	list := &domain.WatchedWordList{
		ID:       1,
		ListName: "updated",
		Words:    []string{"new"},
	}

	err := repo.Update(context.Background(), list)
	require.NoError(t, err)
}

func TestWatchedWordsRepository_Update_NotFound(t *testing.T) {
	db, mock := setupWatchedWordsMock(t)
	repo := NewWatchedWordsRepository(db)

	mock.ExpectExec(regexp.QuoteMeta(`UPDATE watched_word_lists
		 SET list_name = $1, words = $2
		 WHERE id = $3`)).
		WithArgs("updated", `["new"]`, int64(999)).
		WillReturnResult(sqlmock.NewResult(0, 0))

	list := &domain.WatchedWordList{
		ID:       999,
		ListName: "updated",
		Words:    []string{"new"},
	}

	err := repo.Update(context.Background(), list)
	assert.ErrorIs(t, err, domain.ErrWatchedWordListNotFound)
}

func TestWatchedWordsRepository_Delete_Found(t *testing.T) {
	db, mock := setupWatchedWordsMock(t)
	repo := NewWatchedWordsRepository(db)

	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM watched_word_lists WHERE id = $1`)).
		WithArgs(int64(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := repo.Delete(context.Background(), 1)
	require.NoError(t, err)
}

func TestWatchedWordsRepository_Delete_NotFound(t *testing.T) {
	db, mock := setupWatchedWordsMock(t)
	repo := NewWatchedWordsRepository(db)

	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM watched_word_lists WHERE id = $1`)).
		WithArgs(int64(999)).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := repo.Delete(context.Background(), 999)
	assert.ErrorIs(t, err, domain.ErrWatchedWordListNotFound)
}
