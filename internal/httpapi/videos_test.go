package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"math/rand/v2"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	gommonbytes "github.com/labstack/gommon/bytes"

	"github.com/vidra/vidra-core/internal/admin"
	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/block"
	"github.com/vidra/vidra-core/internal/captionjob"
	"github.com/vidra/vidra-core/internal/channel"
	"github.com/vidra/vidra-core/internal/comment"
	"github.com/vidra/vidra-core/internal/config"
	"github.com/vidra/vidra-core/internal/e2ee"
	"github.com/vidra/vidra-core/internal/instancemod"
	"github.com/vidra/vidra-core/internal/instancesettings"
	"github.com/vidra/vidra-core/internal/live"
	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/mediagc"
	"github.com/vidra/vidra-core/internal/messaging"
	"github.com/vidra/vidra-core/internal/moderation"
	"github.com/vidra/vidra-core/internal/mute"
	"github.com/vidra/vidra-core/internal/notification"
	"github.com/vidra/vidra-core/internal/playersettings"
	"github.com/vidra/vidra-core/internal/playlist"
	"github.com/vidra/vidra-core/internal/quota"
	"github.com/vidra/vidra-core/internal/rating"
	"github.com/vidra/vidra-core/internal/shortid"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
	"github.com/vidra/vidra-core/internal/transcode"
	"github.com/vidra/vidra-core/internal/upload"
	"github.com/vidra/vidra-core/internal/uploadfinalize"
	"github.com/vidra/vidra-core/internal/video"
	"github.com/vidra/vidra-core/internal/videoimport"
	"github.com/vidra/vidra-core/internal/watchword"
)

// videoFakeRepo is an in-memory video.Repository. It resolves a new video's
// owner from the shared channelFakeRepo so GetVideoByID can return owner_id.
type videoFakeRepo struct {
	channels *channelFakeRepo
	mutes    *muteFakeRepo
	blocks   *moderationFakeRepo
	// userBlocks mirrors the §13 user_blocks content filter (viewer = blocker).
	userBlocks *blockFakeRepo
	videos     map[uuid.UUID]sqlcgen.GetVideoByIDRow
	// peertubeUUIDs mirrors videos.peertube_uuid: video id -> the UUID this video
	// had on the PeerTube instance it was imported from. Seeded directly by the
	// legacy-URL tests; the importer is what writes it in production.
	peertubeUUIDs map[uuid.UUID]uuid.UUID
	files         map[uuid.UUID][]sqlcgen.VideoFile
	metadata      map[uuid.UUID]sqlcgen.VideoMetadatum
	views         map[uuid.UUID]int64
	saved         map[string]time.Time                          // "userID|videoID" -> saved-at
	history       map[string]historyMark                        // "userID|videoID" -> resume position + last-watched
	captions      map[string]sqlcgen.Caption                    // "videoID|lang" -> caption
	tags          map[uuid.UUID][]string                        // video ID -> normalized tag set
	chapters      map[uuid.UUID][]sqlcgen.ListVideoChaptersRow  // video ID -> ordered chapters
	passwords     map[uuid.UUID][]fakeVideoPassword             // video ID -> passwords (CORE-17)
	embed         map[uuid.UUID]sqlcgen.GetVideoEmbedPrivacyRow // video ID -> embed policy override
	viewDays      map[string]int64                              // "videoID|YYYY-MM-DD" -> rolled-up views
	// ratings/commentsRepo mirror the cross-table joins the stats queries do.
	ratings      *ratingFakeRepo
	commentsRepo *commentFakeRepo
	// users mirrors the discovery queries' unlisted-owner exclusion (§16).
	users *authFakeRepo
	// Seeded REMOTE cards, mirroring the UNION branches of the subscription /
	// feed / search queries (remote-content §3-4). Tests append rows with
	// Remote:true + domain/watch_url/stream_url set.
	remoteSubs   []sqlcgen.ListSubscriptionVideosRow
	remoteFeed   []sqlcgen.ListPublicVideosSortedRow
	remoteSearch []sqlcgen.SearchPublicVideosRow
	// rejections mirrors video_rejections (0130): video ID -> the moderator's
	// rejection note. Empty means "never rejected", which is what the query's
	// COALESCE(..., '') returns.
	rejections map[uuid.UUID]string
}

func (f *videoFakeRepo) RecordVideoRejection(_ context.Context, a sqlcgen.RecordVideoRejectionParams) error {
	if f.rejections == nil {
		f.rejections = map[uuid.UUID]string{}
	}
	f.rejections[a.VideoID] = a.Note // ON CONFLICT DO UPDATE: last write wins
	return nil
}

// ownerUnlisted mirrors the feed/search queries' NOT EXISTS unlisted check:
// whether the channel's owning account opted out of discovery.
func (f *videoFakeRepo) ownerUnlisted(channelID uuid.UUID) bool {
	if f.users == nil {
		return false
	}
	owner := f.channelOwner(channelID)
	for _, u := range f.users.users {
		if u.ID == owner {
			return u.Unlisted
		}
	}
	return false
}

func (f *videoFakeRepo) DeleteVideoTags(_ context.Context, videoID uuid.UUID) error {
	delete(f.tags, videoID)
	return nil
}

func (f *videoFakeRepo) InsertVideoTags(_ context.Context, a sqlcgen.InsertVideoTagsParams) error {
	if f.tags == nil {
		f.tags = map[uuid.UUID][]string{}
	}
	existing := map[string]bool{}
	for _, t := range f.tags[a.VideoID] {
		existing[t] = true
	}
	for _, t := range a.Tags {
		if !existing[t] {
			f.tags[a.VideoID] = append(f.tags[a.VideoID], t)
			existing[t] = true
		}
	}
	return nil
}

func (f *videoFakeRepo) ListVideoTags(_ context.Context, videoID uuid.UUID) ([]string, error) {
	out := append([]string(nil), f.tags[videoID]...)
	sort.Strings(out)
	return out, nil
}

// hasTag mirrors the feed query's exact-match tag EXISTS clause.
// durationOf mirrors the LEFT JOIN on video_metadata: nil when no probe has
// recorded a duration for the video.
func (f *videoFakeRepo) durationOf(videoID uuid.UUID) *int32 {
	if md, ok := f.metadata[videoID]; ok {
		return md.DurationSeconds
	}
	return nil
}

func (f *videoFakeRepo) hasTag(videoID uuid.UUID, tag string) bool {
	for _, t := range f.tags[videoID] {
		if t == tag {
			return true
		}
	}
	return false
}

// tagMatches mirrors the search query's substring tag clause (tags are stored
// lowercased, the query is matched case-insensitively).
func (f *videoFakeRepo) tagMatches(videoID uuid.UUID, q string) bool {
	for _, t := range f.tags[videoID] {
		if strings.Contains(t, strings.ToLower(q)) {
			return true
		}
	}
	return false
}

func (f *videoFakeRepo) UpsertCaption(_ context.Context, a sqlcgen.UpsertCaptionParams) (sqlcgen.Caption, error) {
	if f.captions == nil {
		f.captions = map[string]sqlcgen.Caption{}
	}
	c := sqlcgen.Caption{
		ID: uuid.New(), VideoID: a.VideoID, Language: a.Language, Label: a.Label,
		StorageKey: a.StorageKey, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	f.captions[a.VideoID.String()+"|"+a.Language] = c
	return c, nil
}

func (f *videoFakeRepo) ListCaptionsByVideo(_ context.Context, videoID uuid.UUID) ([]sqlcgen.Caption, error) {
	var out []sqlcgen.Caption
	for _, c := range f.captions {
		if c.VideoID == videoID {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Language < out[j].Language })
	return out, nil
}

func (f *videoFakeRepo) GetCaptionByLang(_ context.Context, a sqlcgen.GetCaptionByLangParams) (sqlcgen.Caption, error) {
	c, ok := f.captions[a.VideoID.String()+"|"+a.Language]
	if !ok {
		return sqlcgen.Caption{}, errors.New("not found")
	}
	return c, nil
}

func (f *videoFakeRepo) DeleteCaption(_ context.Context, a sqlcgen.DeleteCaptionParams) (int64, error) {
	k := a.VideoID.String() + "|" + a.Language
	if _, ok := f.captions[k]; !ok {
		return 0, nil
	}
	delete(f.captions, k)
	return 1, nil
}

// historyMark is the in-memory watch_history row for the fake repo.
type historyMark struct {
	position  int32
	watchedAt time.Time
}

func (f *videoFakeRepo) SaveVideo(_ context.Context, a sqlcgen.SaveVideoParams) error {
	if f.saved == nil {
		f.saved = map[string]time.Time{}
	}
	key := a.UserID.String() + "|" + a.VideoID.String()
	if _, ok := f.saved[key]; !ok {
		f.saved[key] = time.Now()
	}
	return nil
}

func (f *videoFakeRepo) UnsaveVideo(_ context.Context, a sqlcgen.UnsaveVideoParams) error {
	delete(f.saved, a.UserID.String()+"|"+a.VideoID.String())
	return nil
}

func (f *videoFakeRepo) ListSavedVideos(_ context.Context, a sqlcgen.ListSavedVideosParams) ([]sqlcgen.ListSavedVideosRow, error) {
	type saved struct {
		vid uuid.UUID
		at  time.Time
	}
	var list []saved
	prefix := a.UserID.String() + "|"
	for k, t := range f.saved {
		if strings.HasPrefix(k, prefix) {
			list = append(list, saved{uuid.MustParse(strings.TrimPrefix(k, prefix)), t})
		}
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].at.After(list[j].at) })
	var rows []sqlcgen.ListSavedVideosRow
	for _, sv := range list {
		r, ok := f.videos[sv.vid]
		if !ok || r.Privacy != "public" || r.State != "published" {
			continue
		}
		rows = append(rows, sqlcgen.ListSavedVideosRow{
			ID: r.ID, ShortCode: r.ShortCode, ChannelID: r.ChannelID, Title: r.Title, Description: r.Description,
			Privacy: r.Privacy, State: r.State, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
			Views: f.views[r.ID], HasThumbnail: f.hasThumb(r.ID),
			IsSensitive: r.IsSensitive, SensitiveReason: r.SensitiveReason,
		})
	}
	return rows, nil
}

func (f *videoFakeRepo) UpsertWatchProgress(_ context.Context, a sqlcgen.UpsertWatchProgressParams) (sqlcgen.WatchHistory, error) {
	if f.history == nil {
		f.history = map[string]historyMark{}
	}
	key := a.UserID.String() + "|" + a.VideoID.String()
	now := time.Now()
	f.history[key] = historyMark{position: a.PositionSeconds, watchedAt: now}
	return sqlcgen.WatchHistory{
		UserID: a.UserID, VideoID: a.VideoID, PositionSeconds: a.PositionSeconds,
		CreatedAt: now, UpdatedAt: now,
	}, nil
}

func (f *videoFakeRepo) GetWatchProgress(_ context.Context, a sqlcgen.GetWatchProgressParams) (sqlcgen.WatchHistory, error) {
	m, ok := f.history[a.UserID.String()+"|"+a.VideoID.String()]
	if !ok {
		return sqlcgen.WatchHistory{}, errors.New("not found")
	}
	return sqlcgen.WatchHistory{
		UserID: a.UserID, VideoID: a.VideoID, PositionSeconds: m.position,
		CreatedAt: m.watchedAt, UpdatedAt: m.watchedAt,
	}, nil
}

func (f *videoFakeRepo) ListWatchHistory(_ context.Context, a sqlcgen.ListWatchHistoryParams) ([]sqlcgen.ListWatchHistoryRow, error) {
	type entry struct {
		vid uuid.UUID
		m   historyMark
	}
	var list []entry
	prefix := a.UserID.String() + "|"
	for k, m := range f.history {
		if strings.HasPrefix(k, prefix) {
			list = append(list, entry{uuid.MustParse(strings.TrimPrefix(k, prefix)), m})
		}
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].m.watchedAt.After(list[j].m.watchedAt) })
	var rows []sqlcgen.ListWatchHistoryRow
	for _, e := range list {
		r, ok := f.videos[e.vid]
		if !ok || r.Privacy != "public" || r.State != "published" {
			continue
		}
		handle, name := f.channelInfo(r.ChannelID)
		rows = append(rows, sqlcgen.ListWatchHistoryRow{
			ID: r.ID, ShortCode: r.ShortCode, ChannelID: r.ChannelID, Title: r.Title, Description: r.Description,
			Privacy: r.Privacy, State: r.State, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
			Views: f.views[r.ID], HasThumbnail: f.hasThumb(r.ID),
			ChannelHandle: handle, ChannelDisplayName: name,
			IsSensitive:     r.IsSensitive,
			SensitiveReason: r.SensitiveReason,
			PositionSeconds: e.m.position, WatchedAt: e.m.watchedAt,
		})
	}
	return rows, nil
}

func (f *videoFakeRepo) ListWatchHistoryInProgress(_ context.Context, a sqlcgen.ListWatchHistoryInProgressParams) ([]sqlcgen.ListWatchHistoryInProgressRow, error) {
	type entry struct {
		vid uuid.UUID
		m   historyMark
	}
	var list []entry
	prefix := a.UserID.String() + "|"
	for k, m := range f.history {
		if strings.HasPrefix(k, prefix) {
			list = append(list, entry{uuid.MustParse(strings.TrimPrefix(k, prefix)), m})
		}
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].m.watchedAt.After(list[j].m.watchedAt) })
	var rows []sqlcgen.ListWatchHistoryInProgressRow
	for _, e := range list {
		r, ok := f.videos[e.vid]
		if !ok || r.Privacy != "public" || r.State != "published" {
			continue
		}
		// Mirror the SQL filter: started (>= 5s) and not effectively finished
		// (>= 95% of a known, positive duration).
		if e.m.position < 5 {
			continue
		}
		var duration *int32
		if md, ok := f.metadata[e.vid]; ok {
			duration = md.DurationSeconds
		}
		if duration != nil && *duration > 0 && float64(e.m.position)/float64(*duration) >= 0.95 {
			continue
		}
		handle, name := f.channelInfo(r.ChannelID)
		rows = append(rows, sqlcgen.ListWatchHistoryInProgressRow{
			ID: r.ID, ShortCode: r.ShortCode, ChannelID: r.ChannelID, Title: r.Title, Description: r.Description,
			Privacy: r.Privacy, State: r.State, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
			Views: f.views[r.ID], HasThumbnail: f.hasThumb(r.ID),
			ChannelHandle: handle, ChannelDisplayName: name,
			DurationSeconds: duration,
			IsSensitive:     r.IsSensitive,
			SensitiveReason: r.SensitiveReason,
			PositionSeconds: e.m.position, WatchedAt: e.m.watchedAt,
		})
	}
	return rows, nil
}

func (f *videoFakeRepo) DeleteWatchHistoryEntry(_ context.Context, a sqlcgen.DeleteWatchHistoryEntryParams) error {
	delete(f.history, a.UserID.String()+"|"+a.VideoID.String())
	return nil
}

func (f *videoFakeRepo) ClearWatchHistory(_ context.Context, userID uuid.UUID) error {
	prefix := userID.String() + "|"
	for k := range f.history {
		if strings.HasPrefix(k, prefix) {
			delete(f.history, k)
		}
	}
	return nil
}

// ListSubscriptionVideos mirrors the SQL by consulting the real follow data in
// the channel fake (videos whose channel the FollowerID follows).
func (f *videoFakeRepo) ListSubscriptionVideos(_ context.Context, a sqlcgen.ListSubscriptionVideosParams) ([]sqlcgen.ListSubscriptionVideosRow, error) {
	var rows []sqlcgen.ListSubscriptionVideosRow
	for _, r := range f.videos {
		follows := f.channels != nil && f.channels.follows[a.FollowerID.String()+"|"+r.ChannelID.String()]
		hidden := f.mutedFromFeed(pgtype.UUID{Bytes: a.FollowerID, Valid: true}, r.ChannelID)
		if f.blockedFromFeed(r.ID) {
			continue
		}
		if r.Privacy == "public" && r.State == "published" && follows && !hidden {
			rows = append(rows, sqlcgen.ListSubscriptionVideosRow{
				ID: r.ID, ShortCode: r.ShortCode, ChannelID: r.ChannelID, Title: r.Title, Description: r.Description,
				Privacy: r.Privacy, State: r.State, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
				Views: f.views[r.ID], HasThumbnail: f.hasThumb(r.ID),
				IsSensitive: r.IsSensitive, SensitiveReason: r.SensitiveReason,
			})
		}
	}
	// Remote branch of the UNION (remote-content §3): seeded remote cards.
	rows = append(rows, f.remoteSubs...)
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].CreatedAt.After(rows[j].CreatedAt) })
	return rows, nil
}

func (f *videoFakeRepo) IncrementVideoViews(_ context.Context, videoID uuid.UUID) (int64, error) {
	if f.views == nil {
		f.views = map[uuid.UUID]int64{}
	}
	f.views[videoID]++
	return f.views[videoID], nil
}

func (f *videoFakeRepo) GetVideoViews(_ context.Context, videoID uuid.UUID) (int64, error) {
	n, ok := f.views[videoID]
	if !ok {
		return 0, errors.New("not found")
	}
	return n, nil
}

func (f *videoFakeRepo) UpsertVideoMetadata(_ context.Context, a sqlcgen.UpsertVideoMetadataParams) (sqlcgen.VideoMetadatum, error) {
	if f.metadata == nil {
		f.metadata = map[uuid.UUID]sqlcgen.VideoMetadatum{}
	}
	m := sqlcgen.VideoMetadatum{
		VideoID: a.VideoID, DurationSeconds: a.DurationSeconds, Width: a.Width, Height: a.Height,
		UpdatedAt: time.Now(),
	}
	f.metadata[a.VideoID] = m
	return m, nil
}

func (f *videoFakeRepo) GetVideoMetadata(_ context.Context, videoID uuid.UUID) (sqlcgen.VideoMetadatum, error) {
	m, ok := f.metadata[videoID]
	if !ok {
		return sqlcgen.VideoMetadatum{}, errors.New("not found")
	}
	return m, nil
}

func (f *videoFakeRepo) CreateVideo(_ context.Context, a sqlcgen.CreateVideoParams) (sqlcgen.Video, error) {
	var owner uuid.UUID
	for _, ch := range f.channels.byHandle {
		if ch.ID == a.ChannelID {
			owner = ch.OwnerID
		}
	}
	v := sqlcgen.Video{
		ID: uuid.New(), ShortCode: fakeShortCode(), ChannelID: a.ChannelID, Title: a.Title,
		Description: a.Description, Privacy: a.Privacy, State: "draft",
		Category: a.Category, Language: a.Language, License: a.License,
		PublishAt: a.PublishAt, IsSensitive: a.IsSensitive, SensitiveReason: a.SensitiveReason,
		CommentsPolicy: a.CommentsPolicy, DownloadEnabled: a.DownloadEnabled,
		PublishAfterTranscode: a.PublishAfterTranscode,
		CreatedAt:             time.Now(), UpdatedAt: time.Now(),
	}
	f.videos[v.ID] = sqlcgen.GetVideoByIDRow{
		ID: v.ID, ShortCode: v.ShortCode, ChannelID: v.ChannelID, Title: v.Title, Description: v.Description,
		Privacy: v.Privacy, State: v.State, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt,
		Category: v.Category, Language: v.Language, License: v.License,
		PublishAt: v.PublishAt, IsSensitive: v.IsSensitive, SensitiveReason: v.SensitiveReason,
		CommentsPolicy: v.CommentsPolicy, DownloadEnabled: v.DownloadEnabled,
		PublishAfterTranscode: v.PublishAfterTranscode,
		OwnerID:               owner,
	}
	return v, nil
}

// GetVideoIDByShortCode mirrors the unique index on videos.short_code. The
// empty-code guard matters: rows seeded directly by a test (rather than through
// CreateVideo) carry no code, and without it a lookup for "" would match them.
func (f *videoFakeRepo) GetVideoIDByShortCode(_ context.Context, code string) (uuid.UUID, error) {
	for id, v := range f.videos {
		if v.ShortCode != "" && v.ShortCode == code {
			return id, nil
		}
	}
	return uuid.Nil, pgx.ErrNoRows
}

// GetVideoIDByLegacyUUID mirrors "WHERE id = $1 OR peertube_uuid = $1", keeping
// the query's preference for this instance's own id namespace.
func (f *videoFakeRepo) GetVideoIDByLegacyUUID(_ context.Context, legacy uuid.UUID) (uuid.UUID, error) {
	if _, ok := f.videos[legacy]; ok {
		return legacy, nil
	}
	for id, src := range f.peertubeUUIDs {
		if src == legacy {
			return id, nil
		}
	}
	return uuid.Nil, pgx.ErrNoRows
}

// fakeShortCode mints an 11-character base58 code, mirroring what migration 0126
// makes Postgres do on INSERT via the vidra_short_code() DEFAULT. Tests that
// resolve /v/{code} need the fake to behave like the column, not like a zero
// value.
func fakeShortCode() string {
	b := make([]byte, video.ShortCodeLen)
	for i := range b {
		b[i] = shortid.Alphabet[rand.IntN(len(shortid.Alphabet))]
	}
	return string(b)
}

func (f *videoFakeRepo) GetVideoByID(_ context.Context, id uuid.UUID) (sqlcgen.GetVideoByIDRow, error) {
	v, ok := f.videos[id]
	if !ok {
		return sqlcgen.GetVideoByIDRow{}, errors.New("not found")
	}
	// Mirror the real query's channels + users JOINs so the detail view carries
	// the owning channel's identity and the uploader's display name.
	v.ChannelHandle, v.ChannelDisplayName = f.channelInfo(v.ChannelID)
	v.AuthorDisplayName = f.authorName(v.ChannelID)
	return v, nil
}

// authorName mirrors the discovery queries' users JOIN: the owning account's
// display name (config-parity W5/W9 author_display_name).
func (f *videoFakeRepo) authorName(channelID uuid.UUID) string {
	if f.users == nil {
		return ""
	}
	owner := f.channelOwner(channelID)
	for _, u := range f.users.users {
		if u.ID == owner {
			return u.DisplayName
		}
	}
	return ""
}

func vidRowToVideo(r sqlcgen.GetVideoByIDRow) sqlcgen.Video {
	return sqlcgen.Video{
		ID: r.ID, ChannelID: r.ChannelID, Title: r.Title, Description: r.Description,
		Privacy: r.Privacy, State: r.State, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		Category: r.Category, Language: r.Language, License: r.License, PublishAt: r.PublishAt,
		OriginallyPublishedAt: r.OriginallyPublishedAt,
		IsSensitive:           r.IsSensitive, SensitiveReason: r.SensitiveReason,
		CommentsPolicy: r.CommentsPolicy, DownloadEnabled: r.DownloadEnabled,
		PublishAfterTranscode: r.PublishAfterTranscode,
	}
}

// ListVideoIDsByChannel mirrors the unpaginated id sweep (ORDER BY id).
func (f *videoFakeRepo) ListVideoIDsByChannel(_ context.Context, channelID uuid.UUID) ([]uuid.UUID, error) {
	var out []uuid.UUID
	for _, r := range f.videos {
		if r.ChannelID == channelID {
			out = append(out, r.ID)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out, nil
}

func (f *videoFakeRepo) ListVideosByChannel(_ context.Context, a sqlcgen.ListVideosByChannelParams) ([]sqlcgen.ListVideosByChannelRow, error) {
	channelID := a.ChannelID
	var out []sqlcgen.ListVideosByChannelRow
	for _, r := range f.videos {
		if r.ChannelID == channelID {
			out = append(out, sqlcgen.ListVideosByChannelRow{
				ID: r.ID, ShortCode: r.ShortCode, ChannelID: r.ChannelID, Title: r.Title, Description: r.Description,
				Privacy: r.Privacy, State: r.State, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
				PublishAt: r.PublishAt, IsSensitive: r.IsSensitive, SensitiveReason: r.SensitiveReason,
				Views: f.views[r.ID], HasThumbnail: f.hasThumb(r.ID),
				// The SQL selects EXISTS(video_blocks) here. The owner view is
				// the ONE listing that keeps a blocked row, so the fake must
				// carry the marker or a green handler test would prove nothing
				// (the slice-2 fake-fidelity lesson).
				Blocked: f.blockedFromFeed(r.ID),
				// ...and the reason beside it (A16 ruling). Same fake-fidelity
				// rule: the SQL's correlated subselect returns '' for an
				// unblocked row, so the fake must too or the owner-only test
				// would pass against a fake that leaks nothing.
				BlockReason: f.blockReasonFromFeed(r.ID),
			})
		}
	}
	// "published_at" is oldest-first, anything else newest-first — the query's
	// two CASE branches.
	sort.SliceStable(out, func(i, j int) bool {
		if a.Sort == "published_at" {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return pageRows(out, a.ResultLimit, a.ResultOffset), nil
}

func pageRows[T any](rows []T, limit, offset int32) []T {
	lo := min(int(offset), len(rows))
	rows = rows[lo:]
	if limit > 0 && int(limit) < len(rows) {
		rows = rows[:limit]
	}
	return rows
}

func (f *videoFakeRepo) ListDueScheduledVideos(_ context.Context, limit int32) ([]sqlcgen.ListDueScheduledVideosRow, error) {
	var rows []sqlcgen.ListDueScheduledVideosRow
	now := time.Now()
	for _, r := range f.videos {
		if r.State != "scheduled" || !r.PublishAt.Valid || r.PublishAt.Time.After(now) {
			continue
		}
		for _, vf := range f.files[r.ID] {
			if vf.Kind == "original" {
				rows = append(rows, sqlcgen.ListDueScheduledVideosRow{ID: r.ID, StorageKey: vf.StorageKey})
				break
			}
		}
	}
	if limit > 0 && int(limit) < len(rows) {
		rows = rows[:limit]
	}
	return rows, nil
}

func (f *videoFakeRepo) ListStuckTranscodingVideos(_ context.Context, a sqlcgen.ListStuckTranscodingVideosParams) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	for _, r := range f.videos {
		if r.State == "transcoding" && r.UpdatedAt.Before(a.Cutoff) {
			ids = append(ids, r.ID)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	if a.ResultLimit > 0 && int(a.ResultLimit) < len(ids) {
		ids = ids[:a.ResultLimit]
	}
	return ids, nil
}

// PublishTranscodingVideo mirrors the release CAS: published only while still
// held, reporting whether this call won the transition.
func (f *videoFakeRepo) PublishTranscodingVideo(_ context.Context, id uuid.UUID) (int64, error) {
	r, ok := f.videos[id]
	if !ok || r.State != "transcoding" {
		return 0, nil
	}
	r.State = "published"
	r.UpdatedAt = time.Now()
	f.videos[id] = r
	return 1, nil
}

func (f *videoFakeRepo) ListPublicVideosByChannel(_ context.Context, a sqlcgen.ListPublicVideosByChannelParams) ([]sqlcgen.ListPublicVideosByChannelRow, error) {
	channelID := a.ChannelID
	var out []sqlcgen.ListPublicVideosByChannelRow
	for _, r := range f.videos {
		if a.HideSensitive && r.IsSensitive {
			continue
		}
		if f.blockedFromFeed(r.ID) {
			continue
		}
		if r.ChannelID == channelID && r.Privacy == "public" && r.State == "published" {
			out = append(out, sqlcgen.ListPublicVideosByChannelRow{
				ID: r.ID, ShortCode: r.ShortCode, ChannelID: r.ChannelID, Title: r.Title, Description: r.Description,
				Privacy: r.Privacy, State: r.State, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
				Views: f.views[r.ID], HasThumbnail: f.hasThumb(r.ID),
				IsSensitive: r.IsSensitive, SensitiveReason: r.SensitiveReason,
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if a.Sort == "published_at" {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return pageRows(out, a.ResultLimit, a.ResultOffset), nil
}

func (f *videoFakeRepo) UpdateVideo(_ context.Context, a sqlcgen.UpdateVideoParams) (sqlcgen.Video, error) {
	r, ok := f.videos[a.ID]
	if !ok {
		return sqlcgen.Video{}, errors.New("not found")
	}
	if a.Title != nil {
		r.Title = *a.Title
	}
	if a.Description != nil {
		r.Description = *a.Description
	}
	if a.Privacy != nil {
		r.Privacy = *a.Privacy
	}
	if a.Category != nil {
		r.Category = a.Category
	}
	if a.Language != nil {
		r.Language = a.Language
	}
	if a.License != nil {
		r.License = a.License
	}
	if a.PublishAt.Valid {
		r.PublishAt = a.PublishAt
	}
	// COALESCE(narg, column): an absent value keeps what is there. Clearing back
	// to NULL is not expressible, exactly as in the real query.
	if a.OriginallyPublishedAt.Valid {
		r.OriginallyPublishedAt = a.OriginallyPublishedAt
	}
	if a.IsSensitive != nil {
		r.IsSensitive = *a.IsSensitive
	}
	if a.SensitiveReason != nil {
		r.SensitiveReason = *a.SensitiveReason
	}
	if a.CommentsPolicy != nil {
		r.CommentsPolicy = *a.CommentsPolicy
	}
	if a.DownloadEnabled != nil {
		r.DownloadEnabled = *a.DownloadEnabled
	}
	if a.PublishAfterTranscode != nil {
		r.PublishAfterTranscode = *a.PublishAfterTranscode
	}
	f.videos[a.ID] = r
	return vidRowToVideo(r), nil
}

func (f *videoFakeRepo) DeleteVideo(_ context.Context, id uuid.UUID) error {
	delete(f.videos, id)
	return nil
}

func (f *videoFakeRepo) CreateVideoFile(_ context.Context, a sqlcgen.CreateVideoFileParams) (sqlcgen.VideoFile, error) {
	if f.files == nil {
		f.files = map[uuid.UUID][]sqlcgen.VideoFile{}
	}
	vf := sqlcgen.VideoFile{
		ID: uuid.New(), VideoID: a.VideoID, Kind: a.Kind, StorageKey: a.StorageKey,
		ContentType: a.ContentType, OriginalName: a.OriginalName, SizeBytes: a.SizeBytes,
		Sha256: a.Sha256, CreatedAt: time.Now(),
	}
	f.files[a.VideoID] = append(f.files[a.VideoID], vf)
	return vf, nil
}

func (f *videoFakeRepo) GetVideoFileByKind(_ context.Context, a sqlcgen.GetVideoFileByKindParams) (sqlcgen.VideoFile, error) {
	var newest sqlcgen.VideoFile
	found := false
	for _, vf := range f.files[a.VideoID] {
		if vf.Kind == a.Kind && (!found || vf.CreatedAt.After(newest.CreatedAt)) {
			newest, found = vf, true
		}
	}
	if !found {
		// The production sentinel, not a generic error: video.AttachOriginal now
		// distinguishes "there is no prior original" (bill this upload) from
		// "the lookup failed" (fail the attempt so a retry re-reads), so a fake
		// that answers a miss with a database-fault-shaped error would make
		// every first upload look like an internal error.
		return sqlcgen.VideoFile{}, pgx.ErrNoRows
	}
	return newest, nil
}

func (f *videoFakeRepo) DeleteVideoFilesByVideoAndKind(_ context.Context, a sqlcgen.DeleteVideoFilesByVideoAndKindParams) error {
	kept := f.files[a.VideoID][:0]
	for _, vf := range f.files[a.VideoID] {
		if vf.Kind != a.Kind {
			kept = append(kept, vf)
		}
	}
	f.files[a.VideoID] = kept
	return nil
}

// UploadRequiresQuarantine mirrors the §11 gate query: the video's owner must
// have role 'user' without the admin-granted bypass_quarantine flag.
func (f *videoFakeRepo) UploadRequiresQuarantine(_ context.Context, id uuid.UUID) (bool, error) {
	r, ok := f.videos[id]
	if !ok {
		return false, errors.New("not found")
	}
	if f.users == nil {
		return false, nil
	}
	for _, u := range f.users.users {
		if u.ID == r.OwnerID {
			return u.Role == "user" && !u.BypassQuarantine, nil
		}
	}
	return false, nil
}

// ListQuarantinedVideos mirrors the moderation quarantine-queue query.
func (f *videoFakeRepo) ListQuarantinedVideos(_ context.Context, a sqlcgen.ListQuarantinedVideosParams) ([]sqlcgen.ListQuarantinedVideosRow, error) {
	var rows []sqlcgen.ListQuarantinedVideosRow
	for _, r := range f.videos {
		if r.State != "quarantined" {
			continue
		}
		handle, name := f.channelInfo(r.ChannelID)
		owner := ""
		if f.users != nil {
			for _, u := range f.users.users {
				if u.ID == r.OwnerID {
					owner = u.Username
				}
			}
		}
		rows = append(rows, sqlcgen.ListQuarantinedVideosRow{
			ID: r.ID, Title: r.Title, Privacy: r.Privacy, State: r.State, CreatedAt: r.CreatedAt,
			ChannelHandle: handle, ChannelDisplayName: name, OwnerUsername: owner,
		})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].CreatedAt.After(rows[j].CreatedAt) })
	lo := min(int(a.ResultOffset), len(rows))
	rows = rows[lo:]
	if a.ResultLimit > 0 && int(a.ResultLimit) < len(rows) {
		rows = rows[:a.ResultLimit]
	}
	return rows, nil
}

func (f *videoFakeRepo) SetVideoState(_ context.Context, a sqlcgen.SetVideoStateParams) (sqlcgen.Video, error) {
	r, ok := f.videos[a.ID]
	if !ok {
		return sqlcgen.Video{}, errors.New("not found")
	}
	r.State = a.State
	r.UpdatedAt = time.Now()
	f.videos[a.ID] = r
	return vidRowToVideo(r), nil
}

// hasAllTags/hasAnyTag mirror the ?tags_all_of / ?tags_one_of set filters. Tags
// reach the repo already lowercased, as they are in the table.
func (f *videoFakeRepo) hasAllTags(videoID uuid.UUID, want []string) bool {
	for _, t := range want {
		if !f.hasTag(videoID, t) {
			return false
		}
	}
	return true
}

func (f *videoFakeRepo) hasAnyTag(videoID uuid.UUID, want []string) bool {
	for _, t := range want {
		if f.hasTag(videoID, t) {
			return true
		}
	}
	return false
}

// durationInRange mirrors the SQL's duration bounds, including the part that is
// easy to get wrong: an UNKNOWN duration fails a bound rather than passing it,
// because `NULL >= 60` is NULL, not true.
func durationInRange(d *int32, lo, hi *int32) bool {
	if lo == nil && hi == nil {
		return true
	}
	if d == nil {
		return false
	}
	return (lo == nil || *d >= *lo) && (hi == nil || *d <= *hi)
}

func (f *videoFakeRepo) SearchPublicVideos(_ context.Context, a sqlcgen.SearchPublicVideosParams) ([]sqlcgen.SearchPublicVideosRow, error) {
	q := strings.ToLower(a.Query)
	// Any active taxonomy/tag-set filter excludes remote rows, exactly as the
	// SQL's remote arm does.
	localOnly := a.Tag != nil || a.Category != nil || a.Language != nil ||
		len(a.TagsAllOf) > 0 || len(a.TagsOneOf) > 0
	var all []sqlcgen.SearchPublicVideosRow
	for _, r := range f.videos {
		if a.HideSensitive && r.IsSensitive {
			continue // sensitive-content policy "hide" mirrors the SQL filter
		}
		if a.Tag != nil && !f.hasTag(r.ID, *a.Tag) {
			continue
		}
		if a.Category != nil && (r.Category == nil || *r.Category != *a.Category) {
			continue
		}
		if a.Language != nil && (r.Language == nil || *r.Language != *a.Language) {
			continue
		}
		if len(a.TagsAllOf) > 0 && !f.hasAllTags(r.ID, a.TagsAllOf) {
			continue
		}
		if len(a.TagsOneOf) > 0 && !f.hasAnyTag(r.ID, a.TagsOneOf) {
			continue
		}
		if f.blockedFromFeed(r.ID) {
			continue
		}
		if r.Privacy == "public" && r.State == "published" &&
			(strings.Contains(strings.ToLower(r.Title), q) || f.tagMatches(r.ID, q)) &&
			!f.mutedFromFeed(a.ViewerID, r.ChannelID) && !f.ownerUnlisted(r.ChannelID) {
			all = append(all, sqlcgen.SearchPublicVideosRow{
				ID: r.ID, ShortCode: r.ShortCode, ChannelID: r.ChannelID, Title: r.Title, Description: r.Description,
				Privacy: r.Privacy, State: r.State, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
				Views: f.views[r.ID], HasThumbnail: f.hasThumb(r.ID),
				AuthorDisplayName: f.authorName(r.ChannelID),
				DurationSeconds:   f.durationOf(r.ID),
				IsSensitive:       r.IsSensitive,
				SensitiveReason:   r.SensitiveReason,
			})
		}
	}
	// Remote branch of the UNION (remote-content §4): seeded remote cards,
	// title-matched like the SQL.
	if !localOnly {
		for _, rr := range f.remoteSearch {
			if strings.Contains(strings.ToLower(rr.Title), q) {
				all = append(all, rr)
			}
		}
	}
	// The outer WHERE: duration and the publish window narrow BOTH arms.
	kept := all[:0]
	for _, r := range all {
		if !durationInRange(r.DurationSeconds, a.DurationMin, a.DurationMax) {
			continue
		}
		if a.PublishedAfter.Valid && r.CreatedAt.Before(a.PublishedAfter.Time) {
			continue
		}
		if a.PublishedBefore.Valid && r.CreatedAt.After(a.PublishedBefore.Time) {
			continue
		}
		kept = append(kept, r)
	}
	all = kept
	// The ORDER BY. The fake has no trigram similarity, so 'relevance' keeps the
	// created_at tiebreak the SQL falls through to — which is also what the
	// endpoint returned before it took a sort at all.
	sort.SliceStable(all, func(i, j int) bool {
		switch a.Sort {
		case "published_at":
			return all[i].CreatedAt.Before(all[j].CreatedAt)
		case "views":
			if all[i].Views != all[j].Views {
				return all[i].Views < all[j].Views
			}
		case "-views":
			if all[i].Views != all[j].Views {
				return all[i].Views > all[j].Views
			}
		}
		return all[i].CreatedAt.After(all[j].CreatedAt)
	})
	lo := int(a.ResultOffset)
	if lo > len(all) {
		lo = len(all)
	}
	hi := lo + int(a.ResultLimit)
	if hi > len(all) {
		hi = len(all)
	}
	return all[lo:hi], nil
}

// ListPublicVideosByIDs hydrates a set of ids under the (simplified) canonical
// predicate, mirroring the real query for the search-service W4 handlers.
func (f *videoFakeRepo) ListPublicVideosByIDs(_ context.Context, a sqlcgen.ListPublicVideosByIDsParams) ([]sqlcgen.ListPublicVideosByIDsRow, error) {
	want := make(map[uuid.UUID]bool, len(a.Ids))
	for _, id := range a.Ids {
		want[id] = true
	}
	var rows []sqlcgen.ListPublicVideosByIDsRow
	for _, r := range f.videos {
		if !want[r.ID] || r.Privacy != "public" || r.State != "published" {
			continue
		}
		if f.blockedFromFeed(r.ID) {
			continue
		}
		if a.HideSensitive && r.IsSensitive {
			continue
		}
		if f.mutedFromFeed(a.ViewerID, r.ChannelID) || f.ownerUnlisted(r.ChannelID) {
			continue
		}
		rows = append(rows, sqlcgen.ListPublicVideosByIDsRow{
			ID: r.ID, ShortCode: r.ShortCode, ChannelID: r.ChannelID, Title: r.Title, Description: r.Description,
			Privacy: r.Privacy, State: r.State, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
			Views: f.views[r.ID], HasThumbnail: f.hasThumb(r.ID),
			AuthorDisplayName: f.authorName(r.ChannelID), IsSensitive: r.IsSensitive,
			SensitiveReason: r.SensitiveReason,
		})
	}
	return rows, nil
}

// ListRelatedVideosFallback mirrors the server-side related heuristic.
func (f *videoFakeRepo) ListRelatedVideosFallback(_ context.Context, a sqlcgen.ListRelatedVideosFallbackParams) ([]sqlcgen.ListRelatedVideosFallbackRow, error) {
	var rows []sqlcgen.ListRelatedVideosFallbackRow
	for _, r := range f.videos {
		if r.ID == a.ExcludeID || r.Privacy != "public" || r.State != "published" {
			continue
		}
		if f.blockedFromFeed(r.ID) {
			continue
		}
		if a.HideSensitive && r.IsSensitive {
			continue
		}
		if f.mutedFromFeed(a.ViewerID, r.ChannelID) || f.ownerUnlisted(r.ChannelID) {
			continue
		}
		sameChannel := r.ChannelID == a.ChannelID
		matchCat := a.Category != nil && r.Category != nil && *r.Category == *a.Category
		if !sameChannel && !matchCat {
			continue
		}
		rows = append(rows, sqlcgen.ListRelatedVideosFallbackRow{
			ID: r.ID, ShortCode: r.ShortCode, ChannelID: r.ChannelID, Title: r.Title, Description: r.Description,
			Privacy: r.Privacy, State: r.State, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
			Views: f.views[r.ID], HasThumbnail: f.hasThumb(r.ID),
			AuthorDisplayName: f.authorName(r.ChannelID), IsSensitive: r.IsSensitive,
			SensitiveReason: r.SensitiveReason,
			SameChannel:     sameChannel,
		})
	}
	if int(a.ResultLimit) < len(rows) {
		rows = rows[:a.ResultLimit]
	}
	return rows, nil
}

// ListAdminVideos returns all videos (any privacy/state) with the current block
// status, mirroring the real admin overview query. An optional title filter.
// adminInventory mirrors the real ListAdminVideos/CountAdminVideos pair: it
// builds the unfiltered inventory, applies EVERY filter, then sorts. Both fake
// methods go through it for the same reason the SQL pair shares its WHERE — a
// total that counts a different set than the page it labels is worse than none.
func (f *videoFakeRepo) adminInventory(a sqlcgen.ListAdminVideosParams) []sqlcgen.ListAdminVideosRow {
	contains := func(set []string, v string) bool {
		for _, x := range set {
			if x == v {
				return true
			}
		}
		return false
	}
	var rows []sqlcgen.ListAdminVideosRow
	for _, r := range f.videos {
		if a.Query != nil && !strings.Contains(strings.ToLower(r.Title), strings.ToLower(*a.Query)) {
			continue
		}
		if len(a.States) > 0 && !contains(a.States, r.State) {
			continue
		}
		if len(a.Privacies) > 0 && !contains(a.Privacies, r.Privacy) {
			continue
		}
		// The fake holds only local videos, so scope=remote matches nothing.
		if a.Scope == "remote" {
			continue
		}
		blocked := false
		if f.blocks != nil {
			blocked, _ = f.blocks.IsVideoBlocked(context.Background(), r.ID)
		}
		ch, cn := f.channelInfo(r.ChannelID)
		if a.Channel != nil && ch != *a.Channel {
			continue
		}
		if a.PublishedAfter.Valid && r.CreatedAt.Before(a.PublishedAfter.Time) {
			continue
		}
		if a.PublishedBefore.Valid && r.CreatedAt.After(a.PublishedBefore.Time) {
			continue
		}
		var duration *int32
		if md, ok := f.metadata[r.ID]; ok {
			duration = md.DurationSeconds
		}
		var hasThumbnail, hasOriginal bool
		var sizeBytes int64
		var webVideoCount int32
		for _, file := range f.files[r.ID] {
			hasThumbnail = hasThumbnail || file.Kind == "thumbnail"
			hasOriginal = hasOriginal || file.Kind == "original"
			if file.Kind == "rendition" || file.Kind == "webm" {
				webVideoCount++
			}
			sizeBytes += file.SizeBytes
		}
		if a.HasOriginal != nil && hasOriginal != *a.HasOriginal {
			continue
		}
		if a.HasHls != nil && *a.HasHls {
			continue // the fake records no renditions, so hls_count is always 0
		}
		if a.HasWebFiles != nil && (webVideoCount > 0) != *a.HasWebFiles {
			continue
		}
		rows = append(rows, sqlcgen.ListAdminVideosRow{
			ID: r.ID, Title: r.Title, Privacy: r.Privacy, State: r.State,
			ChannelHandle: ch, ChannelDisplayName: cn,
			Views: f.views[r.ID], CreatedAt: r.CreatedAt, DurationSeconds: duration,
			IsLocal: true, IsSensitive: r.IsSensitive, HasThumbnail: hasThumbnail,
			HasOriginal: hasOriginal, WebVideoCount: webVideoCount,
			SizeBytes: sizeBytes, Blocked: blocked,
			ModerationNote: f.rejections[r.ID], // '' when never rejected, as COALESCE returns
		})
	}
	less := func(i, j int) bool { return rows[i].CreatedAt.After(rows[j].CreatedAt) }
	switch a.Sort {
	case "created_at", "published_at":
		less = func(i, j int) bool { return rows[i].CreatedAt.Before(rows[j].CreatedAt) }
	case "views":
		less = func(i, j int) bool { return rows[i].Views < rows[j].Views }
	case "-views":
		less = func(i, j int) bool { return rows[i].Views > rows[j].Views }
	case "title":
		less = func(i, j int) bool { return rows[i].Title < rows[j].Title }
	case "-title":
		less = func(i, j int) bool { return rows[i].Title > rows[j].Title }
	case "state":
		less = func(i, j int) bool { return rows[i].State < rows[j].State }
	case "-state":
		less = func(i, j int) bool { return rows[i].State > rows[j].State }
	case "size_bytes":
		less = func(i, j int) bool { return rows[i].SizeBytes < rows[j].SizeBytes }
	case "-size_bytes":
		less = func(i, j int) bool { return rows[i].SizeBytes > rows[j].SizeBytes }
	}
	sort.SliceStable(rows, less)
	return rows
}

func (f *videoFakeRepo) ListAdminVideos(_ context.Context, a sqlcgen.ListAdminVideosParams) ([]sqlcgen.ListAdminVideosRow, error) {
	rows := f.adminInventory(a)
	lo := min(int(a.ResultOffset), len(rows))
	rows = rows[lo:]
	if a.ResultLimit > 0 && int(a.ResultLimit) < len(rows) {
		rows = rows[:a.ResultLimit]
	}
	return rows, nil
}

func (f *videoFakeRepo) CountAdminVideos(_ context.Context, a sqlcgen.CountAdminVideosParams) (int64, error) {
	return int64(len(f.adminInventory(sqlcgen.ListAdminVideosParams{
		Query: a.Query, States: a.States, Privacies: a.Privacies, Scope: a.Scope,
		Channel: a.Channel, PublishedAfter: a.PublishedAfter, PublishedBefore: a.PublishedBefore,
		HasOriginal: a.HasOriginal, HasHls: a.HasHls, HasWebFiles: a.HasWebFiles,
	}))), nil
}

func (f *videoFakeRepo) hasThumb(id uuid.UUID) bool {
	for _, vf := range f.files[id] {
		if vf.Kind == "thumbnail" {
			return true
		}
	}
	return false
}

// channelInfo reverse-looks-up a channel's handle + display name by id, mirroring
// the JOIN the real feed/card queries do.
func (f *videoFakeRepo) channelInfo(channelID uuid.UUID) (handle, displayName string) {
	if f.channels == nil {
		return "", ""
	}
	for _, c := range f.channels.byHandle {
		if c.ID == channelID {
			return c.Handle, c.DisplayName
		}
	}
	return "", ""
}

// channelOwner returns the owner (account id) of a channel, mirroring the real
// videos→channels join used for mute-filtering.
func (f *videoFakeRepo) channelOwner(channelID uuid.UUID) uuid.UUID {
	if f.channels == nil {
		return uuid.Nil
	}
	for _, c := range f.channels.byHandle {
		if c.ID == channelID {
			return c.OwnerID
		}
	}
	return uuid.Nil
}

// mutedFromFeed reports whether an authenticated viewer has muted OR blocked
// the owner of the given channel — mirrors the feed queries' per-viewer mute
// filter plus the §13 user_blocks extension.
func (f *videoFakeRepo) mutedFromFeed(viewer pgtype.UUID, channelID uuid.UUID) bool {
	if !viewer.Valid {
		return false
	}
	owner := f.channelOwner(channelID)
	if f.mutes != nil && f.mutes.isMuted(uuid.UUID(viewer.Bytes), owner) {
		return true
	}
	return f.userBlocks != nil && f.userBlocks.isBlocked(uuid.UUID(viewer.Bytes), owner)
}

// blockedFromFeed reports whether a moderator has blocked the video — the
// `NOT EXISTS (SELECT 1 FROM video_blocks ...)` predicate that every public
// discovery query carries. It lived only in adminInventory before, so the feed,
// channel, subscription, search and related fakes all answered as if a block
// changed nothing about what a viewer sees, and no handler test could tell the
// difference.
func (f *videoFakeRepo) blockedFromFeed(videoID uuid.UUID) bool {
	if f.blocks == nil {
		return false
	}
	blocked, _ := f.blocks.IsVideoBlocked(context.Background(), videoID)
	return blocked
}

// blockReasonFromFeed mirrors the owner listing's correlated subselect on
// video_blocks.reason: the moderator's prose while a block stands, and "" the
// moment it is lifted.
func (f *videoFakeRepo) blockReasonFromFeed(videoID uuid.UUID) string {
	if f.blocks == nil {
		return ""
	}
	return f.blocks.blockReason(videoID)
}

func (f *videoFakeRepo) ListPublicVideosSorted(_ context.Context, a sqlcgen.ListPublicVideosSortedParams) ([]sqlcgen.ListPublicVideosSortedRow, error) {
	var rows []sqlcgen.ListPublicVideosSortedRow
	for _, r := range f.videos {
		if a.Tag != nil && !f.hasTag(r.ID, *a.Tag) {
			continue
		}
		if a.Category != nil && (r.Category == nil || *r.Category != *a.Category) {
			continue
		}
		if a.Language != nil && (r.Language == nil || *r.Language != *a.Language) {
			continue
		}
		if a.HideSensitive && r.IsSensitive {
			continue // sensitive-content policy "hide" mirrors the SQL filter
		}
		if f.blockedFromFeed(r.ID) {
			continue
		}
		if r.Privacy == "public" && r.State == "published" && !f.mutedFromFeed(a.ViewerID, r.ChannelID) &&
			!f.ownerUnlisted(r.ChannelID) {
			ch, cn := f.channelInfo(r.ChannelID)
			rows = append(rows, sqlcgen.ListPublicVideosSortedRow{
				ID: r.ID, ShortCode: r.ShortCode, ChannelID: r.ChannelID, Title: r.Title, Description: r.Description,
				Privacy: r.Privacy, State: r.State, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
				Views: f.views[r.ID], HasThumbnail: f.hasThumb(r.ID),
				ChannelHandle: ch, ChannelDisplayName: cn,
				AuthorDisplayName: f.authorName(r.ChannelID),
				IsSensitive:       r.IsSensitive,
				SensitiveReason:   r.SensitiveReason,
			})
		}
	}
	// Remote branch of the UNION (remote-content §4): only with scope=all
	// (IncludeRemote) and no local-taxonomy filters, like the SQL.
	if a.IncludeRemote && a.Tag == nil && a.Category == nil && a.Language == nil {
		rows = append(rows, f.remoteFeed...)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		switch a.Sort {
		case "popular":
			if rows[i].Views != rows[j].Views {
				return rows[i].Views > rows[j].Views
			}
		case "trending":
			si := float64(rows[i].Views) / math.Pow(time.Since(rows[i].CreatedAt).Hours()+2, 1.5)
			sj := float64(rows[j].Views) / math.Pow(time.Since(rows[j].CreatedAt).Hours()+2, 1.5)
			if si != sj {
				return si > sj
			}
		}
		return rows[i].CreatedAt.After(rows[j].CreatedAt)
	})
	lo := int(a.ResultOffset)
	if lo > len(rows) {
		lo = len(rows)
	}
	hi := lo + int(a.ResultLimit)
	if hi > len(rows) {
		hi = len(rows)
	}
	return rows[lo:hi], nil
}

func videoServer(t *testing.T) *Server { return videoServerCfg(t, testConfig()) }

func videoServerCfg(t *testing.T, cfg *config.Config, opts ...video.Option) *Server {
	srv, _, _, _ := videoServerEnv(t, cfg, opts...)
	return srv
}

// videoServerEnv builds the full test server and also returns the blob backend,
// the in-memory transcode repo, and the in-memory notification repo so tests
// can seed stored media / playlist state directly (the HLS tests need the first
// two) or inject notification failures (the report-resolution test).
func videoServerEnv(t *testing.T, cfg *config.Config, opts ...video.Option) (*Server, storage.Backend, *transcodeFakeRepo, *notifFakeRepo) {
	srv, blobs, tcRepo, notifRepo, _ := videoServerFull(t, cfg, opts...)
	return srv, blobs, tcRepo, notifRepo
}

// videoServerFull is videoServerEnv plus the in-memory video repo, for tests
// that need to manipulate stored video state directly (e.g. rewinding a
// scheduled publish_at so the sweeper sees it as due).
func videoServerFull(t *testing.T, cfg *config.Config, opts ...video.Option) (*Server, storage.Backend, *transcodeFakeRepo, *notifFakeRepo, *videoFakeRepo) {
	return videoServerFullWith(t, cfg, nil, opts...)
}

// videoServerFullWith is videoServerFull with extra httpapi options (e.g. a fake
// IPFS mirror), so a test can exercise the additive IPFS serving fields end to end.
func videoServerFullWith(t *testing.T, cfg *config.Config, httpOpts []Option, opts ...video.Option) (*Server, storage.Backend, *transcodeFakeRepo, *notifFakeRepo, *videoFakeRepo) {
	t.Helper()
	// The replace-completion hook reaches back into the Server that is built at
	// the bottom of this function (cmd/api wires the same hook against its own
	// transcode service). A late-bound ref is the same trick settingsRef uses.
	var srvRef *Server
	chRepo := newChannelFakeRepo()
	authRepo := newAuthFakeRepo()
	chRepo.users = authRepo // resolve member-invite usernames against the auth fake
	issuer := auth.NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", 15*time.Minute)
	// The auth service reads the sign-up & new-user settings (config-parity W7)
	// through late-bound refs: the settings service is constructed further down
	// (it needs the config defaults mapping), so the funcs tolerate nil until
	// then. The verification-gate fn mirrors ONLY the runtime setting — in
	// production cmd/api folds the mail capability in; here the HTTP layer's
	// registrationRequiresEmailVerification (contactMailer != nil) is the mail
	// gate, and pending accounts can only be minted through it. The capture
	// mailer records verification tokens so tests can complete the round trip.
	var settingsRef *instancesettings.Service
	captureMailer := auth.NewCaptureMailer()
	authsvc := auth.NewService(authRepo, issuer, 720*time.Hour,
		auth.WithMailer(captureMailer),
		auth.WithNewUserHistoryEnabledFunc(func() bool {
			if settingsRef == nil {
				return true
			}
			return settingsRef.Bool(instancesettings.KeyNewUserHistoryEnabled)
		}),
		auth.WithEmailVerificationGateFunc(func() bool {
			return settingsRef != nil && settingsRef.Bool(instancesettings.KeyRegistrationRequireEmailVerification)
		}),
	)
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("storage.NewLocal: %v", err)
	}
	repo := &videoFakeRepo{
		channels: chRepo,
		videos:   map[uuid.UUID]sqlcgen.GetVideoByIDRow{},
		files:    map[uuid.UUID][]sqlcgen.VideoFile{},
		metadata: map[uuid.UUID]sqlcgen.VideoMetadatum{},
		views:    map[uuid.UUID]int64{},
	}
	notifRepo := &notifFakeRepo{auth: authRepo, channels: chRepo, videos: repo}
	plRepo := &playlistFakeRepo{videos: repo, playlists: map[uuid.UUID]sqlcgen.Playlist{}, items: map[uuid.UUID][]uuid.UUID{}}
	muteRepo := &muteFakeRepo{auth: authRepo}
	repo.mutes = muteRepo
	// One shared user-blocks fake backs the block service AND the §13
	// content-hiding mirrors in the video/comment fakes.
	userBlockRepo := &blockFakeRepo{auth: authRepo}
	repo.userBlocks = userBlockRepo
	// The channel- and account-search fakes apply the same per-viewer
	// mute/block predicates their SQL does, so they read the same two fakes.
	chRepo.mutes, chRepo.userBlocks = muteRepo, userBlockRepo
	authRepo.mutes, authRepo.userBlocks = muteRepo, userBlockRepo
	cmRepo := &commentFakeRepo{users: authRepo, mutes: muteRepo, userBlocks: userBlockRepo, videos: repo}
	// The reply-notification recipient is resolved from the comment thread, so
	// the notification fake reads the same comment/mute/block fakes its SQL joins.
	notifRepo.comments = cmRepo
	modRepo := &moderationFakeRepo{auth: authRepo, videos: repo, comments: cmRepo}
	repo.blocks = modRepo
	ratingRepo := newRatingFakeRepo()
	repo.ratings = ratingRepo
	repo.commentsRepo = cmRepo
	repo.users = authRepo
	notifRepo.reports = modRepo
	msgRepo := newMessagingFakeRepo(authRepo)
	blocksvc := block.NewService(userBlockRepo)
	tcRepo := newTranscodeFakeRepo()
	uploadRepo := newUploadFakeRepo()
	finalizeRepo := newFinalizeFakeRepo()
	importRepo := newImportFakeRepo()
	// Wire storage-usage aggregation over the video fake's files (mirrors the
	// SumUserStorageUsage SQL: every file of every video owned via the user's
	// channels), so the quota service and admin view see real usage.
	authRepo.usage = func(owner uuid.UUID) int64 {
		var sum int64
		for vid, files := range repo.files {
			v, ok := repo.videos[vid]
			if !ok || v.OwnerID != owner {
				continue
			}
			for _, vf := range files {
				sum += vf.SizeBytes
			}
		}
		return sum
	}
	// Instance-wide overview aggregates (admin.Repository.Stats), wired to the
	// same fakes so GET /admin/stats reflects created state. Federated peers has
	// no fake in this harness → 0.
	authRepo.statPublicVideos = func() int64 {
		var n int64
		for _, v := range repo.videos {
			if v.Privacy == "public" && v.State == "published" {
				n++
			}
		}
		return n
	}
	authRepo.statAllStorage = func() int64 {
		var sum int64
		for _, files := range repo.files {
			for _, vf := range files {
				sum += vf.SizeBytes
			}
		}
		return sum
	}
	authRepo.statComments = func() int64 { return int64(len(cmRepo.comments)) }
	liveRepo := newLiveFakeRepo(chRepo)
	// Daily-upload accounting (config-parity W7): the recorder feeds the quota
	// service's rolling ledger from AttachOriginal, late-bound because the
	// quota service is constructed after the video service (as in cmd/api,
	// where construction order is inverted instead).
	var quotaRef *quota.Service
	opts = append(opts, video.WithUploadUsageRecorder(func(ctx context.Context, ownerID uuid.UUID, bytes int64) error {
		if quotaRef != nil {
			_ = quotaRef.RecordUpload(ctx, ownerID, bytes)
		}
		return nil
	}))
	// Instance publish defaults (config-parity W9), mirroring cmd/api: unset
	// create fields seed from the defaults.publish overlay. Late-bound like the
	// auth funcs above (the settings service is constructed after the video
	// service here).
	opts = append(opts, video.WithPublishDefaultsFunc(func() video.PublishDefaults {
		if settingsRef == nil {
			return video.PublishDefaults{Privacy: "private", CommentsPolicy: video.CommentsPolicyEnabled, DownloadEnabled: true}
		}
		def := video.PublishDefaults{
			Privacy:         settingsRef.String(instancesettings.KeyDefaultVideoPrivacy),
			CommentsPolicy:  settingsRef.String(instancesettings.KeyDefaultCommentPolicy),
			DownloadEnabled: settingsRef.Bool(instancesettings.KeyDefaultDownloadEnabled),
		}
		if licence := settingsRef.Int(instancesettings.KeyDefaultVideoLicence); licence != 0 {
			def.License = strconv.FormatInt(licence, 10)
		}
		return def
	}))
	// New-video follower notifications, mirroring cmd/api: THE publish transition
	// fans the "new video from a channel you follow" notification out to the
	// channel's followers, best-effort. Wiring it here is what lets the handler
	// tests prove the whole round trip — publish a video, the follower's
	// notification list shows it — rather than just the fan-out in isolation.
	notifsvc := notification.NewService(notifRepo)
	opts = append(opts, video.WithPublishHook(func(ctx context.Context, videoID uuid.UUID) {
		_, _ = notifsvc.NotifyNewVideo(ctx, videoID)
	}))
	videosvc := video.NewService(repo, blobs, opts...)
	// DB-backed instance-settings overlay: an in-memory fake repo, seeded with the
	// config defaults. With no overrides the effective values equal the config, so
	// wiring it is behaviour-preserving for existing tests; the P10 tests flip
	// settings through PATCH /admin/instance-settings. Built FIRST so the
	// quota/upload/import services can resolve their runtime-overridable limits
	// from it (the operational-limits config-parity slice).
	settingssvc := instancesettings.NewService(newInstanceSettingsFakeRepo(), settingsDefaultsFromConfig(cfg))
	if err := settingssvc.Load(context.Background()); err != nil {
		t.Fatalf("settings load: %v", err)
	}
	settingsRef = settingssvc
	quotasvc := quota.NewService(authRepo, cfg.InstanceDefaultQuotaBytes,
		quota.WithDefaultBytesFunc(func() int64 { return settingssvc.Int(instancesettings.KeyDefaultUserQuotaBytes) }),
		quota.WithDailyBytesFunc(func() int64 { return settingssvc.Int(instancesettings.KeyDefaultUserDailyQuotaBytes) }))
	quotaRef = quotasvc
	importMaxBytes, _ := gommonbytes.Parse(cfg.UploadMaxSize)
	// The import service uses a plain client so unit tests reach the loopback
	// httptest origin (the production SSRF guard, tested in the videoimport
	// package, refuses it). A tiny chunk size lets small fixtures exercise the
	// multi-chunk resumable path over HTTP.
	importsvc := videoimport.NewService(importRepo, videosvc, importMaxBytes,
		videoimport.WithAllowPrivateFetch(cfg.ImportAllowPrivateURLs),
		videoimport.WithQuota(quotasvc),
		videoimport.WithHTTPClient(&http.Client{}),
		videoimport.WithMaxBytesFunc(func() int64 { return settingssvc.Int(instancesettings.KeyUploadMaxSizeBytes) }),
	)
	uploadsvc := upload.NewService(uploadRepo, blobs, upload.WithChunkSize(16),
		upload.WithMaxActiveSessions(cfg.UploadMaxActiveSessionsPerUser),
		upload.WithMaxActiveSessionsFunc(func() int { return int(settingssvc.Int(instancesettings.KeyUploadMaxActiveSessionsPerUser)) }))
	// Asynchronous upload completion (migration 0120): POST .../complete only
	// validates + enqueues, so handler tests that need the pipeline to have run
	// drain this queue themselves (drainFinalize) instead of the worker doing it.
	uploadfinalizesvc := uploadfinalize.NewService(finalizeRepo, uploadsvc, videosvc,
		uploadfinalize.WithReplaceHook(func(ctx context.Context, videoID uuid.UUID, sourceKey string) {
			srvRef.orchestrateReplaceTranscode(ctx, videoID, sourceKey)
		}))
	// Auto-caption (Whisper) service: enabled follows cfg.WhisperEnabled so the
	// request endpoint returns 202 (enabled) or 503 (disabled). The transcriber is
	// nil — handler tests exercise only enqueue/status, never the worker; the
	// videosvc satisfies captionjob.VideoStore.
	captionjobsvc := captionjob.NewService(newCaptionJobFakeRepo(), videosvc, nil,
		captionjob.WithEnabled(cfg.WhisperEnabled))
	serverOpts := []Option{
		WithSettingsService(settingssvc),
		WithAuthService(authsvc, 15*time.Minute),
		// The per-user channel cap follows the overlay (max_channels_per_user,
		// W8), mirroring cmd/api's wiring; default 0 = unlimited.
		WithChannelService(channel.NewService(chRepo,
			channel.WithMaxPerUserFunc(func() int64 { return settingssvc.Int(instancesettings.KeyMaxChannelsPerUser) }))),
		WithVideoService(videosvc),
		WithCommentService(comment.NewService(cmRepo)),
		WithRatingService(rating.NewService(ratingRepo)),
		WithNotificationService(notifsvc),
		WithPlayerSettingsService(playersettings.NewService(newPlayerSettingsFakeRepo(),
			playersettings.WithVideoCardPreviewsDefaultEnabledFunc(func() bool {
				return settingssvc.Bool(instancesettings.KeyVideoCardPreviewsDefaultEnabled)
			}))),
		WithPlaylistService(playlist.NewService(plRepo, playlist.WithStorage(blobs))),
		WithMediaGCService(mediagc.NewService(&mediagcFakeRepo{}, blobs)),
		WithModerationService(moderation.NewService(modRepo)),
		WithMuteService(mute.NewService(muteRepo)),
		WithBlockService(blocksvc),
		WithWatchWordService(watchword.NewService(&watchwordFakeRepo{auth: authRepo, videos: repo})),
		WithAdminService(admin.NewService(authRepo)),
		WithMessagingService(messaging.NewService(msgRepo, messaging.WithBlocker(blocksvc),
			messaging.WithAttachments(blobs, nil, 0))),
		WithE2EEService(e2ee.NewService(newE2EEFakeRepo(authRepo, msgRepo), e2ee.WithBlocker(blocksvc))),
		// Live enforcement knobs follow the overlay (config-parity W11),
		// mirroring cmd/api's wiring: replay gate, simultaneous-live caps at
		// the ingest hooks, and the duration-watchdog limit.
		WithLiveService(live.NewService(liveRepo,
			live.WithAllowReplayFunc(func() bool { return settingssvc.Bool(instancesettings.KeyLiveAllowReplay) }),
			live.WithMaxInstanceLivesFunc(func() int64 { return settingssvc.Int(instancesettings.KeyLiveMaxInstanceLives) }),
			live.WithMaxUserLivesFunc(func() int64 { return settingssvc.Int(instancesettings.KeyLiveMaxUserLives) }),
			live.WithMaxDurationSecsFunc(func() int64 { return settingssvc.Int(instancesettings.KeyLiveMaxDurationSecs) }))),
		WithQuotaService(quotasvc),
		WithTranscodeService(transcode.NewService(tcRepo, nil)),
		WithUploadService(uploadsvc),
		WithUploadFinalizeService(uploadfinalizesvc),
		WithVideoImportService(importsvc),
		WithCaptionJobService(captionjobsvc),
		WithInstanceModerationService(instancemod.NewService(newInstanceModFakeRepo())),
		WithMediaStorage(blobs),
	}
	// Apply extra httpapi options BEFORE New() registers routes, so options that
	// affect route middleware (e.g. WithAuthRateLimiter) take effect.
	serverOpts = append(serverOpts, httpOpts...)
	srv := New(cfg, nil, nil, serverOpts...)
	srvRef = srv
	liveFakeRepoBySrv[srv] = liveRepo
	finalizeSvcBySrv[srv] = uploadfinalizesvc
	finalizeRepoBySrv[srv] = finalizeRepo
	captureMailerBySrv[srv] = captureMailer
	return srv, blobs, tcRepo, notifRepo, repo
}

// captureMailerBySrv lets a test reach the capture mailer behind a harness
// server (e.g. to read the verification token minted by a gated signup, W7).
var captureMailerBySrv = map[*Server]*auth.CaptureMailer{}

// fakeProber lets handler tests drive the publish/fail outcome and metadata of
// an upload.
type fakeProber struct {
	md  media.Metadata
	err error
}

func (p fakeProber) Probe(_ context.Context, _ string) (media.Metadata, error) { return p.md, p.err }

// createChannelFor registers a user, creates a channel, and returns (token, handle).
func createChannelFor(t *testing.T, srv *Server, username, email, handle string) string {
	t.Helper()
	tok := registerAndToken(t, srv, `{"username":"`+username+`","email":"`+email+`","password":"supersecret"}`)
	rec := postJSONAuth(srv, "/api/v1/channels", `{"handle":"`+handle+`","display_name":"`+handle+`"}`, tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create channel %s = %d; body=%s", handle, rec.Code, rec.Body.String())
	}
	return tok
}

func TestCreateVideoRequiresAuth(t *testing.T) {
	srv := videoServer(t)
	rec := postTo(srv, "/api/v1/channels/ada/videos", `{"title":"Hi"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestCreateVideoValidation(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	rec := postJSONAuth(srv, "/api/v1/channels/ada/videos", `{"title":""}`, tok)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
}

func TestCreateVideoNonOwnerForbidden(t *testing.T) {
	srv := videoServer(t)
	_ = createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	otherTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	rec := postJSONAuth(srv, "/api/v1/channels/ada/videos", `{"title":"Hi"}`, otherTok)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestCreateVideoUnknownChannel404(t *testing.T) {
	srv := videoServer(t)
	tok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	rec := postJSONAuth(srv, "/api/v1/channels/ghost/videos", `{"title":"Hi"}`, tok)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// TestCreateVideoSeedsPublishDefaults: an omitted privacy seeds the
// default_video_privacy instance setting (registry default "private",
// preserving the pre-W9 omit-means-private behaviour; admins opt into
// public-by-default via the knob). State still starts at draft.
func TestCreateVideoSeedsPublishDefaults(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	rec := postJSONAuth(srv, "/api/v1/channels/ada/videos", `{"title":"My Draft"}`, tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var v videoView
	_ = json.Unmarshal(rec.Body.Bytes(), &v)
	if v.Privacy != "private" || v.State != "draft" {
		t.Errorf("unexpected video: %+v", v)
	}
	if v.CommentsPolicy != "enabled" {
		t.Errorf("comments_policy = %q, want enabled (registry default)", v.CommentsPolicy)
	}
	if v.DownloadEnabled == nil || !*v.DownloadEnabled {
		t.Errorf("download_enabled = %v, want true (registry default)", v.DownloadEnabled)
	}
	if v.License != nil {
		t.Errorf("license = %v, want unset (default_video_licence 0 = no default)", *v.License)
	}
}

// createVideo returns the created video's id.
func createVideo(t *testing.T, srv *Server, token, handle, body string) string {
	t.Helper()
	rec := postJSONAuth(srv, "/api/v1/channels/"+handle+"/videos", body, token)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create video = %d; body=%s", rec.Code, rec.Body.String())
	}
	var v videoView
	_ = json.Unmarshal(rec.Body.Bytes(), &v)
	return v.ID
}

// createPublishedVideo creates a video and uploads a tiny original so it lands
// published (the default harness has no prober, so Process publishes directly).
// Only published videos appear on the public discovery surfaces.
func createPublishedVideo(t *testing.T, srv *Server, token, handle, body string) string {
	t.Helper()
	id := createVideo(t, srv, token, handle, body)
	rec := uploadVideoFile(srv, id, "clip.mp4", "video/mp4", "tiny", token)
	if rec.Code != http.StatusCreated {
		t.Fatalf("publish upload = %d; body=%s", rec.Code, rec.Body.String())
	}
	return id
}

func getVideo(srv *Server, id, token string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+id, nil)
	if token != "" {
		req.Header.Set("authorization", "Bearer "+token)
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestGetPublicVideoIsAnonymous(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"Public","privacy":"public"}`)

	if rec := getVideo(srv, id, ""); rec.Code != http.StatusOK {
		t.Fatalf("anon get public = %d, want 200", rec.Code)
	}
}

func TestGetPrivateVideoOwnerOnly(t *testing.T) {
	srv := videoServer(t)
	ownerTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	otherTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	id := createVideo(t, srv, ownerTok, "ada", `{"title":"Secret","privacy":"private"}`)

	if rec := getVideo(srv, id, ownerTok); rec.Code != http.StatusOK {
		t.Fatalf("owner get private = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	// Hidden as 404 (not 403) from anon and non-owners.
	if rec := getVideo(srv, id, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("anon get private = %d, want 404", rec.Code)
	}
	if rec := getVideo(srv, id, otherTok); rec.Code != http.StatusNotFound {
		t.Fatalf("non-owner get private = %d, want 404", rec.Code)
	}
}

func TestGetVideoNotFoundAndMalformed(t *testing.T) {
	srv := videoServer(t)
	if rec := getVideo(srv, uuid.New().String(), ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown id = %d, want 404", rec.Code)
	}
	if rec := getVideo(srv, "not-a-uuid", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("malformed id = %d, want 404", rec.Code)
	}
}

func TestUpdateVideoOwnerAndNonOwner(t *testing.T) {
	srv := videoServer(t)
	ownerTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	otherTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	id := createVideo(t, srv, ownerTok, "ada", `{"title":"old","privacy":"private"}`)

	// Owner update.
	rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/videos/"+id, `{"title":"new","privacy":"public"}`, ownerTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner update = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var v videoView
	_ = json.Unmarshal(rec.Body.Bytes(), &v)
	if v.Title != "new" || v.Privacy != "public" {
		t.Errorf("unexpected video: %+v", v)
	}
	// Non-owner -> 404 (existence not leaked).
	if bad := sendJSONAuth(srv, http.MethodPatch, "/api/v1/videos/"+id, `{"title":"hax"}`, otherTok); bad.Code != http.StatusNotFound {
		t.Fatalf("non-owner update = %d, want 404", bad.Code)
	}
	// Empty patch -> 422.
	if empty := sendJSONAuth(srv, http.MethodPatch, "/api/v1/videos/"+id, `{}`, ownerTok); empty.Code != http.StatusUnprocessableEntity {
		t.Fatalf("empty patch = %d, want 422", empty.Code)
	}
}

func TestVideoTaxonomyMetadata(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")

	// Create with valid category/language/license; they round-trip on detail.
	id := createVideo(t, srv, tok, "ada",
		`{"title":"Tagged","category":"1","language":"en","license":"7"}`)
	var v videoView
	_ = json.Unmarshal(getVideo(srv, id, tok).Body.Bytes(), &v)
	if v.Category == nil || *v.Category != "1" || v.Language == nil || *v.Language != "en" ||
		v.License == nil || *v.License != "7" {
		t.Fatalf("taxonomy not stored: %+v", v)
	}

	// A video created without taxonomy omits the fields (NULL -> omitempty).
	id2 := createVideo(t, srv, tok, "ada", `{"title":"Plain"}`)
	var v2 videoView
	_ = json.Unmarshal(getVideo(srv, id2, tok).Body.Bytes(), &v2)
	if v2.Category != nil || v2.Language != nil || v2.License != nil {
		t.Errorf("expected unset taxonomy to be omitted: %+v", v2)
	}

	// Unknown value on create -> 422 (field-scoped).
	if bad := postJSONAuth(srv, "/api/v1/channels/ada/videos",
		`{"title":"x","category":"999"}`, tok); bad.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown category create = %d, want 422; body=%s", bad.Code, bad.Body.String())
	}

	// Update one field; the others are preserved (partial update).
	up := sendJSONAuth(srv, http.MethodPatch, "/api/v1/videos/"+id, `{"language":"fr"}`, tok)
	if up.Code != http.StatusOK {
		t.Fatalf("update language = %d; body=%s", up.Code, up.Body.String())
	}
	var v3 videoView
	_ = json.Unmarshal(up.Body.Bytes(), &v3)
	if v3.Language == nil || *v3.Language != "fr" || v3.Category == nil || *v3.Category != "1" {
		t.Errorf("partial update wrong: %+v", v3)
	}

	// Unknown / empty taxonomy on update -> 422.
	if badU := sendJSONAuth(srv, http.MethodPatch, "/api/v1/videos/"+id,
		`{"license":"nope"}`, tok); badU.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown license update = %d, want 422", badU.Code)
	}
	if emptyU := sendJSONAuth(srv, http.MethodPatch, "/api/v1/videos/"+id,
		`{"category":""}`, tok); emptyU.Code != http.StatusUnprocessableEntity {
		t.Fatalf("empty category update = %d, want 422", emptyU.Code)
	}
}

func TestDeleteVideoOwnerAndNonOwner(t *testing.T) {
	srv := videoServer(t)
	ownerTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	otherTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	id := createVideo(t, srv, ownerTok, "ada", `{"title":"t","privacy":"public"}`)

	if bad := sendJSONAuth(srv, http.MethodDelete, "/api/v1/videos/"+id, "", otherTok); bad.Code != http.StatusNotFound {
		t.Fatalf("non-owner delete = %d, want 404", bad.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/videos/"+id, "", ownerTok); rec.Code != http.StatusNoContent {
		t.Fatalf("owner delete = %d, want 204", rec.Code)
	}
	if get := getVideo(srv, id, ownerTok); get.Code != http.StatusNotFound {
		t.Fatalf("get after delete = %d, want 404", get.Code)
	}
}

func TestListChannelVideosOwnerVsPublic(t *testing.T) {
	srv := videoServer(t)
	ownerTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	otherTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	_ = createPublishedVideo(t, srv, ownerTok, "ada", `{"title":"pub","privacy":"public"}`)
	sensitiveID := createPublishedVideo(t, srv, ownerTok, "ada", `{"title":"sensitive","privacy":"public","is_sensitive":true}`)
	_ = createVideo(t, srv, ownerTok, "ada", `{"title":"priv","privacy":"private"}`)

	list := func(tok string) videoListResponse {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/channels/ada/videos", nil)
		if tok != "" {
			req.Header.Set("authorization", "Bearer "+tok)
		}
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("list = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		var body videoListResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return body
	}

	if owner := list(ownerTok); len(owner.Videos) != 3 {
		t.Errorf("owner list = %d, want 3", len(owner.Videos))
	}
	if anon := list(""); len(anon.Videos) != 1 || anon.Videos[0].Privacy != "public" {
		t.Errorf("anon list = %+v, want 1 public non-sensitive", anon.Videos)
	}
	if other := list(otherTok); len(other.Videos) != 1 {
		t.Errorf("non-owner list = %d, want 1 (public non-sensitive only)", len(other.Videos))
	}
	if direct := getVideo(srv, sensitiveID, ""); direct.Code != http.StatusOK {
		t.Errorf("direct sensitive video read = %d, want 200", direct.Code)
	}
}

// applySensitivePolicy sets the instance-wide sensitive_content_policy setting so
// a test can exercise the effective per-request resolution (0100).
func applySensitivePolicy(t *testing.T, srv *Server, policy string) {
	t.Helper()
	if err := srv.settingssvc.Apply(context.Background(),
		map[string]instancesettings.Update{
			instancesettings.KeySensitiveContentPolicy: {Value: policy},
		}, uuid.New()); err != nil {
		t.Fatalf("apply sensitive policy %q: %v", policy, err)
	}
}

// TestEffectiveSensitivePolicyInListings proves the per-user override (0100)
// wins over the instance policy on the public feed: a user "hide" hides sensitive
// videos even when the instance says display, a user "display" reveals them even
// when the instance says hide, and clearing the override falls back to the
// instance policy. Anonymous requests always follow the instance policy.
func TestEffectiveSensitivePolicyInListings(t *testing.T) {
	srv, _, _, _, _ := videoServerFull(t, testConfig())
	ownerTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	_ = createPublishedVideo(t, srv, ownerTok, "ada", `{"title":"safe","privacy":"public"}`)
	_ = createPublishedVideo(t, srv, ownerTok, "ada", `{"title":"spicy","privacy":"public","is_sensitive":true}`)
	viewer := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	sees := func(tok, title string) bool {
		rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/videos", "", tok)
		if rec.Code != http.StatusOK {
			t.Fatalf("feed = %d; body=%s", rec.Code, rec.Body.String())
		}
		var body videoFeedResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		for _, v := range body.Videos {
			if v.Title == title {
				return true
			}
		}
		return false
	}
	setPolicy := func(tok, policy string) {
		if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/auth/me",
			`{"sensitive_content_policy":"`+policy+`"}`, tok); rec.Code != http.StatusOK {
			t.Fatalf("set user policy %q = %d; body=%s", policy, rec.Code, rec.Body.String())
		}
	}

	// Instance display: anon and an un-overridden user both see the sensitive video.
	applySensitivePolicy(t, srv, "display")
	if !sees("", "spicy") {
		t.Error("anon under instance display should see the sensitive video")
	}
	if !sees(viewer, "spicy") {
		t.Error("un-overridden user should inherit instance display and see the sensitive video")
	}

	// User "hide" overrides instance display: the viewer stops seeing it; anon still does.
	setPolicy(viewer, "hide")
	if sees(viewer, "spicy") {
		t.Error("user hide override should hide the sensitive video despite instance display")
	}
	if !sees("", "spicy") {
		t.Error("anon still follows instance display (overrides are per-user)")
	}

	// Instance hide + user "display" override: the viewer sees it, anon does not.
	applySensitivePolicy(t, srv, "hide")
	setPolicy(viewer, "display")
	if !sees(viewer, "spicy") {
		t.Error("user display override should reveal the sensitive video despite instance hide")
	}
	if sees("", "spicy") {
		t.Error("anon under instance hide should not see the sensitive video")
	}

	// Clearing the override falls back to the instance policy (still hide).
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/auth/me",
		`{"sensitive_content_policy":""}`, viewer); rec.Code != http.StatusOK {
		t.Fatalf("clear user policy = %d", rec.Code)
	}
	if sees(viewer, "spicy") {
		t.Error("cleared override should inherit instance hide and hide the sensitive video")
	}
	// The non-sensitive video is always visible regardless of policy.
	if !sees(viewer, "safe") {
		t.Error("non-sensitive video should always be visible")
	}
}

// TestVideoSensitiveReasonRoundTrip proves the creator content-warning text is
// accepted on create + update (trimmed, storable regardless of is_sensitive,
// clearable), surfaces on the detail DTO, and is capped at 280 chars.
func TestVideoSensitiveReasonRoundTrip(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")

	rec := postJSONAuth(srv, "/api/v1/channels/ada/videos",
		`{"title":"warned","privacy":"public","is_sensitive":true,"sensitive_reason":"  graphic content  "}`, tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d; body=%s", rec.Code, rec.Body.String())
	}
	var created videoView
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if created.SensitiveReason != "graphic content" {
		t.Errorf("create response reason = %q, want trimmed \"graphic content\"", created.SensitiveReason)
	}
	id := created.ID

	detailReason := func() string {
		var v videoView
		_ = json.Unmarshal(getVideo(srv, id, tok).Body.Bytes(), &v)
		return v.SensitiveReason
	}
	if got := detailReason(); got != "graphic content" {
		t.Errorf("detail reason = %q, want \"graphic content\"", got)
	}

	// Update replaces it.
	if u := sendJSONAuth(srv, http.MethodPatch, "/api/v1/videos/"+id, `{"sensitive_reason":"nudity"}`, tok); u.Code != http.StatusOK {
		t.Fatalf("update reason = %d; body=%s", u.Code, u.Body.String())
	}
	if got := detailReason(); got != "nudity" {
		t.Errorf("detail reason after update = %q, want nudity", got)
	}

	// An empty string clears it.
	if u := sendJSONAuth(srv, http.MethodPatch, "/api/v1/videos/"+id, `{"sensitive_reason":""}`, tok); u.Code != http.StatusOK {
		t.Fatalf("clear reason = %d", u.Code)
	}
	if got := detailReason(); got != "" {
		t.Errorf("detail reason after clear = %q, want empty", got)
	}

	// Storable regardless of is_sensitive (the frontend pairs them).
	rec2 := postJSONAuth(srv, "/api/v1/channels/ada/videos",
		`{"title":"noflag","privacy":"public","sensitive_reason":"just in case"}`, tok)
	if rec2.Code != http.StatusCreated {
		t.Fatalf("create noflag = %d; body=%s", rec2.Code, rec2.Body.String())
	}
	var noflag videoView
	_ = json.Unmarshal(rec2.Body.Bytes(), &noflag)
	if noflag.IsSensitive || noflag.SensitiveReason != "just in case" {
		t.Errorf("noflag = is_sensitive %v reason %q, want false + reason stored", noflag.IsSensitive, noflag.SensitiveReason)
	}

	// The 280-char cap is enforced on create and update.
	long := strings.Repeat("a", 281)
	if bad := postJSONAuth(srv, "/api/v1/channels/ada/videos",
		`{"title":"toolong","sensitive_reason":"`+long+`"}`, tok); bad.Code != http.StatusUnprocessableEntity {
		t.Errorf("over-cap create = %d, want 422", bad.Code)
	}
	if bad := sendJSONAuth(srv, http.MethodPatch, "/api/v1/videos/"+id,
		`{"sensitive_reason":"`+long+`"}`, tok); bad.Code != http.StatusUnprocessableEntity {
		t.Errorf("over-cap update = %d, want 422", bad.Code)
	}
}

func TestFeedHidesMutedAccounts(t *testing.T) {
	srv := videoServer(t)
	ada := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	_ = createPublishedVideo(t, srv, ada, "ada", `{"title":"by ada","privacy":"public"}`)

	// A second creator, bob.
	bobTok, bobID := registerAndUser(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/channels", `{"handle":"bob","display_name":"Bob"}`, bobTok); rec.Code != http.StatusCreated {
		t.Fatalf("create bob channel = %d; body=%s", rec.Code, rec.Body.String())
	}
	_ = createPublishedVideo(t, srv, bobTok, "bob", `{"title":"by bob","privacy":"public"}`)

	// A viewer, charlie.
	charlie := registerAndToken(t, srv, `{"username":"charlie","email":"charlie@example.test","password":"supersecret"}`)

	feedTitles := func(tok string) []string {
		rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/videos", "", tok)
		if rec.Code != http.StatusOK {
			t.Fatalf("feed = %d; body=%s", rec.Code, rec.Body.String())
		}
		var body videoFeedResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		out := make([]string, 0, len(body.Videos))
		for _, v := range body.Videos {
			out = append(out, v.Title)
		}
		return out
	}
	searchTitles := func(tok string) []string {
		rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/videos/search?q=by", "", tok)
		var body videoSearchResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		out := make([]string, 0, len(body.Videos))
		for _, v := range body.Videos {
			out = append(out, v.Title)
		}
		return out
	}

	// Before muting, charlie sees both creators' videos.
	if got := feedTitles(charlie); len(got) != 2 {
		t.Fatalf("charlie feed before mute = %v, want 2", got)
	}

	// charlie mutes bob.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/me/mutes/accounts/"+bobID, "", charlie); rec.Code != http.StatusNoContent {
		t.Fatalf("mute bob = %d; body=%s", rec.Code, rec.Body.String())
	}

	// charlie's feed + search now exclude bob's video; an anonymous viewer still sees both.
	if got := feedTitles(charlie); len(got) != 1 || got[0] != "by ada" {
		t.Errorf("charlie feed after mute = %v, want [by ada]", got)
	}
	if got := searchTitles(charlie); len(got) != 1 || got[0] != "by ada" {
		t.Errorf("charlie search after mute = %v, want [by ada]", got)
	}
	if got := feedTitles(""); len(got) != 2 {
		t.Errorf("anon feed = %v, want 2 (mutes are per-viewer)", got)
	}

	// Unmuting restores bob's video to charlie's feed.
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/me/mutes/accounts/"+bobID, "", charlie); rec.Code != http.StatusNoContent {
		t.Fatalf("unmute = %d", rec.Code)
	}
	if got := feedTitles(charlie); len(got) != 2 {
		t.Errorf("charlie feed after unmute = %v, want 2", got)
	}
}

func TestPublicVideoFeed(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	_ = createPublishedVideo(t, srv, tok, "ada", `{"title":"p1","privacy":"public"}`)
	_ = createPublishedVideo(t, srv, tok, "ada", `{"title":"p2","privacy":"public"}`)
	_ = createVideo(t, srv, tok, "ada", `{"title":"secret","privacy":"private"}`)

	feed := func(query string) videoFeedResponse {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/videos"+query, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("feed%s = %d, want 200; body=%s", query, rec.Code, rec.Body.String())
		}
		var body videoFeedResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return body
	}

	// Anonymous feed shows only the 2 public videos.
	all := feed("")
	if len(all.Videos) != 2 || all.Limit != 20 || all.Offset != 0 {
		t.Fatalf("default feed = %+v, want 2 videos, limit 20, offset 0", all)
	}
	for _, v := range all.Videos {
		if v.Privacy != "public" {
			t.Errorf("feed leaked non-public video: %+v", v)
		}
	}

	// Pagination: limit clamps, offset advances.
	page1 := feed("?limit=1&offset=0")
	page2 := feed("?limit=1&offset=1")
	page3 := feed("?limit=1&offset=2")
	if len(page1.Videos) != 1 || page1.Limit != 1 {
		t.Errorf("page1 = %+v, want 1 video, limit 1", page1)
	}
	if len(page2.Videos) != 1 {
		t.Errorf("page2 = %d videos, want 1", len(page2.Videos))
	}
	if len(page3.Videos) != 0 {
		t.Errorf("page3 = %d videos, want 0 (only 2 public)", len(page3.Videos))
	}
	if page1.Videos[0].ID == page2.Videos[0].ID {
		t.Error("pages returned the same video")
	}

	// Over-max limit is clamped to 100.
	if huge := feed("?limit=99999"); huge.Limit != 100 {
		t.Errorf("limit clamp = %d, want 100", huge.Limit)
	}
}

func TestSearchVideos(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	_ = createPublishedVideo(t, srv, tok, "ada", `{"title":"Go concurrency patterns","privacy":"public"}`)
	_ = createPublishedVideo(t, srv, tok, "ada", `{"title":"Rust ownership","privacy":"public"}`)
	_ = createVideo(t, srv, tok, "ada", `{"title":"Go generics secret","privacy":"private"}`)

	search := func(query string) (int, videoSearchResponse) {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/videos/search"+query, nil))
		var body videoSearchResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return rec.Code, body
	}

	// Missing q -> 400.
	if code, _ := search(""); code != http.StatusBadRequest {
		t.Fatalf("missing q = %d, want 400", code)
	}

	// "go" matches the public Go video but not the private one.
	code, body := search("?q=go")
	if code != http.StatusOK {
		t.Fatalf("search = %d, want 200", code)
	}
	if body.Query != "go" || len(body.Videos) != 1 {
		t.Fatalf("search result = %+v, want 1 public match", body)
	}
	if body.Videos[0].Title != "Go concurrency patterns" {
		t.Errorf("matched %q, want the public Go video", body.Videos[0].Title)
	}

	// No matches -> empty.
	if _, none := search("?q=kubernetes"); len(none.Videos) != 0 {
		t.Errorf("no-match search = %+v, want empty", none.Videos)
	}
}

func TestListChannelVideosUnknownChannel404(t *testing.T) {
	srv := videoServer(t)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/channels/ghost/videos", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// uploadVideoFile POSTs a multipart "file" field to the upload endpoint.
func uploadVideoFile(srv *Server, id, filename, contentType, content, token string) *httptest.ResponseRecorder {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	hdr := make(map[string][]string)
	hdr["Content-Disposition"] = []string{`form-data; name="file"; filename="` + filename + `"`}
	if contentType != "" {
		hdr["Content-Type"] = []string{contentType}
	}
	part, _ := w.CreatePart(hdr)
	_, _ = part.Write([]byte(content))
	_ = w.Close()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/"+id+"/file", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	if token != "" {
		req.Header.Set("authorization", "Bearer "+token)
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestUploadVideoFileRequiresAuth(t *testing.T) {
	srv := videoServer(t)
	rec := uploadVideoFile(srv, uuid.New().String(), "clip.mp4", "video/mp4", "bytes", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestUploadVideoFileStoresAndPublishes(t *testing.T) {
	srv := videoServer(t) // no prober configured -> the original is published directly
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"My Draft"}`)

	const content = "pretend this is an mp4"
	rec := uploadVideoFile(srv, id, "Clip.MP4", "video/mp4", content, tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var resp uploadVideoFileResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Video.State != "published" {
		t.Errorf("state = %q, want published", resp.Video.State)
	}
	if resp.File.SizeBytes != int64(len(content)) {
		t.Errorf("size = %d, want %d", resp.File.SizeBytes, len(content))
	}
	if resp.File.Kind != "original" || resp.File.ContentType != "video/mp4" || resp.File.OriginalName != "Clip.MP4" {
		t.Errorf("unexpected file: %+v", resp.File)
	}

	// The video reports published on a fresh read, too.
	got := getVideo(srv, id, tok)
	var v videoView
	_ = json.Unmarshal(got.Body.Bytes(), &v)
	if v.State != "published" {
		t.Errorf("refetched state = %q, want published", v.State)
	}
}

func TestUploadVideoFileProbeFailureMarksFailed(t *testing.T) {
	srv := videoServerCfg(t, testConfig(), video.WithProber(fakeProber{err: errors.New("corrupt media")}))
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"My Draft"}`)

	rec := uploadVideoFile(srv, id, "clip.mp4", "video/mp4", "not really a video", tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var resp uploadVideoFileResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Video.State != "failed" {
		t.Errorf("state = %q, want failed (probe rejected the file)", resp.Video.State)
	}
}

func TestUploadProbeMetadataOnDetail(t *testing.T) {
	srv := videoServerCfg(t, testConfig(), video.WithProber(fakeProber{
		md: media.Metadata{DurationSeconds: 95, Width: 1280, Height: 720},
	}))
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"Clip","privacy":"public"}`)
	if rec := uploadVideoFile(srv, id, "clip.mp4", "video/mp4", "data", tok); rec.Code != http.StatusCreated {
		t.Fatalf("upload = %d; body=%s", rec.Code, rec.Body.String())
	}

	// The detail endpoint exposes the probed metadata.
	rec := getVideo(srv, id, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get = %d, want 200", rec.Code)
	}
	var v videoView
	_ = json.Unmarshal(rec.Body.Bytes(), &v)
	if v.State != "published" {
		t.Errorf("state = %q, want published", v.State)
	}
	if v.DurationSeconds == nil || *v.DurationSeconds != 95 {
		t.Errorf("duration_seconds = %v, want 95", v.DurationSeconds)
	}
	if v.Width == nil || *v.Width != 1280 || v.Height == nil || *v.Height != 720 {
		t.Errorf("dimensions = %v x %v, want 1280x720", v.Width, v.Height)
	}
}

func TestDetailHasNoMetadataWithoutProber(t *testing.T) {
	srv := videoServer(t) // no prober -> no metadata recorded
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"Clip","privacy":"public"}`)
	if rec := uploadVideoFile(srv, id, "clip.mp4", "video/mp4", "data", tok); rec.Code != http.StatusCreated {
		t.Fatalf("upload = %d; body=%s", rec.Code, rec.Body.String())
	}
	rec := getVideo(srv, id, "")
	var v videoView
	_ = json.Unmarshal(rec.Body.Bytes(), &v)
	if v.DurationSeconds != nil || v.Width != nil || v.Height != nil {
		t.Errorf("metadata present without a prober: %+v", v)
	}
}

func TestUploadVideoFileMissingField(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"My Draft"}`)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/"+id+"/file", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("authorization", "Bearer "+tok)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestUploadVideoFileNonOwner404(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"My Draft"}`)
	otherTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	rec := uploadVideoFile(srv, id, "clip.mp4", "video/mp4", "bytes", otherTok)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("non-owner upload = %d, want 404", rec.Code)
	}
}

func TestUploadVideoFileUnknownVideo404(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	rec := uploadVideoFile(srv, uuid.New().String(), "clip.mp4", "video/mp4", "bytes", tok)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown video upload = %d, want 404", rec.Code)
	}
}

func TestUploadVideoFileUnsupportedExtension(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"My Draft"}`)
	rec := uploadVideoFile(srv, id, "notes.pdf", "application/pdf", "not a video", tok)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415; body=%s", rec.Code, rec.Body.String())
	}
}

func TestUploadVideoFileTooLarge(t *testing.T) {
	srv := videoServer(t) // UploadMaxSize is 64K in testConfig
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"My Draft"}`)
	big := strings.Repeat("x", 80*1024) // 80K > 64K cap
	rec := uploadVideoFile(srv, id, "clip.mp4", "video/mp4", big, tok)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413; body=%s", rec.Code, rec.Body.String())
	}
}

func TestUploadRouteBypassesJSONBodyLimit(t *testing.T) {
	cfg := testConfig()
	cfg.HTTPBodyLimit = "2K"   // tiny JSON cap
	cfg.UploadMaxSize = "256K" // generous upload cap
	srv := videoServerCfg(t, cfg)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"My Draft"}`)

	// An upload above the JSON limit but under the upload cap succeeds — proving
	// the upload route is exempt from the small default body limit.
	body := strings.Repeat("x", 8*1024) // 8K > 2K JSON cap, < 256K upload cap
	rec := uploadVideoFile(srv, id, "clip.mp4", "video/mp4", body, tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("large upload = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}

	// ...and the JSON API is still capped by the small default limit.
	bigJSON := `{"title":"` + strings.Repeat("a", 3*1024) + `"}`
	rec = postJSONAuth(srv, "/api/v1/channels/ada/videos", bigJSON, tok)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized JSON = %d, want 413; body=%s", rec.Code, rec.Body.String())
	}
}

// streamOriginal GETs a video's original-file stream, optionally authed and/or
// with a Range header.
func streamOriginal(srv *Server, id, token, rangeHdr string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+id+"/original", nil)
	if token != "" {
		req.Header.Set("authorization", "Bearer "+token)
	}
	if rangeHdr != "" {
		req.Header.Set("Range", rangeHdr)
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestStreamOriginalPublic(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPublishedVideo(t, srv, tok, "ada", `{"title":"Clip","privacy":"public"}`)

	rec := streamOriginal(srv, id, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("stream = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "tiny" {
		t.Errorf("body = %q, want %q", rec.Body.String(), "tiny")
	}
	if got := rec.Header().Get("Accept-Ranges"); got != "bytes" {
		t.Errorf("Accept-Ranges = %q, want bytes", got)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "video/mp4" {
		t.Errorf("Content-Type = %q, want video/mp4", ct)
	}
}

func TestStreamOriginalRange(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPublishedVideo(t, srv, tok, "ada", `{"title":"Clip","privacy":"public"}`)

	rec := streamOriginal(srv, id, "", "bytes=0-1")
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("range stream = %d, want 206; body=%s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "ti" {
		t.Errorf("range body = %q, want %q", rec.Body.String(), "ti")
	}
	if cr := rec.Header().Get("Content-Range"); cr != "bytes 0-1/4" {
		t.Errorf("Content-Range = %q, want bytes 0-1/4", cr)
	}
}

func TestStreamOriginalPrivateVisibility(t *testing.T) {
	srv := videoServer(t)
	ownerTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, ownerTok, "ada", `{"title":"Secret","privacy":"private"}`)
	if rec := uploadVideoFile(srv, id, "clip.mp4", "video/mp4", "tiny", ownerTok); rec.Code != http.StatusCreated {
		t.Fatalf("upload = %d; body=%s", rec.Code, rec.Body.String())
	}
	otherTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	if rec := streamOriginal(srv, id, "", ""); rec.Code != http.StatusNotFound {
		t.Errorf("anon stream of private = %d, want 404", rec.Code)
	}
	if rec := streamOriginal(srv, id, otherTok, ""); rec.Code != http.StatusNotFound {
		t.Errorf("non-owner stream of private = %d, want 404", rec.Code)
	}
	if rec := streamOriginal(srv, id, ownerTok, ""); rec.Code != http.StatusOK {
		t.Errorf("owner stream of private = %d, want 200", rec.Code)
	}
}

func TestStreamOriginalNoFile404(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"Draft","privacy":"public"}`) // never uploaded
	if rec := streamOriginal(srv, id, "", ""); rec.Code != http.StatusNotFound {
		t.Errorf("stream of fileless video = %d, want 404", rec.Code)
	}
}

func TestStreamOriginalUnknown404(t *testing.T) {
	srv := videoServer(t)
	if rec := streamOriginal(srv, uuid.New().String(), "", ""); rec.Code != http.StatusNotFound {
		t.Errorf("stream of unknown video = %d, want 404", rec.Code)
	}
}

type fakeThumbnailer struct {
	jpg []byte
	err error
}

func (f fakeThumbnailer) Thumbnail(_ context.Context, _ string, _ int) ([]byte, error) {
	return f.jpg, f.err
}

func (f fakeThumbnailer) ThumbnailAt(_ context.Context, _ string, _ float64) ([]byte, error) {
	return f.jpg, f.err
}

func getThumbnail(srv *Server, id, token string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+id+"/thumbnail", nil)
	if token != "" {
		req.Header.Set("authorization", "Bearer "+token)
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestThumbnailServedAndFlaggedOnDetail(t *testing.T) {
	jpg := []byte("\xff\xd8\xff\xe0fakejpegbytes")
	srv := videoServerCfg(t, testConfig(), video.WithThumbnailer(fakeThumbnailer{jpg: jpg}))
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"Clip","privacy":"public"}`)
	if rec := uploadVideoFile(srv, id, "clip.mp4", "video/mp4", "data", tok); rec.Code != http.StatusCreated {
		t.Fatalf("upload = %d; body=%s", rec.Code, rec.Body.String())
	}

	rec := getThumbnail(srv, id, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("thumbnail = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != string(jpg) {
		t.Errorf("thumbnail body mismatch (%d bytes, want %d)", rec.Body.Len(), len(jpg))
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("Content-Type = %q, want image/jpeg", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "private, max-age=300, must-revalidate" {
		t.Errorf("Cache-Control = %q, want short private thumbnail caching", cc)
	}

	// Detail flags that a thumbnail exists.
	drec := getVideo(srv, id, "")
	var v videoView
	_ = json.Unmarshal(drec.Body.Bytes(), &v)
	if v.HasThumbnail == nil || !*v.HasThumbnail {
		t.Errorf("has_thumbnail = %v, want true", v.HasThumbnail)
	}
}

func TestNoThumbnailWithoutGenerator(t *testing.T) {
	srv := videoServer(t) // no thumbnailer wired
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPublishedVideo(t, srv, tok, "ada", `{"title":"Clip","privacy":"public"}`)

	if rec := getThumbnail(srv, id, ""); rec.Code != http.StatusNotFound {
		t.Errorf("thumbnail = %d, want 404 (none generated)", rec.Code)
	}
	drec := getVideo(srv, id, "")
	var v videoView
	_ = json.Unmarshal(drec.Body.Bytes(), &v)
	if v.HasThumbnail == nil || *v.HasThumbnail {
		t.Errorf("has_thumbnail = %v, want false (present, not omitted)", v.HasThumbnail)
	}
}

func TestThumbnailPrivateVisibility(t *testing.T) {
	srv := videoServerCfg(t, testConfig(), video.WithThumbnailer(fakeThumbnailer{jpg: []byte("\xff\xd8jpg")}))
	ownerTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, ownerTok, "ada", `{"title":"Secret","privacy":"private"}`)
	if rec := uploadVideoFile(srv, id, "clip.mp4", "video/mp4", "data", ownerTok); rec.Code != http.StatusCreated {
		t.Fatalf("upload = %d", rec.Code)
	}
	if rec := getThumbnail(srv, id, ""); rec.Code != http.StatusNotFound {
		t.Errorf("anon thumbnail of private = %d, want 404", rec.Code)
	}
	if rec := getThumbnail(srv, id, ownerTok); rec.Code != http.StatusOK {
		t.Errorf("owner thumbnail of private = %d, want 200", rec.Code)
	}
}

type fakeDeduper struct{ seen map[string]bool }

func (d *fakeDeduper) First(_ context.Context, key string, _ time.Duration) (bool, error) {
	if d.seen == nil {
		d.seen = map[string]bool{}
	}
	if d.seen[key] {
		return false, nil
	}
	d.seen[key] = true
	return true, nil
}

func postView(srv *Server, id, token string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/"+id+"/view", nil)
	if token != "" {
		req.Header.Set("authorization", "Bearer "+token)
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func detailVideo(t *testing.T, srv *Server, id string) videoView {
	t.Helper()
	var v videoView
	_ = json.Unmarshal(getVideo(srv, id, "").Body.Bytes(), &v)
	return v
}

func TestRecordViewIncrementsDetailCount(t *testing.T) {
	srv := videoServer(t) // no deduper -> each ping counts
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPublishedVideo(t, srv, tok, "ada", `{"title":"Clip","privacy":"public"}`)

	if v := detailVideo(t, srv, id); v.Views == nil || *v.Views != 0 {
		t.Fatalf("initial views = %v, want 0 (present)", v.Views)
	}
	if rec := postView(srv, id, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("view = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if v := detailVideo(t, srv, id); v.Views == nil || *v.Views != 1 {
		t.Errorf("after one view = %v, want 1", v.Views)
	}
}

func TestRecordViewUnknown404(t *testing.T) {
	srv := videoServer(t)
	if rec := postView(srv, uuid.New().String(), ""); rec.Code != http.StatusNotFound {
		t.Errorf("view of unknown = %d, want 404", rec.Code)
	}
}

func TestRecordViewDedupedAcrossRequests(t *testing.T) {
	srv := videoServerCfg(t, testConfig(), video.WithViewDeduper(&fakeDeduper{}))
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPublishedVideo(t, srv, tok, "ada", `{"title":"Clip","privacy":"public"}`)

	// Two pings from the same client (same RemoteAddr -> same viewer key).
	_ = postView(srv, id, "")
	_ = postView(srv, id, "")
	if v := detailVideo(t, srv, id); v.Views == nil || *v.Views != 1 {
		t.Errorf("deduped views = %v, want 1", v.Views)
	}
}

func TestPublicFeedSortAndCards(t *testing.T) {
	srv := videoServerCfg(t, testConfig(), video.WithThumbnailer(fakeThumbnailer{jpg: []byte("\xff\xd8jpg")}))
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	_ = createPublishedVideo(t, srv, tok, "ada", `{"title":"a","privacy":"public"}`)
	b := createPublishedVideo(t, srv, tok, "ada", `{"title":"b","privacy":"public"}`)
	// b gets two views (no deduper in this harness -> each counts); a gets none.
	_ = postView(srv, b, "")
	_ = postView(srv, b, "")

	feed := func(q string) videoFeedResponse {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/videos"+q, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("feed%s = %d; body=%s", q, rec.Code, rec.Body.String())
		}
		var body videoFeedResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return body
	}

	pop := feed("?sort=popular")
	if pop.Sort != "popular" {
		t.Errorf("sort echo = %q, want popular", pop.Sort)
	}
	if len(pop.Videos) != 2 || pop.Videos[0].ID != b {
		t.Fatalf("popular[0] = %+v, want b (%s) first", pop.Videos, b)
	}
	if pop.Videos[0].Views == nil || *pop.Videos[0].Views != 2 {
		t.Errorf("b views = %v, want 2", pop.Videos[0].Views)
	}
	if pop.Videos[0].HasThumbnail == nil || !*pop.Videos[0].HasThumbnail {
		t.Errorf("card has_thumbnail = %v, want true", pop.Videos[0].HasThumbnail)
	}

	if got := feed("?sort=bogus").Sort; got != "recent" {
		t.Errorf("unknown sort echoed %q, want recent (fallback)", got)
	}
}

func TestSearchAndChannelListCarryCards(t *testing.T) {
	srv := videoServerCfg(t, testConfig(), video.WithThumbnailer(fakeThumbnailer{jpg: []byte("\xff\xd8jpg")}))
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPublishedVideo(t, srv, tok, "ada", `{"title":"Go rocks","privacy":"public"}`)
	_ = postView(srv, id, "")

	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d; body=%s", path, rec.Code, rec.Body.String())
		}
		return rec
	}

	// Search results carry view count + poster availability.
	var sr videoSearchResponse
	_ = json.Unmarshal(get("/api/v1/videos/search?q=go").Body.Bytes(), &sr)
	if len(sr.Videos) != 1 {
		t.Fatalf("search = %+v, want 1 result", sr.Videos)
	}
	if c := sr.Videos[0]; c.Views == nil || *c.Views != 1 || c.HasThumbnail == nil || !*c.HasThumbnail {
		t.Errorf("search card missing data: views=%v has_thumbnail=%v", c.Views, c.HasThumbnail)
	}

	// Channel video list carries them too.
	var lr videoListResponse
	_ = json.Unmarshal(get("/api/v1/channels/ada/videos").Body.Bytes(), &lr)
	if len(lr.Videos) != 1 {
		t.Fatalf("channel list = %+v, want 1", lr.Videos)
	}
	if c := lr.Videos[0]; c.Views == nil || *c.Views != 1 || c.HasThumbnail == nil || !*c.HasThumbnail {
		t.Errorf("channel card missing data: views=%v has_thumbnail=%v", c.Views, c.HasThumbnail)
	}
}

func TestSubscriptionFeed(t *testing.T) {
	srv := videoServer(t)
	adaTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	_ = createPublishedVideo(t, srv, adaTok, "ada", `{"title":"from ada","privacy":"public"}`)
	bobTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	sub := func(tok string) videoFeedResponse {
		t.Helper()
		rec := getWithAuth(srv, "/api/v1/me/subscriptions/videos", tok)
		if rec.Code != http.StatusOK {
			t.Fatalf("subscriptions = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		var body videoFeedResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return body
	}

	// Requires auth.
	if anon := getWithAuth(srv, "/api/v1/me/subscriptions/videos", ""); anon.Code != http.StatusUnauthorized {
		t.Fatalf("anon subscriptions = %d, want 401", anon.Code)
	}

	// Before following anyone, the feed is empty.
	if before := sub(bobTok); len(before.Videos) != 0 {
		t.Fatalf("feed before following = %d videos, want 0", len(before.Videos))
	}

	// Bob follows ada, then ada's published video appears in his feed.
	if f := sendJSONAuth(srv, http.MethodPost, "/api/v1/channels/ada/follow", "", bobTok); f.Code != http.StatusNoContent {
		t.Fatalf("follow = %d, want 204; body=%s", f.Code, f.Body.String())
	}
	after := sub(bobTok)
	if len(after.Videos) != 1 || after.Videos[0].Title != "from ada" {
		t.Fatalf("feed after following = %+v, want 1 video 'from ada'", after.Videos)
	}
}

func TestFeedCardsCarryChannelInfo(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	_ = createPublishedVideo(t, srv, tok, "ada", `{"title":"hello","privacy":"public"}`)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/videos", nil))
	var body videoFeedResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Videos) != 1 {
		t.Fatalf("feed has %d videos, want 1", len(body.Videos))
	}
	c := body.Videos[0]
	if c.ChannelHandle == nil || *c.ChannelHandle != "ada" {
		t.Errorf("channel_handle = %v, want ada", c.ChannelHandle)
	}
	if c.ChannelDisplayName == nil || *c.ChannelDisplayName != "ada" {
		t.Errorf("channel_display_name = %v, want ada", c.ChannelDisplayName)
	}
}

// RenewCaptionJobLease is the lease heartbeat; the fake has no leases to keep.
func (*captionJobFakeRepo) RenewCaptionJobLease(_ context.Context, _ uuid.UUID) error { return nil }

// Every Count delegates to its List so the fake pair can never disagree — the
// invariant the real Count/List SQL pairs must hold.
func (f *videoFakeRepo) CountVideosByChannel(ctx context.Context, channelID uuid.UUID) (int64, error) {
	rows, err := f.ListVideosByChannel(ctx, sqlcgen.ListVideosByChannelParams{ChannelID: channelID, ResultLimit: 1 << 30})
	return int64(len(rows)), err
}

func (f *videoFakeRepo) CountPublicVideosByChannelVisible(ctx context.Context, a sqlcgen.CountPublicVideosByChannelVisibleParams) (int64, error) {
	rows, err := f.ListPublicVideosByChannel(ctx, sqlcgen.ListPublicVideosByChannelParams{
		ChannelID: a.ChannelID, HideSensitive: a.HideSensitive, ResultLimit: 1 << 30,
	})
	return int64(len(rows)), err
}

func (f *videoFakeRepo) CountQuarantinedVideos(ctx context.Context) (int64, error) {
	rows, err := f.ListQuarantinedVideos(ctx, sqlcgen.ListQuarantinedVideosParams{ResultLimit: 1 << 30})
	return int64(len(rows)), err
}

func (f *videoFakeRepo) CountPublicVideosSorted(ctx context.Context, a sqlcgen.CountPublicVideosSortedParams) (int64, error) {
	rows, err := f.ListPublicVideosSorted(ctx, sqlcgen.ListPublicVideosSortedParams{
		IncludeRemote: a.IncludeRemote, ViewerID: a.ViewerID, Tag: a.Tag,
		Category: a.Category, Language: a.Language, HideSensitive: a.HideSensitive,
		Sort: "recent", ResultLimit: 1 << 30,
	})
	return int64(len(rows)), err
}

func (f *videoFakeRepo) CountSubscriptionVideos(ctx context.Context, followerID uuid.UUID) (int64, error) {
	rows, err := f.ListSubscriptionVideos(ctx, sqlcgen.ListSubscriptionVideosParams{FollowerID: followerID, ResultLimit: 1 << 30})
	return int64(len(rows)), err
}

func (f *videoFakeRepo) CountSavedVideos(ctx context.Context, userID uuid.UUID) (int64, error) {
	rows, err := f.ListSavedVideos(ctx, sqlcgen.ListSavedVideosParams{UserID: userID, ResultLimit: 1 << 30})
	return int64(len(rows)), err
}

func (f *videoFakeRepo) CountWatchHistory(ctx context.Context, userID uuid.UUID) (int64, error) {
	rows, err := f.ListWatchHistory(ctx, sqlcgen.ListWatchHistoryParams{UserID: userID, ResultLimit: 1 << 30})
	return int64(len(rows)), err
}

func (f *videoFakeRepo) CountWatchHistoryInProgress(ctx context.Context, userID uuid.UUID) (int64, error) {
	rows, err := f.ListWatchHistoryInProgress(ctx, sqlcgen.ListWatchHistoryInProgressParams{UserID: userID, ResultLimit: 1 << 30})
	return int64(len(rows)), err
}

func (f *videoFakeRepo) CountSearchPublicVideos(ctx context.Context, a sqlcgen.CountSearchPublicVideosParams) (int64, error) {
	rows, err := f.SearchPublicVideos(ctx, sqlcgen.SearchPublicVideosParams{
		Query: a.Query, ViewerID: a.ViewerID, Tag: a.Tag, Category: a.Category,
		Language: a.Language, TagsAllOf: a.TagsAllOf, TagsOneOf: a.TagsOneOf,
		HideSensitive: a.HideSensitive,
		DurationMin:   a.DurationMin, DurationMax: a.DurationMax,
		PublishedAfter: a.PublishedAfter, PublishedBefore: a.PublishedBefore,
		ResultLimit: 1 << 30,
	})
	return int64(len(rows)), err
}

// channelVideosPage is GET /channels/{handle}/videos, which now carries the
// pagination envelope it lacked entirely.
type channelVideosPage struct {
	Videos []struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	} `json:"videos"`
	Total  int64 `json:"total"`
	Limit  int   `json:"limit"`
	Offset int   `json:"offset"`
}

// TestChannelVideosArePaginated pins the intended BREAKING change: this route
// used to serialise every video in the channel on every request, so a channel
// with 50k videos was a 50k-row response. It now pages and reports a total.
func TestChannelVideosArePaginated(t *testing.T) {
	srv := videoServer(t)
	owner := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	// Created oldest→newest.
	titles := []string{"first", "second", "third", "fourth", "fifth"}
	for _, title := range titles {
		createPublishedVideo(t, srv, owner, "ada", `{"title":"`+title+`","privacy":"public"}`)
	}

	page := func(query string) channelVideosPage {
		t.Helper()
		rec := get(t, srv, "/api/v1/channels/ada/videos"+query)
		if rec.Code != http.StatusOK {
			t.Fatalf("channel videos%s = %d; body=%s", query, rec.Code, rec.Body.String())
		}
		var out channelVideosPage
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return out
	}

	first := page("?limit=2")
	if len(first.Videos) != 2 {
		t.Fatalf("page size = %d, want 2 — the route must honour ?limit now", len(first.Videos))
	}
	if first.Total != 5 {
		t.Errorf("total = %d, want 5", first.Total)
	}
	if first.Limit != 2 || first.Offset != 0 {
		t.Errorf("limit/offset = %d/%d, want 2/0", first.Limit, first.Offset)
	}

	// Default ordering is newest-first, unchanged from before pagination.
	if first.Videos[0].Title != "fifth" || first.Videos[1].Title != "fourth" {
		t.Errorf("default order = %+v, want fifth then fourth (newest first)", first.Videos)
	}

	// The Latest/Oldest chips the channel page already shows.
	oldest := page("?sort=published_at&limit=2")
	if oldest.Videos[0].Title != "first" || oldest.Videos[1].Title != "second" {
		t.Errorf("sort=published_at = %+v, want first then second (oldest first)", oldest.Videos)
	}
	if oldest.Total != first.Total {
		t.Errorf("sorting changed the total: %d vs %d", oldest.Total, first.Total)
	}

	// Paging walks the whole set without gaps or duplicates.
	seen := map[string]bool{}
	for offset := 0; offset < 5; offset += 2 {
		for _, v := range page("?limit=2&offset=" + strconv.Itoa(offset)).Videos {
			if seen[v.ID] {
				t.Errorf("video %s returned on two pages", v.ID)
			}
			seen[v.ID] = true
		}
	}
	if len(seen) != 5 {
		t.Errorf("paged through %d distinct videos, want 5", len(seen))
	}
}

// TestPublicFeedReportsTotal covers the shared videoFeedResponse envelope, which
// backs /videos, /me/saved and /me/subscriptions.
func TestPublicFeedReportsTotal(t *testing.T) {
	srv := videoServer(t)
	owner := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	for i := 0; i < 4; i++ {
		createPublishedVideo(t, srv, owner, "ada", `{"title":"feed `+strconv.Itoa(i)+`","privacy":"public"}`)
	}
	rec := get(t, srv, "/api/v1/videos?limit=2")
	if rec.Code != http.StatusOK {
		t.Fatalf("feed = %d; body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Videos []struct{} `json:"videos"`
		Total  int64      `json:"total"`
		Limit  int        `json:"limit"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body.Videos) != 2 || body.Total != 4 || body.Limit != 2 {
		t.Errorf("feed = %d rows / total %d / limit %d, want 2 / 4 / 2", len(body.Videos), body.Total, body.Limit)
	}
}
