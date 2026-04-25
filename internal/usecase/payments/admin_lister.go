package payments

import (
	"context"

	"github.com/jmoiron/sqlx"
)

// SQLAdminLister returns the user IDs of every administrator account by
// querying the users table. Used for fan-out notifications when a payout
// enters 'pending' approval.
type SQLAdminLister struct {
	db *sqlx.DB
}

func NewSQLAdminLister(db *sqlx.DB) *SQLAdminLister { return &SQLAdminLister{db: db} }

// ListAdminIDs returns the IDs of admin users (role='admin').
func (l *SQLAdminLister) ListAdminIDs(ctx context.Context) ([]string, error) {
	if l == nil || l.db == nil {
		return nil, nil
	}
	var ids []string
	if err := l.db.SelectContext(ctx, &ids,
		"SELECT id::text FROM users WHERE role = 'admin'"); err != nil {
		return nil, err
	}
	return ids, nil
}
