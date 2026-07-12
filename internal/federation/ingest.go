package federation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
	"github.com/vidra/vidra-core/internal/urlsafety"
)

// Bounded remote-video fields (.ralph/specs/federation-remote-content.md §1):
// truncate, never reject on length.
const (
	maxRemoteTitleLen       = 500
	maxRemoteDescriptionLen = 5000
	// maxObjectBytes bounds a fetched Announce'd object document (§2).
	maxObjectBytes = 1 << 20 // 1 MiB
	// maxThumbnailBytes caps a cached remote thumbnail (§5).
	maxThumbnailBytes = 2 << 20 // 2 MiB
)

// apVideoObject is the subset of an ActivityStreams Video object we ingest.
type apVideoObject struct {
	ID           string          `json:"id"`
	Type         string          `json:"type"`
	Name         string          `json:"name"`
	Content      string          `json:"content"`
	AttributedTo json.RawMessage `json:"attributedTo"`
	Published    string          `json:"published"`
	Duration     string          `json:"duration"`
	URL          json.RawMessage `json:"url"`
	Icon         json.RawMessage `json:"icon"`
}

// handleCreateVideo ingests an inbound Create{Video} (remote-content §2).
// Authority: the activity actor must be the request signer, and the object must
// be attributed to the signer. The signer must also have at least one accepted
// local follow edge (we only ingest content we asked for by following) —
// otherwise the activity is accepted-and-ignored.
func (s *Service) handleCreateVideo(ctx context.Context, act inboxActivity, signerActorURL string) error {
	if act.Actor != signerActorURL {
		return ErrActorMismatch
	}
	var obj apVideoObject
	if err := json.Unmarshal(act.Object, &obj); err != nil || obj.Type != "Video" {
		return nil // not a Video object → accept-and-ignore
	}
	// The object must live on the signer's origin and be attributed to the signer.
	if obj.ID == "" || !sameHost(obj.ID, signerActorURL) {
		return nil
	}
	if !attributedToIncludes(obj.AttributedTo, signerActorURL) {
		return nil
	}
	ok, err := s.hasAcceptedFollowEdge(ctx, signerActorURL)
	if err != nil {
		return err
	}
	if !ok {
		return nil // nobody here follows this actor → accept-and-ignore
	}
	_, err = s.storeRemoteVideo(ctx, signerActorURL, obj)
	return err
}

// handleAnnounce ingests an inbound Announce{Video|url} (remote-content §2): a
// channel announcing a video to its followers. The signer is the announcing
// actor; an unseen object is fetched from its (same-origin) URL through the
// SSRF guard before upsert.
func (s *Service) handleAnnounce(ctx context.Context, act inboxActivity, signerActorURL string) error {
	if act.Actor != signerActorURL {
		return ErrActorMismatch
	}
	objectURL := objectID(act.Object)
	// Only announce objects on the announcer's own origin are ingested — we do
	// not proxy-fetch third-party origins on a remote's say-so.
	if objectURL == "" || !sameHost(objectURL, signerActorURL) {
		return nil
	}
	ok, err := s.hasAcceptedFollowEdge(ctx, signerActorURL)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	// Inline object with full fields? Use it. Otherwise fetch the document.
	var obj apVideoObject
	if err := json.Unmarshal(act.Object, &obj); err != nil || obj.Type == "" {
		fetched, err := s.fetchVideoObject(ctx, objectURL)
		if err != nil {
			return fmt.Errorf("federation: fetch announced object: %w", err)
		}
		obj = fetched
	}
	if obj.Type != "Video" || obj.ID == "" || !sameHost(obj.ID, signerActorURL) {
		return nil
	}
	// Attribution authority: the video's owner actor must share the announcing
	// origin; announces of foreign-attributed objects are ignored.
	owner := firstAttributedTo(obj.AttributedTo)
	if owner == "" {
		owner = signerActorURL
	}
	if !sameHost(owner, signerActorURL) {
		return nil
	}
	if owner != signerActorURL {
		// Make sure the owner actor is resolvable + cached (FK target); the signer
		// itself was cached during signature verification.
		if _, err := s.resolveRemoteActor(ctx, owner); err != nil {
			return nil // unresolvable attribution → accept-and-ignore
		}
	}
	_, err = s.storeRemoteVideo(ctx, owner, obj)
	return err
}

// hasAcceptedFollowEdge consults the ingestion gate (remote-content §2): by
// default the repository's accepted remote_channel_follows edges; a wired
// FollowEdgeChecker (tests) overrides it.
func (s *Service) hasAcceptedFollowEdge(ctx context.Context, remoteActorURL string) (bool, error) {
	if s.edges != nil {
		return s.edges.HasAcceptedFollow(ctx, remoteActorURL)
	}
	return s.repo.HasAcceptedRemoteChannelFollow(ctx, remoteActorURL)
}

// storeRemoteVideo bounds the object's fields, upserts it by object_url
// (idempotent), and best-effort caches its thumbnail. It returns the stored
// row so search-initiated resolution (config-parity W13) can surface the video
// by id; the inbox callers ignore it.
func (s *Service) storeRemoteVideo(ctx context.Context, ownerActorURL string, obj apVideoObject) (sqlcgen.UpsertRemoteVideoRow, error) {
	watchURL, streamURL := videoLinks(obj.URL)
	if watchURL == "" {
		watchURL = obj.ID
	}
	var published pgtype.Timestamptz
	if t, err := time.Parse(time.RFC3339, obj.Published); err == nil {
		published = pgtype.Timestamptz{Time: t, Valid: true}
	}
	row, err := s.repo.UpsertRemoteVideo(ctx, sqlcgen.UpsertRemoteVideoParams{
		ObjectUrl:       obj.ID,
		RemoteActorUrl:  ownerActorURL,
		Title:           truncate(obj.Name, maxRemoteTitleLen),
		Description:     truncate(obj.Content, maxRemoteDescriptionLen),
		DurationSeconds: parseISODurationSeconds(obj.Duration),
		PublishedAt:     published,
		WatchUrl:        watchURL,
		StreamUrl:       streamURL,
	})
	if err != nil {
		return sqlcgen.UpsertRemoteVideoRow{}, err
	}
	// Best-effort poster cache (§5): failure must never fail ingestion, and an
	// already-cached thumbnail is kept.
	if row.ThumbnailKey == nil {
		s.cacheRemoteThumbnail(ctx, row.ID, iconURL(obj.Icon))
	}
	return row, nil
}

// fetchVideoObject GETs and parses an announced object document through the
// SSRF guard, bounded to maxObjectBytes.
func (s *Service) fetchVideoObject(ctx context.Context, objectURL string) (apVideoObject, error) {
	guard := urlsafety.Guard{AllowPrivate: s.allowPrivateFetch}
	target, err := guard.ValidateURL(objectURL)
	if err != nil {
		return apVideoObject{}, fmt.Errorf("federation: unsafe object URL: %w", err)
	}
	client := s.fetchClient
	if client == nil {
		client = guard.NewClient(remoteFetchTimeout)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return apVideoObject{}, err
	}
	req.Header.Set("Accept", "application/activity+json")
	resp, err := client.Do(req)
	if err != nil {
		return apVideoObject{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return apVideoObject{}, fmt.Errorf("federation: object fetch returned status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxObjectBytes))
	if err != nil {
		return apVideoObject{}, err
	}
	var obj apVideoObject
	if err := json.Unmarshal(body, &obj); err != nil {
		return apVideoObject{}, fmt.Errorf("federation: parse object: %w", err)
	}
	return obj, nil
}

// cacheRemoteThumbnail fetches the object's poster through the SSRF guard
// (capped at maxThumbnailBytes), stores it at remote-thumbnails/<id>.jpg, and
// records the key. Strictly best-effort: any failure leaves the video without a
// cached preview (the card shows the no-preview fallback).
func (s *Service) cacheRemoteThumbnail(ctx context.Context, videoID uuid.UUID, thumbURL string) {
	if s.media == nil || thumbURL == "" {
		return
	}
	guard := urlsafety.Guard{AllowPrivate: s.allowPrivateFetch}
	target, err := guard.ValidateURL(thumbURL)
	if err != nil {
		return
	}
	client := s.fetchClient
	if client == nil {
		client = guard.NewClient(remoteFetchTimeout)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return
	}
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" && !strings.HasPrefix(ct, "image/") {
		return
	}
	// Read one byte past the cap to detect an oversized poster and skip it.
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxThumbnailBytes+1))
	if err != nil || len(data) == 0 || len(data) > maxThumbnailBytes {
		return
	}
	key := "remote-thumbnails/" + videoID.String() + ".jpg"
	if _, err := s.media.Put(ctx, key, bytes.NewReader(data)); err != nil {
		return
	}
	_ = s.repo.SetRemoteVideoThumbnail(ctx, sqlcgen.SetRemoteVideoThumbnailParams{
		ID:           videoID,
		ThumbnailKey: &key,
	})
}

// signerDomainBlocked reports whether the signer's origin host is on the admin
// instance blocklist (remote-content §8) — checked after signature verification,
// before any dispatch.
func (s *Service) signerDomainBlocked(ctx context.Context, signerActorURL string) (bool, error) {
	host := hostOf(signerActorURL)
	if host == "" {
		return false, nil
	}
	return s.repo.IsInstanceBlocked(ctx, host)
}

// --- small parsing helpers -------------------------------------------------

// hostOf returns the lowercased host (incl. port) of a URL, or "".
func hostOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Host)
}

// sameHost reports whether two URLs share a (non-empty) host.
func sameHost(a, b string) bool {
	ha, hb := hostOf(a), hostOf(b)
	return ha != "" && ha == hb
}

// truncate bounds s to at most max runes (never rejecting on length, per §1).
func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

// attributedToIncludes reports whether the attributedTo field (a string, an
// object with an id, or an array of those) names the given actor URL.
func attributedToIncludes(raw json.RawMessage, actorURL string) bool {
	for _, a := range attributedActors(raw) {
		if a == actorURL {
			return true
		}
	}
	return false
}

// firstAttributedTo returns the first attributed actor URL, or "".
func firstAttributedTo(raw json.RawMessage) string {
	if actors := attributedActors(raw); len(actors) > 0 {
		return actors[0]
	}
	return ""
}

// attributedActors flattens an AS attributedTo value into actor URLs.
func attributedActors(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		if one == "" {
			return nil
		}
		return []string{one}
	}
	var obj struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil && obj.ID != "" {
		return []string{obj.ID}
	}
	var many []json.RawMessage
	if err := json.Unmarshal(raw, &many); err != nil {
		return nil
	}
	var out []string
	for _, m := range many {
		out = append(out, attributedActors(m)...)
	}
	return out
}

// videoLinks extracts the human watch page URL and the best playable stream URL
// from an AS Video `url` value (§1): prefer an HLS application/x-mpegURL link,
// else a direct video/* link, else no stream URL. A plain string url is the
// watch page.
func videoLinks(raw json.RawMessage) (watchURL string, streamURL *string) {
	if len(raw) == 0 {
		return "", nil
	}
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		return one, nil
	}
	type apLink struct {
		Href      string `json:"href"`
		MediaType string `json:"mediaType"`
	}
	var links []apLink
	var single apLink
	if err := json.Unmarshal(raw, &links); err != nil {
		if err := json.Unmarshal(raw, &single); err != nil {
			return "", nil
		}
		links = []apLink{single}
	}
	var hls, direct string
	for _, l := range links {
		if l.Href == "" {
			continue
		}
		switch {
		case l.MediaType == "application/x-mpegURL" && hls == "":
			hls = l.Href
		case strings.HasPrefix(l.MediaType, "video/") && direct == "":
			direct = l.Href
		case l.MediaType == "text/html" && watchURL == "":
			watchURL = l.Href
		}
	}
	stream := hls
	if stream == "" {
		stream = direct
	}
	if stream == "" {
		return watchURL, nil
	}
	return watchURL, &stream
}

// iconURL extracts the first usable image URL from an AS `icon` value (a
// string, an Image object, or an array of those).
func iconURL(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		return one
	}
	var obj struct {
		URL json.RawMessage `json:"url"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil && len(obj.URL) > 0 {
		var u string
		if err := json.Unmarshal(obj.URL, &u); err == nil {
			return u
		}
		var link struct {
			Href string `json:"href"`
		}
		if err := json.Unmarshal(obj.URL, &link); err == nil {
			return link.Href
		}
		return ""
	}
	var many []json.RawMessage
	if err := json.Unmarshal(raw, &many); err != nil {
		return ""
	}
	for _, m := range many {
		if u := iconURL(m); u != "" {
			return u
		}
	}
	return ""
}

// parseISODurationSeconds parses an xsd:duration ("PT1H2M3S", PeerTube sends
// "PT123S") into whole seconds. Unknown/invalid forms → nil.
func parseISODurationSeconds(s string) *int32 {
	s = strings.TrimSpace(strings.ToUpper(s))
	if !strings.HasPrefix(s, "PT") || len(s) <= 2 {
		return nil
	}
	rest := s[2:]
	var total int64
	num := ""
	for _, r := range rest {
		switch {
		case r >= '0' && r <= '9':
			num += string(r)
		case r == 'H' || r == 'M' || r == 'S':
			if num == "" {
				return nil
			}
			n, err := strconv.ParseInt(num, 10, 64)
			if err != nil {
				return nil
			}
			switch r {
			case 'H':
				total += n * 3600
			case 'M':
				total += n * 60
			case 'S':
				total += n
			}
			num = ""
		default:
			return nil // fractional or unexpected component → treat as unknown
		}
	}
	if num != "" || total <= 0 || total > int64(^uint32(0)>>1) {
		return nil
	}
	out := int32(total)
	return &out
}
