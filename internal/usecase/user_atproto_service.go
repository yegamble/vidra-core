package usecase

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"vidra-core/internal/domain"
)

// UserAtprotoAccount mirrors the repository struct of the same name as a usecase-package
// type so this file doesn't depend on repository (which imports usecase elsewhere — Go
// disallows the cycle). Field-for-field compatible; the wiring at app startup converts.
type UserAtprotoAccount struct {
	UserID          string
	DID             string
	Handle          string
	PDSURL          string
	AccessJWT       string
	RefreshJWT      string
	LastRefreshedAt time.Time
}

// UserAtprotoStore is the per-user storage port. Implemented by repository.UserAtprotoRepository.
type UserAtprotoStore interface {
	Save(ctx context.Context, key []byte, acct *UserAtprotoAccount) error
	Get(ctx context.Context, key []byte, userID string) (*UserAtprotoAccount, error)
	Delete(ctx context.Context, userID string) error
	UpdateTokens(ctx context.Context, key []byte, userID, access, refresh string) error
}

// UserAtprotoService is the Phase 11 per-user ATProto wrapper. It mirrors the singleton
// `atprotoService`'s XRPC patterns (createSession, refreshSession, createRecord) but stores
// sessions per-user via UserAtprotoStore. Refresh races are serialized per-user via
// the mutex map.
type UserAtprotoService struct {
	store     UserAtprotoStore
	masterKey []byte
	client    *http.Client

	// per-user refresh mutex — prevents concurrent refresh from the same user (e.g. a
	// publish-video tab and a read-account tab racing).
	refreshLocks sync.Map // userID → *sync.Mutex
}

// NewUserAtprotoService builds the service. masterKey is the same instance master key
// the singleton AtprotoRepository uses; sourced at app startup.
func NewUserAtprotoService(store UserAtprotoStore, masterKey []byte) *UserAtprotoService {
	return &UserAtprotoService{
		store:     store,
		masterKey: masterKey,
		client:    &http.Client{Timeout: 30 * time.Second},
	}
}

// LinkAccount exchanges (handle, app-password) for a session via XRPC createSession against
// the user-supplied or default PDS, then persists encrypted. handle is the Bluesky handle
// (e.g. "alice.bsky.social"). pdsURL may be empty — defaults to https://bsky.social.
//
// Returns ErrInvalidCredentials on PDS 401, ErrDIDAlreadyLinked when another user owns the
// same DID, and any persistence error otherwise. The caller's app-password is never logged
// or telemetered.
func (s *UserAtprotoService) LinkAccount(ctx context.Context, userID, handle, appPassword, pdsURL string) (*UserAtprotoAccount, error) {
	handle = strings.TrimSpace(handle)
	appPassword = strings.TrimSpace(appPassword)
	pdsURL = strings.TrimSpace(pdsURL)
	if handle == "" || appPassword == "" {
		return nil, ErrInvalidCredentials
	}
	if pdsURL == "" {
		pdsURL = "https://bsky.social"
	}
	pdsURL = strings.TrimRight(pdsURL, "/")
	if _, err := url.Parse(pdsURL); err != nil {
		return nil, fmt.Errorf("atproto: invalid pds_url: %w", err)
	}

	access, refresh, did, err := s.xrpcCreateSession(ctx, pdsURL, handle, appPassword)
	if err != nil {
		return nil, err
	}

	acct := &UserAtprotoAccount{
		UserID:     userID,
		DID:        did,
		Handle:     handle,
		PDSURL:     pdsURL,
		AccessJWT:  access,
		RefreshJWT: refresh,
	}
	if err := s.store.Save(ctx, s.masterKey, acct); err != nil {
		return nil, err
	}
	return acct, nil
}

// Disconnect removes the per-user account. Idempotent.
func (s *UserAtprotoService) Disconnect(ctx context.Context, userID string) error {
	return s.store.Delete(ctx, userID)
}

// GetAccount returns the linked account or domain.ErrNotFound.
func (s *UserAtprotoService) GetAccount(ctx context.Context, userID string) (*UserAtprotoAccount, error) {
	return s.store.Get(ctx, s.masterKey, userID)
}

// ensureSession returns a fresh access token for the user, refreshing if stale. Serialized
// per-user via the refresh mutex map. Returns ErrSessionExpired if both the access and
// refresh tokens are unusable.
func (s *UserAtprotoService) ensureSession(ctx context.Context, userID string) (*UserAtprotoAccount, error) {
	mu := s.lockFor(userID)
	mu.Lock()
	defer mu.Unlock()

	acct, err := s.store.Get(ctx, s.masterKey, userID)
	if err != nil {
		return nil, err
	}
	// Fresh enough? <50 minutes since last refresh.
	if time.Since(acct.LastRefreshedAt) < 50*time.Minute {
		return acct, nil
	}
	// Try refresh.
	access, refresh, _, err := s.xrpcRefreshSession(ctx, acct.PDSURL, acct.RefreshJWT)
	if err != nil {
		return nil, ErrSessionExpired
	}
	if err := s.store.UpdateTokens(ctx, s.masterKey, userID, access, refresh); err != nil {
		return nil, err
	}
	acct.AccessJWT = access
	acct.RefreshJWT = refresh
	acct.LastRefreshedAt = time.Now()
	return acct, nil
}

// PublishVideo creates a Bluesky post for the given video on behalf of userID. The post
// embeds an external link to the video's public URL. Returns the AT URI of the created
// post, which callers persist on videos.atproto_uri.
func (s *UserAtprotoService) PublishVideo(ctx context.Context, userID string, video *domain.Video, publicVideoURL string) (string, error) {
	acct, err := s.ensureSession(ctx, userID)
	if err != nil {
		return "", err
	}
	text := truncateBlueskyText(fmt.Sprintf("\"%s\" — watch on Vidra: %s", video.Title, publicVideoURL))
	embed := map[string]any{
		"$type": "app.bsky.embed.external",
		"external": map[string]any{
			"uri":         publicVideoURL,
			"title":       video.Title,
			"description": video.Description,
		},
	}
	uri, err := s.xrpcCreateRecord(ctx, acct.PDSURL, acct.AccessJWT, acct.DID, text, embed)
	if err != nil {
		return "", err
	}
	return uri, nil
}

// Unsyndicate deletes a previously-published Bluesky post by AT URI.
func (s *UserAtprotoService) Unsyndicate(ctx context.Context, userID, atURI string) error {
	acct, err := s.ensureSession(ctx, userID)
	if err != nil {
		return err
	}
	rkey, err := rkeyFromATURI(atURI)
	if err != nil {
		return err
	}
	return s.xrpcDeleteRecord(ctx, acct.PDSURL, acct.AccessJWT, acct.DID, "app.bsky.feed.post", rkey)
}

func (s *UserAtprotoService) lockFor(userID string) *sync.Mutex {
	v, _ := s.refreshLocks.LoadOrStore(userID, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// ─── XRPC helpers ───

type sessionResp struct {
	AccessJwt  string `json:"accessJwt"`
	RefreshJwt string `json:"refreshJwt"`
	DID        string `json:"did"`
	Handle     string `json:"handle"`
}

func (s *UserAtprotoService) xrpcCreateSession(ctx context.Context, pds, identifier, password string) (access, refresh, did string, err error) {
	body, _ := json.Marshal(map[string]string{"identifier": identifier, "password": password})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, pds+"/xrpc/com.atproto.server.createSession", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return "", "", "", fmt.Errorf("atproto: createSession transport: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == 401 || resp.StatusCode == 400 {
		return "", "", "", ErrInvalidCredentials
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", "", fmt.Errorf("atproto: createSession status %d", resp.StatusCode)
	}
	var sess sessionResp
	if err := json.NewDecoder(resp.Body).Decode(&sess); err != nil {
		return "", "", "", err
	}
	return sess.AccessJwt, sess.RefreshJwt, sess.DID, nil
}

func (s *UserAtprotoService) xrpcRefreshSession(ctx context.Context, pds, refreshJwt string) (access, refresh, did string, err error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, pds+"/xrpc/com.atproto.server.refreshSession", nil)
	req.Header.Set("Authorization", "Bearer "+refreshJwt)
	resp, err := s.client.Do(req)
	if err != nil {
		return "", "", "", fmt.Errorf("atproto: refreshSession transport: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", "", fmt.Errorf("atproto: refreshSession status %d", resp.StatusCode)
	}
	var sess sessionResp
	if err := json.NewDecoder(resp.Body).Decode(&sess); err != nil {
		return "", "", "", err
	}
	return sess.AccessJwt, sess.RefreshJwt, sess.DID, nil
}

type createRecordResp struct {
	URI string `json:"uri"`
	CID string `json:"cid"`
}

func (s *UserAtprotoService) xrpcCreateRecord(ctx context.Context, pds, accessJwt, repoDID, text string, embed map[string]any) (string, error) {
	record := map[string]any{
		"$type":     "app.bsky.feed.post",
		"text":      text,
		"createdAt": time.Now().UTC().Format(time.RFC3339),
		"langs":     []string{"en"},
	}
	if embed != nil {
		record["embed"] = embed
	}
	body, _ := json.Marshal(map[string]any{
		"repo":       repoDID,
		"collection": "app.bsky.feed.post",
		"record":     record,
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, pds+"/xrpc/com.atproto.repo.createRecord", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+accessJwt)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("atproto: createRecord transport: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("atproto: createRecord status %d", resp.StatusCode)
	}
	var out createRecordResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return out.URI, nil
}

func (s *UserAtprotoService) xrpcDeleteRecord(ctx context.Context, pds, accessJwt, repoDID, collection, rkey string) error {
	body, _ := json.Marshal(map[string]string{
		"repo":       repoDID,
		"collection": collection,
		"rkey":       rkey,
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, pds+"/xrpc/com.atproto.repo.deleteRecord", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+accessJwt)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("atproto: deleteRecord transport: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("atproto: deleteRecord status %d", resp.StatusCode)
	}
	return nil
}

// rkeyFromATURI extracts the rkey from at://did:.../collection/rkey.
func rkeyFromATURI(atURI string) (string, error) {
	const prefix = "at://"
	if !strings.HasPrefix(atURI, prefix) {
		return "", fmt.Errorf("atproto: invalid AT URI %q", atURI)
	}
	rest := atURI[len(prefix):]
	parts := strings.Split(rest, "/")
	if len(parts) < 3 {
		return "", fmt.Errorf("atproto: malformed AT URI %q", atURI)
	}
	return parts[len(parts)-1], nil
}

// truncateBlueskyText respects Bluesky's 300-grapheme limit. We approximate via byte length
// (300 chars), which is a slight under-estimate for multi-byte but safe.
func truncateBlueskyText(s string) string {
	const max = 290
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// ─── Errors ───

// ErrInvalidCredentials is returned when the PDS rejects the (handle, app-password) pair.
var ErrInvalidCredentials = errors.New("atproto: invalid handle or app password")

// ErrSessionExpired is returned when the user's stored session can no longer be refreshed —
// they must re-link their account.
var ErrSessionExpired = errors.New("atproto: session expired, re-link required")

// ─── Public XRPC reads (no auth) ───

// AtprotoFeedPost is the wire shape of a Bluesky post returned from getAuthorFeed.
// Mirrors the frontend AtprotoPost type in src/lib/api/types.ts.
type AtprotoFeedPost struct {
	URI       string    `json:"uri"`
	CID       string    `json:"cid"`
	AuthorDID string    `json:"author_did"`
	Handle    string    `json:"handle"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
}

// PublicGetAuthorFeed fetches a Bluesky author feed via the public AppView XRPC endpoint.
// No auth required — used for /feed/{did} which is public. PDS hardcoded to bsky.social
// for now; the AppView resolves DIDs across PDSes.
func (s *UserAtprotoService) PublicGetAuthorFeed(ctx context.Context, did string, count int, cursor string) ([]AtprotoFeedPost, string, error) {
	if count <= 0 || count > 100 {
		count = 20
	}
	u := "https://public.api.bsky.app/xrpc/app.bsky.feed.getAuthorFeed"
	q := url.Values{}
	q.Set("actor", did)
	q.Set("limit", fmt.Sprintf("%d", count))
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u+"?"+q.Encode(), nil)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("atproto: getAuthorFeed transport: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("atproto: getAuthorFeed status %d", resp.StatusCode)
	}
	var raw struct {
		Cursor string `json:"cursor"`
		Feed   []struct {
			Post struct {
				URI    string `json:"uri"`
				CID    string `json:"cid"`
				Author struct {
					DID    string `json:"did"`
					Handle string `json:"handle"`
				} `json:"author"`
				Record struct {
					Text      string    `json:"text"`
					CreatedAt time.Time `json:"createdAt"`
				} `json:"record"`
			} `json:"post"`
		} `json:"feed"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, "", err
	}
	out := make([]AtprotoFeedPost, 0, len(raw.Feed))
	for _, item := range raw.Feed {
		out = append(out, AtprotoFeedPost{
			URI: item.Post.URI, CID: item.Post.CID,
			AuthorDID: item.Post.Author.DID, Handle: item.Post.Author.Handle,
			Text: item.Post.Record.Text, CreatedAt: item.Post.Record.CreatedAt,
		})
	}
	return out, raw.Cursor, nil
}

// AtprotoReply is a single reply in a post thread.
type AtprotoReply struct {
	URI          string    `json:"uri"`
	Text         string    `json:"text"`
	AuthorDID    string    `json:"author_did"`
	AuthorHandle string    `json:"author_handle"`
	CreatedAt    time.Time `json:"created_at"`
}

// PublicGetPostThread fetches replies to a given AT URI via the public AppView. Returns
// only direct replies (depth=1) — caller can flatten further if needed.
func (s *UserAtprotoService) PublicGetPostThread(ctx context.Context, atURI string) ([]federationReplyShim, error) {
	u := "https://public.api.bsky.app/xrpc/app.bsky.feed.getPostThread"
	q := url.Values{}
	q.Set("uri", atURI)
	q.Set("depth", "1")
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u+"?"+q.Encode(), nil)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("atproto: getPostThread transport: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("atproto: getPostThread status %d", resp.StatusCode)
	}
	var raw struct {
		Thread struct {
			Replies []struct {
				Post struct {
					URI    string `json:"uri"`
					Author struct {
						DID    string `json:"did"`
						Handle string `json:"handle"`
					} `json:"author"`
					Record struct {
						Text      string    `json:"text"`
						CreatedAt time.Time `json:"createdAt"`
					} `json:"record"`
				} `json:"post"`
			} `json:"replies"`
		} `json:"thread"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	out := make([]federationReplyShim, 0, len(raw.Thread.Replies))
	for _, r := range raw.Thread.Replies {
		out = append(out, federationReplyShim{
			URI: r.Post.URI, Text: r.Post.Record.Text,
			AuthorDID: r.Post.Author.DID, AuthorHandle: r.Post.Author.Handle,
			CreatedAt: r.Post.Record.CreatedAt,
		})
	}
	return out, nil
}

// federationReplyShim has the same JSON shape as the federation handler's AtprotoComment.
// Defined here so the service doesn't import the handler package (which depends on usecase).
type federationReplyShim struct {
	URI          string    `json:"uri"`
	Text         string    `json:"text"`
	AuthorDID    string    `json:"author_did"`
	AuthorHandle string    `json:"author_handle"`
	CreatedAt    time.Time `json:"created_at"`
}
