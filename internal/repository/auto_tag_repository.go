package repository

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"

	"athena/internal/domain"
	"athena/internal/port"
)

type autoTagRepository struct {
	db *sqlx.DB
}

// NewAutoTagRepository creates a new AutoTagRepository.
func NewAutoTagRepository(db *sqlx.DB) port.AutoTagRepository {
	return &autoTagRepository{db: db}
}

func (r *autoTagRepository) ListByAccount(ctx context.Context, accountName *string) ([]*domain.AutoTagPolicy, error) {
	var policies []*domain.AutoTagPolicy
	var err error

	if accountName == nil {
		err = r.db.SelectContext(ctx, &policies,
			`SELECT id, account_name, tag_type, review_type, list_id
			 FROM auto_tag_policies
			 WHERE account_name IS NULL
			 ORDER BY id`)
	} else {
		err = r.db.SelectContext(ctx, &policies,
			`SELECT id, account_name, tag_type, review_type, list_id
			 FROM auto_tag_policies
			 WHERE account_name = $1
			 ORDER BY id`, *accountName)
	}

	if err != nil {
		return nil, fmt.Errorf("list auto tag policies: %w", err)
	}

	return policies, nil
}

func (r *autoTagRepository) ReplaceByAccount(ctx context.Context, accountName *string, policies []*domain.AutoTagPolicy) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Delete existing policies for this account
	if accountName == nil {
		_, err = tx.ExecContext(ctx,
			`DELETE FROM auto_tag_policies WHERE account_name IS NULL`)
	} else {
		_, err = tx.ExecContext(ctx,
			`DELETE FROM auto_tag_policies WHERE account_name = $1`, *accountName)
	}
	if err != nil {
		return fmt.Errorf("delete existing policies: %w", err)
	}

	// Insert new policies
	for _, p := range policies {
		_, err = tx.ExecContext(ctx,
			`INSERT INTO auto_tag_policies (account_name, tag_type, review_type, list_id)
			 VALUES ($1, $2, $3, $4)`,
			accountName, p.TagType, p.ReviewType, p.ListID)
		if err != nil {
			return fmt.Errorf("insert auto tag policy: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}
