package ipfscontrol

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"time"

	"github.com/google/uuid"
)

type HostOperation struct {
	ID             string `json:"id"`
	Sequence       int64  `json:"sequence"`
	ConfigRevision int64  `json:"config_revision"`
	State          string `json:"state"`
}
type HostOperationResponse struct {
	Operation HostOperation `json:"operation"`
}
type HostEnvelope struct {
	ProtocolVersion int        `json:"protocol_version"`
	OperationID     string     `json:"operation_id"`
	Sequence        int64      `json:"sequence"`
	ConfigRevision  int64      `json:"config_revision"`
	Config          HostConfig `json:"config"`
}
type HostStatus struct {
	ProtocolVersion       int            `json:"protocol_version"`
	ObservedState         string         `json:"observed_state"`
	AppliedConfigRevision int64          `json:"applied_config_revision"`
	LastOperationID       *string        `json:"last_operation_id"`
	LastOperationSequence int64          `json:"last_operation_sequence"`
	Operation             *HostOperation `json:"operation"`
	LastErrorCode         *string        `json:"last_error_code"`
	RepoUsedBytes         *int64         `json:"repo_used_bytes"`
	FilesystemFreeBytes   *int64         `json:"filesystem_free_bytes"`
	ObservedAt            time.Time      `json:"observed_at"`
}

// HostClient can reach only the boot-configured Unix socket. It cannot follow
// redirects or pass browser credentials, Docker paths or arbitrary operations.
type HostClient struct{ http *http.Client }

func NewHostClient(socket string) (*HostClient, error) {
	if !filepath.IsAbs(socket) {
		return nil, errors.New("IPFS manager socket must be absolute")
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "unix", socket)
	}}
	return &HostClient{http: &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *HostClient) Close() { c.http.CloseIdleConnections() }
func (c *HostClient) Status(ctx context.Context) (HostStatus, error) {
	var s HostStatus
	err := c.do(ctx, http.MethodGet, "/v1/status", nil, http.StatusOK, &s)
	if err == nil {
		err = s.Validate()
	}
	return s, err
}
func (c *HostClient) Dispatch(ctx context.Context, action string, e HostEnvelope) (HostOperation, error) {
	var r HostOperationResponse
	if !oneOf(action, "apply", "restart") || e.ProtocolVersion != 1 || e.Sequence < 1 || e.ConfigRevision < 1 {
		return r.Operation, errors.New("invalid IPFS manager operation")
	}
	if _, err := uuid.Parse(e.OperationID); err != nil {
		return r.Operation, errors.New("invalid IPFS manager operation ID")
	}
	if err := e.Config.Validate(); err != nil {
		return r.Operation, err
	}
	b, err := json.Marshal(e)
	if err != nil {
		return r.Operation, err
	}
	err = c.do(ctx, http.MethodPost, "/v1/"+action, b, http.StatusAccepted, &r)
	if err == nil && (r.Operation.ID != e.OperationID || r.Operation.Sequence != e.Sequence || r.Operation.ConfigRevision != e.ConfigRevision || !oneOf(r.Operation.State, "pending", "running", "succeeded", "failed")) {
		err = errors.New("mismatched IPFS manager operation")
	}
	return r.Operation, err
}
func (c *HostClient) do(ctx context.Context, method, path string, body []byte, want int, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, "http://ipfs-manager"+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return errors.New("IPFS manager unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode != want {
		return fmt.Errorf("IPFS manager returned HTTP %d", res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 65537))
	if err != nil || len(b) > 65536 {
		return errors.New("invalid IPFS manager response")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return errors.New("invalid IPFS manager response")
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("invalid IPFS manager response")
	}
	return nil
}
func oneOf(s string, options ...string) bool {
	for _, v := range options {
		if s == v {
			return true
		}
	}
	return false
}

// Validate rejects incomplete or mismatched observations before they can release
// a reservation or turn a pending operation into an apparent success.
func (s HostStatus) Validate() error {
	if s.ProtocolVersion != 1 || s.ObservedAt.IsZero() || s.AppliedConfigRevision < 0 || s.LastOperationSequence < 0 || !oneOf(s.ObservedState, "absent", "starting", "running", "unhealthy", "stopped", "unknown") || (s.RepoUsedBytes != nil && *s.RepoUsedBytes < 0) || (s.FilesystemFreeBytes != nil && *s.FilesystemFreeBytes < 0) {
		return errors.New("invalid IPFS manager status")
	}
	if s.LastOperationSequence == 0 {
		if s.LastOperationID != nil || s.Operation != nil {
			return errors.New("inconsistent IPFS manager operation")
		}
		return nil
	}
	op := s.Operation
	if op == nil || s.LastOperationID == nil || op.ID != *s.LastOperationID || op.Sequence != s.LastOperationSequence || op.ConfigRevision < 1 || !oneOf(op.State, "pending", "running", "succeeded", "failed") {
		return errors.New("inconsistent IPFS manager operation")
	}
	if _, err := uuid.Parse(op.ID); err != nil {
		return errors.New("invalid IPFS manager operation ID")
	}
	return nil
}
