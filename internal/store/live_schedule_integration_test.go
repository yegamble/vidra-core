//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// TestLiveScheduleQueries pins the SQL the fakes only imitate: the Upcoming
// listing's visibility (public + offline + planned + not stale), its per-viewer
// mute clause, the channel filter, soonest-first order, the matching count, and
// that going live consumes the schedule while an edit replaces/clears it.
func TestLiveScheduleQueries(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	suffix := uuid.NewString()[:8]
	seedUser := func(name string) uuid.UUID {
		var id uuid.UUID
		if err := st.Pool.QueryRow(ctx,
			`INSERT INTO users (username, email, password_hash) VALUES ($1, $2, 'x') RETURNING id`,
			name+"-"+suffix, name+"-"+suffix+"@example.test").Scan(&id); err != nil {
			t.Fatalf("seed user: %v", err)
		}
		t.Cleanup(func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id) })
		return id
	}
	owner, viewer := seedUser("own"), seedUser("view")
	handle := "sched_" + suffix
	var channelID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO channels (owner_id, handle, display_name) VALUES ($1, $2, 'S') RETURNING id`,
		owner, handle).Scan(&channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	at := func(d time.Duration) pgtype.Timestamptz {
		return pgtype.Timestamptz{Time: time.Now().Add(d), Valid: true}
	}
	mk := func(title, privacy string, when pgtype.Timestamptz) sqlcgen.CreateLiveStreamRow {
		r, err := q.CreateLiveStream(ctx, sqlcgen.CreateLiveStreamParams{
			ChannelID: channelID, Title: title, Privacy: privacy, StreamKeyHash: title + suffix, ScheduledStartAt: when,
		})
		if err != nil {
			t.Fatalf("create %s: %v", title, err)
		}
		return r
	}
	later := mk("later", "public", at(48*time.Hour))
	soon := mk("soon", "public", at(2*time.Hour))
	late := mk("late-by-10m", "public", at(-10*time.Minute)) // still listed (grace)
	_ = mk("stale", "public", at(-3*time.Hour))              // announcement gone stale
	_ = mk("unlisted", "unlisted", at(time.Hour))
	_ = mk("private", "private", at(time.Hour))
	_ = mk("unscheduled", "public", pgtype.Timestamptz{})
	golive := mk("golive", "public", at(time.Hour))
	modTerm := mk("mod-terminated", "public", at(time.Hour))
	ownerEnded := mk("owner-ended", "public", at(time.Hour))
	// A moderator termination clears the schedule AND leaves a reason code, so the
	// row can never be re-announced by a stale client; an owner's own end stays
	// eligible (only a person-ended-by-staff stream is withheld).
	code := "policy_violation"
	if err := q.TerminateLiveStream(ctx, sqlcgen.TerminateLiveStreamParams{ID: modTerm.ID, State: "offline", TerminationReasonCode: &code}); err != nil {
		t.Fatalf("terminate: %v", err)
	}
	if got, _ := q.GetLiveStreamByID(ctx, modTerm.ID); got.ScheduledStartAt.Valid {
		t.Fatal("moderator termination left the schedule in place")
	}
	if err := q.TerminateLiveStream(ctx, sqlcgen.TerminateLiveStreamParams{ID: ownerEnded.ID, State: "offline"}); err != nil {
		t.Fatalf("owner end: %v", err)
	}
	if _, err := q.UpdateLiveStream(ctx, sqlcgen.UpdateLiveStreamParams{ID: ownerEnded.ID, Title: "owner-ended", Privacy: "public", ScheduledStartAt: at(5 * time.Hour)}); err != nil {
		t.Fatalf("reschedule after owner end: %v", err)
	}
	// A moderator-terminated stream that is somehow re-scheduled stays off the rail.
	if _, err := q.UpdateLiveStream(ctx, sqlcgen.UpdateLiveStreamParams{ID: modTerm.ID, Title: "mod-terminated", Privacy: "public", ScheduledStartAt: at(5 * time.Hour)}); err != nil {
		t.Fatalf("reschedule mod-terminated: %v", err)
	}
	// A stream already live cannot be announced: the UPDATE itself refuses.
	liveNow := mk("live-now", "public", pgtype.Timestamptz{})
	if err := q.SetLiveStreamState(ctx, sqlcgen.SetLiveStreamStateParams{ID: liveNow.ID, State: "live"}); err != nil {
		t.Fatalf("live: %v", err)
	}
	if r, err := q.UpdateLiveStream(ctx, sqlcgen.UpdateLiveStreamParams{ID: liveNow.ID, Title: "live-now", Privacy: "public", ScheduledStartAt: at(time.Hour)}); err != nil || r.ScheduledStartAt.Valid {
		t.Fatalf("scheduling a live stream = (%+v, %v), want the schedule dropped", r.ScheduledStartAt, err)
	}

	list := func(viewerID pgtype.UUID) []string {
		h := handle
		rows, err := q.ListUpcomingLiveStreams(ctx, sqlcgen.ListUpcomingLiveStreamsParams{
			ChannelHandle: &h, ViewerID: viewerID, ResultLimit: 50,
		})
		if err != nil {
			t.Fatalf("ListUpcomingLiveStreams: %v", err)
		}
		n, err := q.CountUpcomingLiveStreams(ctx, sqlcgen.CountUpcomingLiveStreamsParams{ChannelHandle: &h, ViewerID: viewerID})
		if err != nil || int(n) != len(rows) {
			t.Fatalf("count = (%d, %v), rows = %d: the total must match the list", n, err, len(rows))
		}
		var out []string
		for _, r := range rows {
			out = append(out, r.Title)
		}
		return out
	}
	eq := func(got []string, want ...string) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("upcoming = %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("upcoming = %v, want %v", got, want)
			}
		}
	}
	anon := pgtype.UUID{}
	eq(list(anon), "late-by-10m", "golive", "soon", "owner-ended", "later")

	// Another channel's handle filter excludes everything.
	other := "nobody-" + suffix
	if rows, err := q.ListUpcomingLiveStreams(ctx, sqlcgen.ListUpcomingLiveStreamsParams{ChannelHandle: &other, ResultLimit: 50}); err != nil || len(rows) != 0 {
		t.Fatalf("filter by another channel = (%d, %v), want 0", len(rows), err)
	}

	// The owner's account state gates the rail exactly as it gates every other
	// public surface: unlisted (discovery opt-out) and deactivated both hide it,
	// and restoring the account restores it.
	for _, col := range []string{"unlisted = TRUE", "is_active = FALSE"} {
		if _, err := st.Pool.Exec(ctx, `UPDATE users SET `+col+` WHERE id = $1`, owner); err != nil {
			t.Fatalf("set %s: %v", col, err)
		}
		eq(list(anon))
		if _, err := st.Pool.Exec(ctx, `UPDATE users SET unlisted = FALSE, is_active = TRUE WHERE id = $1`, owner); err != nil {
			t.Fatalf("restore: %v", err)
		}
	}
	eq(list(anon), "late-by-10m", "golive", "soon", "owner-ended", "later")

	// Mute drops the owner's streams for the muter only; lifting restores them.
	v := pgtype.UUID{Bytes: viewer, Valid: true}
	if _, err := st.Pool.Exec(ctx, `INSERT INTO muted_accounts (muter_id, muted_id) VALUES ($1, $2)`, viewer, owner); err != nil {
		t.Fatalf("mute: %v", err)
	}
	eq(list(v))
	eq(list(anon), "late-by-10m", "golive", "soon", "owner-ended", "later")
	if _, err := st.Pool.Exec(ctx, `DELETE FROM muted_accounts WHERE muter_id = $1`, viewer); err != nil {
		t.Fatalf("unmute: %v", err)
	}
	eq(list(v), "late-by-10m", "golive", "soon", "owner-ended", "later")

	// Going live consumes the schedule (and the stream leaves the Upcoming list).
	if err := q.SetLiveStreamState(ctx, sqlcgen.SetLiveStreamStateParams{ID: golive.ID, State: "live"}); err != nil {
		t.Fatalf("go live: %v", err)
	}
	if got, err := q.GetLiveStreamByID(ctx, golive.ID); err != nil || got.ScheduledStartAt.Valid {
		t.Fatalf("after go-live = (%+v, %v), want schedule cleared", got.ScheduledStartAt, err)
	}
	eq(list(anon), "late-by-10m", "soon", "owner-ended", "later")

	// An edit REPLACES the planned start: new value sticks, NULL clears.
	upd := sqlcgen.UpdateLiveStreamParams{ID: soon.ID, Title: "soon", Privacy: "public", ScheduledStartAt: at(72 * time.Hour)}
	if r, err := q.UpdateLiveStream(ctx, upd); err != nil || !r.ScheduledStartAt.Valid {
		t.Fatalf("update with start = (%+v, %v)", r, err)
	}
	eq(list(anon), "late-by-10m", "owner-ended", "later", "soon")
	upd.ScheduledStartAt = pgtype.Timestamptz{}
	if r, err := q.UpdateLiveStream(ctx, upd); err != nil || r.ScheduledStartAt.Valid {
		t.Fatalf("update clearing start = (%+v, %v)", r, err)
	}
	eq(list(anon), "late-by-10m", "owner-ended", "later")
	_, _ = later, late
}
