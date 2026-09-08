package httpapi

import (
	"context"
	"strconv"
	"time"
)

// The `federation` component on GET /admin/system (A29-F10).
//
// A29 measured the gap this closes exactly: with FEDERATION_ENABLED=true and
// two dead-lettered deliveries on the books, /admin/system reported `ok` across
// ten components and said nothing about federation at all. The only federation
// signal anywhere was a queue-depth number on the jobs page — no destination, no
// reason, and nothing that would make an operator look.
//
// WHAT IT REPORTS, AND WHY THOSE THREE FACTS. A delivery queue can fail in two
// unrelated ways and the numbers that reveal them are different:
//
//   - NOBODY IS DRAINING. Work sits pending forever. Depth alone cannot show
//     this — a busy instance also has depth — so the signal is the AGE of the
//     oldest pending row measured against the full retry ladder. Past that, no
//     amount of retrying explains it: the drain loop is not running.
//   - A DESTINATION IS GONE. Deliveries walk the ladder and dead-letter. The
//     instance is healthy; a peer is not. That is worth SHOWING and not worth
//     failing the page for, so it reports `degraded` and leaves the overall
//     verdict alone.
//
// last delivery is the third fact because a drained queue and a queue nothing is
// draining look identical by depth: zero pending. Only "when did something last
// succeed" separates them, and it is the number an operator actually asks for.

// FederationHealth is the delivery queue's operator-facing state.
type FederationHealth struct {
	Pending                 int64
	DeadLettered            int64
	OldestPendingAgeSeconds int64
	// LastDeliveredAt is zero when nothing has ever been delivered — a brand
	// new instance, or one whose peers have never been reachable.
	LastDeliveredAt time.Time
}

// federationStallSeconds is how long the oldest PENDING delivery may sit before
// the component calls the queue stalled.
//
// It is the full retry ladder (30s doubling to attempt 6 ≈ 15.5 minutes) plus a
// drain interval's slack, so a queue walking its backoff honestly is never
// reported as stalled — only one that nothing is working on.
const federationStallSeconds = 20 * 60

// federationComponent renders the component from a health snapshot.
//
// Every arm carries the same `detail`, including the failing ones: an operator
// reading a stalled queue wants "and the last thing that DID leave was at …"
// more than anyone reading a healthy one does.
func federationComponent(h FederationHealth, err error) componentStatus {
	if err != nil {
		return componentStatus{
			Status: "down",
			Error:  "the federation delivery queue could not be read, so this page cannot say whether outbound activities are leaving this instance: " + err.Error(),
		}
	}
	detail := federationDetail(h)
	if h.OldestPendingAgeSeconds > federationStallSeconds {
		return componentStatus{
			Status: "down",
			Error: "the oldest pending outbound delivery is " + strconv.FormatInt(h.OldestPendingAgeSeconds, 10) +
				"s old, past the whole retry ladder: nothing is draining the federation queue, so no activity is reaching any peer.",
			Detail: detail,
		}
	}
	if h.DeadLettered > 0 {
		return componentStatus{
			Status: "degraded",
			Error: strconv.FormatInt(h.DeadLettered, 10) + " outbound deliveries exhausted their retries and were dead-lettered" +
				federationBacklogSuffix(h) + ". The instance is serving; one or more peers did not accept what it sent.",
			Detail: detail,
		}
	}
	return componentStatus{Status: "ok", Detail: detail}
}

// federationDetail renders the facts behind the verdict.
//
// last_delivered_at is ABSENT rather than a zero timestamp when nothing has ever
// been delivered, on the same doctrine as the component itself: an operator can
// tell "no successful delivery yet" from "the last one was at midnight" only if
// the two look different, and 0001-01-01T00:00:00Z looks like a bug.
// pending/dead_lettered ride along because they are already in the prose and a
// dashboard should not have to parse a sentence to draw a number.
func federationDetail(h FederationHealth) map[string]string {
	detail := map[string]string{
		"pending":       strconv.FormatInt(h.Pending, 10),
		"dead_lettered": strconv.FormatInt(h.DeadLettered, 10),
	}
	if !h.LastDeliveredAt.IsZero() {
		detail["last_delivered_at"] = h.LastDeliveredAt.UTC().Format(time.RFC3339)
	}
	return detail
}

// federationBacklogSuffix adds the pending backlog to a message when there is
// one, so a dead letter beside a growing queue reads differently from a dead
// letter beside an empty one.
func federationBacklogSuffix(h FederationHealth) string {
	if h.Pending == 0 {
		return ""
	}
	return ", with " + strconv.FormatInt(h.Pending, 10) + " still pending"
}

// federationStatus reads the queue and renders the component, or reports that
// there is nothing to render.
//
// ABSENT rather than "ok" when federation is not wired, on the same doctrine as
// the database and cdn_purge blocks: a component reading "ok" for a feature the
// instance does not run is a claim, and the honest answer to "how is federation
// doing?" on an instance with federation off is silence.
func (s *Server) federationStatus(ctx context.Context) (componentStatus, bool) {
	if s.federationHealth == nil {
		return componentStatus{}, false
	}
	fctx, cancel := context.WithTimeout(ctx, systemProbeTimeout)
	defer cancel()
	h, err := s.federationHealth(fctx)
	return federationComponent(h, err), true
}
