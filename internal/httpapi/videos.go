package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/labstack/gommon/bytes"

	"github.com/vidra/vidra-core/internal/moderation"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/quota"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
	"github.com/vidra/vidra-core/internal/urlsafety"
	"github.com/vidra/vidra-core/internal/video"
)

// validVideoPrivacy is the allowed privacy set; empty defaults to "private".
var validVideoPrivacy = map[string]bool{"public": true, "unlisted": true, "private": true}

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
		fes = append(fes, FieldError{Field: "privacy", Message: "must be one of public, unlisted, private"})
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
	ID              string    `json:"id"`
	Remote          bool      `json:"remote"`
	ChannelID       string    `json:"channel_id,omitempty"`
	Title           string    `json:"title"`
	Description     string    `json:"description"`
	Privacy         string    `json:"privacy,omitempty"`
	State           string    `json:"state,omitempty"`
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
	// Views is the recorded view count, set on the detail endpoint (omitted on
	// list/feed views, which do not look it up).
	Views *int64 `json:"views,omitempty"`
	// ChannelHandle and ChannelDisplayName identify the owning channel on
	// card/feed views, so the client can link a card to /channels/{handle} and
	// show the channel name. Omitted on the detail view (which does not join the
	// channel).
	ChannelHandle      *string `json:"channel_handle,omitempty"`
	ChannelDisplayName *string `json:"channel_display_name,omitempty"`
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
}

func newVideoView(v sqlcgen.Video) videoView {
	return videoView{
		ID:          v.ID.String(),
		ChannelID:   v.ChannelID.String(),
		Title:       v.Title,
		Description: v.Description,
		Privacy:     v.Privacy,
		State:       v.State,
		CreatedAt:   v.CreatedAt,
		Category:    v.Category,
		Language:    v.Language,
		License:     v.License,
		PublishAt:   video.TimePtr(v.PublishAt),
	}
}

func videoViewFromRow(v sqlcgen.GetVideoByIDRow) videoView {
	return videoView{
		ID:          v.ID.String(),
		ChannelID:   v.ChannelID.String(),
		Title:       v.Title,
		Description: v.Description,
		Privacy:     v.Privacy,
		State:       v.State,
		CreatedAt:   v.CreatedAt,
		Category:    v.Category,
		Language:    v.Language,
		License:     v.License,
		PublishAt:   video.TimePtr(v.PublishAt),
	}
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

	privacy := in.Privacy
	if privacy == "" {
		privacy = "private"
	}
	v, err := s.videosvc.CreateDraft(ctx, ch.ID, video.CreateInput{
		Title:       in.Title,
		Description: in.Description,
		Privacy:     privacy,
		Category:    in.Category,
		Language:    in.Language,
		License:     in.License,
		Tags:        in.Tags,
		PublishAt:   in.PublishAt,
	})
	if err != nil {
		return err
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

// handleGetVideo returns a video by id. Runs behind optionalAuth: public and
// unlisted videos are visible to anyone with the link; a private video is
// visible only to its owner, and is reported as 404 (not 403) to everyone else
// so its existence is not leaked.
func (s *Server) handleGetVideo(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	v, err := s.videosvc.GetByID(c.Request().Context(), id)
	if err != nil {
		if errors.Is(err, video.ErrNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "video not found")
		}
		return err
	}
	if v.Privacy == "private" {
		userID, _, ok := principalFromContext(c)
		if !ok || userID != v.OwnerID {
			return echo.NewHTTPError(http.StatusNotFound, "video not found")
		}
	}
	if hidden, err := s.videoHiddenByBlock(c, id); err != nil {
		return err
	} else if hidden {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	if quarantineHidesVideo(c, v.State, v.OwnerID) {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	view := videoViewFromRow(v)
	if md, ok, err := s.videosvc.GetMetadata(c.Request().Context(), id); err == nil && ok {
		view.DurationSeconds = md.DurationSeconds
		view.Width = md.Width
		view.Height = md.Height
	}
	has := s.videosvc.HasThumbnail(c.Request().Context(), id)
	view.HasThumbnail = &has
	views := s.videosvc.Views(c.Request().Context(), id)
	view.Views = &views
	s.attachVideoTags(c.Request().Context(), &view, id)
	view.HLSURL, view.Renditions = s.hlsDetail(c, id)
	return c.JSON(http.StatusOK, view)
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
	views := it.Views
	v.Views = &views
	has := it.HasThumbnail
	v.HasThumbnail = &has
	handle := it.ChannelHandle
	v.ChannelHandle = &handle
	name := it.ChannelDisplayName
	v.ChannelDisplayName = &name
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
	return c.JSON(http.StatusOK, videoFeedResponse{Videos: views, Sort: "recent", Limit: limit, Offset: offset})
}

// maxSearchQueryLen bounds the search term to keep queries cheap.
const maxSearchQueryLen = 100

// videoSearchResponse is the paginated result of a public title search.
type videoSearchResponse struct {
	Query  string      `json:"query"`
	Videos []videoView `json:"videos"`
	Limit  int         `json:"limit"`
	Offset int         `json:"offset"`
}

// handleSearchVideos searches public video titles. No auth required. Requires a
// non-empty ?q (<=100 chars); paginated via ?limit (1–100, default 20)/?offset.
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
	viewerID, _, authed := principalFromContext(c)
	items, err := s.videosvc.SearchPublic(c.Request().Context(), q, viewerID, authed, int32(limit), int32(offset))
	if err != nil {
		return err
	}
	views := make([]videoView, 0, len(items))
	for _, it := range items {
		views = append(views, feedItemView(it))
	}
	return c.JSON(http.StatusOK, videoSearchResponse{Query: q, Videos: views, Limit: limit, Offset: offset})
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
		items, err = s.videosvc.ListPublicByChannel(ctx, ch.ID)
	}
	if err != nil {
		return err
	}
	views := make([]videoView, 0, len(items))
	for _, it := range items {
		views = append(views, feedItemView(it))
	}
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
}

func (r updateVideoRequest) Validate() []FieldError {
	if r.Title == nil && r.Description == nil && r.Privacy == nil &&
		r.Category == nil && r.Language == nil && r.License == nil && r.Tags == nil &&
		r.PublishAt == nil {
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
		fes = append(fes, FieldError{Field: "privacy", Message: "must be one of public, unlisted, private"})
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

// handleUpdateVideo updates a video owned by the authenticated user.
func (s *Server) handleUpdateVideo(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
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
	v, err := s.videosvc.Update(c.Request().Context(), userID, id, video.UpdateInput{
		Title:       in.Title,
		Description: in.Description,
		Privacy:     in.Privacy,
		Category:    in.Category,
		Language:    in.Language,
		License:     in.License,
		Tags:        in.Tags,
		PublishAt:   in.PublishAt,
	})
	if err != nil {
		if errors.Is(err, video.ErrPublished) {
			return &ValidationError{Fields: []FieldError{{Field: "publish_at", Message: "cannot be set after the video is published"}}}
		}
		return videoError(err)
	}
	// Re-flag the edited metadata against the watched-words list (best-effort;
	// an edit can newly introduce a flagged term — §12).
	s.flagVideoWatchedWords(c.Request().Context(), v.ID, v.Title, v.Description)
	view := newVideoView(v)
	s.attachVideoTags(c.Request().Context(), &view, v.ID)
	return c.JSON(http.StatusOK, view)
}

// handleDeleteVideo deletes a video owned by the authenticated user.
func (s *Server) handleDeleteVideo(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	if err := s.videosvc.Delete(c.Request().Context(), userID, id); err != nil {
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
	if s.quotasvc != nil {
		if qerr := s.quotasvc.CheckFits(ctx, userID, fh.Size); qerr != nil {
			if errors.Is(qerr, quota.ErrExceeded) {
				return &QuotaExceededError{}
			}
			return qerr
		}
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

// handleSetVideoThumbnail stores a creator-supplied poster image (multipart form
// field "file") for a video owned by the caller, replacing any previous or
// auto-generated thumbnail. Behind requireAuth; non-owner/unknown → 404; a
// non-image extension → 415. The 8M global body limit bounds the upload.
func (s *Server) handleSetVideoThumbnail(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
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

// importVideoRequest is the POST /videos/:id/import body.
type importVideoRequest struct {
	URL string `json:"url"`
}

func (r importVideoRequest) Validate() []FieldError {
	if strings.TrimSpace(r.URL) == "" {
		return []FieldError{{Field: "url", Message: "is required"}}
	}
	return nil
}

const importFetchTimeout = 60 * time.Second

// errImportTooLarge is returned by maxBytesReader when the fetched body exceeds
// the UPLOAD_MAX_SIZE cap; the handler maps it to 413.
var errImportTooLarge = errors.New("httpapi: imported file exceeds the size limit")

// errImportOverQuota is returned by maxBytesReader when the fetched body would
// push the caller past their storage quota; the handler maps it to 422
// quota_exceeded. It hard-caps the stream so a lying/absent Content-Length
// can't smuggle an over-quota file past the pre-fetch check.
var errImportOverQuota = errors.New("httpapi: imported file exceeds the storage quota")

// maxBytesReader caps how many bytes are read from a remote fetch, failing with
// limitErr once the cap is crossed. It reads one byte past the limit to
// distinguish "exactly at the limit" (ok) from "over", so a lying/absent
// Content-Length can't smuggle an oversized file past the cap.
type maxBytesReader struct {
	r         io.Reader
	remaining int64
	limitErr  error
}

func (m *maxBytesReader) Read(p []byte) (int, error) {
	if m.remaining < 0 {
		return 0, m.limitErr
	}
	if int64(len(p)) > m.remaining+1 {
		p = p[:m.remaining+1]
	}
	n, err := m.r.Read(p)
	m.remaining -= int64(n)
	if m.remaining < 0 {
		return n, m.limitErr
	}
	return n, err
}

// handleImportVideoFile fetches a remote video by URL and stores it as the
// original for a video owned by the caller, then moves the video to processing —
// the URL-import counterpart of handleUploadVideoFile. The outbound fetch goes
// through the SSRF-safe urlsafety client (non-http schemes, private/loopback/
// link-local/CGNAT addresses, and DNS-rebinding are refused, at dial time so
// redirects are covered too) and is hard-bounded by UPLOAD_MAX_SIZE. Ownership is
// checked BEFORE any fetch, so a non-owner cannot use this to make the server
// issue requests. Non-owner/unknown video → 404 (existence is not leaked). When
// a storage quota applies, a declared Content-Length that would not fit is 422
// quota_exceeded before any bytes are stored, and the stream itself is
// hard-capped at the remaining headroom (so an absent/lying length cannot
// smuggle an over-quota file — same defence as UPLOAD_MAX_SIZE's 413).
func (s *Server) handleImportVideoFile(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	var in importVideoRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	// The guard is secure by default; ImportAllowPrivateURLs (dev/test only) relaxes
	// the private-address block so backed e2e can import from a loopback origin.
	guard := urlsafety.Guard{AllowPrivate: s.cfg.ImportAllowPrivateURLs}
	target, err := guard.ValidateURL(strings.TrimSpace(in.URL))
	if err != nil {
		return &ValidationError{Fields: []FieldError{{Field: "url", Message: "must be a public http(s) URL"}}}
	}

	ctx := c.Request().Context()
	// Authorize before any outbound request: a non-owner (or unknown video) gets
	// 404 and the server never fetches on their behalf.
	if v, gerr := s.videosvc.GetByID(ctx, id); gerr != nil || v.OwnerID != userID {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}

	maxBytes, _ := bytes.Parse(s.cfg.UploadMaxSize) // validated at startup

	// Resolve the caller's storage headroom BEFORE fetching. quotaRemaining < 0
	// means unlimited (never enforced).
	quotaRemaining := int64(-1)
	if s.quotasvc != nil {
		remaining, limited, qerr := s.quotasvc.Remaining(ctx, userID)
		if qerr != nil {
			return qerr
		}
		if limited {
			quotaRemaining = remaining
		}
	}

	client := s.importClient
	if client == nil {
		client = guard.NewClient(importFetchTimeout)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return &ValidationError{Fields: []FieldError{{Field: "url", Message: "must be a public http(s) URL"}}}
	}
	resp, err := client.Do(req)
	if err != nil {
		// A blocked address (SSRF guard), DNS failure, timeout, TLS error, etc.
		// The URL is not echoed back (avoids reflecting attacker-controlled input).
		return echo.NewHTTPError(http.StatusUnprocessableEntity, "could not fetch the URL")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, "the URL did not return a downloadable file")
	}
	if maxBytes > 0 && resp.ContentLength > maxBytes {
		return echo.NewHTTPError(http.StatusRequestEntityTooLarge, "the file is too large")
	}
	// Quota pre-check on a declared Content-Length; the streaming cap below is
	// the authoritative backstop for unknown/lying lengths.
	if quotaRemaining >= 0 && resp.ContentLength > quotaRemaining {
		return &QuotaExceededError{}
	}

	body := io.Reader(resp.Body)
	if maxBytes > 0 {
		body = &maxBytesReader{r: body, remaining: maxBytes, limitErr: errImportTooLarge}
	}
	if quotaRemaining >= 0 {
		body = &maxBytesReader{r: body, remaining: quotaRemaining, limitErr: errImportOverQuota}
	}
	// AttachOriginal enforces the video-container extension allow-list on this
	// filename (derived from the URL path); a URL without a video extension → 415.
	_, file, err := s.videosvc.AttachOriginal(ctx, userID, id, video.UploadInput{
		Filename:    path.Base(target.Path),
		ContentType: resp.Header.Get("Content-Type"),
		Reader:      body,
	})
	if err != nil {
		if errors.Is(err, errImportTooLarge) {
			return echo.NewHTTPError(http.StatusRequestEntityTooLarge, "the file is too large")
		}
		if errors.Is(err, errImportOverQuota) {
			return &QuotaExceededError{}
		}
		return videoError(err)
	}
	v, err := s.videosvc.Process(ctx, id, file.StorageKey)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusCreated, uploadVideoFileResponse{
		Video: newVideoView(v),
		File:  newVideoFileView(file),
	})
}

// handleStreamVideoOriginal serves a video's stored original file. Behind
// optionalAuth: visibility mirrors the detail endpoint (public/unlisted to
// anyone, private only to the owner; otherwise 404), and a video without a
// stored original is 404. Range requests are honoured for seeking when the
// backend exposes a filesystem path.
func (s *Server) handleStreamVideoOriginal(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	if hidden, err := s.videoHiddenFromViewer(c, id); err != nil {
		return err
	} else if hidden {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	viewerID, _, authed := principalFromContext(c)
	f, err := s.videosvc.FileForView(c.Request().Context(), id, viewerID, authed, "original")
	if err != nil {
		return videoError(err)
	}
	return s.serveStoredObject(c, f.StorageKey, f.ContentType)
}

// handleGetVideoThumbnail serves a video's generated poster image. Same
// visibility as the detail endpoint; a video without a stored thumbnail is 404.
func (s *Server) handleGetVideoThumbnail(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	if hidden, err := s.videoHiddenFromViewer(c, id); err != nil {
		return err
	} else if hidden {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
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
// when Redis is wired). Behind optionalAuth: visibility mirrors the detail
// endpoint. Always 204 on success — whether or not the view was newly counted.
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

// quarantineHidesVideo reports whether a quarantined video (§11) is hidden from
// this caller: everyone except the owner (who sees it badged in the studio) and
// moderators/admins (who review the queue). Other states are never hidden by
// this rule — the public discovery surfaces already filter on state=published.
func quarantineHidesVideo(c echo.Context, state string, ownerID uuid.UUID) bool {
	if state != "quarantined" {
		return false
	}
	userID, role, ok := principalFromContext(c)
	if ok && (userID == ownerID || role == "admin" || role == "moderator") {
		return false
	}
	return true
}

// videoHiddenFromViewer combines the moderation visibility rules the media/
// detail surfaces share: a blocked video is hidden (moderators excepted) and a
// quarantined one is hidden (owner + moderators excepted). An unknown id is not
// "hidden" — the caller's own lookup reports it as 404.
func (s *Server) videoHiddenFromViewer(c echo.Context, videoID uuid.UUID) (bool, error) {
	if hidden, err := s.videoHiddenByBlock(c, videoID); err != nil || hidden {
		return hidden, err
	}
	v, err := s.videosvc.GetByID(c.Request().Context(), videoID)
	if err != nil {
		return false, nil
	}
	return quarantineHidesVideo(c, v.State, v.OwnerID), nil
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
	default:
		return err
	}
}
