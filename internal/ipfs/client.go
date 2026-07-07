package ipfs

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
)

// AddResult is the outcome of an add: the resulting CID and the size Kubo
// reported. For a directory (wrap) add, CID is the wrapping root.
type AddResult struct {
	CID  string
	Size int64
}

// DirEntry is one file in a directory (wrap) add. Path is the forward-slash
// relative path inside the wrapped directory (e.g. "720p/seg_00001.ts").
type DirEntry struct {
	Path string
	Data io.Reader
}

// Client is the small surface the mirror depends on. *KuboClient talks to a real
// Kubo node over the RPC API; FakeIPFSClient implements it in memory for unit
// tests (which must NEVER touch a real node). Kept intentionally minimal so it is
// trivial to fake and to reason about.
type Client interface {
	// Version probes node health via /api/v0/version. A non-nil error means the
	// node is unreachable/unhealthy — callers must degrade gracefully, never fail
	// the request path (IPFS is non-authoritative).
	Version(ctx context.Context) (string, error)
	// Add streams a single object to the node, pinning it (cid-version=1,
	// raw-leaves=true), and returns its CID + size.
	Add(ctx context.Context, name string, r io.Reader) (AddResult, error)
	// AddDirectory wraps multiple files into one UnixFS directory (recursive,
	// wrap-with-directory) and returns the single root CID — the HLS-tree answer.
	AddDirectory(ctx context.Context, entries []DirEntry) (AddResult, error)
	// Pin pins an already-added CID (idempotent).
	Pin(ctx context.Context, cid string) error
	// Unpin removes a pin (idempotent). Unpinning does NOT guarantee erasure on a
	// public network — block reclamation is the node's own GC.
	Unpin(ctx context.Context, cid string) error
	// IsPinned reports whether the node currently pins the CID.
	IsPinned(ctx context.Context, cid string) (bool, error)
}

// KuboClient is a hand-rolled HTTP client for the Kubo RPC API (/api/v0/*). No
// heavy SDK. The API address is OPERATOR config (e.g. http://ipfs:5001), a
// trusted internal endpoint — it is NOT user-supplied, so it does not go through
// the SSRF guard (which is for user-controlled URLs).
type KuboClient struct {
	apiURL string
	http   *http.Client
}

// compile-time assertion that KuboClient satisfies Client.
var _ Client = (*KuboClient)(nil)

// NewKuboClient builds a client for the given Kubo RPC base URL (no trailing
// slash needed). hc may be nil, in which case http.DefaultClient is used; callers
// bound each call with a context deadline (IPFS_ADD_TIMEOUT).
func NewKuboClient(apiURL string, hc *http.Client) *KuboClient {
	if hc == nil {
		hc = http.DefaultClient
	}
	return &KuboClient{apiURL: strings.TrimRight(apiURL, "/"), http: hc}
}

// addResponse is one NDJSON object from /api/v0/add.
type addResponse struct {
	Name string `json:"Name"`
	Hash string `json:"Hash"`
	Size string `json:"Size"`
}

// versionResponse is /api/v0/version.
type versionResponse struct {
	Version string `json:"Version"`
}

func (c *KuboClient) Version(ctx context.Context) (string, error) {
	body, err := c.post(ctx, "/api/v0/version", nil, "")
	if err != nil {
		return "", err
	}
	defer body.Close()
	var v versionResponse
	if err := json.NewDecoder(body).Decode(&v); err != nil {
		return "", fmt.Errorf("ipfs: decode version: %w", err)
	}
	return v.Version, nil
}

func (c *KuboClient) Add(ctx context.Context, name string, r io.Reader) (AddResult, error) {
	q := url.Values{"pin": {"true"}, "cid-version": {"1"}, "raw-leaves": {"true"}}
	return c.add(ctx, q, []DirEntry{{Path: name, Data: r}})
}

func (c *KuboClient) AddDirectory(ctx context.Context, entries []DirEntry) (AddResult, error) {
	q := url.Values{
		"pin":                 {"true"},
		"cid-version":         {"1"},
		"raw-leaves":          {"true"},
		"wrap-with-directory": {"true"},
		"recursive":           {"true"},
	}
	return c.add(ctx, q, entries)
}

// add performs a multipart /api/v0/add and returns the root result. For a wrap
// add, Kubo emits one NDJSON line per object and the wrapping directory LAST (its
// Name is the top wrap); we take the final object with a non-empty Hash as root.
func (c *KuboClient) add(ctx context.Context, q url.Values, entries []DirEntry) (AddResult, error) {
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		for _, e := range entries {
			part, err := mw.CreateFormFile("file", e.Path)
			if err != nil {
				_ = pw.CloseWithError(err)
				return
			}
			if _, err := io.Copy(part, e.Data); err != nil {
				_ = pw.CloseWithError(err)
				return
			}
		}
		if err := mw.Close(); err != nil {
			_ = pw.CloseWithError(err)
			return
		}
		_ = pw.Close()
	}()

	body, err := c.post(ctx, "/api/v0/add?"+q.Encode(), pr, mw.FormDataContentType())
	if err != nil {
		return AddResult{}, err
	}
	defer body.Close()

	var last addResponse
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ar addResponse
		if err := json.Unmarshal([]byte(line), &ar); err != nil {
			return AddResult{}, fmt.Errorf("ipfs: decode add response: %w", err)
		}
		if ar.Hash != "" {
			last = ar
		}
	}
	if err := sc.Err(); err != nil {
		return AddResult{}, fmt.Errorf("ipfs: read add response: %w", err)
	}
	if last.Hash == "" {
		return AddResult{}, fmt.Errorf("ipfs: add returned no CID")
	}
	if err := ValidateCID(last.Hash); err != nil {
		return AddResult{}, fmt.Errorf("ipfs: node returned an invalid CID: %w", err)
	}
	var size int64
	fmt.Sscan(last.Size, &size)
	return AddResult{CID: last.Hash, Size: size}, nil
}

func (c *KuboClient) Pin(ctx context.Context, cid string) error {
	if err := ValidateCID(cid); err != nil {
		return err
	}
	body, err := c.post(ctx, "/api/v0/pin/add?arg="+url.QueryEscape(cid), nil, "")
	if err != nil {
		return err
	}
	_ = body.Close()
	return nil
}

func (c *KuboClient) Unpin(ctx context.Context, cid string) error {
	if err := ValidateCID(cid); err != nil {
		return err
	}
	body, err := c.post(ctx, "/api/v0/pin/rm?arg="+url.QueryEscape(cid), nil, "")
	if err != nil {
		return err
	}
	_ = body.Close()
	return nil
}

func (c *KuboClient) IsPinned(ctx context.Context, cid string) (bool, error) {
	if err := ValidateCID(cid); err != nil {
		return false, err
	}
	body, err := c.post(ctx, "/api/v0/pin/ls?arg="+url.QueryEscape(cid)+"&type=recursive", nil, "")
	if err != nil {
		// pin/ls returns an error status when the CID is not pinned; treat that as
		// "not pinned" rather than a transport failure.
		return false, nil
	}
	_ = body.Close()
	return true, nil
}

// post issues a POST to the Kubo RPC API (the API is POST-only). The caller owns
// the returned body and must Close it. A non-2xx is turned into an error without
// echoing the (potentially large/sensitive) raw node response verbatim.
func (c *KuboClient) post(ctx context.Context, path string, body io.Reader, contentType string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("ipfs: build request: %w", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ipfs: node request failed: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Drain a bounded amount so the connection can be reused, then discard.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		return nil, fmt.Errorf("ipfs: node returned status %d", resp.StatusCode)
	}
	return resp.Body, nil
}
