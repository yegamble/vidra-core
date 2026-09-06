//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/pgconv"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// TestEmailChangeQueriesPersist pins, against real PostgreSQL, the properties
// the in-memory fakes only mirror — and which are the whole reason the switch is
// written as ONE statement (migration 0129, email_changes.sql):
//
//   - ConfirmEmailChange consumes the token and moves the address together, so
//     there is no window in which the token is spent and the address is not;
//   - its predicate includes used_at IS NULL, so a SECOND confirmation matches
//     nothing — single use is enforced by the database, not by a Go check that
//     two concurrent requests could both pass;
//   - the token is scoped to its OWNING account, so another user's session
//     cannot spend it;
//   - an expired request is neither confirmable nor "pending";
//   - users_email_lower_idx is the final authority on collisions: an address
//     taken since the request was made fails the confirmation as a unique
//     violation rather than silently overwriting anything.
func TestEmailChangeQueriesPersist(t *testing.T) {
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
	otherID, _, cleanupOther := seedUserAndChannel(t, st)
	defer cleanupOther()

	emailOf := func(id uuid.UUID) string {
		t.Helper()
		var e string
		if err := st.Pool.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, id).Scan(&e); err != nil {
			t.Fatalf("read email: %v", err)
		}
		return e
	}
	verifiedOf := func(id uuid.UUID) bool {
		t.Helper()
		var v bool
		if err := st.Pool.QueryRow(ctx, `SELECT email_verified FROM users WHERE id = $1`, id).Scan(&v); err != nil {
			t.Fatalf("read email_verified: %v", err)
		}
		return v
	}
	request := func(owner uuid.UUID, addr, hash string, ttl time.Duration) sqlcgen.EmailChangeRequest {
		t.Helper()
		row, err := q.CreateEmailChangeRequest(ctx, sqlcgen.CreateEmailChangeRequestParams{
			UserID: owner, NewEmail: addr, TokenHash: hash,
			ExpiresAt: time.Now().Add(ttl),
		})
		if err != nil {
			t.Fatalf("create request: %v", err)
		}
		return row
	}

	original := emailOf(userID)
	suffix := uuid.NewString()[:8]
	wanted := "moved-" + suffix + "@example.test"

	// An EXPIRED request is not pending and cannot be confirmed.
	request(userID, wanted, "hash-expired-"+suffix, -time.Minute)
	if _, err := q.GetPendingEmailChangeRequest(ctx, userID); err == nil {
		t.Error("an expired request reads as pending")
	}
	if _, err := q.ConfirmEmailChange(ctx, sqlcgen.ConfirmEmailChangeParams{
		TokenHash: "hash-expired-" + suffix, UserID: userID,
	}); err == nil {
		t.Error("an expired token confirmed")
	}
	if got := emailOf(userID); got != original {
		t.Fatalf("the expired token moved the address to %q", got)
	}

	// A live request IS pending, and its token belongs to exactly one account.
	live := request(userID, wanted, "hash-live-"+suffix, time.Hour)
	pending, err := q.GetPendingEmailChangeRequest(ctx, userID)
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	if pending.ID != live.ID || pending.NewEmail != wanted {
		t.Errorf("pending = %+v, want the live request for %q", pending, wanted)
	}
	if _, err := q.ConfirmEmailChange(ctx, sqlcgen.ConfirmEmailChangeParams{
		TokenHash: "hash-live-" + suffix, UserID: otherID,
	}); err == nil {
		t.Error("another account spent the token")
	}
	if got := emailOf(otherID); got == wanted {
		t.Error("another account's address moved")
	}
	// ...and the refused attempt consumed nothing.
	var usedAfterWrongOwner bool
	if err := st.Pool.QueryRow(ctx,
		`SELECT used_at IS NOT NULL FROM email_change_requests WHERE id = $1`, live.ID).Scan(&usedAfterWrongOwner); err != nil {
		t.Fatalf("read used_at: %v", err)
	}
	if usedAfterWrongOwner {
		t.Error("the wrong owner's attempt marked the token used")
	}

	// The real confirmation: address moved, verified, token spent — one statement.
	row, err := q.ConfirmEmailChange(ctx, sqlcgen.ConfirmEmailChangeParams{
		TokenHash: "hash-live-" + suffix, UserID: userID,
	})
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if row.Email != wanted {
		t.Errorf("confirm returned %q, want %q", row.Email, wanted)
	}
	if got := emailOf(userID); got != wanted {
		t.Errorf("stored address = %q, want %q", got, wanted)
	}
	if !verifiedOf(userID) {
		t.Error("email_verified is false after consuming a token sent to that address")
	}
	if _, err := q.ConfirmEmailChange(ctx, sqlcgen.ConfirmEmailChangeParams{
		TokenHash: "hash-live-" + suffix, UserID: userID,
	}); err == nil {
		t.Error("the token confirmed twice")
	}
	if _, err := q.GetPendingEmailChangeRequest(ctx, userID); err == nil {
		t.Error("a used request still reads as pending")
	}

	// The unique index is the final authority: an address another account holds
	// fails the confirmation instead of overwriting anything.
	taken := emailOf(otherID)
	request(userID, taken, "hash-taken-"+suffix, time.Hour)
	_, err = q.ConfirmEmailChange(ctx, sqlcgen.ConfirmEmailChangeParams{
		TokenHash: "hash-taken-" + suffix, UserID: userID,
	})
	if !pgconv.IsUniqueViolation(err) {
		t.Errorf("confirming onto a taken address = %v, want a unique violation", err)
	}
	if got := emailOf(userID); got != wanted {
		t.Errorf("the failed confirmation moved the address to %q", got)
	}

	// Supersede/cancel reports how many live requests it dropped.
	request(userID, "later-"+suffix+"@example.test", "hash-later-"+suffix, time.Hour)
	n, err := q.DeleteUnusedEmailChangeRequests(ctx, userID)
	if err != nil {
		t.Fatalf("delete unused: %v", err)
	}
	// Three, not one: "unused" is used_at IS NULL and says nothing about expiry,
	// so the delete also sweeps the EXPIRED request from the top of this test
	// along with the 'taken' one. That is the intended reading — a superseding
	// request tidies up every dead row it owns — and it is why the pending READ
	// has to filter on expires_at itself rather than trusting the table to hold
	// only live rows.
	if n != 3 {
		t.Errorf("deleted %d unused requests, want 3 (expired + taken + later)", n)
	}
	if again, err := q.DeleteUnusedEmailChangeRequests(ctx, userID); err != nil || again != 0 {
		t.Errorf("second delete = (%d, %v), want (0, nil)", again, err)
	}

	// ON DELETE CASCADE: the account's requests go with it.
	request(userID, "cascade-"+suffix+"@example.test", "hash-cascade-"+suffix, time.Hour)
	cleanup()
	var left int
	if err := st.Pool.QueryRow(ctx,
		`SELECT count(*) FROM email_change_requests WHERE user_id = $1`, userID).Scan(&left); err != nil {
		t.Fatalf("count after cascade: %v", err)
	}
	if left != 0 {
		t.Errorf("%d requests survived the account", left)
	}
}
