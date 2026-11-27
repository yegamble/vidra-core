package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strconv"
	"time"
)

// Only the operator's collector can inspect the reference node and its approvals.
// The API consumes its atomically replaced observation on a read-only mount.
const maxReferenceStatusBytes = 4096

type referenceObservation struct {
	Version    int       `json:"version"`
	ObservedAt time.Time `json:"observed_at"`
	State      string    `json:"state"`
	Reason     string    `json:"reason"`
	Pinned     *int64    `json:"pinned"`
}

// Use fixed operator-facing messages. A collector's diagnostic may contain
// object names or provider credentials and must never become an API response.
var referenceVerdicts = map[string]componentStatus{
	"verified":             {Status: "ok"},
	"pin_set_mismatch":     {Status: "degraded", Error: "The node's recursive pins differ from the approved reference publications."},
	"media_unverified":     {Status: "degraded", Error: "The reference gateway could not verify media for an approved pin."},
	"approval_unavailable": {Status: "down", Error: "The reporter could not verify the reference approvals."},
	"node_unavailable":     {Status: "down", Error: "The reporter could not read the reference node's pin inventory."},
}

func (s *Server) probeIPFSReference(_ context.Context) componentStatus {
	unavailable := componentStatus{Status: "down", Error: "The IPFS reference status file is missing or unreadable."}
	invalid := componentStatus{Status: "down", Error: "The IPFS reference reporter returned an invalid observation."}
	path := s.cfg.IPFSReferenceStatusFile
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return unavailable
	}
	file, err := os.Open(path)
	if err != nil {
		return unavailable
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxReferenceStatusBytes+1))
	if err != nil {
		return unavailable
	}
	var obs referenceObservation
	if len(data) > maxReferenceStatusBytes || json.Unmarshal(data, &obs) != nil || obs.Version != 1 || obs.ObservedAt.IsZero() {
		return invalid
	}
	verdict, ok := referenceVerdicts[obs.Reason]
	if !ok || obs.State != verdict.Status || (obs.Pinned == nil && obs.State == "ok") ||
		(obs.Pinned != nil && (*obs.Pinned < 0 || obs.State == "down")) {
		return invalid
	}
	detail := map[string]string{"probed_at": obs.ObservedAt.UTC().Format(time.RFC3339)}
	age := time.Since(obs.ObservedAt)
	if age > 5*time.Minute || age < -30*time.Second {
		return componentStatus{Status: "degraded", Error: "The IPFS reference reporter has no current observation. Check the reporter and host clock.", Detail: detail}
	}
	if obs.Pinned != nil {
		detail["pinned"] = strconv.FormatInt(*obs.Pinned, 10)
	}
	verdict.Detail = detail
	return verdict
}
