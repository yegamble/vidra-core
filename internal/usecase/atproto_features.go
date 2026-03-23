package usecase

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"athena/internal/domain"
)

// AutoSyncEnabled returns whether automatic video syndication is enabled.
func (s *atprotoService) AutoSyncEnabled() bool {
	return s.enabled && s.cfg.ATProtoAutoSyncEnabled
}

// PublishComment creates a reply post on ATProto referencing a parent post.
// parentPostURI is the at:// URI of the video's original Bluesky post.
// If parentPostURI is empty the comment cannot be threaded and an error is returned.
func (s *atprotoService) PublishComment(ctx context.Context, comment *domain.Comment, video *domain.Video, parentPostURI string) (*AtprotoPostRef, error) {
	if !s.enabled {
		return nil, nil
	}
	if comment == nil || video == nil {
		return nil, fmt.Errorf("atproto: comment and video must not be nil")
	}
	if strings.TrimSpace(parentPostURI) == "" {
		return nil, fmt.Errorf("atproto: parentPostURI is required to create a threaded reply")
	}
	if comment.Status != domain.CommentStatusActive {
		return nil, nil
	}

	access, repoDID, err := s.ensureSession(ctx)
	if err != nil {
		return nil, fmt.Errorf("atproto: session error: %w", err)
	}

	// Resolve the parent post's CID via getRecord so we can construct a proper reply ref.
	parentCID, err := s.resolveRecordCID(ctx, parentPostURI)
	if err != nil {
		return nil, fmt.Errorf("atproto: resolving parent CID: %w", err)
	}

	// ATProto reply structure: root and parent both point to the video post.
	reply := map[string]any{
		"root": map[string]any{
			"uri": parentPostURI,
			"cid": parentCID,
		},
		"parent": map[string]any{
			"uri": parentPostURI,
			"cid": parentCID,
		},
	}

	// Truncate comment body to Bluesky's 300-grapheme limit.
	text := truncateText(comment.Body, 300)

	return s.createRecord(ctx, access, repoDID, text, nil, reply)
}

// PublishVideoBatch publishes multiple videos in sequence, collecting results.
// Each video is published independently so one failure does not block others.
func (s *atprotoService) PublishVideoBatch(ctx context.Context, videos []*domain.Video) []AtprotoBatchResult {
	results := make([]AtprotoBatchResult, 0, len(videos))
	for _, v := range videos {
		if v == nil {
			continue
		}
		// Eligibility checks (same as PublishVideo)
		if !s.enabled || v.Privacy != domain.PrivacyPublic || v.Status != domain.StatusCompleted {
			results = append(results, AtprotoBatchResult{VideoID: v.ID})
			continue
		}
		access, repoDID, err := s.ensureSession(ctx)
		if err != nil {
			results = append(results, AtprotoBatchResult{VideoID: v.ID, Err: err})
			continue
		}
		// Upload thumbnail (best-effort, same logic as PublishVideo)
		var thumb any
		if strings.TrimSpace(v.ThumbnailPath) != "" {
			if tb, err := s.uploadBlob(ctx, access, v.ThumbnailPath); err == nil {
				thumb = tb
			}
		}
		if thumb == nil && strings.TrimSpace(v.PreviewPath) != "" {
			if tb, err := s.uploadBlob(ctx, access, v.PreviewPath); err == nil {
				thumb = tb
			}
		}
		text := v.Title
		if text == "" {
			text = "New video"
		}
		ref, err := s.publishVideoWithRef(ctx, v, access, repoDID, thumb, text)
		if err != nil {
			results = append(results, AtprotoBatchResult{VideoID: v.ID, Err: err})
			continue
		}
		results = append(results, AtprotoBatchResult{VideoID: v.ID, Ref: ref})
	}
	return results
}

// ResolveHandle resolves a Bluesky handle to a DID via the PDS resolveHandle endpoint.
func (s *atprotoService) ResolveHandle(ctx context.Context, handle string) (*AtprotoIdentity, error) {
	if !s.enabled {
		return nil, fmt.Errorf("atproto: service is disabled")
	}
	handle = strings.TrimSpace(handle)
	if handle == "" {
		return nil, fmt.Errorf("atproto: handle is required")
	}
	// Strip leading @ if present
	handle = strings.TrimPrefix(handle, "@")

	pds := strings.TrimRight(s.resolvePDSURL(ctx), "/")
	if pds == "" {
		return nil, fmt.Errorf("atproto: missing PDS URL")
	}

	var identity AtprotoIdentity
	err := doWithRetry(ctx, s.retry, "resolveHandle", func() error {
		url := fmt.Sprintf("%s/xrpc/com.atproto.identity.resolveHandle?handle=%s", pds, handle)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := s.client.Do(req)
		if err != nil {
			return err
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			b, _ := io.ReadAll(resp.Body)
			return &retryableError{StatusCode: resp.StatusCode, Err: fmt.Errorf("resolveHandle status %d: %s", resp.StatusCode, string(b))}
		}
		var out struct {
			DID string `json:"did"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			return fmt.Errorf("resolveHandle: decode: %w", err)
		}
		identity = AtprotoIdentity{DID: out.DID, Handle: handle}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &identity, nil
}

// resolveRecordCID fetches a record by its AT URI and returns its CID.
func (s *atprotoService) resolveRecordCID(ctx context.Context, atURI string) (string, error) {
	pds := strings.TrimRight(s.resolvePDSURL(ctx), "/")
	if pds == "" {
		return "", fmt.Errorf("atproto: missing PDS URL")
	}
	// Parse at://did:plc:xxx/app.bsky.feed.post/rkey
	parts := strings.SplitN(strings.TrimPrefix(atURI, "at://"), "/", 3)
	if len(parts) < 3 {
		return "", fmt.Errorf("atproto: invalid AT URI: %s", atURI)
	}
	repo, collection, rkey := parts[0], parts[1], parts[2]

	var cid string
	err := doWithRetry(ctx, s.retry, "getRecord", func() error {
		url := fmt.Sprintf("%s/xrpc/com.atproto.repo.getRecord?repo=%s&collection=%s&rkey=%s", pds, repo, collection, rkey)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := s.client.Do(req)
		if err != nil {
			return err
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			b, _ := io.ReadAll(resp.Body)
			return &retryableError{StatusCode: resp.StatusCode, Err: fmt.Errorf("getRecord status %d: %s", resp.StatusCode, string(b))}
		}
		var out struct {
			CID string `json:"cid"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			return fmt.Errorf("getRecord: decode: %w", err)
		}
		cid = out.CID
		return nil
	})
	if err != nil {
		return "", err
	}
	return cid, nil
}

// truncateText truncates text to maxGraphemes, appending "..." if truncated.
func truncateText(text string, maxGraphemes int) string {
	runes := []rune(text)
	if len(runes) <= maxGraphemes {
		return text
	}
	return string(runes[:maxGraphemes-3]) + "..."
}
