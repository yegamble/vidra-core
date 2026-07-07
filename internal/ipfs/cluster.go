package ipfs

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// ClusterClient is the optional IPFS Cluster replication surface (STOR-05). When
// IPFS_CLUSTER_API_URL is configured the mirror calls ClusterPin/ClusterUnpin
// AFTER the node pin/unpin so the CID is replicated across the cluster's peers to
// the operator's replication factor. It is BEST-EFFORT: the authoritative mirror
// action is the local node pin, so a cluster error never fails a ledger row.
//
// ⚠️ Privacy (spec §7): cluster replication in v1 only ever carries content that
// was ALREADY node-pinned — i.e. already-public media. It is NOT a transport for
// private/unlisted/DM media; IPFS_MIRROR_PRIVATE stays out of scope. The cluster
// path is the ONLY transport a future private-media mode may ever use, but that
// mode is not shipped here.
type ClusterClient interface {
	// ClusterPin asks the cluster to pin (replicate) an already-added CID. Idempotent.
	ClusterPin(ctx context.Context, cid string) error
	// ClusterUnpin removes a cluster pin (idempotent).
	ClusterUnpin(ctx context.Context, cid string) error
	// ClusterHealth probes cluster reachability via GET /id. A non-nil error means
	// the cluster REST API is unreachable — callers degrade gracefully (the status
	// endpoint reports cluster_reachable=false), never fail a request.
	ClusterHealth(ctx context.Context) error
}

// KuboClusterClient talks to the IPFS Cluster REST API (POST /pins/<cid>,
// DELETE /pins/<cid>, GET /id). The API address is OPERATOR config (a trusted
// internal endpoint), so — like the Kubo RPC client — it does not go through the
// SSRF guard (which is for user-controlled URLs). The Bearer token is a SECRET:
// held unexported, sent only as an Authorization header, and NEVER logged (errors
// echo only the status code, never the raw response or the token).
type KuboClusterClient struct {
	apiURL string
	token  string
	http   *http.Client
}

// compile-time assertion that KuboClusterClient satisfies ClusterClient.
var _ ClusterClient = (*KuboClusterClient)(nil)

// NewKuboClusterClient builds a client for the given IPFS Cluster REST base URL
// (no trailing slash needed) and Bearer token (may be empty). hc may be nil, in
// which case http.DefaultClient is used; callers bound each call with a context
// deadline (IPFS_ADD_TIMEOUT).
func NewKuboClusterClient(apiURL, token string, hc *http.Client) *KuboClusterClient {
	if hc == nil {
		hc = http.DefaultClient
	}
	return &KuboClusterClient{apiURL: strings.TrimRight(apiURL, "/"), token: token, http: hc}
}

func (c *KuboClusterClient) ClusterPin(ctx context.Context, cid string) error {
	if err := ValidateCID(cid); err != nil {
		return err
	}
	return c.do(ctx, http.MethodPost, "/pins/"+url.PathEscape(cid))
}

func (c *KuboClusterClient) ClusterUnpin(ctx context.Context, cid string) error {
	if err := ValidateCID(cid); err != nil {
		return err
	}
	return c.do(ctx, http.MethodDelete, "/pins/"+url.PathEscape(cid))
}

func (c *KuboClusterClient) ClusterHealth(ctx context.Context) error {
	return c.do(ctx, http.MethodGet, "/id")
}

// do issues an authenticated request and discards the (bounded) body. A non-2xx
// becomes an error WITHOUT echoing the raw response verbatim (which could carry
// sensitive detail) and NEVER includes the Bearer token.
func (c *KuboClusterClient) do(ctx context.Context, method, path string) error {
	req, err := http.NewRequestWithContext(ctx, method, c.apiURL+path, nil)
	if err != nil {
		return fmt.Errorf("ipfs cluster: build request: %w", err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("ipfs cluster: request failed: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("ipfs cluster: status %d", resp.StatusCode)
	}
	return nil
}
