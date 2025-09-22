package usecase

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"athena/internal/config"
	"athena/internal/domain"
)

// AtprotoPublisher publishes activity to ATProto (optional).
// Implementations should be best-effort and never block critical paths.
type AtprotoPublisher interface {
    PublishVideo(ctx context.Context, v *domain.Video) error
    StartBackgroundRefresh(ctx context.Context, interval time.Duration)
}

// AtprotoSessionStore persists/retrieves ATProto session tokens (encrypted outside of this layer)
type AtprotoSessionStore interface {
    SaveSession(ctx context.Context, key []byte, access, refresh, did string) error
    LoadSessionStrings(ctx context.Context, key []byte) (access string, refresh string, did string, err error)
}

// InstanceConfigReader abstracts reading instance configuration.
// Satisfied by repository.ModerationRepository.
type InstanceConfigReader interface {
	GetInstanceConfig(ctx context.Context, key string) (*domain.InstanceConfig, error)
}

type atprotoService struct {
    enabled bool
    cfg     *config.Config
    modRepo InstanceConfigReader
    client  *http.Client

	// session cache
	sessMu     chan struct{}
	accessJwt  string
	refreshJwt string
	repoDID    string
    fetchedAt  time.Time

    // persistence
    store  AtprotoSessionStore
    encKey []byte
}

func NewAtprotoService(modRepo InstanceConfigReader, cfg *config.Config, store AtprotoSessionStore, encKey []byte) AtprotoPublisher {
    httpClient := &http.Client{Timeout: 5 * time.Second}
    return &atprotoService{
        enabled: cfg.EnableATProto,
        cfg:     cfg,
        modRepo: modRepo,
        client:  httpClient,
        sessMu:  make(chan struct{}, 1),
        store:   store,
        encKey:  encKey,
    }
}

// resolvePDSURL returns PDS base URL from instance config, falling back to cfg.
func (s *atprotoService) resolvePDSURL(ctx context.Context) string {
	if s.modRepo != nil {
		if c, err := s.modRepo.GetInstanceConfig(ctx, "atproto_pds_url"); err == nil {
			var url string
			if err := json.Unmarshal(c.Value, &url); err == nil {
				if u := strings.TrimSpace(url); u != "" {
					return u
				}
			}
		}
	}
	return s.cfg.ATProtoPDSURL
}

// resolveRepoDID returns the instance DID (or empty if not configured).
func (s *atprotoService) resolveRepoDID(ctx context.Context) string {
	if s.modRepo != nil {
		if c, err := s.modRepo.GetInstanceConfig(ctx, "atproto_did"); err == nil {
			var did string
			if err := json.Unmarshal(c.Value, &did); err == nil {
				return strings.TrimSpace(did)
			}
		}
	}
	return ""
}

// PublishVideo posts a lightweight record to ATProto once a video is completed and public.
// This is a best-effort call; failures are logged (returned) but should not break the app.
func (s *atprotoService) PublishVideo(ctx context.Context, v *domain.Video) error {
	if !s.enabled || v == nil || v.Privacy != domain.PrivacyPublic || v.Status != domain.StatusCompleted {
		return nil
	}
	access, repoDID, err := s.ensureSession(ctx)
	if err != nil {
		return err
	}
    // Optionally upload thumbnail to PDS
    var thumb any
    if strings.TrimSpace(v.ThumbnailPath) != "" {
        if tb, err := s.uploadBlob(ctx, access, v.ThumbnailPath); err == nil { thumb = tb }
    }
    if thumb == nil && strings.TrimSpace(v.PreviewPath) != "" {
        if tb, err := s.uploadBlob(ctx, access, v.PreviewPath); err == nil { thumb = tb }
    }
    if thumb == nil && strings.TrimSpace(v.ThumbnailPath) != "" {
        for _, ext := range []string{".png", ".webp"} {
            alt := swapExt(v.ThumbnailPath, ext)
            if _, err := os.Stat(alt); err == nil {
                if tb, err := s.uploadBlob(ctx, access, alt); err == nil { thumb = tb; break }
            }
        }
    }
    // Choose embed type
    text := v.Title
    if text == "" { text = "New video" }
    if s.cfg.ATProtoUseImageEmbed && thumb != nil {
        // app.bsky.embed.images with alt text
        alt := v.Description
        if s.cfg.ATProtoImageAltField == "title" || (alt == "" && s.cfg.ATProtoImageAltField != "description") {
            alt = v.Title
        }
        if alt == "" { alt = "Video thumbnail" }
        embed := map[string]any{
            "$type": "app.bsky.embed.images",
            "images": []any{ map[string]any{ "image": thumb, "alt": alt } },
        }
        return s.createPost(ctx, access, repoDID, text, embed)
    }
    // Default: external embed
    url := s.publicVideoURL(v)
    desc := v.Description
    if desc == "" { desc = v.Title }
    embed := map[string]any{
        "$type": "app.bsky.embed.external",
        "external": map[string]any{
            "uri": url,
            "title": v.Title,
            "description": desc,
        },
    }
    if thumb != nil { embed["external"].(map[string]any)["thumb"] = thumb }
    return s.createPost(ctx, access, repoDID, text, embed)
}

// publicVideoURL constructs a public link for the video for external embed.
func (s *atprotoService) publicVideoURL(v *domain.Video) string {
	base := strings.TrimRight(s.cfg.PublicBaseURL, "/")
	if base == "" {
		return fmt.Sprintf("/api/v1/videos/%s", v.ID)
	}
	return fmt.Sprintf("%s/videos/%s", base, v.ID)
}

type atprotoSession struct {
	AccessJwt  string `json:"accessJwt"`
	RefreshJwt string `json:"refreshJwt"`
	DID        string `json:"did"`
}

func (s *atprotoService) createSession(ctx context.Context, identifier, password string) (accessJwt string, did string, err error) {
	pds := strings.TrimRight(s.resolvePDSURL(ctx), "/")
	if pds == "" {
		return "", "", fmt.Errorf("atproto: missing PDS URL")
	}
	body := map[string]any{"identifier": identifier, "password": password}
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, pds+"/xrpc/com.atproto.server.createSession", bytes.NewReader(b))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("createSession status %d", resp.StatusCode)
	}
	var sess atprotoSession
	if err := json.NewDecoder(resp.Body).Decode(&sess); err != nil {
		return "", "", err
	}
	s.refreshJwt = sess.RefreshJwt
	return sess.AccessJwt, sess.DID, nil
}

// ensureSession returns a cached or refreshed session tokens and repo DID
func (s *atprotoService) ensureSession(ctx context.Context) (string, string, error) {
	handle := strings.TrimSpace(s.cfg.ATProtoHandle)
	appPass := strings.TrimSpace(s.cfg.ATProtoAppPassword)
	if handle == "" || appPass == "" {
		return "", "", fmt.Errorf("atproto: missing handle or app password for posting")
	}
	// lock to serialize session changes
	s.sessMu <- struct{}{}
	defer func() { <-s.sessMu }()
	// If cached and fresh (<50m), reuse
	if s.accessJwt != "" && time.Since(s.fetchedAt) < 50*time.Minute {
		did := s.repoDID
		if did == "" {
			did = s.resolveRepoDID(ctx)
		}
		if did == "" {
			return "", "", fmt.Errorf("atproto: missing repo DID")
		}
		return s.accessJwt, did, nil
	}
	// Try refresh
	if s.refreshJwt != "" {
		if acc, ref, did, err := s.refreshSession(ctx, s.refreshJwt); err == nil {
			s.accessJwt, s.refreshJwt, s.repoDID = acc, ref, did
			s.fetchedAt = time.Now()
			if s.repoDID == "" {
				s.repoDID = s.resolveRepoDID(ctx)
			}
			if s.repoDID == "" {
				return "", "", fmt.Errorf("atproto: missing repo DID")
			}
			if s.store != nil && len(s.encKey) == 32 {
				_ = s.store.SaveSession(ctx, s.encKey, s.accessJwt, s.refreshJwt, s.repoDID)
			}
			return s.accessJwt, s.repoDID, nil
		}
	}
	// Try persistent store
	if s.store != nil && len(s.encKey) == 32 {
		if acc, ref, did, err := s.store.LoadSessionStrings(ctx, s.encKey); err == nil && acc != "" {
			s.accessJwt, s.refreshJwt, s.repoDID = acc, ref, did
			s.fetchedAt = time.Now().Add(-40 * time.Minute)
			if s.repoDID == "" {
				s.repoDID = s.resolveRepoDID(ctx)
			}
			return s.accessJwt, s.repoDID, nil
		}
	}
	// Create new session
	acc, did, err := s.createSession(ctx, handle, appPass)
	if err != nil {
		return "", "", err
	}
	s.accessJwt, s.repoDID = acc, did
	s.fetchedAt = time.Now()
	if s.repoDID == "" {
		s.repoDID = s.resolveRepoDID(ctx)
	}
	if s.repoDID == "" {
		return "", "", fmt.Errorf("atproto: missing repo DID")
	}
	if s.store != nil && len(s.encKey) == 32 {
		_ = s.store.SaveSession(ctx, s.encKey, s.accessJwt, s.refreshJwt, s.repoDID)
	}
	return s.accessJwt, s.repoDID, nil
}

// refreshSession requests new tokens using refreshJwt
func (s *atprotoService) refreshSession(ctx context.Context, refreshJwt string) (access string, refresh string, did string, err error) {
	pds := strings.TrimRight(s.resolvePDSURL(ctx), "/")
	if pds == "" {
		return "", "", "", fmt.Errorf("atproto: missing PDS URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, pds+"/xrpc/com.atproto.server.refreshSession", nil)
	if err != nil {
		return "", "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+refreshJwt)
	resp, err := s.client.Do(req)
	if err != nil {
		return "", "", "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", "", fmt.Errorf("refreshSession status %d", resp.StatusCode)
	}
	var sess atprotoSession
	if err := json.NewDecoder(resp.Body).Decode(&sess); err != nil {
		return "", "", "", err
	}
	return sess.AccessJwt, sess.RefreshJwt, sess.DID, nil
}

// swapExt replaces the extension of a path if it has one
func swapExt(path, newExt string) string {
	i := strings.LastIndex(path, ".")
	if i <= 0 {
		return path + newExt
	}
	return path[:i] + newExt
}

func (s *atprotoService) createPost(ctx context.Context, accessJwt string, repoDID string, text string, embed map[string]any) error {
	pds := strings.TrimRight(s.resolvePDSURL(ctx), "/")
	if pds == "" {
		return fmt.Errorf("atproto: missing PDS URL")
	}
	record := map[string]any{
		"$type":     "app.bsky.feed.post",
		"text":      text,
		"createdAt": time.Now().UTC().Format(time.RFC3339),
	}
	if embed != nil {
		record["embed"] = embed
	}
	body := map[string]any{
		"repo":       repoDID,
		"collection": "app.bsky.feed.post",
		"record":     record,
		"validate":   true,
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, pds+"/xrpc/com.atproto.repo.createRecord", bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessJwt)
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("createRecord status %d", resp.StatusCode)
	}
	return nil
}

// getAuthorFeed fetches recent posts from an actor's feed.
func (s *atprotoService) getAuthorFeed(ctx context.Context, actor string, limit int, cursor string) (map[string]any, error) {
	pds := strings.TrimRight(s.resolvePDSURL(ctx), "/")
	if pds == "" {
		return nil, fmt.Errorf("atproto: missing PDS URL")
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	url := fmt.Sprintf("%s/xrpc/app.bsky.feed.getAuthorFeed?actor=%s&limit=%d", pds, actor, limit)
	if strings.TrimSpace(cursor) != "" {
		url += "&cursor=" + cursor
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("getAuthorFeed status %d", resp.StatusCode)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

// uploadBlob sends a file to com.atproto.repo.uploadBlob and returns the blob object for record embeds
func (s *atprotoService) uploadBlob(ctx context.Context, accessJwt string, filePath string) (map[string]any, error) {
	pds := strings.TrimRight(s.resolvePDSURL(ctx), "/")
	if pds == "" {
		return nil, fmt.Errorf("atproto: missing PDS URL")
	}
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	// crude MIME detect by extension
	ct := "application/octet-stream"
	lower := strings.ToLower(filePath)
	switch {
	case strings.HasSuffix(lower, ".jpg"), strings.HasSuffix(lower, ".jpeg"):
		ct = "image/jpeg"
	case strings.HasSuffix(lower, ".png"):
		ct = "image/png"
	case strings.HasSuffix(lower, ".webp"):
		ct = "image/webp"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, pds+"/xrpc/com.atproto.repo.uploadBlob", f)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Authorization", "Bearer "+accessJwt)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("uploadBlob status %d: %s", resp.StatusCode, string(b))
	}
	var out struct {
		Blob map[string]any `json:"blob"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if out.Blob == nil {
		return nil, fmt.Errorf("uploadBlob: missing blob in response")
	}
	return out.Blob, nil
}

// StartBackgroundRefresh periodically ensures a valid session token is available.
func (s *atprotoService) StartBackgroundRefresh(ctx context.Context, interval time.Duration) {
    // Use configured interval from environment if available
    if interval <= 0 {
        if s.cfg.ATProtoRefreshIntervalSeconds > 0 {
            interval = time.Duration(s.cfg.ATProtoRefreshIntervalSeconds) * time.Second
        } else {
            interval = 45 * time.Minute // fallback default
        }
    }
    ticker := time.NewTicker(interval)
    go func() {
        defer ticker.Stop()
        for {
            select {
            case <-ctx.Done():
                return
            case <-ticker.C:
                _, _, _ = s.ensureSession(ctx)
            }
        }
    }()
}
