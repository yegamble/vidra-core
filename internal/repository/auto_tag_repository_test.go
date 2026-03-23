package repository

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"athena/internal/domain"
)

func setupAutoTagMock(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	return sqlx.NewDb(db, "postgres"), mock
}

func TestAutoTagRepository_ListByAccount_ServerLevel(t *testing.T) {
	db, mock := setupAutoTagMock(t)
	repo := NewAutoTagRepository(db)

	rows := sqlmock.NewRows([]string{"id", "account_name", "tag_type", "review_type", "list_id"}).
		AddRow(1, nil, "external-link", "review-comments", nil)

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, account_name, tag_type, review_type, list_id
			 FROM auto_tag_policies
			 WHERE account_name IS NULL
			 ORDER BY id`)).
		WillReturnRows(rows)

	policies, err := repo.ListByAccount(context.Background(), nil)
	require.NoError(t, err)
	require.Len(t, policies, 1)
	assert.Equal(t, "external-link", policies[0].TagType)
	assert.Equal(t, "review-comments", policies[0].ReviewType)
}

func TestAutoTagRepository_ListByAccount_AccountLevel(t *testing.T) {
	db, mock := setupAutoTagMock(t)
	repo := NewAutoTagRepository(db)

	acct := "testuser"
	listID := int64(42)
	rows := sqlmock.NewRows([]string{"id", "account_name", "tag_type", "review_type", "list_id"}).
		AddRow(2, acct, "watched-words", "block-comments", listID)

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, account_name, tag_type, review_type, list_id
			 FROM auto_tag_policies
			 WHERE account_name = $1
			 ORDER BY id`)).
		WithArgs(acct).
		WillReturnRows(rows)

	policies, err := repo.ListByAccount(context.Background(), &acct)
	require.NoError(t, err)
	require.Len(t, policies, 1)
	assert.Equal(t, "watched-words", policies[0].TagType)
	assert.NotNil(t, policies[0].ListID)
	assert.Equal(t, int64(42), *policies[0].ListID)
}

func TestAutoTagRepository_ReplaceByAccount_ServerLevel(t *testing.T) {
	db, mock := setupAutoTagMock(t)
	repo := NewAutoTagRepository(db)

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM auto_tag_policies WHERE account_name IS NULL`)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO auto_tag_policies (account_name, tag_type, review_type, list_id)
			 VALUES ($1, $2, $3, $4)`)).
		WithArgs(nil, "external-link", "review-comments", nil).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	policies := []*domain.AutoTagPolicy{
		{TagType: "external-link", ReviewType: "review-comments"},
	}

	err := repo.ReplaceByAccount(context.Background(), nil, policies)
	require.NoError(t, err)
}

func TestAutoTagRepository_ReplaceByAccount_AccountLevel(t *testing.T) {
	db, mock := setupAutoTagMock(t)
	repo := NewAutoTagRepository(db)

	acct := "testuser"
	listID := int64(42)

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM auto_tag_policies WHERE account_name = $1`)).
		WithArgs(&acct).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO auto_tag_policies (account_name, tag_type, review_type, list_id)
			 VALUES ($1, $2, $3, $4)`)).
		WithArgs(&acct, "watched-words", "block-comments", &listID).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	policies := []*domain.AutoTagPolicy{
		{TagType: "watched-words", ReviewType: "block-comments", ListID: &listID},
	}

	err := repo.ReplaceByAccount(context.Background(), &acct, policies)
	require.NoError(t, err)
}

func TestAutoTagRepository_ReplaceByAccount_EmptyPolicies(t *testing.T) {
	db, mock := setupAutoTagMock(t)
	repo := NewAutoTagRepository(db)

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM auto_tag_policies WHERE account_name IS NULL`)).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()

	err := repo.ReplaceByAccount(context.Background(), nil, []*domain.AutoTagPolicy{})
	require.NoError(t, err)
}
