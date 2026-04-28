package payments

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"vidra-core/internal/usecase/inner_circle"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// ErrPolarBadSignature is returned when the HMAC verification of an incoming
// Polar webhook payload fails. The handler returns 401 in this case.
var ErrPolarBadSignature = errors.New("polar webhook: invalid HMAC signature")

// ErrPolarMissingUser is returned when an inner-circle webhook event has
// neither metadata.user_id nor a valid external_customer_id. The handler
// returns 422 — refusing to silently 200 — so the bug is visible.
var ErrPolarMissingUser = errors.New("polar webhook: cannot resolve user_id from metadata or external_customer_id")

// ErrPolarMissingChannel is returned when metadata.channel_id is missing.
var ErrPolarMissingChannel = errors.New("polar webhook: metadata.channel_id missing")

// ErrPolarBadTier is returned when metadata.tier_id is missing or not a known
// tier identifier.
var ErrPolarBadTier = errors.New("polar webhook: metadata.tier_id missing or invalid")

// PolarWebhookEvent is the canonical shape this service expects after the HTTP
// layer has parsed the Polar payload. The handler is responsible for mapping
// Polar's raw JSON to this struct so tests can drive the service directly.
type PolarWebhookEvent struct {
	EventID            string
	EventType          string // "checkout.completed" | "subscription.created" | "subscription.updated" | "subscription.canceled"
	SubscriptionID     string
	ExternalCustomerID string
	Metadata           map[string]string
	CurrentPeriodEnd   *time.Time
}

// PolarMembershipUpserter is the subset of the Inner Circle membership
// repository the Polar webhook service depends on.
type PolarMembershipUpserter interface {
	UpsertActiveByPolar(ctx context.Context, userID, channelID uuid.UUID, tierID, polarSubscriptionID string, expiresAt time.Time) (uuid.UUID, error)
	SetPolarStatus(ctx context.Context, polarSubscriptionID, status string) error
}

// PolarLedgerWriter writes the `subscription_in` ledger entry on a successful
// Polar activation. Idempotency_key is `ic-polar-sub-{event_id}`. AmountSats
// is 0 for Polar (USD-denominated) but the entry surfaces in transaction
// history alongside BTCPay subscription entries.
type PolarLedgerWriter interface {
	RecordPolarSubscription(ctx context.Context, eventID, userID, channelID, tierID, subscriptionID string) error
}

// PolarWebhookService verifies signatures, dedupes by event_id, and routes
// events to the membership repo. It is the single integration point between
// Polar's webhook stream and Inner Circle state.
type PolarWebhookService struct {
	secret      string
	memberships PolarMembershipUpserter
	ledger      PolarLedgerWriter // optional — nil-safe
	db          *sqlx.DB          // for polar_webhook_events idempotency table
}

// NewPolarWebhookService wires the service.
func NewPolarWebhookService(secret string, memberships PolarMembershipUpserter, db *sqlx.DB) *PolarWebhookService {
	return &PolarWebhookService{secret: secret, memberships: memberships, db: db}
}

// SetLedgerWriter attaches the optional ledger writer. When set, successful
// activation events also write a `subscription_in` ledger row.
func (s *PolarWebhookService) SetLedgerWriter(w PolarLedgerWriter) {
	s.ledger = w
}

// maxWebhookAge is the Standard Webhooks / Svix replay-protection window.
// Polar timestamps older than this are rejected to prevent attackers from
// re-playing a captured webhook indefinitely.
const maxWebhookAge = 5 * time.Minute

// VerifySignature returns nil if the HMAC-SHA256 of body matches the provided
// signature. Kept for backward compat with bare-HMAC sandbox callers and
// existing unit tests; production callers should prefer VerifyStandardWebhook.
// When the secret is empty the service refuses every call.
func (s *PolarWebhookService) VerifySignature(body []byte, signature string) error {
	if s == nil || s.secret == "" {
		return ErrPolarBadSignature
	}
	mac := hmac.New(sha256.New, []byte(s.secret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))

	// Accept either raw hex or "sha256=<hex>" forms. Polar's docs use the
	// prefixed form; some sandbox tooling sends the bare hex.
	candidates := []string{expected, "sha256=" + expected}
	for _, c := range candidates {
		if hmac.Equal([]byte(c), []byte(signature)) {
			return nil
		}
	}
	return ErrPolarBadSignature
}

// VerifyStandardWebhook implements Standard Webhooks / Svix verification.
// Polar.sh signs payloads as `id.timestamp.body` and exposes them via the
// `webhook-id`, `webhook-timestamp`, `webhook-signature` headers. The
// signature header carries one or more space-separated `v1,<base64>` entries.
// Replay protection: timestamps older than maxWebhookAge are rejected.
func (s *PolarWebhookService) VerifyStandardWebhook(id, timestamp, signature string, body []byte) error {
	if s == nil || s.secret == "" || id == "" || timestamp == "" || signature == "" {
		return ErrPolarBadSignature
	}

	// Replay protection — reject stale timestamps.
	tsUnix, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return ErrPolarBadSignature
	}
	tsTime := time.Unix(tsUnix, 0)
	if time.Since(tsTime) > maxWebhookAge || time.Until(tsTime) > maxWebhookAge {
		return ErrPolarBadSignature
	}

	signed := fmt.Sprintf("%s.%s.%s", id, timestamp, string(body))
	mac := hmac.New(sha256.New, []byte(s.secret))
	mac.Write([]byte(signed))
	expected := mac.Sum(nil)
	expectedB64 := base64.StdEncoding.EncodeToString(expected)

	// Accept any space-separated `v1,<base64>` entry that matches.
	for _, entry := range strings.Split(signature, " ") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		parts := strings.SplitN(entry, ",", 2)
		if len(parts) != 2 || parts[0] != "v1" {
			continue
		}
		if hmac.Equal([]byte(parts[1]), []byte(expectedB64)) {
			return nil
		}
	}
	return ErrPolarBadSignature
}

// recordEvent inserts the event_id into polar_webhook_events. Returns true if
// the event was already processed (caller should 200 idempotently). When the
// table is unavailable, logs a warning and returns false (we still process).
func (s *PolarWebhookService) recordEvent(ctx context.Context, eventID, eventType string) (alreadySeen bool) {
	if s.db == nil || eventID == "" {
		return false
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO polar_webhook_events (event_id, event_type)
		VALUES ($1, $2)
		ON CONFLICT (event_id) DO NOTHING
	`, eventID, eventType)
	if err != nil {
		slog.Warn("polar webhook: failed to record event_id", "event_id", eventID, "err", err)
		return false
	}
	n, _ := res.RowsAffected()
	return n == 0
}

// resolveUser picks user_id from metadata first, falling back to
// external_customer_id. Both must be UUIDs. Returns ErrPolarMissingUser when
// neither is usable.
func resolveUser(meta map[string]string, externalCustomerID string) (uuid.UUID, error) {
	if id, ok := meta["user_id"]; ok && id != "" {
		if u, err := uuid.Parse(id); err == nil {
			return u, nil
		}
	}
	if externalCustomerID != "" {
		if u, err := uuid.Parse(externalCustomerID); err == nil {
			return u, nil
		}
	}
	return uuid.Nil, ErrPolarMissingUser
}

// Handle processes a parsed Polar webhook event.
func (s *PolarWebhookService) Handle(ctx context.Context, evt PolarWebhookEvent) error {
	if s == nil || s.memberships == nil {
		return errors.New("polar webhook service not configured")
	}
	if evt.EventID != "" && s.recordEvent(ctx, evt.EventID, evt.EventType) {
		// Already processed once. The membership state is keyed on
		// subscription_id elsewhere, so this guard mostly protects ledger writes
		// (which Phase 9 doesn't yet add for Polar — see comments in T5 plan).
		return nil
	}

	switch evt.EventType {
	case "subscription.canceled":
		if evt.SubscriptionID == "" {
			return errors.New("polar webhook: cancel event missing subscription_id")
		}
		err := s.memberships.SetPolarStatus(ctx, evt.SubscriptionID, "cancelled")
		if err != nil {
			slog.Warn("polar webhook: cancel for unknown subscription", "subscription_id", evt.SubscriptionID, "err", err)
			// Treat unknown subscription as a no-op rather than 5xx — Polar may
			// retry forever otherwise.
			return nil
		}
		return nil

	case "checkout.completed", "subscription.created", "subscription.updated":
		if evt.SubscriptionID == "" {
			return errors.New("polar webhook: activation event missing subscription_id")
		}
		userID, err := resolveUser(evt.Metadata, evt.ExternalCustomerID)
		if err != nil {
			return err
		}
		channelIDStr := evt.Metadata["channel_id"]
		if channelIDStr == "" {
			return ErrPolarMissingChannel
		}
		channelID, err := uuid.Parse(channelIDStr)
		if err != nil {
			return fmt.Errorf("polar webhook: bad channel_id %q: %w", channelIDStr, err)
		}
		tierID := evt.Metadata["tier_id"]
		if !inner_circle.ValidTierID(tierID) {
			return ErrPolarBadTier
		}

		expires := time.Now().UTC().Add(30 * 24 * time.Hour) // fallback when current_period_end is absent
		if evt.CurrentPeriodEnd != nil {
			expires = evt.CurrentPeriodEnd.Add(24 * time.Hour) // 24h grace per plan
		} else {
			slog.Warn("polar_period_end_missing", "subscription_id", evt.SubscriptionID, "event_type", evt.EventType)
		}
		if _, err := s.memberships.UpsertActiveByPolar(ctx, userID, channelID, tierID, evt.SubscriptionID, expires); err != nil {
			return err
		}
		if s.ledger != nil {
			if lerr := s.ledger.RecordPolarSubscription(ctx, evt.EventID, userID.String(), channelID.String(), tierID, evt.SubscriptionID); lerr != nil {
				slog.Warn("polar ledger write failed", "event_id", evt.EventID, "subscription_id", evt.SubscriptionID, "err", lerr)
			}
		}
		return nil

	default:
		// Unknown event type — log and accept. Polar may add new types over
		// time; failing the webhook would cause unnecessary retries.
		slog.Info("polar webhook: ignoring unknown event type", "event_type", evt.EventType)
		return nil
	}
}
