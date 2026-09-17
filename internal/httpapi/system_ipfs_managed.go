package httpapi

import (
	"context"
	"strconv"
	"time"
)

// The admin page can ask the host manager for node health. Readiness and media
// delivery retain their cached gateway probe and never make this round trip.
func (s *Server) managedIPFSStatus(ctx context.Context, gateway componentStatus) (componentStatus, bool) {
	if s.ipfscontrolsvc == nil {
		return componentStatus{}, false
	}
	doc, err := s.ipfscontrolsvc.Config(ctx)
	if err != nil {
		return componentStatus{Status: "down", Error: "The IPFS publication policy could not be read."}, true
	}
	if !doc.PolicyActive || doc.Config.Provider != "internal" {
		return componentStatus{}, false
	}
	detail := make(map[string]string)
	for key, value := range gateway.Detail {
		detail[key] = value
	}
	detail["provider"] = "internal"
	detail["configured"] = "true"
	detail["config_revision"] = strconv.FormatInt(doc.Revision, 10)
	detail["gateway_status"] = gateway.Status
	detail["publication"] = "paused"
	if doc.Config.Enabled {
		detail["publication"] = "enabled"
	}
	verdict := func(status, reason string) (componentStatus, bool) {
		return componentStatus{Status: status, Error: reason, Detail: detail}, true
	}
	runtime, err := s.ipfscontrolsvc.Runtime(ctx)
	if err != nil {
		return verdict("down", "The configured IPFS node could not be checked. Media continues through server storage.")
	}
	m := runtime.Management
	detail["node_state"] = m.ObservedState
	if !m.Available || m.ObservedState != "running" || m.LastErrorCode != nil {
		return verdict("down", "The configured IPFS node is unavailable or unhealthy. Media continues through server storage.")
	}
	if m.ObservedAt == nil || time.Since(*m.ObservedAt) > 30*time.Second || time.Until(*m.ObservedAt) > 5*time.Second {
		return verdict("down", "The configured IPFS node has no recent health observation.")
	}
	detail["node_observed_at"] = m.ObservedAt.UTC().Format(time.RFC3339)
	if runtime.ConfigRevision != doc.Revision || m.AppliedConfigRevision != doc.Revision {
		return verdict("pending", "The IPFS node has not applied the current publication policy yet.")
	}
	if m.Operation != nil && m.Operation.State != "succeeded" {
		if m.Operation.State == "failed" {
			return verdict("down", "The latest IPFS node operation failed. Check the IPFS configuration page.")
		}
		return verdict("pending", "An IPFS node operation is still in progress.")
	}
	// Pausing new publication must not hide a failed gateway for existing pins.
	if gateway.Status == "down" || gateway.Status == "degraded" {
		return verdict(gateway.Status, gateway.Error)
	}
	if !doc.Config.Enabled {
		return verdict("paused", "The IPFS node is running; new public pinning is paused. Gateway delivery is checked separately.")
	}
	if gateway.Status != "ok" {
		return verdict("pending", "The IPFS node is running, but gateway delivery is not verified or is disabled. Media continues through server storage.")
	}
	return verdict("ok", "")
}
