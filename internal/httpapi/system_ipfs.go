package httpapi

import (
	"strconv"
	"time"

	"github.com/vidra/vidra-core/internal/instancesettings"
	"github.com/vidra/vidra-core/internal/ipfsmirror"
)

// The `ipfs` and `private_ipfs` components, and the switch the delivery path
// reads before it mints a 307 to a gateway.
//
// A31 (INT-07) measured the two halves of the same gap. `GET /admin/system`
// probes postgres, redis, s3, smtp, search, ffmpeg, clamav, settings_sync and
// federation — there was no `ipfs` component, so a dead node never degraded the
// page an operator opens BECAUSE something is wrong, and the only honest signal
// lived one endpoint away on `GET /ipfs/status`, which nothing watches. And
// `delivery.mirrorSource` never asked whether the gateway was reachable before
// answering a thumbnail with a `307` carrying `Cache-Control: public,
// max-age=300`, so with the gateway stopped the api kept minting redirects that
// died, and a viewer saw broken images for five minutes per URL with no runtime
// lever to stop it.
//
// Both are answered from the SAME probe record (internal/ipfsmirror.Health), so
// the page and the redirect can never disagree: whatever /admin/system says about
// the gateway is exactly what the next media request will act on.

// ipfsHealthReader is the mirror's probe read side. *ipfsmirror.Service satisfies
// it; a test supplies a fake. An interface so this package depends on the two
// questions it asks — is the gateway serving, and how is the ledger doing — rather
// than on the mirror service.
type ipfsHealthReader interface {
	GatewayHealth() ipfsmirror.Health
	PrivateHealth() ipfsmirror.Health
}

// ipfsStatus renders the public-tier component, or reports that there is nothing
// to render.
//
// ABSENT rather than "ok" when no mirror is wired at all, on the same doctrine as
// the federation block: a component reading "ok" for a feature the instance does
// not run is a claim, and the honest answer to "how is IPFS doing?" on an install
// with no IPFS is silence.
func (s *Server) ipfsStatus() (componentStatus, bool) {
	if s.ipfsHealth == nil {
		return componentStatus{}, false
	}
	return ipfsComponent(s.ipfsHealth.GatewayHealth(), s.ipfsDeliverySettingOn()), true
}

// privateIPFSStatus renders the private replication tier's component.
func (s *Server) privateIPFSStatus() (componentStatus, bool) {
	if s.ipfsHealth == nil {
		return componentStatus{}, false
	}
	h := s.ipfsHealth.PrivateHealth()
	if h.State == ipfsmirror.HealthNotConfigured && h.Pinned == 0 && h.Backlog == 0 {
		// The private tier is off and has never held anything. Rendering a
		// permanently-not_configured row for it on every instance that will never
		// enable it is noise on a page whose whole job is to be scannable.
		return componentStatus{}, false
	}
	return privateIPFSComponent(h), true
}

// ipfsComponent turns one probe record into the operator's verdict.
//
// "down" and never "degraded", once probed and failing, and that is a decision
// rather than a default. A gateway that does not serve is not impaired at
// serving; it is not serving. What it does to the INSTANCE is a separate question
// and this file's callers answer it the way health.go already answers it for
// storage: nothing but PostgreSQL takes a replica out of rotation, so this
// reports the instance degraded with a 200 and no viewer sees anything at all —
// because the api simply serves the bytes itself. That is the point of saying it
// out loud in the message: an operator who reads "ipfs: down" and pages someone
// at 3am has misread a cost problem as an outage.
func ipfsComponent(h ipfsmirror.Health, settingOn bool) componentStatus {
	detail := ipfsDetail(h)
	switch {
	case h.State == ipfsmirror.HealthNotConfigured:
		return componentStatus{Status: "not_configured", Error: h.Reason, Detail: detail}
	case !settingOn:
		// Configured, probed, and switched off by an admin at runtime. Not a fault —
		// it is the lever working — but it must not read "ok" either, because "ok"
		// beside zero gateway traffic is how an operator concludes the flip did not
		// take. not_configured is this page's word for "a supported posture nobody
		// needs to fix", and that is what this is.
		return componentStatus{
			Status: "not_configured",
			Error:  "delivery_ipfs_enabled is off, so no media request is redirected to the gateway and the watch page offers no IPFS control. Pinning continues; this switch governs DELIVERY only, and turning it back on takes effect on the next request",
			Detail: detail,
		}
	case h.State == ipfsmirror.HealthDown:
		return componentStatus{Status: "down", Error: h.Reason, Detail: detail}
	}
	if h.DeadLettered > 0 {
		// The gateway serves, but work has given up. Degraded rather than down, on
		// the federation queue's reasoning: the instance is serving and viewers are
		// fine; some objects are simply not on IPFS and will not get there on their
		// own until an operator or the reconcile pass re-arms them.
		return componentStatus{
			Status: "degraded",
			Error: strconv.FormatInt(h.DeadLettered, 10) +
				" pin operations exhausted their retries and were dead-lettered, so those objects are not on IPFS (or not off it) and nothing will retry them until POST /admin/ipfs/reconcile or the next reconcile tick re-arms them. Media delivery is unaffected — the api serves those objects itself.",
			Detail: detail,
		}
	}
	if h.OldestBacklogAgeSeconds > ipfsStallSeconds {
		return componentStatus{
			Status: "down",
			Error: "the oldest due pin operation is " + strconv.FormatInt(h.OldestBacklogAgeSeconds, 10) +
				"s overdue, past the whole retry ladder: nothing is draining the IPFS mirror queue, so no new media is reaching the gateway and nothing withdrawn is being unpinned from it.",
			Detail: detail,
		}
	}
	return componentStatus{Status: "ok", Detail: detail}
}

// privateIPFSComponent is the replication tier's verdict. It never mentions a
// gateway because the private swarm has none and deliberately cannot have one
// (INT-08: there is no IPFS_PRIVATE_GATEWAY_URL, and not having the knob is the
// guarantee) — so its `down` says what an operator actually loses, which is
// off-instance replication of private media, not delivery.
func privateIPFSComponent(h ipfsmirror.Health) componentStatus {
	detail := ipfsDetail(h)
	switch h.State {
	case ipfsmirror.HealthNotConfigured:
		return componentStatus{Status: "not_configured", Error: h.Reason, Detail: detail}
	case ipfsmirror.HealthDown:
		return componentStatus{Status: "down", Error: h.Reason, Detail: detail}
	}
	if h.DeadLettered > 0 {
		return componentStatus{
			Status: "degraded",
			Error: strconv.FormatInt(h.DeadLettered, 10) +
				" private pin operations exhausted their retries, so that media is not replicated on the private swarm. Nothing a viewer sees is affected: the private swarm never serves viewers.",
			Detail: detail,
		}
	}
	return componentStatus{Status: "ok", Detail: detail}
}

// ipfsStallSeconds is how overdue the oldest claimable pin row may be before the
// component calls the queue stalled. It is the mirror's full retry ladder — six
// attempts, one minute doubling to an hour cap — plus a reconcile interval of
// slack, so a queue walking its backoff honestly is never reported as stalled;
// only one nothing is working on.
const ipfsStallSeconds = 2 * 60 * 60

// ipfsDetail renders the facts behind the verdict, on the federation-health
// pattern: every arm carries the same detail, including the failing ones, because
// an operator reading a stalled queue wants "and the last thing that DID get
// pinned was at …" more than anyone reading a healthy one does.
//
// It is ALSO the whole answer to A31's observability clause. The mirror writes no
// job_runs rows and carries no correlation id — the operational projection is
// maintained by per-queue AFTER triggers (migration 0083) and media_ipfs_pins has
// none, exactly as cdn_purge_jobs and storage_migrations have none — so a mirror
// retry cannot be followed through the correlation chain A17 and A35 built. What
// it CAN have without a schema change is this: the queue's depth on /admin/jobs
// (jobstatus.QueueIPFSPins) and these numbers here.
func ipfsDetail(h ipfsmirror.Health) map[string]string {
	detail := map[string]string{
		"backlog":       strconv.FormatInt(h.Backlog, 10),
		"dead_lettered": strconv.FormatInt(h.DeadLettered, 10),
		"pinned":        strconv.FormatInt(h.Pinned, 10),
	}
	// Absent rather than a zero timestamp when nothing has ever been pinned, on the
	// same doctrine federation_detail applies to last_delivered_at: an operator can
	// tell "nothing published yet" from "the last one was at midnight" only if the
	// two look different, and 0001-01-01T00:00:00Z looks like a bug.
	if h.Pinned > 0 {
		detail["last_pinned_at"] = time.Now().
			Add(-time.Duration(h.LastPinnedAgeSeconds) * time.Second).
			UTC().Format(time.RFC3339)
	}
	if !h.At.IsZero() {
		detail["probed_at"] = h.At.UTC().Format(time.RFC3339)
	}
	// -1 is "the ledger<->node comparison has not completed", which is deliberately
	// not 0. Reporting it as 0 would claim the node holds nothing unaccounted for,
	// which is precisely the claim A31 caught the old reconcile making by silence.
	if h.Strays >= 0 {
		detail["unaccounted_node_pins"] = strconv.FormatInt(h.Strays, 10)
	}
	return detail
}

// ipfsDeliverySettingOn reports the runtime peer-mirror toggle
// (delivery_ipfs_enabled), read PER REQUEST like its two siblings.
//
// Its default is cfg.IPFSEnabled rather than a constant, so an instance that has
// never touched the setting behaves exactly as it did before this key existed —
// see instancesettings.KeyDeliveryIPFSEnabled for why defaulting it off would be
// a behaviour change wearing a safety default's clothes.
//
// A WORKER-ROLE PROCESS NEVER SEES A CHANGE TO IT, and that is fine here. The
// settings poller is role-gated: only the api role runs it, so a worker keeps the
// value it booted with. This switch governs DELIVERY — which route answers a media
// request — and a worker answers none: it pins and unpins, and that is governed by
// IPFS_ENABLED, which is env and restart-only on every role. So the one process
// that cannot see the flip is the one process the flip does not address.
func (s *Server) ipfsDeliverySettingOn() bool {
	// The default is read from THIS PROCESS'S OWN config rather than through the
	// settings service's Defaults, and only an explicit admin override is taken
	// from the service. The two are the same value when everything is wired
	// correctly; they come apart when a caller of instancesettings.NewService
	// forgets to populate Defaults.IPFSEnabled, and then the setting silently
	// resolves false and every gateway redirect stops on an instance whose
	// operator changed nothing. See instancesettings.Overridden.
	if s.settingssvc != nil && s.settingssvc.Overridden(instancesettings.KeyDeliveryIPFSEnabled) {
		return s.settingssvc.Bool(instancesettings.KeyDeliveryIPFSEnabled)
	}
	return s.cfg.IPFSEnabled
}

// ipfsDeliveryEnabled is what delivery.WithMirror consults before it will mint a
// 307 to the gateway. THREE things must hold, and each closes a distinct measured
// failure:
//
//   - the master env switch (cfg.IPFSEnabled), unchanged;
//   - the runtime toggle, so an operator can stop redirecting without a restart;
//   - and a gateway that answered its last probe, so the api never redirects a
//     viewer to something it has evidence is not serving.
//
// The third is the one A31 measured as absent. It is a CACHED verdict, at most one
// probe interval old, and the honest reading of that is written down in
// ipfsmirror/health.go: this narrows the window from "forever" to "one interval
// plus the max-age already minted", it does not close it.
func (s *Server) ipfsDeliveryEnabled() bool {
	if !s.cfg.IPFSEnabled || !s.ipfsDeliverySettingOn() {
		return false
	}
	if s.ipfsHealth == nil {
		// No probe wired (every unit-test server, and any embedder that builds the
		// resolver by hand). Fall back to the pre-existing behaviour rather than
		// refusing: this function's job is to STOP redirecting to a gateway known to
		// be broken, not to invent a new reason not to serve.
		return true
	}
	return s.ipfsHealth.GatewayHealth().Redirectable()
}
