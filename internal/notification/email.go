package notification

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/mail"
	"github.com/vidra/vidra-core/internal/retry"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Outbox drain tuning. A claim takes at most EmailBatchSize rows, and a tick
// keeps claiming while batches come back full, for up to emailTickBudget — so a
// fan-out to ten thousand opted-in followers is worked off in the background at
// the relay's pace, never mailed from the publishing request. The lease covers a
// whole batch at the transports' 30 s ceiling, so another instance cannot
// re-claim (and re-send) a row still being worked. A failing send is retried on
// a backoff capped at an hour for as long as the email is worth sending: a
// day-old email is noise, not news, so that is where retries — and a backlog
// left by an outage or an instance without mail — end.
const (
	EmailBatchSize  = 25
	emailLease      = 15 * time.Minute
	emailMaxAge     = 24 * time.Hour
	emailRetryBase  = time.Minute
	emailRetryMax   = time.Hour
	emailTickBudget = 4 * time.Second
)

// EmailStore is the outbox access the drain needs. *sqlcgen.Queries satisfies it.
type EmailStore interface {
	ClaimDueNotificationEmails(ctx context.Context, arg sqlcgen.ClaimDueNotificationEmailsParams) ([]sqlcgen.ClaimDueNotificationEmailsRow, error)
	DeleteNotificationEmail(ctx context.Context, notificationID uuid.UUID) error
	RescheduleNotificationEmail(ctx context.Context, arg sqlcgen.RescheduleNotificationEmailParams) error
	DeleteStaleNotificationEmails(ctx context.Context, maxAgeSeconds float64) (int64, error)
}

// EmailSender mails one notification. auth.Mailer satisfies it — the composer
// in production, the dev capture mailer when that seam is on.
type EmailSender interface {
	SendNotificationEmail(ctx context.Context, to string, n mail.NotificationEmail) error
}

// EmailDrain delivers the notification email outbox (migration 0157). Delivery
// is a best-effort side effect, exactly like the in-app notification it rides
// on: nothing here can fail the action that raised it, and a failed send is
// retried from the queue, never surfaced to a request.
type EmailDrain struct {
	store      EmailStore
	sender     EmailSender
	configured func() bool
	logger     *slog.Logger
	now        func() time.Time
}

// NewEmailDrain builds the drain. configured is a LIVE read of whether the
// instance has a mail path; while it is false the outbox waits untouched (only
// rows past the day-old limit are pruned), so mail configured later still goes.
func NewEmailDrain(store EmailStore, sender EmailSender, configured func() bool, logger *slog.Logger) *EmailDrain {
	return &EmailDrain{store: store, sender: sender, configured: configured, logger: logger, now: time.Now}
}

// Drain is one worker tick: it claims batch after batch while they come back
// full and the tick's time budget lasts, and reports how many rows it finished
// (sent or dropped). Without a mail path it claims nothing and only prunes rows
// past the day-old limit. Only a claim or prune failure is returned; per-row
// failures live in the queue.
func (d *EmailDrain) Drain(ctx context.Context) (int, error) {
	if !d.configured() {
		_, err := d.store.DeleteStaleNotificationEmails(ctx, emailMaxAge.Seconds())
		return 0, err
	}
	start := d.now()
	total := 0
	for {
		claimed, done, err := d.drainBatch(ctx)
		total += done
		if err != nil || claimed < EmailBatchSize || d.now().Sub(start) >= emailTickBudget {
			return total, err
		}
	}
}

// DrainOnce works through a single claimed batch (see Drain).
func (d *EmailDrain) DrainOnce(ctx context.Context) (int, error) {
	_, done, err := d.drainBatch(ctx)
	return done, err
}

// drainBatch claims one batch and works through it, reporting how many rows it
// claimed and how many it finished. A rescheduled row is not finished, so a
// loop cannot spin on a relay that is down.
func (d *EmailDrain) drainBatch(ctx context.Context) (claimed, done int, err error) {
	rows, err := d.store.ClaimDueNotificationEmails(ctx, sqlcgen.ClaimDueNotificationEmailsParams{
		ClaimLimit: EmailBatchSize, LeaseSeconds: emailLease.Seconds(),
	})
	if err != nil {
		return 0, 0, err
	}
	for _, r := range rows {
		if d.deliver(ctx, r) {
			done++
		}
	}
	return len(rows), done, nil
}

// deliver sends one owed email, or decides it is no longer owed, and reports
// whether the row is finished. Logs name the notification and the failure's
// CLASS only: a relay's refusal text can quote the recipient's address.
func (d *EmailDrain) deliver(ctx context.Context, r sqlcgen.ClaimDueNotificationEmailsRow) bool {
	if r.Deliverable && d.now().Sub(r.QueuedAt) <= emailMaxAge {
		err := d.sender.SendNotificationEmail(ctx, r.Email, mail.NotificationEmail{
			Type: r.Type, ActorName: r.ActorName, ChannelName: r.ChannelName,
			VideoID: uuidString(r.VideoID), VideoTitle: r.VideoTitle,
		})
		if err != nil {
			next := d.now().Add(retry.Backoff(int(r.Attempts), emailRetryBase, emailRetryMax))
			if next.Sub(r.QueuedAt) <= emailMaxAge {
				d.logger.Warn("notification email failed; will retry", "notification_id", r.NotificationID,
					"type", r.Type, "attempt", r.Attempts, "reason", mail.ReasonOf(err))
				if rerr := d.store.RescheduleNotificationEmail(ctx, sqlcgen.RescheduleNotificationEmailParams{
					NotificationID: r.NotificationID, NextAttemptAt: next,
				}); rerr != nil {
					d.logger.Warn("notification email reschedule failed", "notification_id", r.NotificationID, "error", rerr)
				}
				return false
			}
			d.logger.Warn("notification email dropped: retries outlived its day", "notification_id", r.NotificationID,
				"type", r.Type, "attempts", r.Attempts, "reason", mail.ReasonOf(err))
		}
	}
	if err := d.store.DeleteNotificationEmail(ctx, r.NotificationID); err != nil {
		// The lease expires and the row is claimed again: at worst one duplicate.
		d.logger.Warn("notification email dequeue failed", "notification_id", r.NotificationID, "error", err)
		return false
	}
	return true
}
