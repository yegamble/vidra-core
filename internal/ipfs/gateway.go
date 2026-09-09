package ipfs

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// GatewayProbe answers ONE question about a public IPFS gateway: would a viewer
// redirected there actually get bytes?
//
// WHY IT IS NOT THE RPC PROBE. The mirror already asks the node's RPC API for its
// version, and GET /ipfs/status reports that as node_reachable. A31 measured what
// that misses: with the kubo daemon running and its GATEWAY listener stopped, the
// RPC answered, the status page read healthy, and every server-side 307 to
// {gateway}/ipfs/{cid} died with "connection refused" — for up to five minutes per
// URL, because the redirect carries max-age=300. The RPC port and the gateway port
// are different listeners, frequently on different hosts (a shared or third-party
// gateway is a supported configuration), so RPC health is not evidence about the
// gateway at all. This probe therefore speaks HTTP to the gateway itself, on the
// exact URL shape delivery mints.
type GatewayProbe struct {
	baseURL string
	http    *http.Client
}

// NewGatewayProbe builds a probe for IPFS_GATEWAY_URL. hc may be nil
// (http.DefaultClient); callers bound each call with a context deadline.
func NewGatewayProbe(baseURL string, hc *http.Client) *GatewayProbe {
	if hc == nil {
		hc = http.DefaultClient
	}
	return &GatewayProbe{baseURL: strings.TrimRight(baseURL, "/"), http: hc}
}

// Fetch asks the gateway for one CID this instance is supposed to be publishing.
// A nil error means a viewer sent to this gateway for this CID gets a 2xx.
//
// It is a HEAD, with a GET fallback. HEAD is what a health probe wants — no body,
// no bandwidth, no chance of pulling a whole HLS tree through the api every five
// minutes — but gateway implementations vary in how well they support it (some
// answer 405 for a directory CID), and answering "down" because the gateway
// dislikes the VERB would take the mirror offline for a healthy gateway. So a
// non-2xx HEAD is retried once as a Range-limited GET, whose first byte is proof
// enough and whose body is discarded.
func (g *GatewayProbe) Fetch(ctx context.Context, cid string) error {
	if g == nil || g.baseURL == "" {
		return fmt.Errorf("ipfs: no gateway URL is configured")
	}
	if err := ValidateCID(cid); err != nil {
		return err
	}
	url := g.baseURL + "/ipfs/" + cid
	if err := g.try(ctx, http.MethodHead, url, false); err == nil {
		return nil
	} else if ctx.Err() != nil {
		// A cancelled/timed-out context is the gateway's verdict, not the verb's:
		// retrying with a GET on a dead deadline would report the wrong cause.
		return err
	}
	return g.try(ctx, http.MethodGet, url, true)
}

// try issues one probe request. ranged asks for a single byte so a large object
// cannot turn a health check into a transfer; a gateway that ignores Range answers
// 200 with the whole body, which is why the body is drained through a hard limit
// and discarded rather than read.
func (g *GatewayProbe) try(ctx context.Context, method, url string, ranged bool) error {
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return fmt.Errorf("ipfs: build gateway request: %w", err)
	}
	if ranged {
		req.Header.Set("Range", "bytes=0-0")
	}
	resp, err := g.http.Do(req)
	if err != nil {
		return fmt.Errorf("ipfs: the gateway did not answer: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// The status is the whole diagnosis and the body is a gateway's error page,
		// which is not this instance's to quote onto an admin surface.
		return fmt.Errorf("ipfs: the gateway answered %d for a CID this instance publishes", resp.StatusCode)
	}
	return nil
}
