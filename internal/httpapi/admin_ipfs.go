package httpapi

import (
	"context"
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/ipfsmirror"
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
//   - POST /admin/ipfs/reconcile: still 501 until the real one-shot backfill lands
//     in P19.6 (the periodic reconcile runs in-process regardless).

// ipfsMirrorProvider is the slice of the mirror service the HTTP layer needs.
// *ipfsmirror.Service satisfies it; handler tests fake it.
type ipfsMirrorProvider interface {
	Status(ctx context.Context) (ipfsmirror.Status, error)
	ReevaluateUser(ctx context.Context, userID uuid.UUID) error
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

// ipfsStatusView is the GET /ipfs/status body (schema IPFSStatus).
type ipfsStatusView struct {
	Enabled        bool                     `json:"enabled"`
	NodeReachable  bool                     `json:"node_reachable"`
	GatewayURL     string                   `json:"gateway_url"`
	ClusterEnabled bool                     `json:"cluster_enabled"`
	Pins           ipfsPinCountsView        `json:"pins"`
	ByClass        []ipfsClassPinCountsView `json:"by_class"`
}

func toIPFSStatusView(st ipfsmirror.Status) ipfsStatusView {
	v := ipfsStatusView{
		Enabled:        st.Enabled,
		NodeReachable:  st.NodeReachable,
		GatewayURL:     st.GatewayURL,
		ClusterEnabled: st.ClusterEnabled,
		Pins:           ipfsPinCountsView(st.Pins),
		ByClass:        make([]ipfsClassPinCountsView, 0, len(st.ByClass)),
	}
	for _, cc := range st.ByClass {
		v.ByClass = append(v.ByClass, ipfsClassPinCountsView{
			MediaClass:        cc.MediaClass,
			ipfsPinCountsView: ipfsPinCountsView(cc.PinCounts),
		})
	}
	return v
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

// handleIPFSReconcile kicks an immediate backfill/reconciliation scan (admin).
// The real one-shot backfill (enqueue every eligible pre-existing object missing
// a ledger row, audit-logged, idempotent) lands in P19.6; the periodic reconcile
// runs in-process meanwhile. Until then this answers an honest 501.
func (s *Server) handleIPFSReconcile(c echo.Context) error {
	if !s.cfg.IPFSEnabled {
		return &IPFSDisabledError{}
	}
	return echo.NewHTTPError(http.StatusNotImplemented, "ipfs reconcile is not implemented yet (fix_plan P19.6)")
}
