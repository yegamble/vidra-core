package httpapi

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// The in-memory half of email_change_requests (migration 0129) for handler
// tests. It mirrors the SQL's semantics rather than merely working: the confirm
// predicate includes the OWNING account and used_at IS NULL, and the users
// unique index is what refuses an address taken since the request.

func (f *authFakeRepo) emailChangeStore() map[string]*sqlcgen.EmailChangeRequest {
	if f.emailChanges == nil {
		f.emailChanges = map[string]*sqlcgen.EmailChangeRequest{}
	}
	return f.emailChanges
}

func (f *authFakeRepo) CreateEmailChangeRequest(_ context.Context, a sqlcgen.CreateEmailChangeRequestParams) (sqlcgen.EmailChangeRequest, error) {
	r := sqlcgen.EmailChangeRequest{
		ID: uuid.New(), UserID: a.UserID, NewEmail: a.NewEmail,
		TokenHash: a.TokenHash, ExpiresAt: a.ExpiresAt, CreatedAt: time.Now(),
	}
	f.emailChangeStore()[a.TokenHash] = &r
	return r, nil
}

func (f *authFakeRepo) GetPendingEmailChangeRequest(_ context.Context, userID uuid.UUID) (sqlcgen.EmailChangeRequest, error) {
	var newest *sqlcgen.EmailChangeRequest
	for _, r := range f.emailChangeStore() {
		if r.UserID != userID || r.UsedAt.Valid || !r.ExpiresAt.After(time.Now()) {
			continue
		}
		if newest == nil || r.CreatedAt.After(newest.CreatedAt) {
			newest = r
		}
	}
	if newest == nil {
		return sqlcgen.EmailChangeRequest{}, pgx.ErrNoRows
	}
	return *newest, nil
}

func (f *authFakeRepo) DeleteUnusedEmailChangeRequests(_ context.Context, userID uuid.UUID) (int64, error) {
	var n int64
	for h, r := range f.emailChangeStore() {
		if r.UserID == userID && !r.UsedAt.Valid {
			delete(f.emailChanges, h)
			n++
		}
	}
	return n, nil
}

func (f *authFakeRepo) ConfirmEmailChange(_ context.Context, a sqlcgen.ConfirmEmailChangeParams) (sqlcgen.ConfirmEmailChangeRow, error) {
	r, ok := f.emailChangeStore()[a.TokenHash]
	if !ok || r.UserID != a.UserID || r.UsedAt.Valid || !r.ExpiresAt.After(time.Now()) {
		return sqlcgen.ConfirmEmailChangeRow{}, pgx.ErrNoRows
	}
	if other, ok := f.users[strings.ToLower(r.NewEmail)]; ok && other.ID != r.UserID {
		return sqlcgen.ConfirmEmailChangeRow{}, &pgconn.PgError{Code: "23505"}
	}
	r.UsedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	for key, u := range f.users {
		if u.ID != r.UserID {
			continue
		}
		delete(f.users, key)
		u.Email = r.NewEmail
		u.EmailVerified = true
		u.UpdatedAt = time.Now()
		f.users[strings.ToLower(r.NewEmail)] = u
		return sqlcgen.ConfirmEmailChangeRow{ID: u.ID, Email: u.Email}, nil
	}
	return sqlcgen.ConfirmEmailChangeRow{}, pgx.ErrNoRows
}
