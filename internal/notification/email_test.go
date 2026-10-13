package notification

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/mail"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeEmailStore is the outbox as the drain sees it: claim hands back whatever
// rows the test queued (the SQL's selection and deliverable rules are proved in
// store.TestClaimDueNotificationEmailsOnRealPG), and delete / reschedule are
// recorded.
type fakeEmailStore struct {
	rows        []sqlcgen.ClaimDueNotificationEmailsRow
	claims      []sqlcgen.ClaimDueNotificationEmailsParams
	claimErr    error
	deleted     []uuid.UUID
	rescheduled []sqlcgen.RescheduleNotificationEmailParams
	pruned      []float64 // max_age_seconds of each stale-row prune
}

func (f *fakeEmailStore) DeleteStaleNotificationEmails(_ context.Context, maxAgeSeconds float64) (int64, error) {
	f.pruned = append(f.pruned, maxAgeSeconds)
	return 0, nil
}

func (f *fakeEmailStore) ClaimDueNotificationEmails(_ context.Context, p sqlcgen.ClaimDueNotificationEmailsParams) ([]sqlcgen.ClaimDueNotificationEmailsRow, error) {
	f.claims = append(f.claims, p)
	if f.claimErr != nil {
		return nil, f.claimErr
	}
	n := min(int(p.ClaimLimit), len(f.rows))
	out := f.rows[:n]
	f.rows = f.rows[n:]
	return out, nil
}

func (f *fakeEmailStore) DeleteNotificationEmail(_ context.Context, id uuid.UUID) error {
	f.deleted = append(f.deleted, id)
	return nil
}

func (f *fakeEmailStore) RescheduleNotificationEmail(_ context.Context, p sqlcgen.RescheduleNotificationEmailParams) error {
	f.rescheduled = append(f.rescheduled, p)
	return nil
}

type sentEmail struct {
	to string
	n  mail.NotificationEmail
}

type fakeEmailSender struct {
	sent []sentEmail
	err  error
	// tick, when set, runs on every send — to move a test clock.
	tick func()
}

func (f *fakeEmailSender) SendNotificationEmail(_ context.Context, to string, n mail.NotificationEmail) error {
	if f.tick != nil {
		f.tick()
	}
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, sentEmail{to, n})
	return nil
}

var drainNow = time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)

func owedRow(mut func(*sqlcgen.ClaimDueNotificationEmailsRow)) sqlcgen.ClaimDueNotificationEmailsRow {
	r := sqlcgen.ClaimDueNotificationEmailsRow{
		NotificationID: uuid.New(), Attempts: 1, QueuedAt: drainNow.Add(-time.Minute),
		Type: TypeComment, Email: "ada@example.test", ActorName: "bob",
		VideoID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, VideoTitle: "Soldering 101",
		Deliverable: true,
	}
	if mut != nil {
		mut(&r)
	}
	return r
}

func newTestDrain(store *fakeEmailStore, sender *fakeEmailSender, configured bool, logs *bytes.Buffer) *EmailDrain {
	d := NewEmailDrain(store, sender, func() bool { return configured },
		slog.New(slog.NewTextHandler(logs, nil)))
	d.now = func() time.Time { return drainNow }
	return d
}

// A deliverable row is mailed once, with the rendered fields the claim
// resolved, and then forgotten: the outbox only holds outstanding work.
func TestEmailDrainSendsAndForgets(t *testing.T) {
	row := owedRow(nil)
	store, sender := &fakeEmailStore{rows: []sqlcgen.ClaimDueNotificationEmailsRow{row}}, &fakeEmailSender{}
	n, err := newTestDrain(store, sender, true, &bytes.Buffer{}).DrainOnce(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("DrainOnce = %d, %v; want 1, nil", n, err)
	}
	want := mail.NotificationEmail{Type: TypeComment, ActorName: "bob",
		VideoID: uuid.UUID(row.VideoID.Bytes).String(), VideoTitle: "Soldering 101"}
	if len(sender.sent) != 1 || sender.sent[0].to != "ada@example.test" || sender.sent[0].n != want {
		t.Fatalf("sent = %+v, want one %+v to ada", sender.sent, want)
	}
	if len(store.deleted) != 1 || store.deleted[0] != row.NotificationID || len(store.rescheduled) != 0 {
		t.Errorf("deleted=%v rescheduled=%v, want the row deleted only", store.deleted, store.rescheduled)
	}
}

// Rows the claim judged undeliverable and rows older than a day (a backlog
// after an outage is noise, not news) are dropped unsent — and counted, so a
// draining loop moves on.
func TestEmailDrainDropsWhatIsNoLongerOwed(t *testing.T) {
	undeliverable := owedRow(func(r *sqlcgen.ClaimDueNotificationEmailsRow) { r.Deliverable = false })
	stale := owedRow(func(r *sqlcgen.ClaimDueNotificationEmailsRow) { r.QueuedAt = drainNow.Add(-25 * time.Hour) })
	store, sender := &fakeEmailStore{rows: []sqlcgen.ClaimDueNotificationEmailsRow{undeliverable, stale}}, &fakeEmailSender{}
	if n, err := newTestDrain(store, sender, true, &bytes.Buffer{}).DrainOnce(context.Background()); err != nil || n != 2 {
		t.Fatalf("DrainOnce = %d, %v; want 2, nil", n, err)
	}
	if len(sender.sent) != 0 || len(store.deleted) != 2 {
		t.Errorf("sent=%d deleted=%d, want 0 sent and both dropped", len(sender.sent), len(store.deleted))
	}
}

// While the instance has no mail path the outbox WAITS: nothing is claimed,
// sent or deleted, so an admin who configures mail an hour later still gets
// the hour's emails out. Only rows past the day-old limit are pruned, so the
// wait stays bounded on an instance that never configures mail.
func TestEmailDrainWaitsWhileMailIsUnconfigured(t *testing.T) {
	store := &fakeEmailStore{rows: []sqlcgen.ClaimDueNotificationEmailsRow{owedRow(nil)}}
	sender := &fakeEmailSender{}
	n, err := newTestDrain(store, sender, false, &bytes.Buffer{}).Drain(context.Background())
	if err != nil || n != 0 {
		t.Fatalf("Drain = %d, %v; want 0, nil", n, err)
	}
	if len(store.claims) != 0 || len(sender.sent) != 0 || len(store.deleted) != 0 || len(store.rescheduled) != 0 {
		t.Errorf("unconfigured drain touched the outbox: claims=%d sent=%d deleted=%d rescheduled=%d",
			len(store.claims), len(sender.sent), len(store.deleted), len(store.rescheduled))
	}
	if len(store.pruned) != 1 || store.pruned[0] != emailMaxAge.Seconds() {
		t.Errorf("pruned = %v, want one prune at the %s limit", store.pruned, emailMaxAge)
	}
}

// A failing relay is retried on a capped exponential backoff for as long as
// the email is worth sending — a day — not for five quick attempts that a
// fifteen-minute outage would exhaust. Each reschedule is not counted (a
// draining loop cannot spin on a dead relay), no gap exceeds the cap, and the
// row is dropped only when its next attempt would land past the day.
func TestEmailDrainRetriesForADay(t *testing.T) {
	queued, now := drainNow, drainNow
	sender := &fakeEmailSender{err: errors.New("relay down")}
	for attempt := int32(1); ; attempt++ {
		if attempt > 500 {
			t.Fatal("never gave up")
		}
		store := &fakeEmailStore{rows: []sqlcgen.ClaimDueNotificationEmailsRow{
			owedRow(func(r *sqlcgen.ClaimDueNotificationEmailsRow) { r.Attempts = attempt; r.QueuedAt = queued }),
		}}
		d := newTestDrain(store, sender, true, &bytes.Buffer{})
		at := now
		d.now = func() time.Time { return at }
		n, err := d.DrainOnce(context.Background())
		if err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
		if len(store.deleted) == 1 {
			if n != 1 {
				t.Errorf("the give-up was not counted as finished work")
			}
			if age := now.Sub(queued); age < 23*time.Hour || age > emailMaxAge {
				t.Fatalf("gave up at attempt %d after %s, want close to (and within) %s", attempt, age, emailMaxAge)
			}
			return
		}
		if n != 0 || len(store.rescheduled) != 1 {
			t.Fatalf("attempt %d: n=%d rescheduled=%d, want an uncounted reschedule", attempt, n, len(store.rescheduled))
		}
		gap := store.rescheduled[0].NextAttemptAt.Sub(now)
		if gap <= 0 || gap > emailRetryMax {
			t.Fatalf("attempt %d: retry in %s, want within (0, %s]", attempt, gap, emailRetryMax)
		}
		now = store.rescheduled[0].NextAttemptAt
	}
}

// The bulk path logs a failure's CLASS, never its text: a relay's refusal
// quotes the recipient's address back, and this loop would write thousands.
func TestEmailDrainLogsReasonNotRawError(t *testing.T) {
	row := owedRow(nil)
	refusal := &mail.SendError{Reason: mail.ReasonRejected,
		Err: errors.New("550 5.1.1 <ada@example.test>: Recipient address rejected")}
	store := &fakeEmailStore{rows: []sqlcgen.ClaimDueNotificationEmailsRow{row}}
	var logs bytes.Buffer
	if _, err := newTestDrain(store, &fakeEmailSender{err: refusal}, true, &logs).DrainOnce(context.Background()); err != nil {
		t.Fatalf("DrainOnce: %v", err)
	}
	// "550 5.1.1", not a bare "550": the logged notification id is a random
	// UUID, and ~0.7% of them contain the hex run "550".
	if strings.Contains(logs.String(), "ada@example.test") || strings.Contains(logs.String(), "550 5.1.1") {
		t.Errorf("a log line carries the transport's raw text:\n%s", logs.String())
	}
	if !strings.Contains(logs.String(), "reason=rejected") || !strings.Contains(logs.String(), row.NotificationID.String()) {
		t.Errorf("the failure is not logged as a reason against its notification:\n%s", logs.String())
	}
}

// One DrainOnce is one bounded claim, leased long enough to cover a whole
// batch of slow sends, so a second instance cannot re-claim (and re-send) a row
// still being worked.
func TestEmailDrainClaimIsBounded(t *testing.T) {
	store, sender := &fakeEmailStore{rows: owedRows(3 * EmailBatchSize)}, &fakeEmailSender{}
	n, err := newTestDrain(store, sender, true, &bytes.Buffer{}).DrainOnce(context.Background())
	if err != nil || n != EmailBatchSize || len(sender.sent) != EmailBatchSize {
		t.Fatalf("DrainOnce = %d, %v (sent %d); want one batch of %d", n, err, len(sender.sent), EmailBatchSize)
	}
	if len(store.claims) != 1 || int(store.claims[0].ClaimLimit) != EmailBatchSize {
		t.Fatalf("claims = %+v, want one claim of %d", store.claims, EmailBatchSize)
	}
	if lease := time.Duration(store.claims[0].LeaseSeconds * float64(time.Second)); lease < EmailBatchSize*30*time.Second {
		t.Errorf("lease %s cannot cover %d sends at the 30 s transport ceiling", lease, EmailBatchSize)
	}
	store.claimErr = errors.New("db down")
	if _, err := newTestDrain(store, sender, true, &bytes.Buffer{}).Drain(context.Background()); err == nil {
		t.Error("a failed claim was swallowed; the loop must see it to back off")
	}
}

// A tick keeps claiming while batches come back FULL, so a big fan-out is not
// throttled to one batch per tick; it stops at the first short batch without
// an extra empty claim.
func TestEmailDrainKeepsDrainingFullBatches(t *testing.T) {
	store, sender := &fakeEmailStore{rows: owedRows(3*EmailBatchSize + 5)}, &fakeEmailSender{}
	n, err := newTestDrain(store, sender, true, &bytes.Buffer{}).Drain(context.Background())
	if err != nil || n != 3*EmailBatchSize+5 || len(sender.sent) != n {
		t.Fatalf("Drain = %d, %v (sent %d); want all %d", n, err, len(sender.sent), 3*EmailBatchSize+5)
	}
	if len(store.claims) != 4 {
		t.Errorf("claims = %d, want 4 (three full batches and the short one)", len(store.claims))
	}
}

// ...but never past the tick's time budget: a slow relay leaves the rest for
// the next tick rather than holding one pass open indefinitely.
func TestEmailDrainStopsAtTheTickBudget(t *testing.T) {
	clock := drainNow
	sender := &fakeEmailSender{tick: func() { clock = clock.Add(200 * time.Millisecond) }}
	store := &fakeEmailStore{rows: owedRows(3 * EmailBatchSize)}
	d := newTestDrain(store, sender, true, &bytes.Buffer{})
	d.now = func() time.Time { return clock }
	if _, err := d.Drain(context.Background()); err != nil {
		t.Fatalf("Drain: %v", err)
	}
	// 25 sends × 200 ms = 5 s, past the 4 s budget after the first batch.
	if len(store.claims) != 1 || len(sender.sent) != EmailBatchSize {
		t.Errorf("claims=%d sent=%d, want the pass to stop after one batch over budget", len(store.claims), len(sender.sent))
	}
}

func owedRows(n int) []sqlcgen.ClaimDueNotificationEmailsRow {
	rows := make([]sqlcgen.ClaimDueNotificationEmailsRow, n)
	for i := range rows {
		rows[i] = owedRow(nil)
	}
	return rows
}
