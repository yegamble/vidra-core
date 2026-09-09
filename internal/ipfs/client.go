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
	// ListPins enumerates every RECURSIVE pin the node currently holds, capped at
	// max entries. It is the node half of the ledger↔node reconciliation
	// (Service.VerifyPins): one call answers BOTH directions — a ledger row whose
	// CID is absent from the set has lost its pin, and a CID in the set that no
	// ledger row claims is a stray.
	//
	// It is a LIST rather than N × IsPinned deliberately. IsPinned reports a
	// transport failure as "not pinned" (kubo answers a non-2xx for an unpinned
	// CID and the two are indistinguishable at that endpoint), so a reconcile
	// built on it would re-add and re-pin the entire ledger every time the node
	// was unreachable — the exact opposite of what a repair pass should do when
	// it cannot see the node. An error here means "the node did not answer", and
	// the sweep declines to act on it.
	//
	// A node holding MORE than max pins returns an error rather than a truncated
	// set: a partial list is indistinguishable from a node that has lost pins,
	// and acting on it would re-add live content and under-report strays.
	ListPins(ctx context.Context, max int) (map[string]struct{}, error)
	// RepoGC runs the node's garbage collector (/api/v0/repo/gc) and reports how
	// many blocks it removed.
	//
	// UNPINNING IS NOT FORGETTING, and this is the only call that closes the gap.
	// Unpin drops this node's obligation to KEEP a CID; the blocks stay in the
	// datastore and the node's own gateway keeps serving them — verbatim, at the
	// same URL — until the collector runs. A31's rehearsal measured exactly that:
	// after a moderator block unpinned all five of a video's classes, every CID
	// still answered 200 on the instance's own gateway, and only `ipfs repo gc`
	// turned them into 404. So a takedown that must be complete on THIS instance
	// needs a GC, which is why IPFS_GC_AFTER_UNPIN exists.
	//
	// It is EXPENSIVE — a full sweep of the datastore, proportional to repo size,
	// not to what was just unpinned — which is why nothing calls it on a timer and
	// the knob defaults off. It is safe for pinned content by construction: GC
	// collects only blocks no pin (and no MFS/filestore reference) holds.
	//
	// The count is best-effort: kubo streams one NDJSON line per removed key and a
	// line this client cannot decode is skipped rather than failing a collection
	// that has already happened.
	RepoGC(ctx context.Context) (int64, error)
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

// pinLsResponse is /api/v0/pin/ls in its default (non-streaming) shape:
// {"Keys":{"<cid>":{"Type":"recursive"}}}. The streaming shape is deliberately
// NOT requested — the map form has been stable across every kubo generation this
// project has run against, and one JSON object is easier to bound than a line
// protocol.
type pinLsResponse struct {
	Keys map[string]struct {
		Type string `json:"Type"`
	} `json:"Keys"`
}

// pinLsMaxBytes bounds the pin/ls body this client will read. A recursive pin
// entry is roughly 70 bytes of JSON, so 16 MiB is on the order of 200k pins —
// far past any ledger this mirror writes, and small enough that a node with a
// pathological pinset cannot exhaust the api's memory on a five-minute timer.
const pinLsMaxBytes = 16 << 20

func (c *KuboClient) ListPins(ctx context.Context, max int) (map[string]struct{}, error) {
	body, err := c.post(ctx, "/api/v0/pin/ls?type=recursive", nil, "")
	if err != nil {
		return nil, err
	}
	defer body.Close()
	// LimitReader with ONE spare byte: reading exactly the cap cannot be
	// distinguished from a body that ended there, so the extra byte is what turns
	// "we filled the buffer" into a definite "there was more".
	raw, err := io.ReadAll(io.LimitReader(body, pinLsMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("ipfs: read pin list: %w", err)
	}
	if len(raw) > pinLsMaxBytes {
		return nil, fmt.Errorf("ipfs: pin list exceeds %d bytes; refusing to reconcile against a truncated pinset", pinLsMaxBytes)
	}
	var resp pinLsResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("ipfs: decode pin list: %w", err)
	}
	if max > 0 && len(resp.Keys) > max {
		return nil, fmt.Errorf("ipfs: node holds %d recursive pins, more than the %d this sweep will compare", len(resp.Keys), max)
	}
	out := make(map[string]struct{}, len(resp.Keys))
	for cid := range resp.Keys {
		out[cid] = struct{}{}
	}
	return out, nil
}

// gcResponse is one NDJSON object from /api/v0/repo/gc: the key it removed, or
// an error for one key. The Key object is {"/":"<cid>"}.
type gcResponse struct {
	Key struct {
		Slash string `json:"/"`
	} `json:"Key"`
	Error string `json:"Error"`
}

func (c *KuboClient) RepoGC(ctx context.Context) (int64, error) {
	body, err := c.post(ctx, "/api/v0/repo/gc", nil, "")
	if err != nil {
		return 0, err
	}
	defer body.Close()
	var removed int64
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var g gcResponse
		if err := json.Unmarshal([]byte(line), &g); err != nil {
			// The collection already ran; a line this client cannot read costs a
			// number, not correctness. Counting it as removed would be worse.
			continue
		}
		if g.Error == "" && g.Key.Slash != "" {
			removed++
		}
	}
	if err := sc.Err(); err != nil {
		// The GC itself is server-side and does not unwind: report what was counted
		// alongside the read failure rather than pretending nothing happened.
		return removed, fmt.Errorf("ipfs: read repo gc response: %w", err)
	}
	return removed, nil
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
