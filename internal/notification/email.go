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

// Outbox drain tuning. One pass claims at most EmailBatchSize rows and the
// worker runs one pass per tick, so a fan-out to ten thousand opted-in
// followers is worked off a batch at a time in the background rather than
// mailed from the publishing request. The lease covers a whole batch at the
// transports' 30 s ceiling, so another instance cannot re-claim (and re-send) a
// row still being worked. A day-old email is noise, not news, so a backlog left
// by an outage is dropped instead of delivered late.
const (
	EmailBatchSize   = 25
	emailLease       = 15 * time.Minute
	emailMaxAttempts = 5
	emailMaxAge      = 24 * time.Hour
	emailRetryBase   = time.Minute
	emailRetryMax    = time.Hour
)

// EmailStore is the outbox access the drain needs. *sqlcgen.Queries satisfies it.
type EmailStore interface {
	ClaimDueNotificationEmails(ctx context.Context, arg sqlcgen.ClaimDueNotificationEmailsParams) ([]sqlcgen.ClaimDueNotificationEmailsRow, error)
	DeleteNotificationEmail(ctx context.Context, notificationID uuid.UUID) error
	RescheduleNotificationEmail(ctx context.Context, arg sqlcgen.RescheduleNotificationEmailParams) error
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
// instance has a mail path; while it is false, owed emails are dropped unsent.
func NewEmailDrain(store EmailStore, sender EmailSender, configured func() bool, logger *slog.Logger) *EmailDrain {
	return &EmailDrain{store: store, sender: sender, configured: configured, logger: logger, now: time.Now}
}

// DrainOnce claims one batch and works through it, reporting how many rows it
// finished (sent or dropped). A rescheduled row is not counted, so a loop that
// drains until zero cannot spin on a relay that is down. Only the claim's own
// failure is returned; per-row failures live in the queue.
func (d *EmailDrain) DrainOnce(ctx context.Context) (int, error) {
	rows, err := d.store.ClaimDueNotificationEmails(ctx, sqlcgen.ClaimDueNotificationEmailsParams{
		ClaimLimit: EmailBatchSize, LeaseSeconds: emailLease.Seconds(),
	})
	if err != nil {
		return 0, err
	}
	configured := d.configured()
	done := 0
	for _, r := range rows {
		if d.deliver(ctx, r, configured) {
			done++
		}
	}
	return done, nil
}

// deliver sends one owed email, or decides it is no longer owed, and reports
// whether the row is finished. Logs name the notification, never the address.
func (d *EmailDrain) deliver(ctx context.Context, r sqlcgen.ClaimDueNotificationEmailsRow, configured bool) bool {
	if r.Deliverable && configured && d.now().Sub(r.QueuedAt) <= emailMaxAge {
		err := d.sender.SendNotificationEmail(ctx, r.Email, mail.NotificationEmail{
			Type: r.Type, ActorName: r.ActorName, ChannelName: r.ChannelName,
			VideoID: uuidString(r.VideoID), VideoTitle: r.VideoTitle,
		})
		if err != nil && r.Attempts < emailMaxAttempts {
			next := d.now().Add(retry.Backoff(int(r.Attempts), emailRetryBase, emailRetryMax))
			d.logger.Warn("notification email failed; will retry", "notification_id", r.NotificationID,
				"type", r.Type, "attempt", r.Attempts, "error", err)
			if rerr := d.store.RescheduleNotificationEmail(ctx, sqlcgen.RescheduleNotificationEmailParams{
				NotificationID: r.NotificationID, NextAttemptAt: next,
			}); rerr != nil {
				d.logger.Warn("notification email reschedule failed", "notification_id", r.NotificationID, "error", rerr)
			}
			return false
		}
		if err != nil {
			d.logger.Warn("notification email dropped after retries", "notification_id", r.NotificationID,
				"type", r.Type, "attempts", r.Attempts, "error", err)
		}
	}
	if err := d.store.DeleteNotificationEmail(ctx, r.NotificationID); err != nil {
		// The lease expires and the row is claimed again: at worst one duplicate.
		d.logger.Warn("notification email dequeue failed", "notification_id", r.NotificationID, "error", err)
		return false
	}
	return true
}
