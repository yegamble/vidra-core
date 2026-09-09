package httpapi

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// The in-memory half of step_up_tokens (migration 0143). It mirrors the SQL's
// SEMANTICS rather than merely satisfying the interface: the consume predicate
// includes the owning account AND the owning session AND used_at IS NULL AND
// an unexpired expires_at, because those four are exactly what the negatives
// test — another session's token, another user's token, a second use, an
// expired one — have to be able to fail on.

func (f *authFakeRepo) stepUpStore() map[string]*sqlcgen.StepUpToken {
	if f.stepUps == nil {
		f.stepUps = map[string]*sqlcgen.StepUpToken{}
	}
	return f.stepUps
}

func (f *authFakeRepo) CreateStepUpToken(_ context.Context, a sqlcgen.CreateStepUpTokenParams) (sqlcgen.StepUpToken, error) {
	r := sqlcgen.StepUpToken{
		ID: uuid.New(), UserID: a.UserID, SessionID: a.SessionID, Provider: a.Provider,
		TokenHash: a.TokenHash, ExpiresAt: a.ExpiresAt, CreatedAt: time.Now(),
	}
	f.stepUpStore()[a.TokenHash] = &r
	return r, nil
}

func (f *authFakeRepo) ConsumeStepUpToken(_ context.Context, a sqlcgen.ConsumeStepUpTokenParams) (sqlcgen.ConsumeStepUpTokenRow, error) {
	r, ok := f.stepUpStore()[a.TokenHash]
	if !ok || r.UserID != a.UserID || r.SessionID != a.SessionID || r.UsedAt.Valid || !r.ExpiresAt.After(time.Now()) {
		return sqlcgen.ConsumeStepUpTokenRow{}, pgx.ErrNoRows
	}
	r.UsedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	return sqlcgen.ConsumeStepUpTokenRow{ID: r.ID, Provider: r.Provider}, nil
}

func (f *authFakeRepo) DeleteUnusedStepUpTokensForSession(_ context.Context, sessionID uuid.UUID) (int64, error) {
	var n int64
	for hash, r := range f.stepUpStore() {
		if r.SessionID == sessionID && !r.UsedAt.Valid {
			delete(f.stepUps, hash)
			n++
		}
	}
	return n, nil
}

func (f *authFakeRepo) DeleteExpiredStepUpTokens(_ context.Context) (int64, error) {
	var n int64
	for hash, r := range f.stepUpStore() {
		if !r.ExpiresAt.After(time.Now()) {
			delete(f.stepUps, hash)
			n++
		}
	}
	return n, nil
}

// ListOAuthIdentitiesByUser is what the auth service asks to answer "which
// providers could satisfy a step-up for this account". Harnesses that own a
// richer identity store (oauthHTTPFakeRepo, which the real OAuth/ATProto flows
// write into) point identitiesOf at it, so the two stores cannot disagree —
// which is exactly the bug the first draft of these tests had: a login wrote an
// identity the step-up could not see.
func (f *authFakeRepo) ListOAuthIdentitiesByUser(_ context.Context, userID uuid.UUID) ([]sqlcgen.OauthIdentity, error) {
	if f.identitiesOf != nil {
		return f.identitiesOf(userID), nil
	}
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
func (f *authFakeRepo) linkIdentity(userID uuid.UUID, provider, subject string) {
	f.oauthIdents = append(f.oauthIdents, sqlcgen.OauthIdentity{
		ID: uuid.New(), Provider: provider, Subject: subject, UserID: userID, CreatedAt: time.Now(),
	})
}
