package httpapi

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// Hybrid IPFS media mirroring — admin surface (fix_plan P19, spec
// .ralph/specs/ipfs-media.md). These are the CONTRACT-FIRST stubs from slice
// P19.1: the routes and their shapes are fixed now so vidra-user and later
// backend slices can build against a stable contract.
//
// Behavior in P19.1:
//   - IPFS_ENABLED=false (default) ⇒ 503 ipfs_disabled (mirrors the
//     PeerTube-import "not configured" 503).
//   - IPFS_ENABLED=true            ⇒ 501 not_implemented — the mirror subsystem
//     (status aggregation is P19.2, the reconcile scan is P19.6) is not wired on
//     this build yet. This is an honest, client-safe placeholder tied to those
//     tracked fix_plan items, not fake data.

// handleIPFSStatus reports the mirror's status to an admin. Stub in P19.1; the
// real `{enabled, node_reachable, gateway_url, cluster_enabled, pins{}, by_class[]}`
// payload lands in P19.2.
func (s *Server) handleIPFSStatus(c echo.Context) error {
	if !s.cfg.IPFSEnabled {
		return &IPFSDisabledError{}
	}
	return echo.NewHTTPError(http.StatusNotImplemented, "ipfs status is not implemented yet (fix_plan P19.2)")
}

// handleIPFSReconcile kicks an immediate backfill/reconciliation scan (admin).
// Stub in P19.1; the real one-shot scan + enqueue-counts response lands in P19.6.
func (s *Server) handleIPFSReconcile(c echo.Context) error {
	if !s.cfg.IPFSEnabled {
		return &IPFSDisabledError{}
	}
	return echo.NewHTTPError(http.StatusNotImplemented, "ipfs reconcile is not implemented yet (fix_plan P19.6)")
}
