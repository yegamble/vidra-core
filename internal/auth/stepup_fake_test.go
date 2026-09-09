package auth

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// The in-memory half of step_up_tokens (migration 0144). It mirrors the SQL's
// SEMANTICS rather than merely satisfying the interface: the consume predicate
// includes the owning account AND the owning session AND used_at IS NULL AND
// an unexpired expires_at, because those four are exactly what the negatives
// test — another session's token, another user's token, a second use, an
// expired one — have to be able to fail on.

func (f *fakeRepo) stepUpStore() map[string]*sqlcgen.StepUpToken {
	if f.stepUps == nil {
		f.stepUps = map[string]*sqlcgen.StepUpToken{}
	}
	return f.stepUps
}

func (f *fakeRepo) CreateStepUpToken(_ context.Context, a sqlcgen.CreateStepUpTokenParams) (sqlcgen.StepUpToken, error) {
	r := sqlcgen.StepUpToken{
		ID: uuid.New(), UserID: a.UserID, SessionID: a.SessionID, Provider: a.Provider,
		TokenHash: a.TokenHash, ExpiresAt: a.ExpiresAt, CreatedAt: time.Now(),
	}
	f.stepUpStore()[a.TokenHash] = &r
	return r, nil
}

func (f *fakeRepo) ConsumeStepUpToken(_ context.Context, a sqlcgen.ConsumeStepUpTokenParams) (sqlcgen.ConsumeStepUpTokenRow, error) {
	r, ok := f.stepUpStore()[a.TokenHash]
	if !ok || r.UserID != a.UserID || r.SessionID != a.SessionID || r.UsedAt.Valid || !r.ExpiresAt.After(time.Now()) {
		return sqlcgen.ConsumeStepUpTokenRow{}, pgx.ErrNoRows
	}
	r.UsedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	return sqlcgen.ConsumeStepUpTokenRow{ID: r.ID, Provider: r.Provider}, nil
}

func (f *fakeRepo) DeleteUnusedStepUpTokensForSession(_ context.Context, sessionID uuid.UUID) (int64, error) {
	var n int64
	for hash, r := range f.stepUpStore() {
		if r.SessionID == sessionID && !r.UsedAt.Valid {
			delete(f.stepUps, hash)
			n++
		}
	}
	return n, nil
}

func (f *fakeRepo) DeleteExpiredStepUpTokens(_ context.Context) (int64, error) {
	var n int64
	for hash, r := range f.stepUpStore() {
		if !r.ExpiresAt.After(time.Now()) {
			delete(f.stepUps, hash)
			n++
		}
	}
	return n, nil
}

func (f *fakeRepo) ListOAuthIdentitiesByUser(_ context.Context, userID uuid.UUID) ([]sqlcgen.OauthIdentity, error) {
	var out []sqlcgen.OauthIdentity
	for _, id := range f.oauthIdents {
		if id.UserID == userID {
			out = append(out, id)
		}
	}
	return out, nil
}

// linkIdentity is the test-side "this account signs in with that provider",
// which is what a step-up refusal names and what the callback's subject check
// consults.
func (f *fakeRepo) linkIdentity(userID uuid.UUID, provider, subject string) {
	f.oauthIdents = append(f.oauthIdents, sqlcgen.OauthIdentity{
		ID: uuid.New(), Provider: provider, Subject: subject, UserID: userID, CreatedAt: time.Now(),
	})
}

func (f *fakeRepo) MoveStepUpTokensToSession(_ context.Context, a sqlcgen.MoveStepUpTokensToSessionParams) (int64, error) {
	var n int64
	for _, r := range f.stepUpStore() {
		// The same predicate the SQL carries: only this session's rows, only
		// live ones. A spent or expired assertion must not be resurrected by
		// being moved.
		if r.SessionID == a.FromSessionID && !r.UsedAt.Valid && r.ExpiresAt.After(time.Now()) {
			r.SessionID = a.ToSessionID
			n++
		}
	}
	return n, nil
}
