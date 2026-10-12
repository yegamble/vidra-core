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
}

func (f *fakeEmailSender) SendNotificationEmail(_ context.Context, to string, n mail.NotificationEmail) error {
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

// Rows the claim judged undeliverable, rows older than a day (a backlog after
// an outage is noise, not news), and every row while the instance has no mail
// path are dropped unsent — and counted, so a draining loop moves on.
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

	store, sender = &fakeEmailStore{rows: []sqlcgen.ClaimDueNotificationEmailsRow{owedRow(nil)}}, &fakeEmailSender{}
	if n, err := newTestDrain(store, sender, false, &bytes.Buffer{}).DrainOnce(context.Background()); err != nil || n != 1 {
		t.Fatalf("unconfigured DrainOnce = %d, %v; want 1, nil", n, err)
	}
	if len(sender.sent) != 0 || len(store.deleted) != 1 {
		t.Errorf("unconfigured: sent=%d deleted=%d, want nothing sent and the row dropped", len(sender.sent), len(store.deleted))
	}
}

// A failed send is retried on a backoff and NOT counted (so a draining loop
// cannot spin on a dead relay); on the last attempt the row is dropped. The
// log names the notification and never the recipient's address.
func TestEmailDrainRetriesThenGivesUp(t *testing.T) {
	first := owedRow(nil)
	last := owedRow(func(r *sqlcgen.ClaimDueNotificationEmailsRow) { r.Attempts = emailMaxAttempts })
	store := &fakeEmailStore{rows: []sqlcgen.ClaimDueNotificationEmailsRow{first, last}}
	sender := &fakeEmailSender{err: errors.New("mail: relay said 451 try later")}
	var logs bytes.Buffer
	n, err := newTestDrain(store, sender, true, &logs).DrainOnce(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("DrainOnce = %d, %v; want 1 (only the dropped row), nil", n, err)
	}
	if len(store.rescheduled) != 1 || store.rescheduled[0].NotificationID != first.NotificationID ||
		!store.rescheduled[0].NextAttemptAt.After(drainNow) {
		t.Errorf("rescheduled = %+v, want the first row pushed into the future", store.rescheduled)
	}
	if len(store.deleted) != 1 || store.deleted[0] != last.NotificationID {
		t.Errorf("deleted = %v, want only the out-of-retries row", store.deleted)
	}
	if strings.Contains(logs.String(), "ada@example.test") {
		t.Errorf("a log line carries the recipient's address:\n%s", logs.String())
	}
	if !strings.Contains(logs.String(), first.NotificationID.String()) {
		t.Errorf("the retry is not logged against its notification:\n%s", logs.String())
	}
}

// One pass makes exactly one bounded claim, so a popular channel's fan-out is
// worked off EmailBatchSize at a time in the background, never all at once;
// and a lease long enough to cover a whole batch of slow sends, so a second
// instance cannot re-claim (and re-send) a row still being worked.
func TestEmailDrainClaimIsBounded(t *testing.T) {
	rows := make([]sqlcgen.ClaimDueNotificationEmailsRow, 3*EmailBatchSize)
	for i := range rows {
		rows[i] = owedRow(nil)
	}
	store, sender := &fakeEmailStore{rows: rows}, &fakeEmailSender{}
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
	if _, err := newTestDrain(store, sender, true, &bytes.Buffer{}).DrainOnce(context.Background()); err == nil {
		t.Error("a failed claim was swallowed; the loop must see it to back off")
	}
}
