package auth

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/go-chi/chi/v5"

	"athena/internal/domain"
	"athena/internal/httpapi/shared"
	ipfspkg "athena/internal/ipfs"
)

// ipfsHTTPClient is a shared client for all IPFS API calls, enabling connection reuse.
var ipfsHTTPClient = &http.Client{}

// ServeAvatarFromIPFS proxies an avatar by CID from the local IPFS node.
// Route: GET /api/v1/avatars/{cid} — unauthenticated (avatars are public).
func (h *AuthHandlers) ServeAvatarFromIPFS(w http.ResponseWriter, r *http.Request) {
	cid := chi.URLParam(r, "cid")

	// Validate CID to prevent injection attacks
	if err := ipfspkg.ValidateCID(cid); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_CID", err.Error()))
		return
	}

	// Require IPFS to be configured
	if h.ipfsAPI == "" {
		shared.WriteError(w, http.StatusServiceUnavailable, domain.NewDomainError("IPFS_NOT_CONFIGURED", "IPFS is not configured"))
		return
	}

	// Fetch content from IPFS — cancel deferred here so the body stays readable during streaming
	body, cancel, err := h.fetchIPFSContent(r.Context(), cid)
	if err != nil {
		slog.Warn("avatar proxy: IPFS fetch failed", "cid", cid, "error", err)
		shared.WriteError(w, http.StatusServiceUnavailable, domain.NewDomainError("IPFS_UNAVAILABLE", "Failed to fetch content from IPFS"))
		return
	}
	defer cancel()
	defer func() { _ = body.Close() }()

	// Read first 512 bytes to detect content type
	sniff := make([]byte, 512)
	n, err := io.ReadAtLeast(body, sniff, 1)
	if err != nil {
		slog.Warn("avatar proxy: could not read IPFS content", "cid", cid, "error", err)
		shared.WriteError(w, http.StatusServiceUnavailable, domain.NewDomainError("IPFS_UNAVAILABLE", "Failed to read content from IPFS"))
		return
	}
	contentType := http.DetectContentType(sniff[:n])

	// Set headers — IPFS content is content-addressed and immutable
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.WriteHeader(http.StatusOK)

	// Stream: write the sniffed bytes then the rest of the body
	if _, err := w.Write(sniff[:n]); err != nil {
		return
	}
	_, _ = io.Copy(w, body)
}

// fetchIPFSContent retrieves content by CID from the local IPFS node.
// Returns the body and a cancel function; the caller must defer cancel() to release
// the context only after the body has been fully read.
// Uses testIPFSCat hook in tests.
func (h *AuthHandlers) fetchIPFSContent(ctx context.Context, cid string) (io.ReadCloser, context.CancelFunc, error) {
	if testIPFSCat != nil {
		body, err := testIPFSCat(cid)
		return body, func() {}, err
	}

	fetchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)

	u := h.ipfsAPI + "/api/v0/cat?arg=" + url.QueryEscape(cid)
	req, err := http.NewRequestWithContext(fetchCtx, http.MethodPost, u, nil)
	if err != nil {
		cancel()
		return nil, func() {}, err
	}
	resp, err := ipfsHTTPClient.Do(req)
	if err != nil {
		cancel()
		return nil, func() {}, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		cancel()
		return nil, func() {}, domain.NewDomainError("IPFS_UNAVAILABLE", "IPFS returned non-200 status")
	}
	return resp.Body, cancel, nil
}
