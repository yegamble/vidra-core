package httpapi

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/ipfsmirror"
	"github.com/vidra/vidra-core/internal/observability"
)

// Hybrid IPFS media mirroring — admin surface (fix_plan P19, spec
// .ralph/specs/ipfs-media.md). IPFS is a MIRROR SIDECAR — local/S3 stays
// authoritative — so these endpoints are informational and never gate serving.
//
// Behavior:
//   - IPFS_ENABLED=false (default) ⇒ 503 ipfs_disabled (mirrors the
//     PeerTube-import "not configured" 503).
//   - GET /ipfs/status: real payload from the mirror service (P19.2). If enabled
//     but the mirror is not wired on this build, an honest 501.
//   - POST /admin/ipfs/reconcile: real one-shot scan (P19.6) — re-arm dead-letters
//     + seed pin intents for eligible pre-existing public objects, audited and
//     idempotent. 501 only when the mirror subsystem is not wired on this build.

// ipfsMirrorProvider is the slice of the mirror service the HTTP layer needs.
// *ipfsmirror.Service satisfies it; handler tests fake it.
type ipfsMirrorProvider interface {
	Status(ctx context.Context) (ipfsmirror.Status, error)
	// EnqueueUserReeval records a durable, off-request-path per-user mirror
	// re-evaluation after an unlisted toggle / activation change. The handler does
	// this cheap queue write ONLY — the SyncVideo fan-out runs in the mirror worker,
	// never inline on the request goroutine (P19 round-2 audit MAJOR).
	EnqueueUserReeval(ctx context.Context, userID uuid.UUID) error
	// Reconcile re-arms dead-lettered ('failed') ledger rows so the worker retries
	// them; returns how many were re-armed. Backs the failed-pin retry half of the
	// admin reconcile.
	Reconcile(ctx context.Context) (int64, error)
	// Backfill seeds pin intents for every eligible pre-existing object missing a
	// ledger row (all classes), routed to each object's eligible swarm, returning the
	// per-class counts of what was newly enqueued. Idempotent — a second run enqueues
	// zero. networkFilter ("" = all) scopes the seeding to one swarm (the ?network=
	// admin filter, P19.P2).
	Backfill(ctx context.Context, networkFilter string) (ipfsmirror.BackfillCounts, error)
	// VideoPins backs the detail `ipfs` object: the pinned original/HLS CIDs +
	// gateway base for one video (ok=false when nothing is pinned).
	VideoPins(ctx context.Context, videoID uuid.UUID) (ipfsmirror.VideoIPFS, bool, error)
	// PinnedVideoIDs backs the card/feed `ipfs_pinned` badge: which of the given
	// videos have at least one pinned object, in one batched query.
	PinnedVideoIDs(ctx context.Context, videoIDs []uuid.UUID) (map[uuid.UUID]bool, error)
}

// ipfsPinCountsView is the {pinned,pending,failed,unpinned} tally (schema
// IPFSPinCounts).
type ipfsPinCountsView struct {
	Pinned   int64 `json:"pinned"`
	Pending  int64 `json:"pending"`
	Failed   int64 `json:"failed"`
	Unpinned int64 `json:"unpinned"`
}

// ipfsClassPinCountsView is one media class's tally (schema IPFSClassPinCounts).
type ipfsClassPinCountsView struct {
	MediaClass string `json:"media_class"`
	ipfsPinCountsView
}

// ipfsNetworkStatusView is one swarm's slice of the status (schema IPFSNetworkStatus,
// P19.P2): enabled + node reachability + this swarm's pin counts. NO gateway URL and
// NO CIDs — the private swarm is replication, not distribution (spec §5).
type ipfsNetworkStatusView struct {
	Enabled       bool                     `json:"enabled"`
	NodeReachable bool                     `json:"node_reachable"`
	Pins          ipfsPinCountsView        `json:"pins"`
	ByClass       []ipfsClassPinCountsView `json:"by_class"`
}

// ipfsNetworksView is the additive public/private split (schema IPFSNetworks).
type ipfsNetworksView struct {
	Public  ipfsNetworkStatusView `json:"public"`
	Private ipfsNetworkStatusView `json:"private"`
}

// ipfsStatusView is the GET /ipfs/status body (schema IPFSStatus). The top-level
// pins/by_class/node_reachable FOLD across swarms (back-compat); networks splits them.
type ipfsStatusView struct {
	Enabled          bool                     `json:"enabled"`
	NodeReachable    bool                     `json:"node_reachable"`
	GatewayURL       string                   `json:"gateway_url"`
	ClusterEnabled   bool                     `json:"cluster_enabled"`
	ClusterReachable bool                     `json:"cluster_reachable"`
	Pins             ipfsPinCountsView        `json:"pins"`
	ByClass          []ipfsClassPinCountsView `json:"by_class"`
	Networks         ipfsNetworksView         `json:"networks"`
}

func toClassPinCountsView(ccs []ipfsmirror.ClassCounts) []ipfsClassPinCountsView {
	out := make([]ipfsClassPinCountsView, 0, len(ccs))
	for _, cc := range ccs {
		out = append(out, ipfsClassPinCountsView{
			MediaClass:        cc.MediaClass,
			ipfsPinCountsView: ipfsPinCountsView(cc.PinCounts),
		})
	}
	return out
}

func toNetworkStatusView(ns ipfsmirror.NetworkStatus) ipfsNetworkStatusView {
	return ipfsNetworkStatusView{
		Enabled:       ns.Enabled,
		NodeReachable: ns.NodeReachable,
		Pins:          ipfsPinCountsView(ns.Pins),
		ByClass:       toClassPinCountsView(ns.ByClass),
	}
}

func toIPFSStatusView(st ipfsmirror.Status) ipfsStatusView {
	return ipfsStatusView{
		Enabled:          st.Enabled,
		NodeReachable:    st.NodeReachable,
		GatewayURL:       st.GatewayURL,
		ClusterEnabled:   st.ClusterEnabled,
		ClusterReachable: st.ClusterReachable,
		Pins:             ipfsPinCountsView(st.Pins),
		ByClass:          toClassPinCountsView(st.ByClass),
		Networks: ipfsNetworksView{
			Public:  toNetworkStatusView(st.Networks[ipfsmirror.NetworkPublic]),
			Private: toNetworkStatusView(st.Networks[ipfsmirror.NetworkPrivate]),
		},
	}
}

// handleIPFSStatus reports the mirror's status to an admin (P19.2): enabled, node
// reachability, gateway URL, cluster config, and pin counts overall + per class.
func (s *Server) handleIPFSStatus(c echo.Context) error {
	if !s.cfg.IPFSEnabled {
		return &IPFSDisabledError{}
	}
	if s.ipfsmirrorsvc == nil {
		return echo.NewHTTPError(http.StatusNotImplemented, "ipfs mirror subsystem is not wired on this build")
	}
	st, err := s.ipfsmirrorsvc.Status(c.Request().Context())
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, toIPFSStatusView(st))
}

// ipfsReconcileResultView is the POST /admin/ipfs/reconcile body (schema
// IPFSReconcileResult): how many pin intents this scan enqueued, overall and per
// media class. by_class is always a (possibly empty) object, never null.
type ipfsReconcileResultView struct {
	Enqueued int64            `json:"enqueued"`
	ByClass  map[string]int64 `json:"by_class"`
}

// handleIPFSReconcile runs the one-shot admin reconcile (P19.6): it re-arms
// dead-lettered pins so the worker retries them (the outage-recovery half), then
// seeds pin intents for every eligible ALREADY-PUBLIC object that has no ledger row
// yet (the catalog-backfill half — pre-mirror objects and anything missed during an
// outage). Every candidate passes the eligibility privacy fence before it is
// enqueued. Idempotent: a second run over an unchanged catalog enqueues zero.
// Admin-gated and audit-logged. Returns 503 ipfs_disabled when off, 501 when the
// mirror subsystem is not wired on this build.
func (s *Server) handleIPFSReconcile(c echo.Context) error {
	if !s.cfg.IPFSEnabled {
		return &IPFSDisabledError{}
	}
	if s.ipfsmirrorsvc == nil {
		return echo.NewHTTPError(http.StatusNotImplemented, "ipfs mirror subsystem is not wired on this build")
	}
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	ctx := c.Request().Context()

	// Optional ?network= filter (P19.P2): scope the backfill seeding to one swarm.
	// Empty = both. Anything else is a 400 (never silently ignored).
	networkFilter := c.QueryParam("network")
	switch networkFilter {
	case "", ipfsmirror.NetworkPublic, ipfsmirror.NetworkPrivate:
	default:
		return echo.NewHTTPError(http.StatusBadRequest, "network must be 'public' or 'private'")
	}

	// Re-arm dead-letters first (best-effort — a failure here must not abort the
	// backfill, which is the primary work of this endpoint). The re-arm re-derives each
	// failed row's direction in place, preserving its swarm, so it is network-safe
	// without a filter.
	rearmed, err := s.ipfsmirrorsvc.Reconcile(ctx)
	if err != nil {
		s.logger.WarnContext(ctx, "ipfs reconcile re-arm failed", "error", err)
	}

	counts, err := s.ipfsmirrorsvc.Backfill(ctx, networkFilter)
	if err != nil {
		s.audit(c, observability.ActionIPFSReconcile, observability.ResultFailure, userID.String(), "backfill_failed")
		return err
	}

	byClass := counts.ByClass
	if byClass == nil {
		byClass = map[string]int64{}
	}
	s.audit(c, observability.ActionIPFSReconcile, observability.ResultSuccess, userID.String(),
		fmt.Sprintf("enqueued=%d classes=%d rearmed=%d", counts.Total, len(byClass), rearmed))

	return c.JSON(http.StatusAccepted, ipfsReconcileResultView{
		Enqueued: counts.Total,
		ByClass:  byClass,
	})
}
