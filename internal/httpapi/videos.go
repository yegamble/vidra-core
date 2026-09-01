package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/delivery"
	"github.com/vidra/vidra-core/internal/ipfsmirror"
	"github.com/vidra/vidra-core/internal/moderation"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/pgconv"
	"github.com/vidra/vidra-core/internal/searchevents"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
	"github.com/vidra/vidra-core/internal/video"
)

// validVideoPrivacy is the allowed privacy set; empty defaults to "private".
// "password" (CORE-17) is accepted here, but the service additionally requires
// the video to already hold >=1 password (else 400 password_required-style).
var validVideoPrivacy = map[string]bool{"public": true, "unlisted": true, "private": true, "password": true}

// maxSensitiveReasonLen caps the creator's content-warning text (§sensitive
// content), measured on the trimmed value.
const maxSensitiveReasonLen = 280

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
	// SensitiveReason is the creator's optional content-warning text (trimmed,
	// capped at maxSensitiveReasonLen). Storable regardless of IsSensitive — the
	// frontend pairs them.
	SensitiveReason string `json:"sensitive_reason"`
	// CommentsPolicy is the per-video comment policy (config-parity W9):
	// enabled|disabled. Omitted/empty seeds the instance's
	// default_comment_policy setting.
	CommentsPolicy string `json:"comments_policy"`
	// DownloadEnabled is the per-video download policy (config-parity W9),
	// layered on the instance downloads_enabled gate. Omitted (null) seeds the
	// instance's default_download_enabled setting.
	DownloadEnabled *bool `json:"download_enabled"`
	// PublishAfterTranscode opts the video into the publish-after-transcode hold:
	// once processed it stays hidden from every public surface until its HLS
	// transcode completes (default false = publish while transcoding).
	PublishAfterTranscode bool `json:"publish_after_transcode"`
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
	if len(strings.TrimSpace(r.SensitiveReason)) > maxSensitiveReasonLen {
		fes = append(fes, FieldError{Field: "sensitive_reason", Message: "must be at most 280 characters"})
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
	IsSensitive bool `json:"is_sensitive"`
	// SensitiveReason is the creator's optional content-warning text, paired with
	// IsSensitive by the frontend. Always emitted (empty string when unset or on
	// remote cards).
	SensitiveReason string    `json:"sensitive_reason"`
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
	// PublishAfterTranscode is the per-video publish-timing opt-in (owner-facing),
	// present on the create/update/detail views. When true a processed video stays
	// hidden (state 'transcoding') until its HLS transcode completes.
	PublishAfterTranscode *bool `json:"publish_after_transcode,omitempty"`
	// Transcoding is set on the DETAIL view only: true while a transcode job is
	// still live for this video (the frontend's "still processing" signal under
	// the player). It becomes false once the last job finishes — at which point
	// hls_url appears. Omitted (absent = false) elsewhere.
	Transcoding bool `json:"transcoding,omitempty"`
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
	// OriginallyPublishedAt is when the video was first published ELSEWHERE
	// (migration 0119) — a PeerTube import's originallyPublishedAt, or a date the
	// creator set by hand. Omitted when unset, which is the case for everything
	// first published on this instance; created_at remains the only date for
	// those. Set on the detail and create/update views; the list/feed cards do
	// not carry it.
	OriginallyPublishedAt *time.Time `json:"originally_published_at,omitempty"`
	// HLSURL is the master-playlist path for HLS playback, set on the detail
	// endpoint only once the transcoded playlist is ready (omitted otherwise).
	// Renditions lists the available ladder rungs alongside it.
	HLSURL     *string         `json:"hls_url,omitempty"`
	Renditions []renditionView `json:"renditions,omitempty"`
	// PackagingFormat and DASHURL are the same format discovery the playback
	// session carries, on the response a client reads FIRST. Without them a CMAF
	// video and an MPEG-TS video are indistinguishable — both serve HLS from
	// hls_url — so nothing can choose the DASH manifest that has been shipped
	// and unconsumed since phase 3. Set alongside hls_url; dash_url only for a
	// CMAF tree, and deliberately unversioned (see playbackTree).
	PackagingFormat string  `json:"packaging_format,omitempty"`
	DASHURL         *string `json:"dash_url,omitempty"`
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
	publishAfterTranscode := v.PublishAfterTranscode
	return videoView{
		ID:                    v.ID.String(),
		ChannelID:             v.ChannelID.String(),
		Title:                 v.Title,
		Description:           v.Description,
		Privacy:               v.Privacy,
		State:                 v.State,
		IsSensitive:           v.IsSensitive,
		SensitiveReason:       v.SensitiveReason,
		CreatedAt:             v.CreatedAt,
		Category:              v.Category,
		Language:              v.Language,
		License:               v.License,
		PublishAt:             video.TimePtr(v.PublishAt),
		OriginallyPublishedAt: video.TimePtr(v.OriginallyPublishedAt),
		CommentsPolicy:        v.CommentsPolicy,
		DownloadEnabled:       &downloadEnabled,
		PublishAfterTranscode: &publishAfterTranscode,
	}
}

func videoViewFromRow(v sqlcgen.GetVideoByIDRow) videoView {
	handle, name, author := v.ChannelHandle, v.ChannelDisplayName, v.AuthorDisplayName
	downloadEnabled := v.DownloadEnabled
	publishAfterTranscode := v.PublishAfterTranscode
	view := videoView{
		ID:              v.ID.String(),
		ChannelID:       v.ChannelID.String(),
		Title:           v.Title,
		Description:     v.Description,
		Privacy:         v.Privacy,
		State:           v.State,
		IsSensitive:     v.IsSensitive,
		SensitiveReason: v.SensitiveReason,
		CreatedAt:       v.CreatedAt,
		Category:        v.Category,
		Language:        v.Language,
		License:         v.License,
		PublishAt:       video.TimePtr(v.PublishAt),
		// Detail carries the owning channel so the frontend related-rail can link
		// and label without a second request (Wave A contract gap).
		ChannelHandle:         &handle,
		ChannelDisplayName:    &name,
		OriginallyPublishedAt: video.TimePtr(v.OriginallyPublishedAt),
		CommentsPolicy:        v.CommentsPolicy,
		DownloadEnabled:       &downloadEnabled,
		PublishAfterTranscode: &publishAfterTranscode,
	}
	if author != "" {
		view.AuthorDisplayName = &author
	}
	return view
}

// handleCreateVideo creates a draft video under a channel owned by the caller.
func (s *Server) handleCreateVideo(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
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
	// Owner OR an editor collaborator may publish to the channel (migration 0097).
	if !s.canManageChannelContent(ctx, userID, ch.ID) {
		return echo.NewHTTPError(http.StatusForbidden, "you do not manage this channel")
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
		SensitiveReason: in.SensitiveReason,
		CommentsPolicy:  in.CommentsPolicy,
		DownloadEnabled: in.DownloadEnabled,

		PublishAfterTranscode: in.PublishAfterTranscode,
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
	id, err := pathUUID(c, "id", "video not found")
	if err != nil {
		return err
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
	if tree, ok := s.hlsDetail(c, id); ok {
		view.HLSURL = &tree.hlsURL
		view.Renditions = tree.renditions
		view.PackagingFormat = tree.format
		if tree.dashURL != "" {
			view.DASHURL = &tree.dashURL
		}
	}
	// "still processing" signal: true while any transcode job is live for the
	// video. Flips to false once the last job finishes (hls_url then appears).
	if s.transcodesvc != nil {
		view.Transcoding = s.transcodesvc.HasLiveJob(c.Request().Context(), id)
	}
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
	if !ok || !isStaff(role) {
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
	pageMeta
}

// videoFeedResponse is the paginated cross-channel public feed. Scope is set on
// the public feed only ("local" or "all", remote-content §4); the
// subscriptions feed reuses this shape without it.
type videoFeedResponse struct {
	Videos []videoView `json:"videos"`
	Sort   string      `json:"sort"`
	Scope  string      `json:"scope,omitempty"`
	pageMeta
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
	page := parsePage(c, defaultVideoFeedLimit, maxVideoFeedLimit)
	filter := video.FeedFilter{
		Tag:      strings.TrimSpace(c.QueryParam("tag")),
		Category: strings.TrimSpace(c.QueryParam("category")),
		Language: strings.TrimSpace(c.QueryParam("language")),
		// Sensitive-content policy "hide" is enforced server-side on the public
		// discovery surfaces only (instance-platform-info), now per-viewer (0100):
		// a signed-in caller's override wins, else the instance policy.
		HideSensitive: s.effectiveHideSensitive(c),
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
	items, total, err := s.videosvc.ListPublic(c.Request().Context(), sort, scope, filter, viewerID, authed, page.Limit32(), page.Offset32())
	if err != nil {
		return err
	}
	views := make([]videoView, 0, len(items))
	for _, it := range items {
		views = append(views, feedItemView(it))
	}
	s.attachIPFSPinned(c.Request().Context(), views)
	return c.JSON(http.StatusOK, videoFeedResponse{Videos: views, Sort: sort, Scope: scope, pageMeta: page.meta(total)})
}

// handleListSubscriptionVideos returns the authenticated user's "subscriptions"
// feed: public, published videos from the channels they follow, newest first,
// with discovery-card data. Pagination via ?limit (1–100, default 20) and ?offset.
func (s *Server) handleListSubscriptionVideos(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	page := parsePage(c, defaultVideoFeedLimit, maxVideoFeedLimit)
	items, total, err := s.videosvc.ListSubscriptions(c.Request().Context(), userID, page.Limit32(), page.Offset32())
	if err != nil {
		return err
	}
	views := make([]videoView, 0, len(items))
	for _, it := range items {
		views = append(views, feedItemView(it))
	}
	s.attachIPFSPinned(c.Request().Context(), views)
	return c.JSON(http.StatusOK, videoFeedResponse{Videos: views, Sort: "recent", pageMeta: page.meta(total)})
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
	pageMeta
	// SearchTotal / TotalIsLowerBound / HasMore are vidra-search's own view of
	// the result set, passed through when it served the request. All three are
	// POINTERS and omitted when absent, because absent has to mean "unknown":
	// they are omitted on the local SQL path, and they are omitted by a deployed
	// search service released before it grew the fields. A bare `false` for
	// has_more would tell a client it had reached the end of a list it had
	// barely started.
	//
	// SearchTotal is NOT the top-level `total`. `total` stays core's own
	// per-viewer count — the number of matches this caller could actually be
	// shown, which is what a "N results" label means. SearchTotal is the size of
	// the set the RANKER worked over, and TotalIsLowerBound says whether the
	// ranker saw all of it: true means the service stopped looking, so paging
	// will run out before `total` is reached and the count should be rendered
	// "top N" rather than "N". HasMore is the direct answer to "is there another
	// page" and is exact whenever present.
	SearchTotal       *int64 `json:"search_total,omitempty"`
	TotalIsLowerBound *bool  `json:"total_is_lower_bound,omitempty"`
	HasMore           *bool  `json:"has_more,omitempty"`
}

// handleSearchVideos searches public video titles. No auth required. Requires a
// non-empty ?q (<=100 chars); paginated via ?limit (1–100, default 20)/?offset.
//
// Ordering: ?sort is one of video.SearchSorts() — relevance (the default and the
// endpoint's behaviour before it took a sort at all), -published_at/published_at,
// -views/views. An unrecognised value is a 400, never a silent fallback.
//
// Optional facet filters mirror the feed: ?tag (free-form, matched
// case-insensitively), ?category, ?language and ?license (taxonomy ids from GET
// /videos/config; unknown values are 422). Any active taxonomy filter excludes
// remote results. On top of those, the search panel's own narrowing filters:
// ?duration_min/?duration_max (seconds, inclusive), ?published_after/
// ?published_before (RFC3339, inclusive), and ?tags_all_of/?tags_one_of (CSV).
//
// ROUTING (see searchServiceCanRank): the vidra-search path is taken only for
// the default relevance sort with none of the new filters active. Everything
// else runs on local SQL — on BOTH sides of the healthy/unhealthy line, so the
// result set does not change with the search service's mood.
func (s *Server) handleSearchVideos(c echo.Context) error {
	q := strings.TrimSpace(c.QueryParam("q"))
	if q == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "query parameter q is required")
	}
	if len(q) > maxSearchQueryLen {
		return echo.NewHTTPError(http.StatusBadRequest, "query parameter q is too long")
	}
	page := parsePage(c, defaultVideoFeedLimit, maxVideoFeedLimit)
	sortKey, err := parseSortParam(c, video.SearchSorts(), video.SearchSortDefault)
	if err != nil {
		return err
	}
	filter := video.SearchFilter{FeedFilter: video.FeedFilter{
		Tag:      strings.TrimSpace(c.QueryParam("tag")),
		Category: strings.TrimSpace(c.QueryParam("category")),
		Language: strings.TrimSpace(c.QueryParam("language")),
		License:  strings.TrimSpace(c.QueryParam("license")),
		// Sensitive-content policy "hide" is enforced server-side on the public
		// discovery surfaces only (instance-platform-info), now per-viewer (0100):
		// a signed-in caller's override wins, else the instance policy.
		HideSensitive: s.effectiveHideSensitive(c),
	}}
	if filter.DurationMin, filter.DurationMax, err = parseInt32RangeParams(c, "duration_min", "duration_max"); err != nil {
		return err
	}
	if filter.PublishedAfter, filter.PublishedBefore, err = parseTimeRangeParams(c, "published_after", "published_before"); err != nil {
		return err
	}
	filter.TagsAllOf = parseCSVParam(c, "tags_all_of")
	filter.TagsOneOf = parseCSVParam(c, "tags_one_of")
	var fes []FieldError
	if len(filter.Tag) > video.MaxTagLen {
		fes = append(fes, FieldError{Field: "tag", Message: "must be at most 50 characters"})
	}
	// A slice, not a map: the field errors go out in request order, and ranging
	// a map would shuffle them between identical requests.
	for _, set := range []struct {
		field string
		tags  []string
	}{{"tags_all_of", filter.TagsAllOf}, {"tags_one_of", filter.TagsOneOf}} {
		for _, t := range set.tags {
			if len(t) > video.MaxTagLen {
				fes = append(fes, FieldError{Field: set.field, Message: "each tag must be at most 50 characters"})
				break
			}
		}
	}
	if filter.Category != "" && !video.IsCategory(filter.Category) {
		fes = append(fes, FieldError{Field: "category", Message: "unknown category"})
	}
	if filter.Language != "" && !video.IsLanguage(filter.Language) {
		fes = append(fes, FieldError{Field: "language", Message: "unknown language"})
	}
	if filter.License != "" && !video.IsLicense(filter.License) {
		fes = append(fes, FieldError{Field: "license", Message: "unknown license"})
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
	if page.Offset == 0 {
		remoteCh = s.startRemoteSearch(c, q)
	}
	// vidra-search routing (search-service W4/W9): when the service is the
	// authoritative path (wired AND the admin toggle on AND Healthy()), BOTH modes
	// route through it — it handles simple ranking too, and nothing else routes to
	// SQL. Admin-off or a prober/breaker-detected outage takes the local SQL backup
	// WITHOUT paying a per-request timeout; a per-request error/timeout while
	// healthy still falls back (ok == false) as the last-resort safety. The public
	// response contract is identical on every path.
	var views []videoView
	var total int64
	var svcPaging searchServicePaging
	source := "local"
	if s.useSearchService() && searchServiceCanRank(filter, sortKey) {
		svcViews, paging, ok := s.searchViaService(c, q, filter, page.Limit, page.Offset, viewerID, authed)
		if ok && serviceAnsweredNothingOnPageOne(svcViews, page.Offset) {
			// A healthy service that returned NOTHING for the first page is the
			// cold-index case, and it is not an error, so the error-only fallback
			// below never saw it. Taking the service branch here would set
			// source="search" and then overwrite total with core's OWN SQL count —
			// shipping `{"videos": [], "total": 13528}`, an empty grid under
			// "13,528 results" with pagination through nothing. Search is never a
			// hard dependency: fall through to SQL, which answers coherently
			// whether the corpus is empty (nothing, total 0) or merely unindexed.
			ok = false
		}
		if ok {
			views, svcPaging = svcViews, paging
			source = "search"
			// The total stays core's own count of matching visible videos even
			// now that the service reports one: the service's number is not
			// per-viewer (see CountSearchPublic). The service's view of the set
			// rides along separately, in search_total/total_is_lower_bound.
			if n, cerr := s.videosvc.CountSearchPublic(c.Request().Context(), q, filter, viewerID, authed); cerr == nil {
				total = n
			}
		}
	}
	if source == "local" {
		items, n, err := s.videosvc.SearchPublic(c.Request().Context(), q, filter, sortKey, viewerID, authed, page.Limit32(), page.Offset32())
		if err != nil {
			return err
		}
		total = n
		views = make([]videoView, 0, len(items))
		for _, it := range items {
			views = append(views, feedItemView(it))
		}
	}
	s.attachIPFSPinned(c.Request().Context(), views)
	// Additive: record the search as a behavioural event (async via the outbox).
	s.emitSearchSubmitted(c, q, len(views), source)
	resp := videoSearchResponse{
		Query: q, Videos: views, pageMeta: page.meta(total),
		SearchTotal:       svcPaging.Total,
		TotalIsLowerBound: svcPaging.TotalIsLowerBound,
		HasMore:           svcPaging.HasMore,
	}
	if remoteCh != nil {
		resp.Remote = <-remoteCh // always delivers within the resolve deadline
	}
	return c.JSON(http.StatusOK, resp)
}

// searchServiceCanRank reports whether vidra-search can HONESTLY serve this
// request. It is the mechanism that keeps the two backends consistent.
//
// The service's contract is a ranked, already-paged id list over a corpus IT
// filtered — it accepts tag/category/language/license and nothing else, and
// core's job is to hydrate the window it returns. That shape supports exactly one
// ordering, its own relevance, and exactly the filters it knows.
//
// So a request that asks for anything else must not go there:
//
//   - A non-relevance sort is a total order over the whole matching set. The
//     service returns the top-N by RELEVANCE; re-sorting that truncated slice by
//     date or views produces "the newest of the 200 most relevant", which is a
//     different and wrong answer, and one that would silently change the day the
//     corpus grew past the over-fetch.
//   - A duration, publish-window, or tag-set filter is unknown to the service.
//     Core could re-apply it after hydration, but the service would still have
//     ranked and paged over the UNFILTERED corpus, so a narrow filter would
//     return a near-empty page next to a total saying there were hundreds.
//
// Both cases therefore run on local SQL, where the sort and every predicate are
// real — and they do so whether or not the service is healthy, so the answer
// does not depend on its state. The default relevance search with no new filter
// keeps its existing routing exactly.
func searchServiceCanRank(filter video.SearchFilter, sort string) bool {
	return sort == video.SearchSortRelevance && !filter.Narrows()
}

// serviceAnsweredNothingOnPageOne reports the one degenerate answer the service
// path cannot ship: a first page with nothing on it.
//
// It is deliberately narrow. `total` staying core's per-viewer count while the
// page holds fewer rows than that is the DESIGN (the service ranks and pages;
// the count is core's), so this must not fire on any non-empty page. And past
// the end of the ranked list an empty page is the correct answer — "you have run
// out of results" — so it is scoped to offset 0, where an empty page can only
// mean the service knows nothing about a corpus core can still search itself.
// The query is always non-empty here: the handler 400s on a blank ?q.
//
// The cost of being wrong in this direction is one extra SQL search on a query
// that genuinely matches nothing, which returns the same empty list either way.
func serviceAnsweredNothingOnPageOne(views []videoView, offset int) bool {
	return offset == 0 && len(views) == 0
}

// handleListChannelVideos lists a channel's videos. Behind optionalAuth: the
// channel owner sees all of their videos; everyone else sees only public ones.
//
// BREAKING: this is now paginated (?limit 1–100 default 20, ?offset) and returns
// a total. It previously returned EVERY video in the channel, so a channel with
// 50k videos serialised 50k rows on every page load. ?sort accepts
// published_at (oldest first) or -published_at (newest first, the default and
// the previous fixed behaviour) — the Latest/Oldest chips the channel page
// already shows.
func (s *Server) handleListChannelVideos(c echo.Context) error {
	ctx := c.Request().Context()
	ch, err := s.channelsvc.GetByHandle(ctx, c.Param("handle"))
	if err != nil {
		return channelError(err) // ErrNotFound -> 404
	}

	page := parsePage(c, defaultVideoFeedLimit, maxVideoFeedLimit)
	sort := video.NormalizeChannelSort(c.QueryParam("sort"))
	var items []video.FeedItem
	var total int64
	// The owner and editor collaborators (migration 0097) see the full list
	// (drafts, scheduled, private); everyone else sees only public videos.
	if userID, _, ok := principalFromContext(c); ok && s.canManageChannelContent(ctx, userID, ch.ID) {
		items, total, err = s.videosvc.ListByChannel(ctx, ch.ID, sort, page.Limit32(), page.Offset32())
	} else {
		items, total, err = s.videosvc.ListPublicByChannel(ctx, ch.ID, s.effectiveHideSensitive(c), sort, page.Limit32(), page.Offset32())
	}
	if err != nil {
		return err
	}
	views := make([]videoView, 0, len(items))
	for _, it := range items {
		views = append(views, feedItemView(it))
	}
	s.attachIPFSPinned(c.Request().Context(), views)
	return c.JSON(http.StatusOK, videoListResponse{Videos: views, pageMeta: page.meta(total)})
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
	// OriginallyPublishedAt: when the video was first published elsewhere; nil
	// leaves it unchanged. Unlike PublishAt it carries no schedule and is not
	// range-checked — a date in the past is the normal case, and a source that
	// records a wrong one is not something this endpoint can adjudicate.
	OriginallyPublishedAt *time.Time `json:"originally_published_at"`
	IsSensitive           *bool      `json:"is_sensitive"`
	// SensitiveReason: nil leaves the content-warning text unchanged; a non-nil
	// value sets it (an empty string clears it). Trimmed, capped at
	// maxSensitiveReasonLen.
	SensitiveReason *string `json:"sensitive_reason"`
	// CommentsPolicy / DownloadEnabled are the per-video publish policies
	// (config-parity W9); nil leaves each unchanged.
	CommentsPolicy  *string `json:"comments_policy"`
	DownloadEnabled *bool   `json:"download_enabled"`
	// PublishAfterTranscode: nil leaves the flag unchanged; a non-nil value sets
	// it. Setting it true on an already-published, not-yet-public video whose
	// transcode is still running holds the video until that transcode completes.
	PublishAfterTranscode *bool `json:"publish_after_transcode"`
}

func (r updateVideoRequest) Validate() []FieldError {
	if r.Title == nil && r.Description == nil && r.Privacy == nil &&
		r.Category == nil && r.Language == nil && r.License == nil && r.Tags == nil &&
		r.PublishAt == nil && r.OriginallyPublishedAt == nil &&
		r.IsSensitive == nil && r.SensitiveReason == nil &&
		r.CommentsPolicy == nil && r.DownloadEnabled == nil && r.PublishAfterTranscode == nil {
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
	if r.SensitiveReason != nil && len(strings.TrimSpace(*r.SensitiveReason)) > maxSensitiveReasonLen {
		fes = append(fes, FieldError{Field: "sensitive_reason", Message: "must be at most 280 characters"})
	}
	// A provided taxonomy field must be a known, non-empty id (clearing to unset
	// is not supported via update).
	fes = append(fes, validateTaxonomy(pgconv.Deref(r.Category), pgconv.Deref(r.Language), pgconv.Deref(r.License))...)
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

// handleUpdateVideo updates a video owned by the authenticated user. A
// moderator/admin may manage any local video.
func (s *Server) handleUpdateVideo(c echo.Context) error {
	userID, role, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	id, err := pathUUID(c, "id", "video not found")
	if err != nil {
		return err
	}
	var in updateVideoRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	// Staff (admin/moderator) manage any local video via the moderation escape;
	// an editor collaborator (migration 0097) manages their channel's videos.
	// managedOther gates the moderation audit — it stays staff-only, so an
	// editor's ordinary edit is not logged as a moderation action.
	staff := isStaff(role)
	canManage := staff
	managedOther := false
	if existing, lookupErr := s.videosvc.GetByID(c.Request().Context(), id); lookupErr == nil {
		if staff {
			managedOther = existing.OwnerID != userID
		} else if existing.OwnerID != userID {
			canManage = s.canManageChannelContent(c.Request().Context(), userID, existing.ChannelID)
		}
	}
	// Snapshot before the write: a video that has already left the Eligible
	// fence can no longer answer "what could an anonymous visitor have
	// fetched?", which is the question the edge's contents answer
	// (media_purge.go).
	//
	// Only for a PATCH that could move it out of public+published, because the
	// snapshot costs a handful of reads and the overwhelmingly common edit is a
	// title or a description. THE INVARIANT: any future field that can change
	// privacy or state belongs in this list. Missing one is a missed purge, not
	// a wrong one — the post-update check below is what decides.
	var purge edgePurgeSnapshot
	if in.Privacy != nil || in.PublishAt != nil || in.PublishAfterTranscode != nil {
		purge = s.videoEdgePurgeSnapshot(c.Request().Context(), id)
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
		SensitiveReason: in.SensitiveReason,
		CommentsPolicy:  in.CommentsPolicy,
		DownloadEnabled: in.DownloadEnabled,

		OriginallyPublishedAt: in.OriginallyPublishedAt,
		PublishAfterTranscode: in.PublishAfterTranscode,
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
	// The trigger is the LOSS of eligibility, not the PATCH. An ordinary title
	// edit must not cold-start the edge for the whole ladder; a flip to
	// private/unlisted — or out of `published` — is the moment every shared copy
	// became unauthorized.
	if !publicVideoForIPFS(v.Privacy, v.State) {
		s.purgeVideoEdgeCopies(c.Request().Context(), id, purge)
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
	if in.SensitiveReason != nil {
		fields = append(fields, "sensitive_reason")
	}
	if in.CommentsPolicy != nil {
		fields = append(fields, "comments_policy")
	}
	if in.DownloadEnabled != nil {
		fields = append(fields, "download_enabled")
	}
	if in.PublishAfterTranscode != nil {
		fields = append(fields, "publish_after_transcode")
	}
	return fields
}

// handleDeleteVideo deletes a video owned by the authenticated user. A
// moderator/admin may delete any local video; the audit actor remains the
// authenticated caller, never the video's owner.
func (s *Server) handleDeleteVideo(c echo.Context) error {
	userID, role, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	id, err := pathUUID(c, "id", "video not found")
	if err != nil {
		return err
	}
	// Staff delete any local video (moderation escape); an editor collaborator
	// (migration 0097) deletes their channel's videos.
	ctx := c.Request().Context()
	staff := isStaff(role)
	canManage := staff
	if !staff {
		if v, lookupErr := s.videosvc.GetByID(ctx, id); lookupErr == nil && v.OwnerID != userID {
			canManage = s.canManageChannelContent(ctx, userID, v.ChannelID)
		}
	}
	// The edge may still be holding this video's bytes, and deleting the video
	// deletes the rows that NAME them — so the key set is snapshotted here and
	// invalidated after the delete commits (media_purge.go).
	purge := s.videoEdgePurgeSnapshot(ctx, id)
	if err := s.videosvc.DeleteForActor(ctx, userID, id, canManage); err != nil {
		return videoError(err)
	}
	s.audit(c, observability.ActionVideoDelete, observability.ResultSuccess, userID.String(), id.String())
	s.purgeVideoEdgeCopies(ctx, id, purge)
	return c.NoContent(http.StatusNoContent)
}

// videoFileView is the public projection of a stored video file. The storage
// key is internal and deliberately not exposed, and so is the content hash
// (phase-2 storage, work item 2): it is an operational column for verified
// storage migration, it has states — empty, 'missing' — that mean nothing to a
// client, and the field list here is written out rather than embedded exactly so
// a new column cannot reach the wire by being added to the row.
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
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	if !s.uploadsEnabled() {
		return &FeatureDisabledError{Feature: "uploads"}
	}
	id, err := pathUUID(c, "id", "video not found")
	if err != nil {
		return err
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
	// Ownership before the quota check so a non-manager still sees 404, then the
	// quota BEFORE storing anything. The usage counts the current original even
	// when this upload replaces it — simple and conservative. An editor
	// collaborator (migration 0097) uploads AS the channel owner: the quota and
	// stored bytes count against the owner, which canManageVideo returns.
	mv, canManage := s.canManageVideo(ctx, userID, id)
	if !canManage {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	if qerr := s.checkUploadQuotas(ctx, mv.OwnerID, fh.Size); qerr != nil {
		return qerr
	}
	_, file, err := s.videosvc.AttachOriginal(ctx, mv.OwnerID, id, video.UploadInput{
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
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	id, err := pathUUID(c, "id", "video not found")
	if err != nil {
		return err
	}
	// Owner OR editor collaborator (migration 0097). Once authorized, the write
	// executes as the channel owner (v.OwnerID) — the id the owner-gated thumbnail
	// methods expect.
	v, canManage := s.canManageVideo(c.Request().Context(), userID, id)
	if !canManage {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	ct := strings.ToLower(strings.TrimSpace(c.Request().Header.Get(echo.HeaderContentType)))
	if strings.HasPrefix(ct, echo.MIMEApplicationJSON) {
		return s.setThumbnailFromFrame(c, v.OwnerID, id)
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

	file, err := s.videosvc.SetThumbnail(c.Request().Context(), v.OwnerID, id, video.UploadInput{
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
	id, err := pathUUID(c, "id", "video not found")
	if err != nil {
		return err
	}
	v, err := s.videoVisibleForMedia(c, id)
	if err != nil {
		return err
	}
	viewerID, _, authed := principalFromContext(c)
	f, err := s.videosvc.FileForView(c.Request().Context(), id, viewerID, authed, "original")
	if err != nil {
		return videoError(err)
	}
	return s.serveMediaAsset(c, mediaAsset{
		key:         f.StorageKey,
		contentType: f.ContentType,
		class:       delivery.ClassOriginal,
		eligible:    publicVideoForIPFS(v.Privacy, v.State),
		notFound:    "video not found",
	})
}

// handleGetVideoThumbnail serves a video's generated poster image under the
// ordinary media visibility policy; a video without a stored thumbnail is 404.
func (s *Server) handleGetVideoThumbnail(c echo.Context) error {
	id, err := pathUUID(c, "id", "video not found")
	if err != nil {
		return err
	}
	v, err := s.videoVisibleForMedia(c, id)
	if err != nil {
		return err
	}
	viewerID, _, authed := principalFromContext(c)
	f, err := s.videosvc.FileForView(c.Request().Context(), id, viewerID, authed, "thumbnail")
	if err != nil {
		return videoError(err)
	}
	// The public thumbnail URL is stable and owners can replace its bytes, so it
	// cannot honestly be immutable: the delivery policy gives it a short
	// browser-private reuse window, and never retains authenticated or
	// password-token media.
	return s.serveMediaAsset(c, mediaAsset{
		key:         f.StorageKey,
		contentType: f.ContentType,
		class:       delivery.ClassThumbnail,
		mirrorClass: ipfsmirror.ClassThumbnail,
		eligible:    publicVideoForIPFS(v.Privacy, v.State),
		notFound:    "video not found",
	})
}

// serveStoredObjectNamed is the API-PROXY delivery source in code — the
// authoritative path every media route falls back to. It streams the object at
// key, reporting a missing object as a 404 with notFoundMsg (so avatar routes
// don't say "video not found").
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
	id, err := pathUUID(c, "id", "video not found")
	if err != nil {
		return err
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
	if isStaff(role) {
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
	return !ok || (userID != ownerID && !isStaff(role))
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

// transcodingHidesVideo keeps a publish-after-transcode held video off public
// surfaces until its transcode completes and the release path publishes it.
// Owners and moderation staff may inspect it meanwhile (like quarantine — NOT
// scheduled's owner-only rule).
func transcodingHidesVideo(c echo.Context, state string, ownerID uuid.UUID) bool {
	if state != "transcoding" {
		return false
	}
	userID, role, ok := principalFromContext(c)
	return !ok || (userID != ownerID && !isStaff(role))
}

// videoHiddenFromViewer combines the moderation visibility rules the media/
// detail surfaces share: a blocked video is hidden (moderators excepted) and a
// quarantined or transcoding-held one is hidden (owner + moderators excepted).
// An unknown id is not "hidden" — the caller's own lookup reports it as 404. It
// also returns the fetched video row so the caller can apply the password gate
// without a second GetByID; that row is the zero value when the video is
// block-hidden or unknown, cases in which the caller returns before ever
// consulting it.
func (s *Server) videoHiddenFromViewer(c echo.Context, videoID uuid.UUID) (sqlcgen.GetVideoByIDRow, bool, error) {
	if hidden, err := s.videoHiddenByBlock(c, videoID); err != nil || hidden {
		return sqlcgen.GetVideoByIDRow{}, hidden, err
	}
	v, err := s.videosvc.GetByID(c.Request().Context(), videoID)
	if err != nil {
		return sqlcgen.GetVideoByIDRow{}, false, nil
	}
	hidden := quarantineHidesVideo(c, v.State, v.OwnerID) || transcodingHidesVideo(c, v.State, v.OwnerID)
	return v, hidden, nil
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
	if ok && (userID == v.OwnerID || isStaff(role)) {
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
	if transcodingHidesVideo(c, v.State, v.OwnerID) {
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
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	id, err := pathUUID(c, "id", "video not found")
	if err != nil {
		return err
	}
	var in blockVideoRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	// A block changes neither privacy nor state, so nothing about the row says
	// the edge must be evicted — but a blocked video that keeps streaming from a
	// CDN is not blocked. Snapshot before, invalidate after (media_purge.go).
	purge := s.videoEdgePurgeSnapshot(c.Request().Context(), id)
	if err := s.moderationsvc.BlockVideo(c.Request().Context(), userID, id, strings.TrimSpace(in.Reason)); err != nil {
		if errors.Is(err, moderation.ErrVideoNotFound) {
			s.audit(c, observability.ActionVideoBlock, observability.ResultFailure, userID.String(), "not_found")
			return echo.NewHTTPError(http.StatusNotFound, "video not found")
		}
		return err
	}
	s.audit(c, observability.ActionVideoBlock, observability.ResultSuccess, userID.String(), "")
	// Search: suppress the doc (search-service W4). Best-effort.
	s.searchEvents.EnqueueVideoSuppress(c.Request().Context(), id, searchevents.SuppressBlocked)
	s.purgeVideoEdgeCopies(c.Request().Context(), id, purge)
	return c.NoContent(http.StatusNoContent)
}

// handleUnblockVideo lifts a video's block. Behind requireRole(admin, moderator).
// Idempotent (unblocking a video that is not blocked still succeeds). Emits an
// audit event.
func (s *Server) handleUnblockVideo(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	id, err := pathUUID(c, "id", "video not found")
	if err != nil {
		return err
	}
	if err := s.moderationsvc.UnblockVideo(c.Request().Context(), id); err != nil {
		return err
	}
	s.audit(c, observability.ActionVideoUnblock, observability.ResultSuccess, userID.String(), "")
	// Search: re-index the doc; the service recomputes eligibility from it
	// (search-service W4). Best-effort.
	s.searchEvents.EnqueueVideoUpsert(c.Request().Context(), id)
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
	pageMeta
}

// handleListBlockedVideos returns currently-blocked videos (newest block first)
// for the moderation block-list. Behind requireRole(admin, moderator).
// Pagination via ?limit (1–100, default 20) and ?offset.
func (s *Server) handleListBlockedVideos(c echo.Context) error {
	page := parsePage(c, defaultVideoFeedLimit, maxVideoFeedLimit)
	items, total, err := s.moderationsvc.ListBlocked(c.Request().Context(), page.Limit32(), page.Offset32())
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
	return c.JSON(http.StatusOK, blockedVideoListResponse{Videos: views, pageMeta: page.meta(total)})
}

// videoError maps video service sentinels to HTTP error envelopes. A non-owner
// sees 404 (not 403) so a private video's existence is not leaked; an owned but
// missing video is also 404.
func videoError(err error) error {
	var rre *video.ReplaceRejectedError
	if errors.As(err, &rre) {
		// A refused replacement source (malware scan / probe, W14): the video
		// and its current source are untouched; the reason is client-safe.
		return echo.NewHTTPError(http.StatusUnprocessableEntity, rre.Reason)
	}
	switch {
	case errors.Is(err, video.ErrReplaceConflict):
		return &ReplaceConflictError{}
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
