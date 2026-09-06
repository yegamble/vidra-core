//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// TestSessionRevocationQueriesPersist pins, against real PostgreSQL, the SQL
// semantics the in-memory fakes only mirror: the per-request revocation check
// behind every authenticated route (GetActiveSessionForAccessToken) and the
// "sign out my other devices" write a password change makes
// (RevokeOtherUserSessions). Both are load-bearing for AUTH-05: before them a
// revoked, deactivated or hard-deleted account's unexpired access token kept
// working for the whole JWT_ACCESS_TTL.
func TestSessionRevocationQueriesPersist(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	userID, _, cleanup := seedUserAndChannel(t, st)
	defer cleanup()

	newSession := func(label string, expiresIn time.Duration) uuid.UUID {
		t.Helper()
		row, err := q.CreateSession(ctx, sqlcgen.CreateSessionParams{
			UserID:      userID,
			RefreshHash: label + "-" + uuid.NewString(),
			UserAgent:   label,
			ExpiresAt:   time.Now().Add(expiresIn),
		})
		if err != nil {
			t.Fatalf("create session %s: %v", label, err)
		}
		return row.ID
	}
	active := func(id uuid.UUID) bool {
		t.Helper()
		row, err := q.GetActiveSessionForAccessToken(ctx, id)
		if err != nil {
			return false
		}
		if row.UserID != userID {
			t.Fatalf("session %s resolved to the wrong account", id)
		}
		return true
	}

	current := newSession("current", time.Hour)
	other := newSession("other", time.Hour)
	expired := newSession("expired", -time.Minute)

	// A live session resolves; an EXPIRED one does not, even though its row is
	// still present and not revoked.
	if !active(current) || !active(other) {
		t.Fatalf("a freshly created session does not resolve")
	}
	if active(expired) {
		t.Error("an expired session still authenticates an access token")
	}
	if active(uuid.New()) {
		t.Error("an unknown session id authenticates an access token")
	}

	// RevokeOtherUserSessions spares exactly one row — the point of it is that a
	// password change does not sign the changer out of their own browser.
	if err := q.RevokeOtherUserSessions(ctx, sqlcgen.RevokeOtherUserSessionsParams{
		UserID: userID,
		ID:     current,
	}); err != nil {
		t.Fatalf("revoke other sessions: %v", err)
	}
	if !active(current) {
		t.Error("the caller's own session was revoked by RevokeOtherUserSessions")
	}
	if active(other) {
		t.Error("another device's session survived RevokeOtherUserSessions")
	}

	// DEACTIVATION kills the surviving session's access token too, with no
	// session write at all: the check re-reads the account rather than trusting
	// the token's copy of it. This is what stops a disabled account writing on
	// every route instead of only the handlers that load the user row.
	if err := q.DeactivateUser(ctx, userID); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	if active(current) {
		t.Error("a deactivated account's session still authenticates an access token")
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE users SET is_active = TRUE WHERE id = $1`, userID); err != nil {
		t.Fatalf("reactivate: %v", err)
	}
	if !active(current) {
		t.Fatalf("reactivation did not restore the session")
	}

	// A TOMBSTONED account (the §1 hard delete anonymises the row rather than
	// removing it, so nothing cascades) is refused on deleted_at alone.
	if _, err := st.Pool.Exec(ctx, `UPDATE users SET deleted_at = now() WHERE id = $1`, userID); err != nil {
		t.Fatalf("tombstone: %v", err)
	}
	if active(current) {
		t.Error("a tombstoned account's session still authenticates an access token")
	}
}
