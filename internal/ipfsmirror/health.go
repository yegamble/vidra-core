package ipfsmirror

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// The mirror's OPERATOR-FACING health, and the switch the delivery path reads.
//
// THE FAILURE MODE THIS CLOSES (A31, INT-07 clause (b) and (c)). Thumbnails and
// storyboards are redirected SERVER-SIDE to the gateway with
// `Cache-Control: public, max-age=300`, and delivery.mirrorSource never asked
// whether the gateway was reachable. With the gateway stopped the api kept
// issuing those redirects and every one of them died — a viewer saw broken
// images for up to five minutes per cached URL. The admin could not see it
// either: /admin/system probes postgres, redis, s3, smtp, search, ffmpeg, clamav,
// settings_sync and federation, and had no `ipfs` component at all, so an
// unreachable node never degraded the page an operator opens BECAUSE something is
// wrong. The honest signal existed one endpoint away, on GET /ipfs/status, which
// nobody watches.
//
// WHY THE PROBE IS THE GATEWAY AND NOT THE RPC API. /ipfs/status's
// node_reachable is an RPC `version` call. A31 ran the lab with the daemon up and
// its gateway listener down: RPC answered, the status read healthy, and every
// redirect failed. They are different listeners, and on a shared or third-party
// gateway they are different HOSTS. The probe therefore fetches a CID this
// instance is actually publishing, from the gateway, over HTTP — the same URL
// shape the 307 carries.
//
// WHY IT IS CACHED AND NOT PER REQUEST. This gates a media request. A probe on
// the request path would put a second network hop in front of every thumbnail
// and would turn a slow gateway into a slow api. So it runs on its own five
// minute ticker — the cadence internal/storage.WriteHealth already established
// for the object store's write probe — and the request path reads the record it
// keeps. The cost of that choice is stated plainly: after the gateway dies, up to
// one probe interval of redirects are still minted, and those carry max-age=300
// of their own. The interval is the bound on how wrong this can be, not a promise
// that it is never wrong.

// gatewayProbeTimeout bounds one probe. It is generous relative to the api's own
// probes because a gateway hop can legitimately be a WAN round trip, and short
// enough that a hung gateway cannot stack probes behind each other on a
// five-minute ticker.
const gatewayProbeTimeout = 10 * time.Second

// GatewayFetcher asks a gateway for one CID. *ipfs.GatewayProbe implements it; a
// test substitutes a fake. A nil error means a viewer redirected there gets bytes.
type GatewayFetcher interface {
	Fetch(ctx context.Context, cid string) error
}

// HealthState is one swarm's verdict, in the vocabulary /admin/system already
// speaks (A17): not_configured never degrades an instance, ok and down do.
type HealthState string

const (
	// HealthNotConfigured — this tier is off, or has no gateway to probe. A
	// supported deployment, never a fault.
	HealthNotConfigured HealthState = "not_configured"
	// HealthOK — the gateway served a CID this instance publishes.
	HealthOK HealthState = "ok"
	// HealthDown — the gateway did not.
	HealthDown HealthState = "down"
)

// Health is one swarm's probe record: the verdict, when it was taken, and the
// sentence an operator can act on.
type Health struct {
	State HealthState
	// Probed is false until the first probe completes. Wired-but-not-yet-asked is
	// reported as not_configured, the same rule httpapi applies to a nil Pinger and
	// to an unprobed storage write monitor — a page must not claim a verdict it has
	// not taken.
	Probed bool
	At     time.Time
	Reason string
	// Backlog, DeadLettered, Pinned and the two ages are the ledger facts the
	// component states behind its verdict (the federation-health pattern). They are
	// read with the probe, not per page load.
	Backlog                 int64
	DeadLettered            int64
	Pinned                  int64
	OldestBacklogAgeSeconds int64
	LastPinnedAgeSeconds    int64
	// Strays is the count the last ledger<->node comparison could not account for
	// (see verify.go). -1 means no comparison has run or the last one could not
	// complete, which is deliberately different from 0.
	Strays int64
}

// Redirectable reports whether the delivery path may mint a 307 to this swarm's
// gateway. ONLY an affirmative, completed probe qualifies: an unprobed monitor
// and a down one both answer false, because "we have not looked" is not evidence
// that a viewer sent there gets bytes.
func (h Health) Redirectable() bool { return h.Probed && h.State == HealthOK }

// GatewayHealth returns the last public-tier probe record. Safe on a nil service
// and before the first probe.
func (s *Service) GatewayHealth() Health {
	if s == nil {
		return Health{State: HealthNotConfigured, Strays: -1}
	}
	if h := s.publicHealth.Load(); h != nil {
		return *h
	}
	if !s.publicEnabled || s.gatewayURL == "" {
		return Health{State: HealthNotConfigured, Strays: -1}
	}
	return Health{State: HealthNotConfigured, Strays: -1}
}

// PrivateHealth returns the last private-tier record.
//
// The private tier has NO gateway and deliberately no IPFS_PRIVATE_GATEWAY_URL —
// it is replication, never distribution (A31/INT-08), and not having the knob is
// the guarantee. So its component reports the NODE's reachability plus its ledger
// facts, and nothing about serving viewers, because it never serves viewers.
func (s *Service) PrivateHealth() Health {
	if s == nil {
		return Health{State: HealthNotConfigured, Strays: -1}
	}
	if h := s.privateHealth.Load(); h != nil {
		return *h
	}
	return Health{State: HealthNotConfigured, Strays: -1}
}

// ProbeHealth takes one round of probes — both tiers — and stores the results.
// It never returns an error: an unreachable gateway is a VERDICT, not a failure
// of the probe, and the caller is a ticker with nothing to do with an error
// anyway. Callers that want the outcome read GatewayHealth/PrivateHealth after.
func (s *Service) ProbeHealth(ctx context.Context) {
	s.publicHealth.Store(ptr(s.probePublic(ctx)))
	s.privateHealth.Store(ptr(s.probePrivate(ctx)))
}

// probePublic asks the public gateway for a CID this instance publishes.
func (s *Service) probePublic(ctx context.Context) Health {
	h := Health{At: time.Now(), Strays: s.lastStrays(networkPublic)}
	if !s.publicEnabled {
		h.State = HealthNotConfigured
		h.Reason = "the public IPFS mirror is off (IPFS_ENABLED=false), so nothing is published and no media request is redirected to a gateway"
		return h
	}
	s.fillLedger(ctx, networkPublic, &h)
	if s.gateway == nil || s.gatewayURL == "" {
		// The mirror pins, but no gateway is configured to serve what it pins. That
		// is a real posture (a private-only publisher, or a gateway added later),
		// and delivery already refuses to redirect without a URL, so it is not a
		// fault — but it is worth saying, because "we are pinning bytes nobody can
		// fetch" is not what most operators think they configured.
		h.State = HealthNotConfigured
		h.Reason = "no IPFS_GATEWAY_URL is configured, so pinned objects are never offered to viewers and no media request is redirected"
		return h
	}
	cid, ok, err := s.probeCID(ctx, networkPublic)
	if err != nil {
		h.State = HealthDown
		h.Probed = true
		h.Reason = "the pin ledger could not be read, so this instance cannot say whether its gateway is serving what it published: " + err.Error()
		return h
	}
	if !ok {
		// Nothing pinned yet. There is no CID to ask for, so there is no verdict to
		// take — and inventing "ok" here would be the same claim the A31 lab caught
		// the RPC probe making. Redirectable() is false for it, which costs nothing:
		// with no pinned row PublicAssetURL answers ok=false anyway.
		h.State = HealthNotConfigured
		h.Reason = "the public mirror has pinned nothing yet, so there is no CID to ask the gateway for"
		return h
	}
	pctx, cancel := context.WithTimeout(ctx, gatewayProbeTimeout)
	defer cancel()
	h.Probed = true
	if err := s.gateway.Fetch(pctx, cid); err != nil {
		h.State = HealthDown
		// The sentence names the CONSEQUENCE as well as the cause, the discipline the
		// rest of /admin/system already keeps: an operator who reads only "connection
		// refused" fixes the gateway and never learns what the api did about it.
		h.Reason = "the IPFS gateway did not serve a CID this instance publishes, so thumbnails and storyboards are being served by the api instead of redirected (no viewer is broken by this, but no gateway bandwidth is being used either): " + err.Error()
		return h
	}
	h.State = HealthOK
	return h
}

// probePrivate reports the private replication tier: node reachability plus its
// ledger facts. No gateway, by design.
func (s *Service) probePrivate(ctx context.Context) Health {
	h := Health{At: time.Now(), Strays: s.lastStrays(networkPrivate)}
	if !s.privateEnabled || s.privateClient == nil {
		h.State = HealthNotConfigured
		h.Reason = "the private replication tier is off (IPFS_MIRROR_PRIVATE=false)"
		return h
	}
	s.fillLedger(ctx, networkPrivate, &h)
	pctx, cancel := context.WithTimeout(ctx, gatewayProbeTimeout)
	defer cancel()
	h.Probed = true
	if _, err := s.privateClient.Version(pctx); err != nil {
		h.State = HealthDown
		h.Reason = "the private-swarm IPFS node is unreachable, so private media is not being replicated off this instance (delivery is unaffected — the private swarm never serves viewers): " + err.Error()
		return h
	}
	h.State = HealthOK
	return h
}

// fillLedger reads one swarm's backlog/dead-letter/pinned facts into h. A read
// failure leaves the counts at zero and is reflected by the caller's verdict when
// it matters; the probe's own job is the gateway, not the database.
func (s *Service) fillLedger(ctx context.Context, network string, h *Health) {
	reader, ok := s.repo.(ledgerHealthReader)
	if !ok {
		return
	}
	row, err := reader.IPFSPinLedgerHealth(ctx, network)
	if err != nil {
		return
	}
	h.Backlog = row.Backlog
	h.DeadLettered = row.DeadLettered
	h.Pinned = row.Pinned
	h.OldestBacklogAgeSeconds = row.OldestBacklogAgeSeconds
	h.LastPinnedAgeSeconds = row.LastPinnedAgeSeconds
}

// probeCID picks the CID to ask the gateway for: the first pinned row on that
// swarm in object-key order. Deterministic on purpose — a probe that asked for a
// different object every five minutes would turn a single broken object into an
// intermittent verdict, and this is a question about the GATEWAY, not about any
// one CID.
func (s *Service) probeCID(ctx context.Context, network string) (string, bool, error) {
	reader, ok := s.repo.(verifyReader)
	if !ok {
		return "", false, nil
	}
	rows, err := reader.ListIPFSPinsForVerify(ctx, sqlcgen.ListIPFSPinsForVerifyParams{
		Network: network, AfterObjectKey: "", BatchSize: 1,
	})
	if err != nil {
		return "", false, err
	}
	if len(rows) == 0 {
		return "", false, nil
	}
	return rows[0].Cid, true, nil
}

// lastStrays reports the stray count the last verify sweep recorded for a swarm,
// or -1 when none has completed.
func (s *Service) lastStrays(network string) int64 {
	v, ok := s.strays.Load(network)
	if !ok {
		return -1
	}
	n, _ := v.(int64)
	return n
}

// ptr is the one-liner atomic.Pointer stores need.
func ptr[T any](v T) *T { return &v }

// healthSlot is the atomic the service keeps one of per swarm.
type healthSlot = atomic.Pointer[Health]
