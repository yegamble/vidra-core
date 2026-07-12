package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/moderation"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
	"github.com/vidra/vidra-core/internal/video"
)

// validVideoPrivacy is the allowed privacy set; empty defaults to "private".
// "password" (CORE-17) is accepted here, but the service additionally requires
// the video to already hold >=1 password (else 400 password_required-style).
var validVideoPrivacy = map[string]bool{"public": true, "unlisted": true, "private": true, "password": true}

// createVideoRequest is the POST /api/v1/channels/{handle}/videos body.
type createVideoRequest struct {
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Privacy     string     `json:"privacy"`
	Category    string     `json:"category"`
	Language    string     `json:"language"`
	License     string     `json:"license"`
	Tags        []string   `json:"tags"`
	PublishAt   *time.Time `json:"publish_at"`
	// IsSensitive optionally marks the video as sensitive content
	// (instance-platform-info); defaults to false.
	IsSensitive bool `json:"is_sensitive"`
	// CommentsPolicy is the per-video comment policy (config-parity W9):
	// enabled|disabled. Omitted/empty seeds the instance's
	// default_comment_policy setting.
	CommentsPolicy string `json:"comments_policy"`
	// DownloadEnabled is the per-video download policy (config-parity W9),
	// layered on the instance downloads_enabled gate. Omitted (null) seeds the
	// instance's default_download_enabled setting.
	DownloadEnabled *bool `json:"download_enabled"`
}

func (r createVideoRequest) Validate() []FieldError {
	var fes []FieldError
	switch n := len(strings.TrimSpace(r.Title)); {
	case n == 0:
		fes = append(fes, FieldError{Field: "title", Message: "is required"})
	case n > 200:
		fes = append(fes, FieldError{Field: "title", Message: "must be at most 200 characters"})
	}
	if len(r.Description) > 5000 {
		fes = append(fes, FieldError{Field: "description", Message: "must be at most 5000 characters"})
	}
	if r.Privacy != "" && !validVideoPrivacy[r.Privacy] {
		fes = append(fes, FieldError{Field: "privacy", Message: "must be one of public, unlisted, private, password"})
	}
	if r.CommentsPolicy != "" && !video.IsCommentsPolicy(r.CommentsPolicy) {
		fes = append(fes, FieldError{Field: "comments_policy", Message: "must be one of enabled, disabled"})
	}
	fes = append(fes, validateTaxonomy(r.Category, r.Language, r.License)...)
	fes = append(fes, validateTags(r.Tags)...)
	fes = append(fes, validatePublishAt(r.PublishAt)...)
	return fes
}

// validatePublishAt requires a provided scheduled-publish time to lie in the
// future (§17). Nil (absent) is fine.
func validatePublishAt(t *time.Time) []FieldError {
	if t != nil && !t.After(time.Now()) {
		return []FieldError{{Field: "publish_at", Message: "must be in the future"}}
	}
	return nil
}

// validateTags enforces the free-form tag limits (product-decisions §18): at
// most video.MaxTagsPerVideo distinct tags after normalization, each at most
// video.MaxTagLen characters. Empty/duplicate entries are dropped silently by
// normalization, not rejected.
func validateTags(tags []string) []FieldError {
	var fes []FieldError
	normalized := video.NormalizeTags(tags)
	if len(normalized) > video.MaxTagsPerVideo {
		fes = append(fes, FieldError{Field: "tags", Message: "at most 5 tags are allowed"})
	}
	for _, t := range normalized {
		if len(t) > video.MaxTagLen {
			fes = append(fes, FieldError{Field: "tags", Message: "each tag must be at most 50 characters"})
			break
		}
	}
	return fes
}

// validateTaxonomy checks optional category/language/license values against the
// canonical GET /videos/config maps. Empty values are unset (skipped); a
// non-empty unknown value is a 422 field error.
func validateTaxonomy(category, language, license string) []FieldError {
	var fes []FieldError
	if category != "" && !video.IsCategory(category) {
		fes = append(fes, FieldError{Field: "category", Message: "unknown category"})
	}
	if language != "" && !video.IsLanguage(language) {
		fes = append(fes, FieldError{Field: "language", Message: "unknown language"})
	}
	if license != "" && !video.IsLicense(license) {
		fes = append(fes, FieldError{Field: "license", Message: "unknown license"})
	}
	return fes
}

// videoView is the public projection of a video. The technical metadata fields
// are populated on the detail endpoint once a probe has recorded them; they are
// omitted when unknown.
//
// A feed/search/subscriptions card may also be a REMOTE video
// (remote-content §3-4): Remote is true, Domain names the origin instance,
// WatchURL/StreamURL point at the origin, and the local-only fields
// (channel_id, privacy, state) are omitted. Its metadata detail lives at
// GET /remote-videos/{id} and its cached poster at
// GET /remote-videos/{id}/thumbnail.
type videoView struct {
	ID          string `json:"id"`
	Remote      bool   `json:"remote"`
	ChannelID   string `json:"channel_id,omitempty"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Privacy     string `json:"privacy,omitempty"`
	State       string `json:"state,omitempty"`
	// IsSensitive marks sensitive content (instance-platform-info). Always
	// emitted; false for remote cards (no local flag exists for them).
	IsSensitive     bool      `json:"is_sensitive"`
	CreatedAt       time.Time `json:"created_at"`
	DurationSeconds *int32    `json:"duration_seconds,omitempty"`
	Width           *int32    `json:"width,omitempty"`
	Height          *int32    `json:"height,omitempty"`
	// Domain, WatchURL, and StreamURL are the remote-card fields (set only when
	// Remote): the origin instance's domain, its human watch page, and the best
	// playable origin stream URL (HLS preferred; absent when the origin
	// advertises none — the card then links out via WatchURL).
	Domain    string  `json:"domain,omitempty"`
	WatchURL  string  `json:"watch_url,omitempty"`
	StreamURL *string `json:"stream_url,omitempty"`
	// HasThumbnail is set on the detail endpoint (nil/omitted on list/feed views,
	// which do not look it up); when set it reports whether a poster image is
	// available at GET /videos/{id}/thumbnail.
	HasThumbnail *bool `json:"has_thumbnail,omitempty"`
	// HasStoryboard is set on the detail endpoint; when true a seek-preview
	// storyboard is available at GET /videos/{id}/storyboard.jpg (+ .vtt map).
	HasStoryboard *bool `json:"has_storyboard,omitempty"`
	// HasChapters is set on the detail endpoint; when true a chapter list is
	// available at GET /videos/{id}/chapters (so the player fetches it only when
	// chapters exist). Same presence rule as has_thumbnail/has_storyboard.
	HasChapters *bool `json:"has_chapters,omitempty"`
	// Views is the recorded view count, set on the detail endpoint (omitted on
	// list/feed views, which do not look it up).
	Views *int64 `json:"views,omitempty"`
	// ChannelHandle and ChannelDisplayName identify the owning channel on
	// card/feed views, so the client can link a card to /channels/{handle} and
	// show the channel name. Omitted on the detail view (which does not join the
	// channel).
	ChannelHandle      *string `json:"channel_handle,omitempty"`
	ChannelDisplayName *string `json:"channel_display_name,omitempty"`
	// AuthorDisplayName is the uploader ACCOUNT's display name (config-parity
	// W5/W9: miniature_prefer_author_display_name), present alongside the
	// channel identity on local card/feed views and the detail view; omitted on
	// remote cards (no local account).
	AuthorDisplayName *string `json:"author_display_name,omitempty"`
	// CommentsPolicy is the per-video comment policy (config-parity W9):
	// enabled|disabled. Present on the create/update/detail views; omitted on
	// list/feed views and remote cards.
	CommentsPolicy string `json:"comments_policy,omitempty"`
	// CommentsEnabled is the EFFECTIVE comment availability on the DETAIL view:
	// the instance-wide comments_enabled setting AND this video's
	// comments_policy. Omitted elsewhere. Reading existing comments stays open
	// either way; this gates posting.
	CommentsEnabled *bool `json:"comments_enabled,omitempty"`
	// DownloadEnabled is the per-video download policy (config-parity W9),
	// present on the create/update/detail views. Effective availability is this
	// flag AND the instance downloads feature (GET /instance features.downloads);
	// moderators/admins bypass both.
	DownloadEnabled *bool `json:"download_enabled,omitempty"`
	// Category, Language, License are the optional taxonomy ids (see GET
	// /videos/config); omitted when unset. Populated on create/update/detail.
	Category *string `json:"category,omitempty"`
	Language *string `json:"language,omitempty"`
	License  *string `json:"license,omitempty"`
	// Tags is the video's free-form tag set (lowercased, alphabetical).
	// Populated on the create/update/detail views; omitted on list/feed views.
	Tags []string `json:"tags,omitempty"`
	// PublishAt is the scheduled publish time (§17), set on the detail,
	// create/update, and owner (studio) channel-list views while a schedule
	// exists; omitted when never scheduled. State stays 'scheduled' (public
	// surfaces filter on state=published) until the sweeper publishes it.
	PublishAt *time.Time `json:"publish_at,omitempty"`
	// HLSURL is the master-playlist path for HLS playback, set on the detail
	// endpoint only once the transcoded playlist is ready (omitted otherwise).
	// Renditions lists the available ladder rungs alongside it.
	HLSURL     *string         `json:"hls_url,omitempty"`
	Renditions []renditionView `json:"renditions,omitempty"`
	// IPFSPinned drives the card/feed IPFS badge (fix_plan P19): true when at
	// least one of the video's objects is pinned to IPFS. Emitted only when true
	// (a false/absent value means not pinned), and never for non-public videos —
	// only public+published videos have pinned media. Always absent when
	// IPFS_ENABLED is off.
	IPFSPinned bool `json:"ipfs_pinned,omitempty"`
	// IPFS carries the pinned CIDs on the DETAIL view, present only for a
	// public+published video with at least one pinned object; omitted otherwise.
	// CIDs are never emitted for non-public videos regardless of caller.
	IPFS *videoIPFSView `json:"ipfs,omitempty"`
}

// videoIPFSView is the detail `ipfs` object (schema VideoIPFS): validated CIDs +
// the gateway base a client resolves them under ({gateway_url}/ipfs/{cid}).
type videoIPFSView struct {
	OriginalCID string `json:"original_cid,omitempty"`
	HLSCID      string `json:"hls_cid,omitempty"`
	GatewayURL  string `json:"gateway_url,omitempty"`
}

func newVideoView(v sqlcgen.Video) videoView {
	downloadEnabled := v.DownloadEnabled
	return videoView{
		ID:              v.ID.String(),
		ChannelID:       v.ChannelID.String(),
		Title:           v.Title,
		Description:     v.Description,
		Privacy:         v.Privacy,
		State:           v.State,
		IsSensitive:     v.IsSensitive,
		CreatedAt:       v.CreatedAt,
		Category:        v.Category,
		Language:        v.Language,
		License:         v.License,
		PublishAt:       video.TimePtr(v.PublishAt),
		CommentsPolicy:  v.CommentsPolicy,
		DownloadEnabled: &downloadEnabled,
	}
}

func videoViewFromRow(v sqlcgen.GetVideoByIDRow) videoView {
	handle, name, author := v.ChannelHandle, v.ChannelDisplayName, v.AuthorDisplayName
	downloadEnabled := v.DownloadEnabled
	view := videoView{
		ID:          v.ID.String(),
		ChannelID:   v.ChannelID.String(),
		Title:       v.Title,
		Description: v.Description,
		Privacy:     v.Privacy,
		State:       v.State,
		IsSensitive: v.IsSensitive,
		CreatedAt:   v.CreatedAt,
		Category:    v.Category,
		Language:    v.Language,
		License:     v.License,
		PublishAt:   video.TimePtr(v.PublishAt),
		// Detail carries the owning channel so the frontend related-rail can link
		// and label without a second request (Wave A contract gap).
		ChannelHandle:      &handle,
		ChannelDisplayName: &name,
		CommentsPolicy:     v.CommentsPolicy,
		DownloadEnabled:    &downloadEnabled,
	}
	if author != "" {
		view.AuthorDisplayName = &author
	}
	return view
}

// handleCreateVideo creates a draft video under a channel owned by the caller.
func (s *Server) handleCreateVideo(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	var in createVideoRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}

	ctx := c.Request().Context()
	ch, err := s.channelsvc.GetByHandle(ctx, c.Param("handle"))
	if err != nil {
		return channelError(err) // ErrNotFound -> 404
	}
	if ch.OwnerID != userID {
		return echo.NewHTTPError(http.StatusForbidden, "you do not own this channel")
	}

	// Omitted fields seed from the instance publish defaults (config-parity
	// W9): an empty privacy/license/comments_policy and a null download_enabled
	// pass through unset and CreateDraft resolves them via the
	// WithPublishDefaultsFunc seam (default_video_privacy,
	// default_video_licence, default_comment_policy, default_download_enabled).
	v, err := s.videosvc.CreateDraft(ctx, ch.ID, video.CreateInput{
		Title:           in.Title,
		Description:     in.Description,
		Privacy:         in.Privacy,
		Category:        in.Category,
		Language:        in.Language,
		License:         in.License,
		Tags:            in.Tags,
		PublishAt:       in.PublishAt,
		IsSensitive:     in.IsSensitive,
		CommentsPolicy:  in.CommentsPolicy,
		DownloadEnabled: in.DownloadEnabled,
	})
	if err != nil {
		return videoError(err) // ErrPasswordRequired → 400
	}
	s.flagVideoWatchedWords(ctx, v.ID, v.Title, v.Description)
	view := newVideoView(v)
	s.attachVideoTags(ctx, &view, v.ID)
	return c.JSON(http.StatusCreated, view)
}

// flagVideoWatchedWords checks a video's title+description against the
// moderation watched-words list (§12), recording matches for the review queue.
// Best-effort: it never blocks the create/edit.
func (s *Server) flagVideoWatchedWords(ctx context.Context, videoID uuid.UUID, title, description string) {
	if s.watchwordsvc == nil {
		return
	}
	if _, err := s.watchwordsvc.FlagVideo(ctx, videoID, title+"\n"+description); err != nil {
		s.logger.WarnContext(ctx, "watched-word flagging failed", "error", err, "video_id", videoID)
	}
}

// attachVideoTags populates view.Tags from the stored tag set (best-effort: a
// lookup failure leaves tags absent rather than failing the request).
func (s *Server) attachVideoTags(ctx context.Context, view *videoView, videoID uuid.UUID) {
	if tags, err := s.videosvc.Tags(ctx, videoID); err == nil {
		view.Tags = tags
	}
}

// ipfsMirrorEnabled reports whether the IPFS mirror read surfaces should be
// consulted at all (master switch on + a mirror service wired on this build).
func (s *Server) ipfsMirrorEnabled() bool {
	return s.cfg.IPFSEnabled && s.ipfsmirrorsvc != nil
}

// attachVideoIPFS populates the detail `ipfs` object for a PUBLIC+PUBLISHED video
// from the pin ledger (fix_plan P19.3). It is the CID-emission gate: CIDs are
// NEVER exposed for a non-public/non-published video regardless of caller, and
// the gateway URL is built only from the configured IPFS_GATEWAY_URL + validated
// CIDs (inside the mirror service). Best-effort — a lookup failure leaves the
// field absent rather than failing the read (IPFS is non-authoritative).
func (s *Server) attachVideoIPFS(ctx context.Context, view *videoView, videoID uuid.UUID, privacy, state string) {
	if !s.ipfsMirrorEnabled() || privacy != "public" || state != "published" {
		return
	}
	pins, ok, err := s.ipfsmirrorsvc.VideoPins(ctx, videoID)
	if err != nil {
		s.logger.WarnContext(ctx, "ipfs video pins lookup failed", "error", err, "video_id", videoID)
		return
	}
	if !ok {
		return
	}
	view.IPFS = &videoIPFSView{
		OriginalCID: pins.OriginalCID,
		HLSCID:      pins.HLSCID,
		GatewayURL:  pins.GatewayURL,
	}
}

// attachIPFSPinned sets ipfs_pinned=true on each LOCAL card whose video has at
// least one pinned object (fix_plan P19.3), resolved in one batched query for the
// whole page (never a per-card lookup). Remote cards carry no local ledger and are
// skipped. Best-effort — a lookup failure leaves every badge false.
func (s *Server) attachIPFSPinned(ctx context.Context, views []videoView) {
	if !s.ipfsMirrorEnabled() || len(views) == 0 {
		return
	}
	ids := make([]uuid.UUID, 0, len(views))
	for i := range views {
		if views[i].Remote {
			continue
		}
		if id, err := uuid.Parse(views[i].ID); err == nil {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return
	}
	pinned, err := s.ipfsmirrorsvc.PinnedVideoIDs(ctx, ids)
	if err != nil {
		s.logger.WarnContext(ctx, "ipfs pinned-videos lookup failed", "error", err)
		return
	}
	for i := range views {
		if views[i].Remote {
			continue
		}
		if id, err := uuid.Parse(views[i].ID); err == nil && pinned[id] {
			views[i].IPFSPinned = true
		}
	}
}

// handleGetVideo returns a video by id. Runs behind optionalAuth: public and
// unlisted videos are visible to anyone with the link; private/non-public-state
// videos are visible to their owner and to moderators/admins managing local
// content. Invisible videos are reported as 404 so their existence is not
// leaked. This exception is metadata-detail only: media/playback routes keep
// their own narrower authorization.
func (s *Server) handleGetVideo(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	v, err := s.videoVisibleForDetail(c, id)
	if err != nil {
		return err
	}
	view := videoViewFromRow(v)
	// The EFFECTIVE comment availability (config-parity W9): the instance-wide
	// comments_enabled toggle AND this video's comments_policy. The watch page
	// shows/hides its composer from this one field.
	commentsEnabled := s.commentsEnabled() && v.CommentsPolicy == video.CommentsPolicyEnabled
	view.CommentsEnabled = &commentsEnabled
	if md, ok, err := s.videosvc.GetMetadata(c.Request().Context(), id); err == nil && ok {
		view.DurationSeconds = md.DurationSeconds
		view.Width = md.Width
		view.Height = md.Height
	}
	has := s.videosvc.HasThumbnail(c.Request().Context(), id)
	view.HasThumbnail = &has
	hasSB := s.videosvc.HasStoryboard(c.Request().Context(), id)
	view.HasStoryboard = &hasSB
	hasCh := s.videosvc.HasChapters(c.Request().Context(), id)
	view.HasChapters = &hasCh
	views := s.videosvc.Views(c.Request().Context(), id)
	view.Views = &views
	s.attachVideoTags(c.Request().Context(), &view, id)
	view.HLSURL, view.Renditions = s.hlsDetail(c, id)
	s.attachVideoIPFS(c.Request().Context(), &view, id, v.Privacy, v.State)
	return c.JSON(http.StatusOK, view)
}

// videoVisibleForDetail applies the ordinary read policy unless the caller is
// a moderator/admin, in which case any existing local video's metadata is
// returned for the Manage workflow. It is deliberately used only by
// handleGetVideo; sharing it with media/playback helpers would widen access to
// the underlying files.
func (s *Server) videoVisibleForDetail(c echo.Context, videoID uuid.UUID) (sqlcgen.GetVideoByIDRow, error) {
	_, role, ok := principalFromContext(c)
	if !ok || (role != "admin" && role != "moderator") {
		return s.videoVisibleForRead(c, videoID)
	}
	v, err := s.videosvc.GetByID(c.Request().Context(), videoID)
	if err != nil {
		return sqlcgen.GetVideoByIDRow{}, videoError(err)
	}
	return v, nil
}

// videoListResponse wraps a list of videos.
type videoListResponse struct {
	Videos []videoView `json:"videos"`
}

// videoFeedResponse is the paginated cross-channel public feed. Scope is set on
// the public feed only ("local" or "all", remote-content §4); the
// subscriptions feed reuses this shape without it.
type videoFeedResponse struct {
	Videos []videoView `json:"videos"`
	Sort   string      `json:"sort"`
	Scope  string      `json:"scope,omitempty"`
	Limit  int         `json:"limit"`
	Offset int         `json:"offset"`
}

const (
	defaultVideoFeedLimit = 20
	maxVideoFeedLimit     = 100
)

// feedItemView projects a feed item, including its discovery-card data (view
// count and poster availability). Remote cards (remote-content §3-4) carry
// remote/domain/watch_url/stream_url and omit the local-only channel_id/
// privacy/state (zero values, dropped by omitempty).
func feedItemView(it video.FeedItem) videoView {
	v := newVideoView(it.Video)
	// Card queries do not select the per-video publish policies; drop the
	// zero-valued fields newVideoView stamped so cards never claim a policy.
	v.CommentsPolicy = ""
	v.DownloadEnabled = nil
	views := it.Views
	v.Views = &views
	has := it.HasThumbnail
	v.HasThumbnail = &has
	handle := it.ChannelHandle
	v.ChannelHandle = &handle
	name := it.ChannelDisplayName
	v.ChannelDisplayName = &name
	if it.AuthorDisplayName != "" {
		author := it.AuthorDisplayName
		v.AuthorDisplayName = &author
	}
	v.DurationSeconds = it.DurationSeconds
	if it.PublishAt != nil {
		v.PublishAt = it.PublishAt // owner (studio) list: scheduled badge
	}
	if it.Remote {
		v.Remote = true
		v.ChannelID = "" // no local channel; omitted from the JSON
		v.Domain = it.Domain
		v.WatchURL = it.WatchURL
		v.StreamURL = it.StreamURL
	}
	return v
}

// handleListPublicVideos returns the public cross-channel feed. No auth
// required. Ordered by ?sort (recent|popular|trending, default recent; unknown
// values fall back to recent). ?scope=local|all (default local; unknown values
// fall back to local): "all" adds federated remote videos to the feed for
// discovery (remote-content §4), flagged remote with origin domain +
// watch/stream URLs; remote cards respect the admin instance blocklist and a
// signed-in viewer's instance mutes, and drop out whenever a
// tag/category/language filter is active (remote videos carry no local
// taxonomy). Optional filters: ?tag (free-form tag, matched case-insensitively
// against the stored lowercased set), ?category and ?language (taxonomy ids
// from GET /videos/config; unknown values are 422 — the frontend
// filter-controls contract). Each item carries its view count and whether a
// poster image exists. Pagination via ?limit (1–100, default 20) and ?offset
// (>=0).
func (s *Server) handleListPublicVideos(c echo.Context) error {
	sort := video.NormalizeFeedSort(c.QueryParam("sort"))
	scope := video.NormalizeFeedScope(c.QueryParam("scope"))
	limit := clampInt(queryInt(c, "limit", defaultVideoFeedLimit), 1, maxVideoFeedLimit)
	offset := queryInt(c, "offset", 0)
	if offset < 0 {
		offset = 0
	}
	filter := video.FeedFilter{
		Tag:      strings.TrimSpace(c.QueryParam("tag")),
		Category: strings.TrimSpace(c.QueryParam("category")),
		Language: strings.TrimSpace(c.QueryParam("language")),
		// Sensitive-content policy "hide" is enforced server-side on the public
		// discovery surfaces only (instance-platform-info).
		HideSensitive: s.hideSensitiveVideos(),
	}
	var fes []FieldError
	if len(filter.Tag) > video.MaxTagLen {
		fes = append(fes, FieldError{Field: "tag", Message: "must be at most 50 characters"})
	}
	if filter.Category != "" && !video.IsCategory(filter.Category) {
		fes = append(fes, FieldError{Field: "category", Message: "unknown category"})
	}
	if filter.Language != "" && !video.IsLanguage(filter.Language) {
		fes = append(fes, FieldError{Field: "language", Message: "unknown language"})
	}
	if len(fes) > 0 {
		return &ValidationError{Fields: fes}
	}
	viewerID, _, authed := principalFromContext(c)
	items, err := s.videosvc.ListPublic(c.Request().Context(), sort, scope, filter, viewerID, authed, int32(limit), int32(offset))
	if err != nil {
		return err
	}
	views := make([]videoView, 0, len(items))
	for _, it := range items {
		views = append(views, feedItemView(it))
	}
	s.attachIPFSPinned(c.Request().Context(), views)
	return c.JSON(http.StatusOK, videoFeedResponse{Videos: views, Sort: sort, Scope: scope, Limit: limit, Offset: offset})
}

// handleListSubscriptionVideos returns the authenticated user's "subscriptions"
// feed: public, published videos from the channels they follow, newest first,
// with discovery-card data. Pagination via ?limit (1–100, default 20) and ?offset.
func (s *Server) handleListSubscriptionVideos(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	limit := clampInt(queryInt(c, "limit", defaultVideoFeedLimit), 1, maxVideoFeedLimit)
	offset := queryInt(c, "offset", 0)
	if offset < 0 {
		offset = 0
	}
	items, err := s.videosvc.ListSubscriptions(c.Request().Context(), userID, int32(limit), int32(offset))
	if err != nil {
		return err
	}
	views := make([]videoView, 0, len(items))
	for _, it := range items {
		views = append(views, feedItemView(it))
	}
	s.attachIPFSPinned(c.Request().Context(), views)
	return c.JSON(http.StatusOK, videoFeedResponse{Videos: views, Sort: "recent", Limit: limit, Offset: offset})
}

// maxSearchQueryLen bounds the search term to keep queries cheap.
const maxSearchQueryLen = 100

// videoSearchResponse is the paginated result of a public title search. Remote
// carries the typed remote-URI search hits (config-parity W13) resolved from a
// URI/handle-shaped query — present only on the first page (offset 0), when
// the caller's auth-state gate allows resolution AND something resolved;
// omitted otherwise (unresolvable/timeout degrades silently to local-only).
type videoSearchResponse struct {
	Query  string                   `json:"query"`
	Videos []videoView              `json:"videos"`
	Remote []remoteSearchResultView `json:"remote,omitempty"`
	Limit  int                      `json:"limit"`
	Offset int                      `json:"offset"`
}

// handleSearchVideos searches public video titles. No auth required. Requires a
// non-empty ?q (<=100 chars); paginated via ?limit (1–100, default 20)/?offset.
// Optional facet filters mirror the feed: ?tag (free-form, matched
// case-insensitively), ?category and ?language (taxonomy ids from GET
// /videos/config; unknown values are 422). Any active filter excludes remote
// results.
func (s *Server) handleSearchVideos(c echo.Context) error {
	q := strings.TrimSpace(c.QueryParam("q"))
	if q == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "query parameter q is required")
	}
	if len(q) > maxSearchQueryLen {
		return echo.NewHTTPError(http.StatusBadRequest, "query parameter q is too long")
	}
	limit := clampInt(queryInt(c, "limit", defaultVideoFeedLimit), 1, maxVideoFeedLimit)
	offset := queryInt(c, "offset", 0)
	if offset < 0 {
		offset = 0
	}
	filter := video.FeedFilter{
		Tag:      strings.TrimSpace(c.QueryParam("tag")),
		Category: strings.TrimSpace(c.QueryParam("category")),
		Language: strings.TrimSpace(c.QueryParam("language")),
		// Sensitive-content policy "hide" is enforced server-side on the public
		// discovery surfaces only (instance-platform-info).
		HideSensitive: s.hideSensitiveVideos(),
	}
	var fes []FieldError
	if len(filter.Tag) > video.MaxTagLen {
		fes = append(fes, FieldError{Field: "tag", Message: "must be at most 50 characters"})
	}
	if filter.Category != "" && !video.IsCategory(filter.Category) {
		fes = append(fes, FieldError{Field: "category", Message: "unknown category"})
	}
	if filter.Language != "" && !video.IsLanguage(filter.Language) {
		fes = append(fes, FieldError{Field: "language", Message: "unknown language"})
	}
	if len(fes) > 0 {
		return &ValidationError{Fields: fes}
	}
	viewerID, _, authed := principalFromContext(c)
	// Remote-URI search (config-parity W13): a URI/handle-shaped first-page
	// query kicks off remote resolution CONCURRENTLY with the local search,
	// under its own strict deadline (see search_remote.go). Later pages never
	// re-resolve (the remote hit rode page one).
	var remoteCh <-chan []remoteSearchResultView
	if offset == 0 {
		remoteCh = s.startRemoteSearch(c, q)
	}
	items, err := s.videosvc.SearchPublic(c.Request().Context(), q, filter, viewerID, authed, int32(limit), int32(offset))
	if err != nil {
		return err
	}
	views := make([]videoView, 0, len(items))
	for _, it := range items {
		views = append(views, feedItemView(it))
	}
	s.attachIPFSPinned(c.Request().Context(), views)
	resp := videoSearchResponse{Query: q, Videos: views, Limit: limit, Offset: offset}
	if remoteCh != nil {
		resp.Remote = <-remoteCh // always delivers within the resolve deadline
	}
	return c.JSON(http.StatusOK, resp)
}

// queryInt reads an integer query param, returning def when absent or malformed.
func queryInt(c echo.Context, name string, def int) int {
	raw := c.QueryParam(name)
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	return n
}

// clampInt bounds v to [lo, hi].
func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// handleListChannelVideos lists a channel's videos. Behind optionalAuth: the
// channel owner sees all of their videos; everyone else sees only public ones.
func (s *Server) handleListChannelVideos(c echo.Context) error {
	ctx := c.Request().Context()
	ch, err := s.channelsvc.GetByHandle(ctx, c.Param("handle"))
	if err != nil {
		return channelError(err) // ErrNotFound -> 404
	}

	var items []video.FeedItem
	if userID, _, ok := principalFromContext(c); ok && userID == ch.OwnerID {
		items, err = s.videosvc.ListByChannel(ctx, ch.ID)
	} else {
		items, err = s.videosvc.ListPublicByChannel(ctx, ch.ID, s.hideSensitiveVideos())
	}
	if err != nil {
		return err
	}
	views := make([]videoView, 0, len(items))
	for _, it := range items {
		views = append(views, feedItemView(it))
	}
	s.attachIPFSPinned(c.Request().Context(), views)
	return c.JSON(http.StatusOK, videoListResponse{Videos: views})
}

// updateVideoRequest is the PATCH /api/v1/videos/{id} body. Fields are optional;
// only those present are changed.
type updateVideoRequest struct {
	Title       *string    `json:"title"`
	Description *string    `json:"description"`
	Privacy     *string    `json:"privacy"`
	Category    *string    `json:"category"`
	Language    *string    `json:"language"`
	License     *string    `json:"license"`
	Tags        *[]string  `json:"tags"`
	PublishAt   *time.Time `json:"publish_at"`
	IsSensitive *bool      `json:"is_sensitive"`
	// CommentsPolicy / DownloadEnabled are the per-video publish policies
	// (config-parity W9); nil leaves each unchanged.
	CommentsPolicy  *string `json:"comments_policy"`
	DownloadEnabled *bool   `json:"download_enabled"`
}

func (r updateVideoRequest) Validate() []FieldError {
	if r.Title == nil && r.Description == nil && r.Privacy == nil &&
		r.Category == nil && r.Language == nil && r.License == nil && r.Tags == nil &&
		r.PublishAt == nil && r.IsSensitive == nil &&
		r.CommentsPolicy == nil && r.DownloadEnabled == nil {
		return []FieldError{{Field: "title", Message: "at least one updatable field is required"}}
	}
	var fes []FieldError
	if r.Title != nil {
		switch n := len(strings.TrimSpace(*r.Title)); {
		case n == 0:
			fes = append(fes, FieldError{Field: "title", Message: "must not be blank"})
		case n > 200:
			fes = append(fes, FieldError{Field: "title", Message: "must be at most 200 characters"})
		}
	}
	if r.Description != nil && len(*r.Description) > 5000 {
		fes = append(fes, FieldError{Field: "description", Message: "must be at most 5000 characters"})
	}
	if r.Privacy != nil && !validVideoPrivacy[*r.Privacy] {
		fes = append(fes, FieldError{Field: "privacy", Message: "must be one of public, unlisted, private, password"})
	}
	if r.CommentsPolicy != nil && !video.IsCommentsPolicy(*r.CommentsPolicy) {
		fes = append(fes, FieldError{Field: "comments_policy", Message: "must be one of enabled, disabled"})
	}
	// A provided taxonomy field must be a known, non-empty id (clearing to unset
	// is not supported via update).
	fes = append(fes, validateTaxonomy(derefOr(r.Category), derefOr(r.Language), derefOr(r.License))...)
	if r.Category != nil && *r.Category == "" {
		fes = append(fes, FieldError{Field: "category", Message: "unknown category"})
	}
	if r.Language != nil && *r.Language == "" {
		fes = append(fes, FieldError{Field: "language", Message: "unknown language"})
	}
	if r.License != nil && *r.License == "" {
		fes = append(fes, FieldError{Field: "license", Message: "unknown license"})
	}
	if r.Tags != nil {
		fes = append(fes, validateTags(*r.Tags)...)
	}
	fes = append(fes, validatePublishAt(r.PublishAt)...)
	return fes
}

// derefOr returns the pointee or "" when nil (so validateTaxonomy skips absent
// fields while still validating present ones).
func derefOr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// handleUpdateVideo updates a video owned by the authenticated user. A
// moderator/admin may manage any local video.
func (s *Server) handleUpdateVideo(c echo.Context) error {
	userID, role, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	var in updateVideoRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	canManage := role == "admin" || role == "moderator"
	managedOther := false
	if canManage {
		if existing, lookupErr := s.videosvc.GetByID(c.Request().Context(), id); lookupErr == nil {
			managedOther = existing.OwnerID != userID
		}
	}
	v, err := s.videosvc.UpdateForActor(c.Request().Context(), userID, id, video.UpdateInput{
		Title:           in.Title,
		Description:     in.Description,
		Privacy:         in.Privacy,
		Category:        in.Category,
		Language:        in.Language,
		License:         in.License,
		Tags:            in.Tags,
		PublishAt:       in.PublishAt,
		IsSensitive:     in.IsSensitive,
		CommentsPolicy:  in.CommentsPolicy,
		DownloadEnabled: in.DownloadEnabled,
	}, canManage)
	if err != nil {
		if errors.Is(err, video.ErrPublished) {
			return &ValidationError{Fields: []FieldError{{Field: "publish_at", Message: "cannot be set after the video is published"}}}
		}
		return videoError(err)
	}
	// Re-flag the edited metadata against the watched-words list (best-effort;
	// an edit can newly introduce a flagged term — §12).
	s.flagVideoWatchedWords(c.Request().Context(), v.ID, v.Title, v.Description)
	if managedOther {
		s.audit(c, observability.ActionVideoUpdate, observability.ResultSuccess, userID.String(),
			"video="+id.String()+" fields="+strings.Join(updateVideoFieldNames(in), ","))
	}
	view := newVideoView(v)
	s.attachVideoTags(c.Request().Context(), &view, v.ID)
	return c.JSON(http.StatusOK, view)
}

// updateVideoFieldNames returns only the names present in a managed PATCH.
// Audit reasons must never include creator-authored values.
func updateVideoFieldNames(in updateVideoRequest) []string {
	fields := make([]string, 0, 9)
	if in.Title != nil {
		fields = append(fields, "title")
	}
	if in.Description != nil {
		fields = append(fields, "description")
	}
	if in.Privacy != nil {
		fields = append(fields, "privacy")
	}
	if in.Category != nil {
		fields = append(fields, "category")
	}
	if in.Language != nil {
		fields = append(fields, "language")
	}
	if in.License != nil {
		fields = append(fields, "license")
	}
	if in.Tags != nil {
		fields = append(fields, "tags")
	}
	if in.PublishAt != nil {
		fields = append(fields, "publish_at")
	}
	if in.IsSensitive != nil {
		fields = append(fields, "is_sensitive")
	}
	if in.CommentsPolicy != nil {
		fields = append(fields, "comments_policy")
	}
	if in.DownloadEnabled != nil {
		fields = append(fields, "download_enabled")
	}
	return fields
}

// handleDeleteVideo deletes a video owned by the authenticated user. A
// moderator/admin may delete any local video; the audit actor remains the
// authenticated caller, never the video's owner.
func (s *Server) handleDeleteVideo(c echo.Context) error {
	userID, role, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	canManage := role == "admin" || role == "moderator"
	if err := s.videosvc.DeleteForActor(c.Request().Context(), userID, id, canManage); err != nil {
		return videoError(err)
	}
	s.audit(c, observability.ActionVideoDelete, observability.ResultSuccess, userID.String(), id.String())
	return c.NoContent(http.StatusNoContent)
}

// videoFileView is the public projection of a stored video file. The storage
// key is internal and deliberately not exposed.
type videoFileView struct {
	ID           string    `json:"id"`
	Kind         string    `json:"kind"`
	ContentType  string    `json:"content_type"`
	OriginalName string    `json:"original_name"`
	SizeBytes    int64     `json:"size_bytes"`
	CreatedAt    time.Time `json:"created_at"`
}

func newVideoFileView(f sqlcgen.VideoFile) videoFileView {
	return videoFileView{
		ID:           f.ID.String(),
		Kind:         f.Kind,
		ContentType:  f.ContentType,
		OriginalName: f.OriginalName,
		SizeBytes:    f.SizeBytes,
		CreatedAt:    f.CreatedAt,
	}
}

// uploadVideoFileResponse is returned by the original-file upload: the video in
// its new (processing) state plus the stored file's metadata.
type uploadVideoFileResponse struct {
	Video videoView     `json:"video"`
	File  videoFileView `json:"file"`
}

// handleUploadVideoFile stores the original file for a video owned by the
// authenticated user (multipart form field "file") and moves the video to
// processing. Non-owner/unknown video → 404 (existence is not leaked). When a
// storage quota applies to the caller, an upload that would not fit is refused
// up front with 422 quota_exceeded — the multipart part is already buffered by
// the form parse, so its size is authoritative (not a client-declared header).
func (s *Server) handleUploadVideoFile(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	if !s.uploadsEnabled() {
		return &FeatureDisabledError{Feature: "uploads"}
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	fh, err := c.FormFile("file")
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, `multipart form field "file" is required`)
	}
	f, err := fh.Open()
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	ctx := c.Request().Context()
	// Ownership before the quota check so a non-owner still sees 404, then the
	// quota BEFORE storing anything. The usage counts the current original even
	// when this upload replaces it — simple and conservative.
	if v, gerr := s.videosvc.GetByID(ctx, id); gerr != nil || v.OwnerID != userID {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	if qerr := s.checkUploadQuotas(ctx, userID, fh.Size); qerr != nil {
		return qerr
	}
	_, file, err := s.videosvc.AttachOriginal(ctx, userID, id, video.UploadInput{
		Filename:    fh.Filename,
		ContentType: fh.Header.Get("Content-Type"),
		Reader:      f,
	})
	if err != nil {
		return videoError(err)
	}
	// Finalise synchronously: probe (if configured) and publish or fail. Real
	// transcoding will move this off the request path; for now it is immediate.
	v, err := s.videosvc.Process(ctx, id, file.StorageKey)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusCreated, uploadVideoFileResponse{
		Video: newVideoView(v),
		File:  newVideoFileView(file),
	})
}

// thumbnailFrameRequest is the application/json variant of POST
// /videos/{id}/thumbnail (UPLOAD-04 / W2.C5): pick the poster from an exact frame
// of the processed original. The multipart image-upload variant is unchanged.
// at_seconds is a pointer so a missing field is distinguishable from 0.
type thumbnailFrameRequest struct {
	AtSeconds *float64 `json:"at_seconds"`
}

// handleSetVideoThumbnail sets a video's poster (owner only, behind requireAuth;
// non-owner/unknown → 404). It dispatches on Content-Type: an application/json
// body {at_seconds} extracts that exact frame from the processed original
// server-side (frame-pick); otherwise a multipart form field "file" stores a
// creator-supplied image, replacing any previous or auto-generated thumbnail (a
// non-image extension → 415). The 8M global body limit bounds the upload.
func (s *Server) handleSetVideoThumbnail(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	ct := strings.ToLower(strings.TrimSpace(c.Request().Header.Get(echo.HeaderContentType)))
	if strings.HasPrefix(ct, echo.MIMEApplicationJSON) {
		return s.setThumbnailFromFrame(c, userID, id)
	}
	fh, err := c.FormFile("file")
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, `multipart form field "file" is required`)
	}
	f, err := fh.Open()
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	file, err := s.videosvc.SetThumbnail(c.Request().Context(), userID, id, video.UploadInput{
		Filename:    fh.Filename,
		ContentType: fh.Header.Get("Content-Type"),
		Reader:      f,
	})
	if err != nil {
		return videoError(err)
	}
	return c.JSON(http.StatusCreated, newVideoFileView(file))
}

// setThumbnailFromFrame handles the application/json variant of the thumbnail POST
// (W2.C5): it extracts the frame at at_seconds from the video's processed original
// and stores it as the poster. A missing/malformed body → 400; not owner/unknown
// → 404; no processed original yet → 409; at_seconds outside [0, duration) → 422;
// no server-side frame extractor → 503.
func (s *Server) setThumbnailFromFrame(c echo.Context, userID, id uuid.UUID) error {
	var in thumbnailFrameRequest
	if err := c.Bind(&in); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "malformed or invalid request body")
	}
	if in.AtSeconds == nil {
		return echo.NewHTTPError(http.StatusBadRequest, `"at_seconds" is required`)
	}
	file, err := s.videosvc.SetThumbnailFromFrame(c.Request().Context(), userID, id, *in.AtSeconds)
	if err != nil {
		return videoError(err)
	}
	return c.JSON(http.StatusCreated, newVideoFileView(file))
}

// handleStreamVideoOriginal serves a video's stored original file. Behind
// optionalAuth: ordinary playback visibility applies (public/unlisted to
// anyone, private only to the owner; otherwise 404). The manager-only metadata
// detail exception does not apply, and a video without a stored original is
// 404. Range requests are honoured for seeking when the backend exposes a
// filesystem path.
func (s *Server) handleStreamVideoOriginal(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	if _, err := s.videoVisibleForMedia(c, id); err != nil {
		return err
	}
	viewerID, _, authed := principalFromContext(c)
	f, err := s.videosvc.FileForView(c.Request().Context(), id, viewerID, authed, "original")
	if err != nil {
		return videoError(err)
	}
	return s.serveStoredObject(c, f.StorageKey, f.ContentType)
}

// handleGetVideoThumbnail serves a video's generated poster image under the
// ordinary media visibility policy; a video without a stored thumbnail is 404.
func (s *Server) handleGetVideoThumbnail(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	if _, err := s.videoVisibleForMedia(c, id); err != nil {
		return err
	}
	viewerID, _, authed := principalFromContext(c)
	f, err := s.videosvc.FileForView(c.Request().Context(), id, viewerID, authed, "thumbnail")
	if err != nil {
		return videoError(err)
	}
	return s.serveStoredObject(c, f.StorageKey, f.ContentType)
}

// serveStoredObject streams the object at key with the video routes' 404
// message. See serveStoredObjectNamed.
func (s *Server) serveStoredObject(c echo.Context, key, contentType string) error {
	return s.serveStoredObjectNamed(c, key, contentType, "video not found")
}

// serveStoredObjectNamed streams the object at key, reporting a missing object
// as a 404 with notFoundMsg (so avatar routes don't say "video not found").
// When the backend exposes a local path (storage.PathProvider) it uses
// http.ServeContent so Range, conditional, and 206 handling come for free.
// A backend without a path can still return a seekable reader from Open (the
// S3 backend does — seeks become ranged GETs), which gets the same
// http.ServeContent treatment; only a plain reader degrades to a full-body 200.
func (s *Server) serveStoredObjectNamed(c echo.Context, key, contentType, notFoundMsg string) error {
	if s.media == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "media storage not configured")
	}
	if contentType != "" {
		c.Response().Header().Set("Content-Type", contentType)
	}
	if pp, ok := s.media.(storage.PathProvider); ok {
		path, err := pp.Path(key)
		if err != nil {
			return echo.NewHTTPError(http.StatusNotFound, notFoundMsg)
		}
		file, err := os.Open(path)
		if err != nil {
			if os.IsNotExist(err) {
				return echo.NewHTTPError(http.StatusNotFound, notFoundMsg)
			}
			return err
		}
		defer func() { _ = file.Close() }()
		info, err := file.Stat()
		if err != nil {
			return err
		}
		http.ServeContent(c.Response(), c.Request(), info.Name(), info.ModTime(), file)
		return nil
	}
	rc, err := s.media.Open(c.Request().Context(), key)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, notFoundMsg)
		}
		return err
	}
	defer func() { _ = rc.Close() }()
	if rs, ok := rc.(io.ReadSeeker); ok {
		// Zero modtime suppresses Last-Modified/conditional handling; the name
		// is unused because Content-Type is already set above.
		http.ServeContent(c.Response(), c.Request(), "", time.Time{}, rs)
		return nil
	}
	c.Response().WriteHeader(http.StatusOK)
	_, err = io.Copy(c.Response(), rc)
	return err
}

// handleRecordVideoView records a view of a video (deduped per viewer per window
// when Redis is wired). Behind optionalAuth, it retains ordinary playback
// visibility. Always 204 on success — whether or not the view was newly counted.
func (s *Server) handleRecordVideoView(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	viewerID, _, authed := principalFromContext(c)
	if err := s.videosvc.RecordView(c.Request().Context(), id, viewerID, authed, viewerKey(c, viewerID, authed)); err != nil {
		return videoError(err)
	}
	return c.NoContent(http.StatusNoContent)
}

// viewerKey derives a stable, non-identifying key for the viewer: the user id
// when authenticated, else the client IP. It is hashed so raw IPs/ids are not
// used as Redis keys (PII minimisation).
func viewerKey(c echo.Context, viewerID uuid.UUID, authed bool) string {
	var raw string
	if authed {
		raw = "u:" + viewerID.String()
	} else {
		raw = "ip:" + c.RealIP()
	}
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// videoHiddenByBlock reports whether videoID is blocked and therefore hidden from
// this caller. A blocked video is hidden from everyone except moderators/admins
// (who may still view it, e.g. to confirm before unblocking). When no moderation
// service is wired (some tests), nothing is blocked.
func (s *Server) videoHiddenByBlock(c echo.Context, videoID uuid.UUID) (bool, error) {
	if s.moderationsvc == nil {
		return false, nil
	}
	blocked, err := s.moderationsvc.IsBlocked(c.Request().Context(), videoID)
	if err != nil || !blocked {
		return false, err
	}
	_, role, _ := principalFromContext(c)
	if role == "admin" || role == "moderator" {
		return false, nil
	}
	return true, nil
}

// quarantineHidesVideo reports whether a quarantined video is hidden from this
// caller. Owners and moderation staff may inspect it.
func quarantineHidesVideo(c echo.Context, state string, ownerID uuid.UUID) bool {
	if state != "quarantined" {
		return false
	}
	userID, role, ok := principalFromContext(c)
	return !ok || (userID != ownerID && role != "admin" && role != "moderator")
}

// scheduledHidesVideo keeps a scheduled video private until the publish
// sweeper flips it to published. Its owner may preview it meanwhile.
func scheduledHidesVideo(c echo.Context, state string, ownerID uuid.UUID) bool {
	if state != "scheduled" {
		return false
	}
	userID, _, ok := principalFromContext(c)
	return !ok || userID != ownerID
}

// videoHiddenFromViewer combines the moderation visibility rules the media/
// detail surfaces share: a blocked video is hidden (moderators excepted) and a
// quarantined one is hidden (owner + moderators excepted). An unknown id is not
// "hidden" — the caller's own lookup reports it as 404. It also returns the
// fetched video row so the caller can apply the password gate without a second
// GetByID; that row is the zero value when the video is block-hidden or unknown,
// cases in which the caller returns before ever consulting it.
func (s *Server) videoHiddenFromViewer(c echo.Context, videoID uuid.UUID) (sqlcgen.GetVideoByIDRow, bool, error) {
	if hidden, err := s.videoHiddenByBlock(c, videoID); err != nil || hidden {
		return sqlcgen.GetVideoByIDRow{}, hidden, err
	}
	v, err := s.videosvc.GetByID(c.Request().Context(), videoID)
	if err != nil {
		return sqlcgen.GetVideoByIDRow{}, false, nil
	}
	return v, quarantineHidesVideo(c, v.State, v.OwnerID), nil
}

// videoVisibleForRead resolves a video under the ordinary read/media policy: an
// unknown id → 404; a private video → owner only (else 404 so existence is not
// leaked); a blocked video → moderators only; quarantined → owner + moderators;
// scheduled → owner. GET /videos/{id} layers its metadata-only manager exception
// separately. On any failure this returns an *echo.HTTPError; otherwise it
// returns the joined video row.
func (s *Server) videoVisibleForRead(c echo.Context, videoID uuid.UUID) (sqlcgen.GetVideoByIDRow, error) {
	v, err := s.videoReadBase(c, videoID)
	if err != nil {
		return sqlcgen.GetVideoByIDRow{}, err
	}
	// A password-protected video is gated to owner/moderators or a valid playback
	// token; everyone else gets 401 password_required (CORE-17 / W1.C2). This is
	// the deliberate exception to the 404-for-invisible rule so the watch page can
	// render an unlock prompt.
	if err := s.passwordGate(c, v.ID, v.Privacy, v.OwnerID); err != nil {
		return sqlcgen.GetVideoByIDRow{}, err
	}
	return v, nil
}

// videoVisibleForMedia adds a publication-state gate to ordinary metadata
// visibility. Draft/processing/failed public rows may remain readable as
// metadata for compatibility, but their stored bytes must never be public by
// UUID. Owners and moderation staff may inspect non-published media.
func (s *Server) videoVisibleForMedia(c echo.Context, videoID uuid.UUID) (sqlcgen.GetVideoByIDRow, error) {
	v, err := s.videoVisibleForRead(c, videoID)
	if err != nil {
		return sqlcgen.GetVideoByIDRow{}, err
	}
	if v.State == "published" {
		return v, nil
	}
	userID, role, ok := principalFromContext(c)
	if ok && (userID == v.OwnerID || role == "admin" || role == "moderator") {
		return v, nil
	}
	return sqlcgen.GetVideoByIDRow{}, echo.NewHTTPError(http.StatusNotFound, "video not found")
}

// videoReadBase applies the base read visibility (unknown id → 404; private →
// owner only; blocked → moderators only; quarantined → owner + moderators;
// scheduled → owner) WITHOUT the password gate. GET /embed-privacy uses it so the
// embed page can read a password video's embed policy pre-unlock; every other
// read path wraps it via videoVisibleForRead (which adds the password gate).
func (s *Server) videoReadBase(c echo.Context, videoID uuid.UUID) (sqlcgen.GetVideoByIDRow, error) {
	v, err := s.videosvc.GetByID(c.Request().Context(), videoID)
	if err != nil {
		if errors.Is(err, video.ErrNotFound) {
			return sqlcgen.GetVideoByIDRow{}, echo.NewHTTPError(http.StatusNotFound, "video not found")
		}
		return sqlcgen.GetVideoByIDRow{}, err
	}
	if v.Privacy == "private" {
		userID, _, ok := principalFromContext(c)
		if !ok || userID != v.OwnerID {
			return sqlcgen.GetVideoByIDRow{}, echo.NewHTTPError(http.StatusNotFound, "video not found")
		}
	}
	if hidden, err := s.videoHiddenByBlock(c, videoID); err != nil {
		return sqlcgen.GetVideoByIDRow{}, err
	} else if hidden {
		return sqlcgen.GetVideoByIDRow{}, echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	if quarantineHidesVideo(c, v.State, v.OwnerID) {
		return sqlcgen.GetVideoByIDRow{}, echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	if scheduledHidesVideo(c, v.State, v.OwnerID) {
		return sqlcgen.GetVideoByIDRow{}, echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	return v, nil
}

// blockVideoRequest is the optional POST /admin/videos/{id}/block body; the reason
// is recorded for the audit trail (it may be empty).
type blockVideoRequest struct {
	Reason string `json:"reason"`
}

func (r blockVideoRequest) Validate() []FieldError {
	if len(r.Reason) > maxReportReasonLen {
		return []FieldError{{Field: "reason", Message: "must be at most 2000 characters"}}
	}
	return nil
}

// handleBlockVideo blocks a video so it disappears from public surfaces. Behind
// requireRole(admin, moderator). An unknown video is 404. Idempotent. Emits an
// audit event.
func (s *Server) handleBlockVideo(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	var in blockVideoRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	if err := s.moderationsvc.BlockVideo(c.Request().Context(), userID, id, strings.TrimSpace(in.Reason)); err != nil {
		if errors.Is(err, moderation.ErrVideoNotFound) {
			s.audit(c, observability.ActionVideoBlock, observability.ResultFailure, userID.String(), "not_found")
			return echo.NewHTTPError(http.StatusNotFound, "video not found")
		}
		return err
	}
	s.audit(c, observability.ActionVideoBlock, observability.ResultSuccess, userID.String(), "")
	return c.NoContent(http.StatusNoContent)
}

// handleUnblockVideo lifts a video's block. Behind requireRole(admin, moderator).
// Idempotent (unblocking a video that is not blocked still succeeds). Emits an
// audit event.
func (s *Server) handleUnblockVideo(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	if err := s.moderationsvc.UnblockVideo(c.Request().Context(), id); err != nil {
		return err
	}
	s.audit(c, observability.ActionVideoUnblock, observability.ResultSuccess, userID.String(), "")
	return c.NoContent(http.StatusNoContent)
}

// blockedVideoView is the moderation block-list projection of a blocked video.
type blockedVideoView struct {
	VideoID            string    `json:"video_id"`
	Title              string    `json:"title"`
	Privacy            string    `json:"privacy"`
	State              string    `json:"state"`
	ChannelHandle      string    `json:"channel_handle"`
	ChannelDisplayName string    `json:"channel_display_name"`
	Reason             string    `json:"reason"`
	BlockedBy          string    `json:"blocked_by,omitempty"`
	BlockedAt          time.Time `json:"blocked_at"`
}

// blockedVideoListResponse is the paginated moderation block-list.
type blockedVideoListResponse struct {
	Videos []blockedVideoView `json:"videos"`
	Limit  int                `json:"limit"`
	Offset int                `json:"offset"`
}

// handleListBlockedVideos returns currently-blocked videos (newest block first)
// for the moderation block-list. Behind requireRole(admin, moderator).
// Pagination via ?limit (1–100, default 20) and ?offset.
func (s *Server) handleListBlockedVideos(c echo.Context) error {
	limit := clampInt(queryInt(c, "limit", defaultVideoFeedLimit), 1, maxVideoFeedLimit)
	offset := queryInt(c, "offset", 0)
	if offset < 0 {
		offset = 0
	}
	items, err := s.moderationsvc.ListBlocked(c.Request().Context(), int32(limit), int32(offset))
	if err != nil {
		return err
	}
	views := make([]blockedVideoView, 0, len(items))
	for _, it := range items {
		views = append(views, blockedVideoView{
			VideoID:            it.VideoID.String(),
			Title:              it.Title,
			Privacy:            it.Privacy,
			State:              it.State,
			ChannelHandle:      it.ChannelHandle,
			ChannelDisplayName: it.ChannelDisplayName,
			Reason:             it.Reason,
			BlockedBy:          it.BlockedByUsername,
			BlockedAt:          it.BlockedAt,
		})
	}
	return c.JSON(http.StatusOK, blockedVideoListResponse{Videos: views, Limit: limit, Offset: offset})
}

// videoError maps video service sentinels to HTTP error envelopes. A non-owner
// sees 404 (not 403) so a private video's existence is not leaked; an owned but
// missing video is also 404.
func videoError(err error) error {
	switch {
	case errors.Is(err, video.ErrNotFound):
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	case errors.Is(err, video.ErrForbidden):
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	case errors.Is(err, video.ErrUnsupportedMedia):
		return echo.NewHTTPError(http.StatusUnsupportedMediaType, "unsupported media type")
	case errors.Is(err, video.ErrPasswordRequired):
		return echo.NewHTTPError(http.StatusBadRequest, "a password-protected video needs at least one password")
	case errors.Is(err, video.ErrLastPassword):
		return echo.NewHTTPError(http.StatusConflict, "cannot remove the last password of a password-protected video")
	case errors.Is(err, video.ErrPasswordNotFound):
		return echo.NewHTTPError(http.StatusNotFound, "password not found")
	case errors.Is(err, video.ErrNoProcessedOriginal):
		return echo.NewHTTPError(http.StatusConflict, "the video has no processed original yet")
	case errors.Is(err, video.ErrThumbnailOutOfRange):
		return echo.NewHTTPError(http.StatusUnprocessableEntity, "at_seconds is outside the video duration")
	case errors.Is(err, video.ErrThumbnailUnavailable):
		return echo.NewHTTPError(http.StatusServiceUnavailable, "server-side frame extraction is not available")
	default:
		return err
	}
}
