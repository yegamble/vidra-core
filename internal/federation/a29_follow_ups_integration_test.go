//go:build integration

// A29 follow-ups against a REAL PostgreSQL: a comment Create cancelled by an
// instance block comes back out of the block window, and only when the comment
// it names is still something a stranger may read. The unit test beside this one
// pins the predicate; this pins the whole path — the real cancel marker, the
// real window, the real LIKE prefilter and the real requeue — on seeded rows.
package federation_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/federation"
	"github.com/vidra/vidra-core/internal/store"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// integrationStore is integrationQueries plus the pool, for the handful of
// assertions that read a delivery row's state directly — there is no query for
// "show me this exact payload's row" and inventing one for a test would put a
// test-only query in the production surface.
func integrationStore(t *testing.T) (context.Context, *store.Store) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	st, err := store.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(st.Close)
	return ctx, st
}

// noteCreate is the wire shape federateComment queues: the object id is this
// instance's own comment URL, which is the exact string the redelivery resolver
// could not read before this slice.
func noteCreate(base string, commentID, videoID uuid.UUID) []byte {
	return []byte(`{"@context":"https://www.w3.org/ns/activitystreams",` +
		`"id":"` + base + `/accounts/alice/activities/create/` + uuid.NewString() + `",` +
		`"type":"Create","actor":"` + base + `/accounts/alice",` +
		`"to":["https://www.w3.org/ns/activitystreams#Public"],` +
		`"object":{"id":"` + base + `/comments/` + commentID.String() + `","type":"Note",` +
		`"content":"posted during the block","inReplyTo":"` + base + `/videos/` + videoID.String() + `",` +
		`"attributedTo":"` + base + `/accounts/alice"}}`)
}

func TestUnblockResumesCommentCreatesForVisibleComments(t *testing.T) {
	ctx, st := integrationStore(t)
	q := st.Queries()
	suf := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	author := seedUser(t, ctx, q, suf)
	leaver := seedUser(t, ctx, q, suf+"x")
	_, public := seedPublicVideo(t, ctx, q, author.ID, suf)
	_, hidden := seedPublicVideo(t, ctx, q, author.ID, suf+"p")

	// The video that went private AFTER the comment on it was queued.
	if _, err := st.Pool.Exec(ctx, `UPDATE videos SET privacy = 'private' WHERE id = $1`, hidden.ID); err != nil {
		t.Fatalf("take the video private: %v", err)
	}

	mk := func(videoID, userID uuid.UUID, body string) sqlcgen.Comment {
		t.Helper()
		c, err := q.CreateComment(ctx, sqlcgen.CreateCommentParams{
			VideoID: videoID, UserID: pgtype.UUID{Bytes: userID, Valid: true}, Body: body,
		})
		if err != nil {
			t.Fatalf("CreateComment: %v", err)
		}
		return c
	}
	visible := mk(public.ID, author.ID, "still here")
	tombstoned := mk(public.ID, leaver.ID, "written by someone who then deleted their account")
	removed := mk(public.ID, author.ID, "removed outright")
	onPrivate := mk(hidden.ID, author.ID, "on a video that went private")

	// The two ways a comment stops being readable here, each written by the
	// product's own query rather than by hand: an account deletion tombstones
	// (body emptied, row kept so the thread survives), and a removal deletes.
	if err := q.TombstoneUserComments(ctx, pgtype.UUID{Bytes: leaver.ID, Valid: true}); err != nil {
		t.Fatalf("TombstoneUserComments: %v", err)
	}
	if err := q.DeleteComment(ctx, removed.ID); err != nil {
		t.Fatalf("DeleteComment: %v", err)
	}

	domain := "a29fu-" + suf + ".example"
	base := "https://videos.example"
	svc := federation.NewService(q, federation.WithBaseURL(base))

	cases := []struct {
		name    string
		payload []byte
		want    bool
	}{
		{"a comment still readable on a still-public video", noteCreate(base, visible.ID, public.ID), true},
		{"a comment tombstoned by an account deletion", noteCreate(base, tombstoned.ID, public.ID), false},
		{"a comment removed outright", noteCreate(base, removed.ID, public.ID), false},
		{"a comment whose video went private", noteCreate(base, onPrivate.ID, hidden.ID), false},
	}
	for _, tc := range cases {
		if err := q.EnqueueDelivery(ctx, sqlcgen.EnqueueDeliveryParams{
			InboxUrl:        "https://" + domain + "/inbox",
			Payload:         tc.payload,
			SigningUserID:   pgtype.UUID{Bytes: author.ID, Valid: true},
			SigningUsername: author.Username,
		}); err != nil {
			t.Fatalf("EnqueueDelivery: %v", err)
		}
	}

	// The block and the cancellation it causes, through the REAL drain: the
	// marker the drain writes and the marker the redelivery matches on have to
	// be the same string, and a test that spelled it twice would pass while the
	// two drifted apart.
	if _, err := q.BlockInstance(ctx, sqlcgen.BlockInstanceParams{Domain: domain, Reason: "a29 follow-ups"}); err != nil {
		t.Fatalf("BlockInstance: %v", err)
	}
	blockedAt := time.Now().Add(-time.Minute)
	if _, err := svc.DrainDeliveries(ctx, 50); err != nil {
		t.Fatalf("DrainDeliveries: %v", err)
	}

	requeued, err := svc.RedeliverAfterUnblock(ctx, domain, blockedAt)
	if err != nil {
		t.Fatalf("RedeliverAfterUnblock: %v", err)
	}
	if requeued != 1 {
		t.Fatalf("requeued = %d, want 1 (the readable comment, and only it)", requeued)
	}

	for _, tc := range cases {
		var state, lastError string
		if err := st.Pool.QueryRow(ctx,
			`SELECT state, last_error FROM federation_deliveries WHERE payload = $1`, tc.payload).
			Scan(&state, &lastError); err != nil {
			t.Fatalf("%s: read delivery: %v", tc.name, err)
		}
		if got := state == "pending"; got != tc.want {
			t.Errorf("%s: resumed = %v (state %q, last_error %q), want %v", tc.name, got, state, lastError, tc.want)
		}
		if !tc.want && lastError != federation.DeliveryCancelledByPolicy {
			t.Errorf("%s: last_error = %q, want the cancel marker left intact", tc.name, lastError)
		}
	}
}
