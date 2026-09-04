package video

import (
	"context"
	"errors"
	"io"
	"math"
	"math/rand/v2"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/shortid"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeRepo is an in-memory video.Repository. Each video remembers its channel's
// owner so GetVideoByID can return the joined owner_id.
type fakeRepo struct {
	videos map[uuid.UUID]sqlcgen.GetVideoByIDRow
	// peertubeUUIDs mirrors videos.peertube_uuid: video id -> imported source UUID.
	peertubeUUIDs map[uuid.UUID]uuid.UUID
	files         map[uuid.UUID][]sqlcgen.VideoFile
	metadata      map[uuid.UUID]sqlcgen.VideoMetadatum
	views         map[uuid.UUID]int64
	followed      map[uuid.UUID]bool                            // channel IDs the test subject follows
	saved         map[uuid.UUID]bool                            // video IDs the test subject has saved
	history       map[uuid.UUID]historyMark                     // video ID -> resume position + last-watched
	captions      map[string]sqlcgen.Caption                    // "videoID|lang" -> caption
	tags          map[uuid.UUID][]string                        // video ID -> normalized tag set
	chapters      map[uuid.UUID][]sqlcgen.ListVideoChaptersRow  // video ID -> ordered chapters
	passwords     map[uuid.UUID][]fakePasswordRow               // video ID -> passwords (CORE-17)
	embed         map[uuid.UUID]sqlcgen.GetVideoEmbedPrivacyRow // video ID -> embed policy override
	viewDays      map[dayEntry]int64                            // (video, day) -> rolled-up views
	likes         map[uuid.UUID]int64                           // seedable stats totals
	dislikes      map[uuid.UUID]int64
	comments      map[uuid.UUID]int64
	owner         uuid.UUID
	// fileByKindErr, when set, makes GetVideoFileByKind fail with a transient
	// error instead of answering. Distinct from a miss (pgx.ErrNoRows).
	fileByKindErr error
	// requiresQuarantine mirrors the UploadRequiresQuarantine gate result for
	// the test subject's uploads (true = role 'user' without bypass).
	requiresQuarantine bool
	// mu guards the release-CAS method (PublishTranscodingVideo), the one path
	// tests exercise concurrently (racing ReleaseTranscodeHold callers). The
	// other methods stay unguarded — tests call them from one goroutine.
	mu sync.Mutex
}

func captionKeyFor(videoID uuid.UUID, lang string) string { return videoID.String() + "|" + lang }

func (f *fakeRepo) UpsertCaption(_ context.Context, a sqlcgen.UpsertCaptionParams) (sqlcgen.Caption, error) {
	if f.captions == nil {
		f.captions = map[string]sqlcgen.Caption{}
	}
	c := sqlcgen.Caption{
		ID: uuid.New(), VideoID: a.VideoID, Language: a.Language, Label: a.Label,
		StorageKey: a.StorageKey, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	f.captions[captionKeyFor(a.VideoID, a.Language)] = c
	return c, nil
}

func (f *fakeRepo) ListCaptionsByVideo(_ context.Context, videoID uuid.UUID) ([]sqlcgen.Caption, error) {
	var out []sqlcgen.Caption
	for _, c := range f.captions {
		if c.VideoID == videoID {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Language < out[j].Language })
	return out, nil
}

func (f *fakeRepo) GetCaptionByLang(_ context.Context, a sqlcgen.GetCaptionByLangParams) (sqlcgen.Caption, error) {
	c, ok := f.captions[captionKeyFor(a.VideoID, a.Language)]
	if !ok {
		return sqlcgen.Caption{}, errors.New("not found")
	}
	return c, nil
}

func (f *fakeRepo) DeleteCaption(_ context.Context, a sqlcgen.DeleteCaptionParams) (int64, error) {
	k := captionKeyFor(a.VideoID, a.Language)
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

func newFakeRepo(owner uuid.UUID) *fakeRepo {
	return &fakeRepo{
		videos:   map[uuid.UUID]sqlcgen.GetVideoByIDRow{},
		files:    map[uuid.UUID][]sqlcgen.VideoFile{},
		metadata: map[uuid.UUID]sqlcgen.VideoMetadatum{},
		views:    map[uuid.UUID]int64{},
		followed: map[uuid.UUID]bool{},
		saved:    map[uuid.UUID]bool{},
		history:  map[uuid.UUID]historyMark{},
		owner:    owner,
	}
}

func (f *fakeRepo) UpsertWatchProgress(_ context.Context, a sqlcgen.UpsertWatchProgressParams) (sqlcgen.WatchHistory, error) {
	now := time.Now()
	f.history[a.VideoID] = historyMark{position: a.PositionSeconds, watchedAt: now}
	return sqlcgen.WatchHistory{
		UserID: a.UserID, VideoID: a.VideoID, PositionSeconds: a.PositionSeconds,
		CreatedAt: now, UpdatedAt: now,
	}, nil
}

func (f *fakeRepo) GetWatchProgress(_ context.Context, a sqlcgen.GetWatchProgressParams) (sqlcgen.WatchHistory, error) {
	m, ok := f.history[a.VideoID]
	if !ok {
		return sqlcgen.WatchHistory{}, errors.New("not found")
	}
	return sqlcgen.WatchHistory{
		UserID: a.UserID, VideoID: a.VideoID, PositionSeconds: m.position,
		CreatedAt: m.watchedAt, UpdatedAt: m.watchedAt,
	}, nil
}

func (f *fakeRepo) ListWatchHistory(_ context.Context, a sqlcgen.ListWatchHistoryParams) ([]sqlcgen.ListWatchHistoryRow, error) {
	var rows []sqlcgen.ListWatchHistoryRow
	for vid, m := range f.history {
		r, ok := f.videos[vid]
		if !ok || r.Privacy != "public" || r.State != "published" {
			continue
		}
		rows = append(rows, sqlcgen.ListWatchHistoryRow{
			ID: r.ID, ChannelID: r.ChannelID, Title: r.Title, Description: r.Description,
			Privacy: r.Privacy, State: r.State, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
			Views: f.views[r.ID], HasThumbnail: f.hasThumb(r.ID), DurationSeconds: f.duration(r.ID),
			PositionSeconds: m.position, WatchedAt: m.watchedAt,
		})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].WatchedAt.After(rows[j].WatchedAt) })
	return rows, nil
}

func (f *fakeRepo) ListWatchHistoryInProgress(_ context.Context, a sqlcgen.ListWatchHistoryInProgressParams) ([]sqlcgen.ListWatchHistoryInProgressRow, error) {
	var rows []sqlcgen.ListWatchHistoryInProgressRow
	for vid, m := range f.history {
		r, ok := f.videos[vid]
		if !ok || r.Privacy != "public" || r.State != "published" {
			continue
		}
		// Mirror the SQL filter: started (>= 5s) and not effectively finished.
		if m.position < 5 {
			continue
		}
		if d := f.duration(r.ID); d != nil && *d > 0 && float64(m.position)/float64(*d) >= 0.95 {
			continue
		}
		rows = append(rows, sqlcgen.ListWatchHistoryInProgressRow{
			ID: r.ID, ChannelID: r.ChannelID, Title: r.Title, Description: r.Description,
			Privacy: r.Privacy, State: r.State, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
			Views: f.views[r.ID], HasThumbnail: f.hasThumb(r.ID), DurationSeconds: f.duration(r.ID),
			PositionSeconds: m.position, WatchedAt: m.watchedAt,
		})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].WatchedAt.After(rows[j].WatchedAt) })
	return rows, nil
}

func (f *fakeRepo) DeleteWatchHistoryEntry(_ context.Context, a sqlcgen.DeleteWatchHistoryEntryParams) error {
	delete(f.history, a.VideoID)
	return nil
}

func (f *fakeRepo) ClearWatchHistory(_ context.Context, _ uuid.UUID) error {
	f.history = map[uuid.UUID]historyMark{}
	return nil
}

func (f *fakeRepo) SaveVideo(_ context.Context, a sqlcgen.SaveVideoParams) error {
	f.saved[a.VideoID] = true
	return nil
}

func (f *fakeRepo) UnsaveVideo(_ context.Context, a sqlcgen.UnsaveVideoParams) error {
	delete(f.saved, a.VideoID)
	return nil
}

func (f *fakeRepo) ListSavedVideos(_ context.Context, a sqlcgen.ListSavedVideosParams) ([]sqlcgen.ListSavedVideosRow, error) {
	var rows []sqlcgen.ListSavedVideosRow
	for _, r := range f.videos {
		if f.saved[r.ID] && r.Privacy == "public" && r.State == "published" {
			rows = append(rows, sqlcgen.ListSavedVideosRow{
				ID: r.ID, ChannelID: r.ChannelID, Title: r.Title, Description: r.Description,
				Privacy: r.Privacy, State: r.State, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
				Views: f.views[r.ID], HasThumbnail: f.hasThumb(r.ID), DurationSeconds: f.duration(r.ID),
			})
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].CreatedAt.After(rows[j].CreatedAt) })
	return rows, nil
}

func (f *fakeRepo) ListSubscriptionVideos(_ context.Context, a sqlcgen.ListSubscriptionVideosParams) ([]sqlcgen.ListSubscriptionVideosRow, error) {
	var rows []sqlcgen.ListSubscriptionVideosRow
	for _, r := range f.videos {
		if r.Privacy == "public" && r.State == "published" && f.followed[r.ChannelID] {
			rows = append(rows, sqlcgen.ListSubscriptionVideosRow{
				ID: r.ID, ChannelID: r.ChannelID, Title: r.Title, Description: r.Description,
				Privacy: r.Privacy, State: r.State, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
				Views: f.views[r.ID], HasThumbnail: f.hasThumb(r.ID), DurationSeconds: f.duration(r.ID),
			})
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].CreatedAt.After(rows[j].CreatedAt) })
	return rows, nil
}

func (f *fakeRepo) IncrementVideoViews(_ context.Context, videoID uuid.UUID) (int64, error) {
	f.views[videoID]++
	return f.views[videoID], nil
}

func (f *fakeRepo) GetVideoViews(_ context.Context, videoID uuid.UUID) (int64, error) {
	n, ok := f.views[videoID]
	if !ok {
		return 0, errors.New("not found")
	}
	return n, nil
}

func (f *fakeRepo) UpsertVideoMetadata(_ context.Context, a sqlcgen.UpsertVideoMetadataParams) (sqlcgen.VideoMetadatum, error) {
	m := sqlcgen.VideoMetadatum{
		VideoID: a.VideoID, DurationSeconds: a.DurationSeconds, Width: a.Width, Height: a.Height,
		UpdatedAt: time.Now(),
	}
	f.metadata[a.VideoID] = m
	return m, nil
}

func (f *fakeRepo) GetVideoMetadata(_ context.Context, videoID uuid.UUID) (sqlcgen.VideoMetadatum, error) {
	m, ok := f.metadata[videoID]
	if !ok {
		return sqlcgen.VideoMetadatum{}, errors.New("not found")
	}
	return m, nil
}

func (f *fakeRepo) CreateVideo(_ context.Context, a sqlcgen.CreateVideoParams) (sqlcgen.Video, error) {
	v := sqlcgen.Video{
		ID: uuid.New(), ShortCode: fakeShortCode(), ChannelID: a.ChannelID, Title: a.Title,
		Description: a.Description, Privacy: a.Privacy, State: "draft",
		Category: a.Category, Language: a.Language, License: a.License,
		PublishAt: a.PublishAt, IsSensitive: a.IsSensitive,
		PublishAfterTranscode: a.PublishAfterTranscode,
		CreatedAt:             time.Now(), UpdatedAt: time.Now(),
	}
	f.videos[v.ID] = sqlcgen.GetVideoByIDRow{
		ID: v.ID, ShortCode: v.ShortCode, ChannelID: v.ChannelID, Title: v.Title, Description: v.Description,
		Privacy: v.Privacy, State: v.State, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt,
		Category: v.Category, Language: v.Language, License: v.License,
		PublishAt: v.PublishAt, IsSensitive: v.IsSensitive,
		PublishAfterTranscode: v.PublishAfterTranscode,
		OwnerID:               f.owner,
	}
	return v, nil
}

func (f *fakeRepo) GetVideoByID(_ context.Context, id uuid.UUID) (sqlcgen.GetVideoByIDRow, error) {
	v, ok := f.videos[id]
	if !ok {
		return sqlcgen.GetVideoByIDRow{}, errors.New("not found")
	}
	return v, nil
}

// GetVideoIDByShortCode mirrors the unique index on videos.short_code.
func (f *fakeRepo) GetVideoIDByShortCode(_ context.Context, code string) (uuid.UUID, error) {
	for id, v := range f.videos {
		if v.ShortCode != "" && v.ShortCode == code {
			return id, nil
		}
	}
	return uuid.Nil, errors.New("not found")
}

// GetVideoIDByLegacyUUID mirrors "WHERE id = $1 OR peertube_uuid = $1", with the
// query's preference for our own id namespace.
func (f *fakeRepo) GetVideoIDByLegacyUUID(_ context.Context, legacy uuid.UUID) (uuid.UUID, error) {
	if _, ok := f.videos[legacy]; ok {
		return legacy, nil
	}
	for id, src := range f.peertubeUUIDs {
		if src == legacy {
			return id, nil
		}
	}
	return uuid.Nil, errors.New("not found")
}

// fakeShortCode mints an 11-character base58 code, mirroring what migration
// 0126 makes Postgres do on INSERT via the vidra_short_code() DEFAULT. Tests
// that resolve /v/{code} need the fake to behave like the column, not like a
// zero value — a fake that left short_code empty would let a resolver bug that
// matches "" pass unnoticed.
func fakeShortCode() string {
	b := make([]byte, ShortCodeLen)
	for i := range b {
		b[i] = shortid.Alphabet[rand.IntN(len(shortid.Alphabet))]
	}
	return string(b)
}

func rowToVideo(r sqlcgen.GetVideoByIDRow) sqlcgen.Video {
	return sqlcgen.Video{
		ID: r.ID, ChannelID: r.ChannelID, Title: r.Title, Description: r.Description,
		Privacy: r.Privacy, State: r.State, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		Category: r.Category, Language: r.Language, License: r.License, PublishAt: r.PublishAt,
		IsSensitive: r.IsSensitive, PublishAfterTranscode: r.PublishAfterTranscode,
	}
}

// ListVideoIDsByChannel mirrors the unpaginated id sweep (ORDER BY id).
func (f *fakeRepo) ListVideoIDsByChannel(_ context.Context, channelID uuid.UUID) ([]uuid.UUID, error) {
	var out []uuid.UUID
	for _, r := range f.videos {
		if r.ChannelID == channelID {
			out = append(out, r.ID)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out, nil
}

func (f *fakeRepo) ListVideosByChannel(_ context.Context, a sqlcgen.ListVideosByChannelParams) ([]sqlcgen.ListVideosByChannelRow, error) {
	channelID := a.ChannelID
	var out []sqlcgen.ListVideosByChannelRow
	for _, r := range f.videos {
		if r.ChannelID == channelID {
			out = append(out, sqlcgen.ListVideosByChannelRow{
				ID: r.ID, ChannelID: r.ChannelID, Title: r.Title, Description: r.Description,
				Privacy: r.Privacy, State: r.State, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
				Views: f.views[r.ID], HasThumbnail: f.hasThumb(r.ID), DurationSeconds: f.duration(r.ID),
			})
		}
	}
	// "published_at" is oldest-first; anything else is newest-first, matching the
	// query's CASE branches.
	sort.SliceStable(out, func(i, j int) bool {
		if a.Sort == "published_at" {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return pageSlice(out, a.ResultLimit, a.ResultOffset), nil
}

func pageSlice[T any](rows []T, limit, offset int32) []T {
	lo := min(int(offset), len(rows))
	rows = rows[lo:]
	if limit > 0 && int(limit) < len(rows) {
		rows = rows[:limit]
	}
	return rows
}

func (f *fakeRepo) ListPublicVideosByChannel(_ context.Context, a sqlcgen.ListPublicVideosByChannelParams) ([]sqlcgen.ListPublicVideosByChannelRow, error) {
	channelID := a.ChannelID
	var out []sqlcgen.ListPublicVideosByChannelRow
	for _, r := range f.videos {
		if r.ChannelID == channelID && r.Privacy == "public" && r.State == "published" {
			if a.HideSensitive && r.IsSensitive {
				continue
			}
			out = append(out, sqlcgen.ListPublicVideosByChannelRow{
				ID: r.ID, ChannelID: r.ChannelID, Title: r.Title, Description: r.Description,
				Privacy: r.Privacy, State: r.State, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
				Views: f.views[r.ID], HasThumbnail: f.hasThumb(r.ID), DurationSeconds: f.duration(r.ID),
				IsSensitive: r.IsSensitive,
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if a.Sort == "published_at" {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return pageSlice(out, a.ResultLimit, a.ResultOffset), nil
}

func (f *fakeRepo) CountVideosByChannel(ctx context.Context, channelID uuid.UUID) (int64, error) {
	rows, err := f.ListVideosByChannel(ctx, sqlcgen.ListVideosByChannelParams{ChannelID: channelID, ResultLimit: 1 << 30})
	return int64(len(rows)), err
}

func (f *fakeRepo) CountPublicVideosByChannelVisible(ctx context.Context, a sqlcgen.CountPublicVideosByChannelVisibleParams) (int64, error) {
	rows, err := f.ListPublicVideosByChannel(ctx, sqlcgen.ListPublicVideosByChannelParams{
		ChannelID: a.ChannelID, HideSensitive: a.HideSensitive, ResultLimit: 1 << 30,
	})
	return int64(len(rows)), err
}

func (f *fakeRepo) UpdateVideo(_ context.Context, a sqlcgen.UpdateVideoParams) (sqlcgen.Video, error) {
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
	if a.PublishAfterTranscode != nil {
		r.PublishAfterTranscode = *a.PublishAfterTranscode
	}
	r.UpdatedAt = time.Now()
	f.videos[a.ID] = r
	return rowToVideo(r), nil
}

func (f *fakeRepo) DeleteVideo(_ context.Context, id uuid.UUID) error {
	delete(f.videos, id)
	return nil
}

func (f *fakeRepo) CreateVideoFile(_ context.Context, a sqlcgen.CreateVideoFileParams) (sqlcgen.VideoFile, error) {
	vf := sqlcgen.VideoFile{
		ID: uuid.New(), VideoID: a.VideoID, Kind: a.Kind, StorageKey: a.StorageKey,
		ContentType: a.ContentType, OriginalName: a.OriginalName, SizeBytes: a.SizeBytes,
		Sha256: a.Sha256, CreatedAt: time.Now(),
	}
	f.files[a.VideoID] = append(f.files[a.VideoID], vf)
	return vf, nil
}

func (f *fakeRepo) GetVideoFileByKind(_ context.Context, a sqlcgen.GetVideoFileByKindParams) (sqlcgen.VideoFile, error) {
	// fileByKindErr injects a TRANSIENT failure (never a miss): callers now have
	// to tell "there is no such row" apart from "I could not find out", so the
	// fake has to be able to produce both.
	if f.fileByKindErr != nil {
		return sqlcgen.VideoFile{}, f.fileByKindErr
	}
	var newest sqlcgen.VideoFile
	found := false
	for _, vf := range f.files[a.VideoID] {
		if vf.Kind == a.Kind && (!found || vf.CreatedAt.After(newest.CreatedAt)) {
			newest, found = vf, true
		}
	}
	if !found {
		// The production sentinel, not a generic error: a miss is a legitimate
		// "no prior original" and must not read as a database fault.
		return sqlcgen.VideoFile{}, pgx.ErrNoRows
	}
	return newest, nil
}

func (f *fakeRepo) DeleteVideoFilesByVideoAndKind(_ context.Context, a sqlcgen.DeleteVideoFilesByVideoAndKindParams) error {
	kept := f.files[a.VideoID][:0]
	for _, vf := range f.files[a.VideoID] {
		if vf.Kind != a.Kind {
			kept = append(kept, vf)
		}
	}
	f.files[a.VideoID] = kept
	return nil
}

func (f *fakeRepo) SetVideoState(_ context.Context, a sqlcgen.SetVideoStateParams) (sqlcgen.Video, error) {
	r, ok := f.videos[a.ID]
	if !ok {
		return sqlcgen.Video{}, errors.New("not found")
	}
	r.State = a.State
	r.UpdatedAt = time.Now()
	f.videos[a.ID] = r
	return rowToVideo(r), nil
}

func (f *fakeRepo) ListAdminVideos(_ context.Context, a sqlcgen.ListAdminVideosParams) ([]sqlcgen.ListAdminVideosRow, error) {
	var all []sqlcgen.ListAdminVideosRow
	for _, r := range f.videos {
		if a.Query != nil && !strings.Contains(strings.ToLower(r.Title), strings.ToLower(*a.Query)) {
			continue
		}
		all = append(all, sqlcgen.ListAdminVideosRow{
			ID: r.ID, Title: r.Title, Privacy: r.Privacy, State: r.State,
			Views: f.views[r.ID], CreatedAt: r.CreatedAt,
		})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].CreatedAt.After(all[j].CreatedAt) })
	return all, nil
}

func (f *fakeRepo) SearchPublicVideos(_ context.Context, a sqlcgen.SearchPublicVideosParams) ([]sqlcgen.SearchPublicVideosRow, error) {
	q := strings.ToLower(a.Query)
	var all []sqlcgen.SearchPublicVideosRow
	for _, r := range f.videos {
		if r.Privacy == "public" && r.State == "published" && strings.Contains(strings.ToLower(r.Title), q) {
			all = append(all, sqlcgen.SearchPublicVideosRow{
				ID: r.ID, ChannelID: r.ChannelID, Title: r.Title, Description: r.Description,
				Privacy: r.Privacy, State: r.State, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
				Views: f.views[r.ID], HasThumbnail: f.hasThumb(r.ID), DurationSeconds: f.duration(r.ID),
			})
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].CreatedAt.After(all[j].CreatedAt) })
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

func (f *fakeRepo) ListPublicVideosByIDs(_ context.Context, a sqlcgen.ListPublicVideosByIDsParams) ([]sqlcgen.ListPublicVideosByIDsRow, error) {
	want := make(map[uuid.UUID]bool, len(a.Ids))
	for _, id := range a.Ids {
		want[id] = true
	}
	var rows []sqlcgen.ListPublicVideosByIDsRow
	for _, r := range f.videos {
		if !want[r.ID] || r.Privacy != "public" || r.State != "published" {
			continue
		}
		if a.HideSensitive && r.IsSensitive {
			continue
		}
		rows = append(rows, sqlcgen.ListPublicVideosByIDsRow{
			ID: r.ID, ChannelID: r.ChannelID, Title: r.Title, Description: r.Description,
			Privacy: r.Privacy, State: r.State, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
			Views: f.views[r.ID], HasThumbnail: f.hasThumb(r.ID), DurationSeconds: f.duration(r.ID),
			IsSensitive: r.IsSensitive,
		})
	}
	return rows, nil
}

func (f *fakeRepo) ListRelatedVideosFallback(_ context.Context, a sqlcgen.ListRelatedVideosFallbackParams) ([]sqlcgen.ListRelatedVideosFallbackRow, error) {
	var rows []sqlcgen.ListRelatedVideosFallbackRow
	for _, r := range f.videos {
		if r.ID == a.ExcludeID || r.Privacy != "public" || r.State != "published" {
			continue
		}
		if a.HideSensitive && r.IsSensitive {
			continue
		}
		sameChannel := r.ChannelID == a.ChannelID
		matchCat := a.Category != nil && r.Category != nil && *r.Category == *a.Category
		if !sameChannel && !matchCat {
			continue
		}
		rows = append(rows, sqlcgen.ListRelatedVideosFallbackRow{
			ID: r.ID, ChannelID: r.ChannelID, Title: r.Title, Description: r.Description,
			Privacy: r.Privacy, State: r.State, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
			Views: f.views[r.ID], HasThumbnail: f.hasThumb(r.ID), DurationSeconds: f.duration(r.ID),
			IsSensitive: r.IsSensitive, SameChannel: sameChannel,
		})
	}
	if int(a.ResultLimit) < len(rows) {
		rows = rows[:a.ResultLimit]
	}
	return rows, nil
}

func (f *fakeRepo) hasThumb(id uuid.UUID) bool {
	for _, vf := range f.files[id] {
		if vf.Kind == "thumbnail" {
			return true
		}
	}
	return false
}

// duration mirrors the card queries' LEFT JOIN video_metadata: the probed
// duration when metadata exists, nil (unknown) otherwise.
func (f *fakeRepo) duration(id uuid.UUID) *int32 {
	if m, ok := f.metadata[id]; ok {
		return m.DurationSeconds
	}
	return nil
}

func (f *fakeRepo) ListPublicVideosSorted(_ context.Context, a sqlcgen.ListPublicVideosSortedParams) ([]sqlcgen.ListPublicVideosSortedRow, error) {
	var rows []sqlcgen.ListPublicVideosSortedRow
	for _, r := range f.videos {
		if r.Privacy == "public" && r.State == "published" {
			rows = append(rows, sqlcgen.ListPublicVideosSortedRow{
				ID: r.ID, ChannelID: r.ChannelID, Title: r.Title, Description: r.Description,
				Privacy: r.Privacy, State: r.State, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
				Views: f.views[r.ID], HasThumbnail: f.hasThumb(r.ID), DurationSeconds: f.duration(r.ID),
			})
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		switch a.Sort {
		case "popular":
			if rows[i].Views != rows[j].Views {
				return rows[i].Views > rows[j].Views
			}
		case "trending":
			si, sj := trendingScore(rows[i]), trendingScore(rows[j])
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

func trendingScore(r sqlcgen.ListPublicVideosSortedRow) float64 {
	hours := time.Since(r.CreatedAt).Hours()
	return float64(r.Views) / math.Pow(hours+2, 1.5)
}

func TestCreateDraftDefaultsToDraftState(t *testing.T) {
	owner := uuid.New()
	svc := NewService(newFakeRepo(owner), nil)
	ch := uuid.New()
	v, err := svc.CreateDraft(context.Background(), ch, CreateInput{Title: "Hello", Privacy: "private"})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	if v.State != "draft" {
		t.Errorf("state = %q, want draft", v.State)
	}
	if v.ChannelID != ch || v.Title != "Hello" || v.Privacy != "private" {
		t.Errorf("unexpected video: %+v", v)
	}
}

func TestGetByIDReturnsOwner(t *testing.T) {
	owner := uuid.New()
	svc := NewService(newFakeRepo(owner), nil)
	created, _ := svc.CreateDraft(context.Background(), uuid.New(), CreateInput{Title: "T", Privacy: "public"})

	got, err := svc.GetByID(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.OwnerID != owner {
		t.Errorf("owner = %v, want %v", got.OwnerID, owner)
	}
}

func TestGetByIDNotFound(t *testing.T) {
	svc := NewService(newFakeRepo(uuid.New()), nil)
	if _, err := svc.GetByID(context.Background(), uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestListSubscriptionsOnlyFollowedChannels(t *testing.T) {
	repo := newFakeRepo(uuid.New())
	followed, other := uuid.New(), uuid.New()
	now := time.Now()
	inFollowed, inOther := uuid.New(), uuid.New()
	repo.videos[inFollowed] = sqlcgen.GetVideoByIDRow{
		ID: inFollowed, ChannelID: followed, Title: "Followed", Privacy: "public", State: "published", CreatedAt: now, UpdatedAt: now,
	}
	repo.videos[inOther] = sqlcgen.GetVideoByIDRow{
		ID: inOther, ChannelID: other, Title: "Other", Privacy: "public", State: "published", CreatedAt: now, UpdatedAt: now,
	}
	repo.followed[followed] = true

	items, _, err := NewService(repo, nil).ListSubscriptions(context.Background(), uuid.New(), 20, 0)
	if err != nil {
		t.Fatalf("ListSubscriptions: %v", err)
	}
	if len(items) != 1 || items[0].Video.ID != inFollowed {
		t.Fatalf("want only the followed-channel video, got %d items: %+v", len(items), items)
	}
}

func TestWatchHistoryRecordListAndProgress(t *testing.T) {
	repo := newFakeRepo(uuid.New())
	ch := uuid.New()
	now := time.Now()
	v1, v2 := uuid.New(), uuid.New()
	repo.videos[v1] = sqlcgen.GetVideoByIDRow{ID: v1, ChannelID: ch, Title: "first", Privacy: "public", State: "published", CreatedAt: now, UpdatedAt: now}
	repo.videos[v2] = sqlcgen.GetVideoByIDRow{ID: v2, ChannelID: ch, Title: "second", Privacy: "public", State: "published", CreatedAt: now, UpdatedAt: now}

	svc := NewService(repo, nil)
	ctx := context.Background()
	user := uuid.New()

	// A negative position is clamped to 0.
	if err := svc.RecordProgress(ctx, v1, user, -3); err != nil {
		t.Fatalf("RecordProgress v1: %v", err)
	}
	if pos, ok, _ := svc.Progress(ctx, v1, user); !ok || pos != 0 {
		t.Fatalf("Progress v1 = (%d, %v), want (0, true)", pos, ok)
	}

	// Record v2 most recently → it sorts first in history.
	if err := svc.RecordProgress(ctx, v2, user, 12); err != nil {
		t.Fatalf("RecordProgress v2: %v", err)
	}
	items, _, err := svc.ListHistory(ctx, user, 20, 0, false)
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	if len(items) != 2 || items[0].Video.ID != v2 || items[0].PositionSeconds != 12 {
		t.Fatalf("history = %+v, want [v2 pos 12, v1]", items)
	}

	// No progress recorded → absent.
	if _, ok, _ := svc.Progress(ctx, uuid.New(), user); ok {
		t.Errorf("Progress for unwatched video reported present")
	}

	// Remove one entry, then clear.
	if err := svc.RemoveHistoryEntry(ctx, v2, user); err != nil {
		t.Fatalf("RemoveHistoryEntry: %v", err)
	}
	if items, _, _ := svc.ListHistory(ctx, user, 20, 0, false); len(items) != 1 || items[0].Video.ID != v1 {
		t.Fatalf("after remove, history = %+v, want [v1]", items)
	}
	if err := svc.ClearHistory(ctx, user); err != nil {
		t.Fatalf("ClearHistory: %v", err)
	}
	if items, _, _ := svc.ListHistory(ctx, user, 20, 0, false); len(items) != 0 {
		t.Fatalf("after clear, history = %+v, want empty", items)
	}
}

func strptr(s string) *string { return &s }

func TestUpdateVideoOwnerNonOwnerNotFound(t *testing.T) {
	owner := uuid.New()
	svc := NewService(newFakeRepo(owner), nil)
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "old", Privacy: "private"})

	// Owner partial update.
	up, err := svc.Update(ctx, owner, v.ID, UpdateInput{Title: strptr("new")})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if up.Title != "new" || up.Privacy != "private" {
		t.Errorf("unexpected update: %+v", up)
	}
	// Non-owner forbidden.
	if _, err := svc.Update(ctx, uuid.New(), v.ID, UpdateInput{Title: strptr("hax")}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-owner err = %v, want ErrForbidden", err)
	}
	// A caller explicitly authorized by the HTTP policy layer may manage a
	// non-owned local video, while the ordinary Update contract stays owner-only.
	manager := uuid.New()
	managed, err := svc.UpdateForActor(ctx, manager, v.ID, UpdateInput{Title: strptr("moderated")}, true)
	if err != nil {
		t.Fatalf("managed update: %v", err)
	}
	if managed.Title != "moderated" {
		t.Errorf("managed title = %q, want moderated", managed.Title)
	}
	// Unknown id not found.
	if _, err := svc.UpdateForActor(ctx, manager, uuid.New(), UpdateInput{Title: strptr("x")}, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown err = %v, want ErrNotFound", err)
	}
}

func TestDeleteVideoOwnerAndNonOwner(t *testing.T) {
	owner := uuid.New()
	svc := NewService(newFakeRepo(owner), nil)
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "private"})

	if err := svc.Delete(ctx, uuid.New(), v.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-owner delete err = %v, want ErrForbidden", err)
	}
	if err := svc.DeleteForActor(ctx, uuid.New(), v.ID, true); err != nil {
		t.Fatalf("managed delete: %v", err)
	}
	if _, err := svc.GetByID(ctx, v.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after managed delete err = %v, want ErrNotFound", err)
	}

	owned, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "owned", Privacy: "private"})
	if err := svc.Delete(ctx, owner, owned.ID); err != nil {
		t.Fatalf("owner delete: %v", err)
	}
	if _, err := svc.GetByID(ctx, owned.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete err = %v, want ErrNotFound", err)
	}
}

func TestListPublicPaginates(t *testing.T) {
	owner := uuid.New()
	svc := NewService(newFakeRepo(owner), nil)
	ctx := context.Background()
	ch := uuid.New()
	// 3 public + 1 private across (logically) the instance. Only published
	// videos surface in the public feed.
	for i := 0; i < 3; i++ {
		publishDraft(t, svc, ctx, ch, CreateInput{Title: "pub", Privacy: "public"})
	}
	_, _ = svc.CreateDraft(ctx, ch, CreateInput{Title: "priv", Privacy: "private"})

	page1, _, err := svc.ListPublic(ctx, "recent", "local", FeedFilter{}, uuid.Nil, false, 2, 0)
	if err != nil {
		t.Fatalf("ListPublic: %v", err)
	}
	if len(page1) != 2 {
		t.Errorf("page1 = %d, want 2", len(page1))
	}
	page2, _, _ := svc.ListPublic(ctx, "recent", "local", FeedFilter{}, uuid.Nil, false, 2, 2)
	if len(page2) != 1 { // only 3 public total
		t.Errorf("page2 = %d, want 1", len(page2))
	}
	for _, it := range append(page1, page2...) {
		if it.Video.Privacy != "public" {
			t.Errorf("feed contained non-public video: %+v", it)
		}
	}
}

func TestListPublicSortModes(t *testing.T) {
	owner := uuid.New()
	repo := newFakeRepo(owner)
	svc := NewService(repo, nil)
	ctx := context.Background()
	ch := uuid.New()
	a := publishDraft(t, svc, ctx, ch, CreateInput{Title: "a", Privacy: "public"})
	b := publishDraft(t, svc, ctx, ch, CreateInput{Title: "b", Privacy: "public"})
	// b has more views than a.
	for i := 0; i < 5; i++ {
		_, _ = repo.IncrementVideoViews(ctx, b.ID)
	}
	_, _ = repo.IncrementVideoViews(ctx, a.ID)

	popular, _, _ := svc.ListPublic(ctx, "popular", "local", FeedFilter{}, uuid.Nil, false, 20, 0)
	if len(popular) != 2 || popular[0].Video.ID != b.ID {
		t.Errorf("popular order = %v, want most-viewed (b) first", feedIDs(popular))
	}
	if popular[0].Views != 5 {
		t.Errorf("top item views = %d, want 5", popular[0].Views)
	}
	// Unknown sort falls back to recent (newest first -> b created after a).
	recent, _, _ := svc.ListPublic(ctx, "bogus", "local", FeedFilter{}, uuid.Nil, false, 20, 0)
	if len(recent) != 2 || recent[0].Video.ID != b.ID {
		t.Errorf("fallback order = %v, want recent (b first)", feedIDs(recent))
	}
}

func feedIDs(items []FeedItem) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(items))
	for _, it := range items {
		out = append(out, it.Video.ID)
	}
	return out
}

// TestFeedCardsCarryDuration asserts the probed duration rides discovery-card
// views (feed + watch history), so the frontend can show a length badge and a
// resume progress bar; it is nil (omitted) when the video has not been probed.
func TestFeedCardsCarryDuration(t *testing.T) {
	owner := uuid.New()
	repo := newFakeRepo(owner)
	svc := NewService(repo, nil)
	ctx := context.Background()
	ch := uuid.New()

	withDur := publishDraft(t, svc, ctx, ch, CreateInput{Title: "probed", Privacy: "public"})
	noDur := publishDraft(t, svc, ctx, ch, CreateInput{Title: "unprobed", Privacy: "public"})

	// Record a probed duration for one video; the other stays unprobed.
	d := int32(137)
	if _, err := repo.UpsertVideoMetadata(ctx, sqlcgen.UpsertVideoMetadataParams{
		VideoID: withDur.ID, DurationSeconds: &d,
	}); err != nil {
		t.Fatalf("UpsertVideoMetadata: %v", err)
	}

	feed, _, err := svc.ListPublic(ctx, "recent", "local", FeedFilter{}, uuid.Nil, false, 20, 0)
	if err != nil {
		t.Fatalf("ListPublic: %v", err)
	}
	got := map[uuid.UUID]*int32{}
	for _, it := range feed {
		got[it.Video.ID] = it.DurationSeconds
	}
	if got[withDur.ID] == nil || *got[withDur.ID] != 137 {
		t.Errorf("probed card duration = %v, want 137", got[withDur.ID])
	}
	if got[noDur.ID] != nil {
		t.Errorf("unprobed card duration = %v, want nil (omitted)", got[noDur.ID])
	}

	// The same duration rides the watch-history card so a resume progress bar
	// can be scaled against the full video length.
	if err := svc.RecordProgress(ctx, withDur.ID, owner, 42); err != nil {
		t.Fatalf("RecordProgress: %v", err)
	}
	hist, _, err := svc.ListHistory(ctx, owner, 20, 0, false)
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	if len(hist) != 1 || hist[0].DurationSeconds == nil || *hist[0].DurationSeconds != 137 {
		t.Errorf("history card duration = %+v, want a card with duration 137", hist)
	}
}

// TestListHistoryInProgressFilter covers the "Continue watching" filter
// (home-featured-banner A3): a started-but-unfinished video is included, a
// barely-started (< 5s) one and an effectively-finished (>= 95%) one are
// excluded, and zero/NULL-duration entries keep the lower-bound-only behaviour.
// The unfiltered history still returns every recorded entry.
func TestListHistoryInProgressFilter(t *testing.T) {
	owner := uuid.New()
	repo := newFakeRepo(owner)
	svc := NewService(repo, nil)
	ctx := context.Background()
	ch := uuid.New()
	user := uuid.New()

	// Five videos with distinct (duration, position) cases.
	halfway := publishDraft(t, svc, ctx, ch, CreateInput{Title: "halfway", Privacy: "public"})   // 50/100 = 50%
	finished := publishDraft(t, svc, ctx, ch, CreateInput{Title: "finished", Privacy: "public"}) // 96/100 = 96%
	barely := publishDraft(t, svc, ctx, ch, CreateInput{Title: "barely", Privacy: "public"})     // 3s, below RESUME_MIN
	zeroPos := publishDraft(t, svc, ctx, ch, CreateInput{Title: "zeropos", Privacy: "public"})   // 0s (0%)
	noDur := publishDraft(t, svc, ctx, ch, CreateInput{Title: "nodur", Privacy: "public"})       // 30s, unprobed (NULL duration)

	d100 := int32(100)
	for _, id := range []uuid.UUID{halfway.ID, finished.ID, barely.ID, zeroPos.ID} {
		if _, err := repo.UpsertVideoMetadata(ctx, sqlcgen.UpsertVideoMetadataParams{VideoID: id, DurationSeconds: &d100}); err != nil {
			t.Fatalf("UpsertVideoMetadata: %v", err)
		}
	}
	// noDur stays unprobed (NULL duration).

	positions := map[uuid.UUID]int32{
		halfway.ID: 50, finished.ID: 96, barely.ID: 3, zeroPos.ID: 0, noDur.ID: 30,
	}
	for id, pos := range positions {
		if err := svc.RecordProgress(ctx, id, user, pos); err != nil {
			t.Fatalf("RecordProgress: %v", err)
		}
	}

	// Unfiltered: every recorded entry is present.
	all, _, err := svc.ListHistory(ctx, user, 20, 0, false)
	if err != nil {
		t.Fatalf("ListHistory(all): %v", err)
	}
	if len(all) != 5 {
		t.Fatalf("unfiltered history = %d entries, want 5", len(all))
	}

	// in_progress: halfway + noDur only. finished (>= 95%), barely (< 5s) and
	// zeroPos (0s) are all excluded.
	inProg, _, err := svc.ListHistory(ctx, user, 20, 0, true)
	if err != nil {
		t.Fatalf("ListHistory(in_progress): %v", err)
	}
	got := map[uuid.UUID]bool{}
	for _, it := range inProg {
		got[it.Video.ID] = true
	}
	want := map[uuid.UUID]bool{halfway.ID: true, noDur.ID: true}
	if len(got) != len(want) {
		t.Fatalf("in_progress history = %d entries %v, want %v", len(got), got, want)
	}
	for id := range want {
		if !got[id] {
			t.Errorf("in_progress missing expected video %s", id)
		}
	}
	for _, id := range []uuid.UUID{finished.ID, barely.ID, zeroPos.ID} {
		if got[id] {
			t.Errorf("in_progress unexpectedly included %s", id)
		}
	}

	// Zero-duration edge: a probed 0-second video with a real position is treated
	// like unknown duration (lower-bound only) and stays in the shelf.
	zeroDur := publishDraft(t, svc, ctx, ch, CreateInput{Title: "zerodur", Privacy: "public"})
	zero := int32(0)
	if _, err := repo.UpsertVideoMetadata(ctx, sqlcgen.UpsertVideoMetadataParams{VideoID: zeroDur.ID, DurationSeconds: &zero}); err != nil {
		t.Fatalf("UpsertVideoMetadata(zero): %v", err)
	}
	if err := svc.RecordProgress(ctx, zeroDur.ID, user, 30); err != nil {
		t.Fatalf("RecordProgress(zeroDur): %v", err)
	}
	inProg2, _, err := svc.ListHistory(ctx, user, 20, 0, true)
	if err != nil {
		t.Fatalf("ListHistory(in_progress) 2: %v", err)
	}
	found := false
	for _, it := range inProg2 {
		if it.Video.ID == zeroDur.ID {
			found = true
		}
	}
	if !found {
		t.Error("zero-duration video with position 30 should appear in Continue watching")
	}
}

func TestSearchPublicMatchesTitleAndExcludesPrivate(t *testing.T) {
	owner := uuid.New()
	svc := NewService(newFakeRepo(owner), nil)
	ctx := context.Background()
	ch := uuid.New()
	publishDraft(t, svc, ctx, ch, CreateInput{Title: "Go concurrency", Privacy: "public"})
	publishDraft(t, svc, ctx, ch, CreateInput{Title: "Rust basics", Privacy: "public"})
	_, _ = svc.CreateDraft(ctx, ch, CreateInput{Title: "Go internals", Privacy: "private"})

	res, _, err := svc.SearchPublic(ctx, "go", SearchFilter{}, SearchSortDefault, uuid.Nil, false, 20, 0)
	if err != nil {
		t.Fatalf("SearchPublic: %v", err)
	}
	if len(res) != 1 || res[0].Video.Title != "Go concurrency" {
		t.Errorf("search = %+v, want only the public Go video", res)
	}
}

func TestListByChannelVsPublic(t *testing.T) {
	owner := uuid.New()
	svc := NewService(newFakeRepo(owner), nil)
	ctx := context.Background()
	ch := uuid.New()
	publishDraft(t, svc, ctx, ch, CreateInput{Title: "pub", Privacy: "public"})
	_, _ = svc.CreateDraft(ctx, ch, CreateInput{Title: "priv", Privacy: "private"})

	all, _, _ := svc.ListByChannel(ctx, ch, "", 100, 0)
	if len(all) != 2 {
		t.Errorf("ListByChannel = %d, want 2 (owner sees all states)", len(all))
	}
	pub, _, _ := svc.ListPublicByChannel(ctx, ch, false, "", 100, 0)
	if len(pub) != 1 || pub[0].Video.Privacy != "public" {
		t.Errorf("ListPublicByChannel = %+v, want 1 public", pub)
	}
}

func TestListPublicByChannelCanHideSensitive(t *testing.T) {
	owner := uuid.New()
	svc := NewService(newFakeRepo(owner), nil)
	ctx := context.Background()
	ch := uuid.New()
	publishDraft(t, svc, ctx, ch, CreateInput{Title: "visible", Privacy: "public"})
	publishDraft(t, svc, ctx, ch, CreateInput{Title: "sensitive", Privacy: "public", IsSensitive: true})

	all, _, err := svc.ListPublicByChannel(ctx, ch, false, "", 100, 0)
	if err != nil {
		t.Fatalf("ListPublicByChannel all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("ListPublicByChannel all = %d, want 2", len(all))
	}
	filtered, _, err := svc.ListPublicByChannel(ctx, ch, true, "", 100, 0)
	if err != nil {
		t.Fatalf("ListPublicByChannel filtered: %v", err)
	}
	if len(filtered) != 1 || filtered[0].Video.Title != "visible" {
		t.Errorf("ListPublicByChannel filtered = %+v, want only visible", filtered)
	}
}

// publishDraft creates a draft and publishes it via Process (nil-prober path),
// returning the created video. Used by feed/search tests that need published rows.
func publishDraft(t *testing.T, svc *Service, ctx context.Context, ch uuid.UUID, in CreateInput) sqlcgen.Video {
	t.Helper()
	v, err := svc.CreateDraft(ctx, ch, in)
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	if _, err := svc.Process(ctx, v.ID, ""); err != nil {
		t.Fatalf("Process: %v", err)
	}
	return v
}

func TestAttachOriginalStoresBytesAndFlipsToProcessing(t *testing.T) {
	owner := uuid.New()
	repo := newFakeRepo(owner)
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	svc := NewService(repo, blobs)
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "private"})

	const content = "fake original video bytes"
	updated, file, err := svc.AttachOriginal(ctx, owner, v.ID, UploadInput{
		Filename: "Clip.MP4", ContentType: "video/mp4", Reader: strings.NewReader(content),
	})
	if err != nil {
		t.Fatalf("AttachOriginal: %v", err)
	}
	if updated.State != "processing" {
		t.Errorf("state = %q, want processing", updated.State)
	}
	if file.Kind != "original" || file.ContentType != "video/mp4" || file.OriginalName != "Clip.MP4" {
		t.Errorf("unexpected file metadata: %+v", file)
	}
	if file.SizeBytes != int64(len(content)) {
		t.Errorf("size = %d, want %d", file.SizeBytes, len(content))
	}
	if want := "web-videos/" + v.ID.String() + ".mp4"; file.StorageKey != want {
		t.Errorf("key = %q, want %q", file.StorageKey, want)
	}
	rc, err := blobs.Open(ctx, file.StorageKey)
	if err != nil {
		t.Fatalf("Open stored blob: %v", err)
	}
	defer func() { _ = rc.Close() }()
	got, _ := io.ReadAll(rc)
	if string(got) != content {
		t.Errorf("stored bytes = %q, want %q", got, content)
	}
}

// TestAttachOriginalRecordsContentHash proves the file row an upload creates
// carries the SHA-256 of the bytes that were stored (phase-2 storage, work item
// 2). The expected digest is the published vector for "hello world", pinned as
// a literal rather than recomputed here, so a change to how or where the hash is
// taken fails this test instead of agreeing with itself.
//
// It also covers the thumbnail path, which stores a byte slice rather than a
// stream: both shapes have to produce a digest, because a row that lands with
// the empty state silently defers to the backfill worker instead of failing.
func TestAttachOriginalRecordsContentHash(t *testing.T) {
	const (
		content = "hello world"
		want    = "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"
	)
	owner := uuid.New()
	repo := newFakeRepo(owner)
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	svc := NewService(repo, blobs)
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "private"})

	_, file, err := svc.AttachOriginal(ctx, owner, v.ID, UploadInput{
		Filename: "clip.mp4", ContentType: "video/mp4", Reader: strings.NewReader(content),
	})
	if err != nil {
		t.Fatalf("AttachOriginal: %v", err)
	}
	if file.Sha256 != want {
		t.Errorf("original sha256 = %q, want %q", file.Sha256, want)
	}
	stored, err := repo.GetVideoFileByKind(ctx, sqlcgen.GetVideoFileByKindParams{VideoID: v.ID, Kind: "original"})
	if err != nil {
		t.Fatalf("GetVideoFileByKind: %v", err)
	}
	if stored.Sha256 != want {
		t.Errorf("persisted sha256 = %q, want %q", stored.Sha256, want)
	}

	thumb, err := svc.SetThumbnail(ctx, owner, v.ID, UploadInput{
		Filename: "poster.jpg", Reader: strings.NewReader(content),
	})
	if err != nil {
		t.Fatalf("SetThumbnail: %v", err)
	}
	if thumb.Sha256 != want {
		t.Errorf("thumbnail sha256 = %q, want %q", thumb.Sha256, want)
	}
}

// TestAttachOriginalContentTypeFallback: a filename with no usable extension
// (a URL import from an extension-less path) is accepted when the Content-Type is
// a known video container, and the stored key gets the derived extension.
func TestAttachOriginalContentTypeFallback(t *testing.T) {
	owner := uuid.New()
	blobs, _ := storage.NewLocal(t.TempDir())
	svc := NewService(newFakeRepo(owner), blobs)
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "private"})

	// No extension on the name, but Content-Type video/mp4 (with a charset param).
	_, file, err := svc.AttachOriginal(ctx, owner, v.ID, UploadInput{
		Filename: "original", ContentType: "video/mp4; charset=binary", Reader: strings.NewReader("bytes"),
	})
	if err != nil {
		t.Fatalf("AttachOriginal with content-type fallback: %v", err)
	}
	if want := "web-videos/" + v.ID.String() + ".mp4"; file.StorageKey != want {
		t.Errorf("key = %q, want %q (extension derived from content type)", file.StorageKey, want)
	}

	// An unrecognised content type on an extension-less name is still rejected.
	v2, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t2", Privacy: "private"})
	if _, _, err := svc.AttachOriginal(ctx, owner, v2.ID, UploadInput{
		Filename: "download", ContentType: "application/octet-stream", Reader: strings.NewReader("x"),
	}); !errors.Is(err, ErrUnsupportedMedia) {
		t.Errorf("octet-stream err = %v, want ErrUnsupportedMedia", err)
	}
}

func TestExtForContentType(t *testing.T) {
	cases := map[string]string{
		"video/mp4":                ".mp4",
		"VIDEO/WEBM":               ".webm",
		"video/quicktime; x=y":     ".mov",
		"  video/x-matroska  ":     ".mkv",
		"application/octet-stream": "",
		"":                         "",
	}
	for ct, want := range cases {
		got, ok := extForContentType(ct)
		if want == "" {
			if ok {
				t.Errorf("extForContentType(%q) = (%q, true), want not ok", ct, got)
			}
			continue
		}
		if !ok || got != want {
			t.Errorf("extForContentType(%q) = (%q, %v), want (%q, true)", ct, got, ok, want)
		}
	}
}

func TestAttachOriginalRejectsNonOwnerAndUnknown(t *testing.T) {
	owner := uuid.New()
	blobs, _ := storage.NewLocal(t.TempDir())
	svc := NewService(newFakeRepo(owner), blobs)
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "private"})

	if _, _, err := svc.AttachOriginal(ctx, uuid.New(), v.ID, UploadInput{Filename: "a.mp4", Reader: strings.NewReader("x")}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-owner err = %v, want ErrForbidden", err)
	}
	if _, _, err := svc.AttachOriginal(ctx, owner, uuid.New(), UploadInput{Filename: "a.mp4", Reader: strings.NewReader("x")}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id err = %v, want ErrNotFound", err)
	}
}

func TestAttachOriginalReplacesPreviousOriginal(t *testing.T) {
	owner := uuid.New()
	repo := newFakeRepo(owner)
	blobs, _ := storage.NewLocal(t.TempDir())
	svc := NewService(repo, blobs)
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "private"})

	if _, _, err := svc.AttachOriginal(ctx, owner, v.ID, UploadInput{Filename: "a.mp4", Reader: strings.NewReader("first")}); err != nil {
		t.Fatalf("first upload: %v", err)
	}
	if _, _, err := svc.AttachOriginal(ctx, owner, v.ID, UploadInput{Filename: "b.mp4", Reader: strings.NewReader("second-take")}); err != nil {
		t.Fatalf("second upload: %v", err)
	}
	if got := len(repo.files[v.ID]); got != 1 {
		t.Errorf("original rows = %d, want 1 (re-upload replaces)", got)
	}
}

func TestAttachOriginalWithoutStorage(t *testing.T) {
	owner := uuid.New()
	svc := NewService(newFakeRepo(owner), nil)
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "private"})
	if _, _, err := svc.AttachOriginal(ctx, owner, v.ID, UploadInput{Filename: "a.mp4", Reader: strings.NewReader("x")}); !errors.Is(err, ErrStorageUnavailable) {
		t.Fatalf("err = %v, want ErrStorageUnavailable", err)
	}
}

func TestAcceptedExtAndOriginalKey(t *testing.T) {
	id := uuid.New()
	accepted := map[string]string{ // filename -> normalized extension
		"clip.mp4":  ".mp4",
		"clip.WEBM": ".webm",
		"a.MOV":     ".mov",
		"x.mkv":     ".mkv",
	}
	for filename, wantExt := range accepted {
		ext, ok := AcceptedVideoExt(filename)
		if !ok || ext != wantExt {
			t.Errorf("AcceptedVideoExt(%q) = (%q, %v), want (%q, true)", filename, ext, ok, wantExt)
		}
		if got, want := originalKey(id, ext), "web-videos/"+id.String()+wantExt; got != want {
			t.Errorf("originalKey(%q) = %q, want %q", ext, got, want)
		}
	}
	for _, filename := range []string{"noext", "weird.tar.gz", "evil.../x.m p", "doc.pdf", "image.png", "a.exe", ""} {
		if ext, ok := AcceptedVideoExt(filename); ok {
			t.Errorf("AcceptedVideoExt(%q) = (%q, true), want false (not a video container)", filename, ext)
		}
	}
}

func TestAttachOriginalRejectsUnsupportedExtension(t *testing.T) {
	owner := uuid.New()
	repo := newFakeRepo(owner)
	blobs, _ := storage.NewLocal(t.TempDir())
	svc := NewService(repo, blobs)
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "private"})

	if _, _, err := svc.AttachOriginal(ctx, owner, v.ID, UploadInput{Filename: "notes.pdf", Reader: strings.NewReader("x")}); !errors.Is(err, ErrUnsupportedMedia) {
		t.Fatalf("err = %v, want ErrUnsupportedMedia", err)
	}
	// A rejected upload stores nothing and leaves the video a draft.
	if n := len(repo.files[v.ID]); n != 0 {
		t.Errorf("file rows = %d, want 0", n)
	}
	if got, _ := svc.GetByID(ctx, v.ID); got.State != "draft" {
		t.Errorf("state = %q, want draft (unchanged)", got.State)
	}
	// Ownership is still checked before media type: a non-owner gets ErrForbidden.
	if _, _, err := svc.AttachOriginal(ctx, uuid.New(), v.ID, UploadInput{Filename: "notes.pdf", Reader: strings.NewReader("x")}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-owner err = %v, want ErrForbidden", err)
	}
}

type fakeProber struct {
	md  media.Metadata
	err error
}

func (p fakeProber) Probe(_ context.Context, _ string) (media.Metadata, error) { return p.md, p.err }

func TestProcessPublishesWithoutProber(t *testing.T) {
	svc := NewService(newFakeRepo(uuid.New()), nil)
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"})
	got, err := svc.Process(ctx, v.ID, "web-videos/x.mp4")
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if got.State != "published" {
		t.Errorf("state = %q, want published (no prober trusts the original)", got.State)
	}
}

func TestProcessPublishesWhenProberSucceeds(t *testing.T) {
	svc := NewService(newFakeRepo(uuid.New()), nil, WithProber(fakeProber{}))
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"})
	got, _ := svc.Process(ctx, v.ID, "k")
	if got.State != "published" {
		t.Errorf("state = %q, want published", got.State)
	}
}

type fakeScanner struct {
	clean bool
	err   error
}

func (f fakeScanner) Scan(context.Context, string) (bool, error) { return f.clean, f.err }

func TestProcessPublishesWhenScanClean(t *testing.T) {
	svc := NewService(newFakeRepo(uuid.New()), nil, WithScanner(fakeScanner{clean: true}))
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"})
	got, err := svc.Process(ctx, v.ID, "k")
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if got.State != "published" {
		t.Errorf("state = %q, want published (clean scan)", got.State)
	}
}

func TestProcessFailsWhenScanInfected(t *testing.T) {
	// An infected original must never publish (fail-closed), and the prober must
	// not even run (it would fail the test if reached).
	svc := NewService(newFakeRepo(uuid.New()), nil,
		WithScanner(fakeScanner{clean: false}),
		WithProber(fakeProber{err: errors.New("prober must not run for infected media")}),
	)
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"})
	got, err := svc.Process(ctx, v.ID, "k")
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if got.State != "failed" {
		t.Errorf("state = %q, want failed (infected)", got.State)
	}
}

func TestProcessFailsWhenScanUnavailable(t *testing.T) {
	// A scan error is fail-closed too — an unscannable upload is not published.
	svc := NewService(newFakeRepo(uuid.New()), nil, WithScanner(fakeScanner{err: errors.New("clamd down")}))
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"})
	got, err := svc.Process(ctx, v.ID, "k")
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if got.State != "failed" {
		t.Errorf("state = %q, want failed (scan unavailable)", got.State)
	}
}

func TestProcessFailsWhenProberErrors(t *testing.T) {
	repo := newFakeRepo(uuid.New())
	svc := NewService(repo, nil, WithProber(fakeProber{err: errors.New("not media")}))
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"})
	got, err := svc.Process(ctx, v.ID, "k")
	if err != nil { // a probe failure is a state transition, not a call error
		t.Fatalf("Process returned err: %v", err)
	}
	if got.State != "failed" {
		t.Errorf("state = %q, want failed", got.State)
	}
	if _, ok, _ := svc.GetMetadata(ctx, v.ID); ok {
		t.Error("metadata stored for a failed probe, want none")
	}
}

func TestProcessFiresTranscodeHookOnPublish(t *testing.T) {
	var gotVideo uuid.UUID
	var gotKey string
	calls := 0
	svc := NewService(newFakeRepo(uuid.New()), nil,
		WithProber(fakeProber{}),
		WithTranscodeHook(func(_ context.Context, videoID uuid.UUID, sourceKey string) {
			calls++
			gotVideo, gotKey = videoID, sourceKey
		}),
	)
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"})
	if _, err := svc.Process(ctx, v.ID, "web-videos/x.mp4"); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if calls != 1 || gotVideo != v.ID || gotKey != "web-videos/x.mp4" {
		t.Errorf("transcode hook = (calls %d, %s, %q), want (1, %s, web-videos/x.mp4)", calls, gotVideo, gotKey, v.ID)
	}
}

func TestProcessSkipsTranscodeHookOnFailedProbe(t *testing.T) {
	svc := NewService(newFakeRepo(uuid.New()), nil,
		WithProber(fakeProber{err: errors.New("not media")}),
		WithTranscodeHook(func(context.Context, uuid.UUID, string) {
			t.Error("transcode hook must not fire for a failed probe")
		}),
	)
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"})
	if got, err := svc.Process(ctx, v.ID, "k"); err != nil || got.State != "failed" {
		t.Fatalf("Process = (%v, %v), want failed state", got.State, err)
	}
}

func TestProcessPersistsProbeMetadata(t *testing.T) {
	repo := newFakeRepo(uuid.New())
	svc := NewService(repo, nil, WithProber(fakeProber{md: media.Metadata{DurationSeconds: 42, Width: 1280, Height: 720}}))
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"})
	if _, err := svc.Process(ctx, v.ID, "k"); err != nil {
		t.Fatalf("Process: %v", err)
	}
	md, ok, err := svc.GetMetadata(ctx, v.ID)
	if err != nil || !ok {
		t.Fatalf("GetMetadata ok=%v err=%v, want found", ok, err)
	}
	if md.DurationSeconds == nil || *md.DurationSeconds != 42 {
		t.Errorf("duration = %v, want 42", md.DurationSeconds)
	}
	if md.Width == nil || *md.Width != 1280 || md.Height == nil || *md.Height != 720 {
		t.Errorf("dimensions = %v x %v, want 1280x720", md.Width, md.Height)
	}
}

func TestProcessLeavesUnknownMetadataNull(t *testing.T) {
	repo := newFakeRepo(uuid.New())
	// Audio-only style probe: duration known, dimensions zero (unknown).
	svc := NewService(repo, nil, WithProber(fakeProber{md: media.Metadata{DurationSeconds: 10}}))
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"})
	if _, err := svc.Process(ctx, v.ID, "k"); err != nil {
		t.Fatalf("Process: %v", err)
	}
	md, _, _ := svc.GetMetadata(ctx, v.ID)
	if md.DurationSeconds == nil || *md.DurationSeconds != 10 {
		t.Errorf("duration = %v, want 10", md.DurationSeconds)
	}
	if md.Width != nil || md.Height != nil {
		t.Errorf("dimensions = %v x %v, want NULL/NULL", md.Width, md.Height)
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

func TestProcessStoresThumbnail(t *testing.T) {
	repo := newFakeRepo(uuid.New())
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	jpg := []byte("\xff\xd8\xff\xe0fakejpeg")
	svc := NewService(repo, blobs, WithThumbnailer(fakeThumbnailer{jpg: jpg}))
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"})
	if _, err := svc.Process(ctx, v.ID, "web-videos/x.mp4"); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if !svc.HasThumbnail(ctx, v.ID) {
		t.Fatal("HasThumbnail = false, want true")
	}
	f, err := svc.FileForView(ctx, v.ID, uuid.Nil, false, "thumbnail")
	if err != nil {
		t.Fatalf("FileForView thumbnail: %v", err)
	}
	if f.ContentType != "image/jpeg" || f.StorageKey != "thumbnails/"+v.ID.String()+".jpg" {
		t.Errorf("unexpected thumbnail file: %+v", f)
	}
	rc, err := blobs.Open(ctx, f.StorageKey)
	if err != nil {
		t.Fatalf("open stored thumbnail: %v", err)
	}
	defer func() { _ = rc.Close() }()
	got, _ := io.ReadAll(rc)
	if string(got) != string(jpg) {
		t.Errorf("stored thumbnail bytes = %q, want %q", got, jpg)
	}
}

func TestProcessThumbnailFailureStillPublishes(t *testing.T) {
	repo := newFakeRepo(uuid.New())
	blobs, _ := storage.NewLocal(t.TempDir())
	svc := NewService(repo, blobs, WithThumbnailer(fakeThumbnailer{err: errors.New("ffmpeg boom")}))
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"})
	got, err := svc.Process(ctx, v.ID, "k")
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if got.State != "published" {
		t.Errorf("state = %q, want published (thumbnail failure is non-fatal)", got.State)
	}
	if svc.HasThumbnail(ctx, v.ID) {
		t.Error("thumbnail stored despite generator error")
	}
}

func TestProcessFailedProbeSkipsThumbnail(t *testing.T) {
	repo := newFakeRepo(uuid.New())
	blobs, _ := storage.NewLocal(t.TempDir())
	svc := NewService(repo, blobs,
		WithProber(fakeProber{err: errors.New("bad media")}),
		WithThumbnailer(fakeThumbnailer{jpg: []byte("x")}),
	)
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"})
	got, _ := svc.Process(ctx, v.ID, "k")
	if got.State != "failed" {
		t.Errorf("state = %q, want failed", got.State)
	}
	if svc.HasThumbnail(ctx, v.ID) {
		t.Error("thumbnail generated for a failed video")
	}
}

// capturingThumbnailer records the ThumbnailAt arguments so the frame-pick tests
// can assert the exact source key and timestamp reach the extractor.
type capturingThumbnailer struct {
	jpg    []byte
	err    error
	gotKey string
	gotAt  float64
	atHits int
}

func (c *capturingThumbnailer) Thumbnail(_ context.Context, _ string, _ int) ([]byte, error) {
	return c.jpg, c.err
}

func (c *capturingThumbnailer) ThumbnailAt(_ context.Context, key string, at float64) ([]byte, error) {
	c.gotKey, c.gotAt, c.atHits = key, at, c.atHits+1
	return c.jpg, c.err
}

// processedVideo creates a draft, attaches an original, and probes it to a known
// duration so it has a processed original a frame-pick can target. Returns the
// video id and the original's storage key.
func processedVideo(t *testing.T, svc *Service, owner uuid.UUID, durationSeconds int) (uuid.UUID, string) {
	t.Helper()
	ctx := context.Background()
	v, err := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	_, file, err := svc.AttachOriginal(ctx, owner, v.ID, UploadInput{
		Filename: "clip.mp4", ContentType: "video/mp4", Reader: strings.NewReader("fake original bytes"),
	})
	if err != nil {
		t.Fatalf("AttachOriginal: %v", err)
	}
	if _, err := svc.Process(ctx, v.ID, file.StorageKey); err != nil {
		t.Fatalf("Process: %v", err)
	}
	return v.ID, file.StorageKey
}

func TestSetThumbnailFromFrameStoresFrame(t *testing.T) {
	owner := uuid.New()
	repo := newFakeRepo(owner)
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	frame := []byte("\xff\xd8\xff\xe0framejpeg")
	cap := &capturingThumbnailer{jpg: frame}
	svc := NewService(repo, blobs,
		WithProber(fakeProber{md: media.Metadata{DurationSeconds: 10, Width: 320, Height: 240}}),
		WithThumbnailer(cap),
	)
	ctx := context.Background()
	id, origKey := processedVideo(t, svc, owner, 10)

	f, err := svc.SetThumbnailFromFrame(ctx, owner, id, 5.5)
	if err != nil {
		t.Fatalf("SetThumbnailFromFrame: %v", err)
	}
	// The exact timestamp and the original's key reached the extractor.
	if cap.atHits != 1 || cap.gotAt != 5.5 || cap.gotKey != origKey {
		t.Errorf("extractor call = {hits:%d at:%v key:%q}, want {1 5.5 %q}", cap.atHits, cap.gotAt, cap.gotKey, origKey)
	}
	// Stored at the deterministic poster key as JPEG.
	if f.Kind != "thumbnail" || f.ContentType != "image/jpeg" || f.StorageKey != "thumbnails/"+id.String()+".jpg" {
		t.Errorf("stored file = %+v, want kind=thumbnail image/jpeg at thumbnails/%s.jpg", f, id)
	}
	if !svc.HasThumbnail(ctx, id) {
		t.Error("HasThumbnail = false after frame-pick")
	}
	rc, err := blobs.Open(ctx, f.StorageKey)
	if err != nil {
		t.Fatalf("open stored frame: %v", err)
	}
	defer func() { _ = rc.Close() }()
	got, _ := io.ReadAll(rc)
	if string(got) != string(frame) {
		t.Errorf("stored frame bytes = %q, want %q", got, frame)
	}
}

func TestSetThumbnailFromFrameErrors(t *testing.T) {
	owner := uuid.New()

	// Service with a wired extractor and a 10s-duration prober.
	newSvc := func(t *testing.T) (*Service, uuid.UUID) {
		t.Helper()
		repo := newFakeRepo(owner)
		blobs, err := storage.NewLocal(t.TempDir())
		if err != nil {
			t.Fatalf("NewLocal: %v", err)
		}
		svc := NewService(repo, blobs,
			WithProber(fakeProber{md: media.Metadata{DurationSeconds: 10}}),
			WithThumbnailer(&capturingThumbnailer{jpg: []byte("\xff\xd8jpg")}),
		)
		id, _ := processedVideo(t, svc, owner, 10)
		return svc, id
	}
	ctx := context.Background()

	t.Run("at duration is out of range", func(t *testing.T) {
		svc, id := newSvc(t)
		if _, err := svc.SetThumbnailFromFrame(ctx, owner, id, 10); !errors.Is(err, ErrThumbnailOutOfRange) {
			t.Fatalf("at==duration err = %v, want ErrThumbnailOutOfRange", err)
		}
	})
	t.Run("negative is out of range", func(t *testing.T) {
		svc, id := newSvc(t)
		if _, err := svc.SetThumbnailFromFrame(ctx, owner, id, -0.5); !errors.Is(err, ErrThumbnailOutOfRange) {
			t.Fatalf("at<0 err = %v, want ErrThumbnailOutOfRange", err)
		}
	})
	t.Run("non-owner is forbidden", func(t *testing.T) {
		svc, id := newSvc(t)
		if _, err := svc.SetThumbnailFromFrame(ctx, uuid.New(), id, 5); !errors.Is(err, ErrForbidden) {
			t.Fatalf("non-owner err = %v, want ErrForbidden", err)
		}
	})
	t.Run("unknown video is not found", func(t *testing.T) {
		svc, _ := newSvc(t)
		if _, err := svc.SetThumbnailFromFrame(ctx, owner, uuid.New(), 5); !errors.Is(err, ErrNotFound) {
			t.Fatalf("unknown err = %v, want ErrNotFound", err)
		}
	})
	t.Run("no original is a conflict", func(t *testing.T) {
		repo := newFakeRepo(owner)
		blobs, _ := storage.NewLocal(t.TempDir())
		svc := NewService(repo, blobs, WithThumbnailer(&capturingThumbnailer{jpg: []byte("j")}))
		v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"})
		if _, err := svc.SetThumbnailFromFrame(ctx, owner, v.ID, 1); !errors.Is(err, ErrNoProcessedOriginal) {
			t.Fatalf("no-original err = %v, want ErrNoProcessedOriginal", err)
		}
	})
	t.Run("original without probed duration is a conflict", func(t *testing.T) {
		// No prober -> Process publishes without recording metadata/duration.
		repo := newFakeRepo(owner)
		blobs, _ := storage.NewLocal(t.TempDir())
		svc := NewService(repo, blobs, WithThumbnailer(&capturingThumbnailer{jpg: []byte("j")}))
		v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"})
		_, file, _ := svc.AttachOriginal(ctx, owner, v.ID, UploadInput{Filename: "c.mp4", ContentType: "video/mp4", Reader: strings.NewReader("x")})
		if _, err := svc.Process(ctx, v.ID, file.StorageKey); err != nil {
			t.Fatalf("Process: %v", err)
		}
		if _, err := svc.SetThumbnailFromFrame(ctx, owner, v.ID, 1); !errors.Is(err, ErrNoProcessedOriginal) {
			t.Fatalf("no-duration err = %v, want ErrNoProcessedOriginal", err)
		}
	})
	t.Run("no extractor is unavailable", func(t *testing.T) {
		// No thumbnailer wired: frame extraction is impossible regardless of state.
		repo := newFakeRepo(owner)
		blobs, _ := storage.NewLocal(t.TempDir())
		svc := NewService(repo, blobs, WithProber(fakeProber{md: media.Metadata{DurationSeconds: 10}}))
		v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"})
		if _, err := svc.SetThumbnailFromFrame(ctx, owner, v.ID, 5); !errors.Is(err, ErrThumbnailUnavailable) {
			t.Fatalf("no-extractor err = %v, want ErrThumbnailUnavailable", err)
		}
	})
}

type fakeDeduper struct{ seen map[string]bool }

func newFakeDeduper() *fakeDeduper { return &fakeDeduper{seen: map[string]bool{}} }

func (d *fakeDeduper) First(_ context.Context, key string, _ time.Duration) (bool, error) {
	if d.seen[key] {
		return false, nil
	}
	d.seen[key] = true
	return true, nil
}

func TestRecordViewDedupesPerViewer(t *testing.T) {
	owner := uuid.New()
	svc := NewService(newFakeRepo(owner), nil, WithViewDeduper(newFakeDeduper()))
	ctx := context.Background()
	v := publishDraft(t, svc, ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"})

	for i := 0; i < 3; i++ { // viewer A pings 3x -> counts once
		if err := svc.RecordView(ctx, v.ID, uuid.Nil, false, "viewerA"); err != nil {
			t.Fatalf("RecordView A: %v", err)
		}
	}
	if err := svc.RecordView(ctx, v.ID, uuid.Nil, false, "viewerB"); err != nil { // distinct viewer -> +1
		t.Fatalf("RecordView B: %v", err)
	}
	if got := svc.Views(ctx, v.ID); got != 2 {
		t.Errorf("views = %d, want 2 (one per distinct viewer)", got)
	}
}

func TestRecordViewWithoutDeduperCountsEach(t *testing.T) {
	owner := uuid.New()
	svc := NewService(newFakeRepo(owner), nil)
	ctx := context.Background()
	v := publishDraft(t, svc, ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"})
	_ = svc.RecordView(ctx, v.ID, uuid.Nil, false, "x")
	_ = svc.RecordView(ctx, v.ID, uuid.Nil, false, "x")
	if got := svc.Views(ctx, v.ID); got != 2 {
		t.Errorf("views = %d, want 2 (no dedupe)", got)
	}
}

func TestRecordViewUnpublishedIsNoOp(t *testing.T) {
	owner := uuid.New()
	svc := NewService(newFakeRepo(owner), nil)
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"}) // draft, not processed
	if err := svc.RecordView(ctx, v.ID, uuid.Nil, false, "x"); err != nil {
		t.Fatalf("RecordView: %v", err)
	}
	if got := svc.Views(ctx, v.ID); got != 0 {
		t.Errorf("views = %d, want 0 (drafts do not accrue views)", got)
	}
}

func TestRecordViewVisibilityAndUnknown(t *testing.T) {
	owner := uuid.New()
	svc := NewService(newFakeRepo(owner), nil)
	ctx := context.Background()
	v := publishDraft(t, svc, ctx, uuid.New(), CreateInput{Title: "t", Privacy: "private"})

	if err := svc.RecordView(ctx, v.ID, uuid.Nil, false, "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("anon view of private = %v, want ErrNotFound", err)
	}
	if err := svc.RecordView(ctx, v.ID, owner, true, "owner"); err != nil {
		t.Fatalf("owner view of own private: %v", err)
	}
	if got := svc.Views(ctx, v.ID); got != 1 {
		t.Errorf("views = %d, want 1", got)
	}
	if err := svc.RecordView(ctx, uuid.New(), uuid.Nil, false, "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("view of unknown = %v, want ErrNotFound", err)
	}
}

// --- config-parity W10: upload_additional_extensions_enabled gate ---

func TestAcceptedVideoExtGated(t *testing.T) {
	// Base containers pass regardless of the gate.
	for _, f := range []string{"a.mp4", "b.WEBM", "c.ogv", "d.ogg"} {
		if _, ok := AcceptedVideoExtGated(f, false); !ok {
			t.Errorf("AcceptedVideoExtGated(%q, false) = false, want true (base set)", f)
		}
	}
	// Additional containers follow the gate.
	for _, f := range []string{"a.avi", "b.mov", "c.mkv", "d.flv", "e.wmv", "f.m4v", "g.3gp", "h.mpg", "i.mpeg", "j.ts"} {
		if _, ok := AcceptedVideoExtGated(f, true); !ok {
			t.Errorf("AcceptedVideoExtGated(%q, true) = false, want true (additional set on)", f)
		}
		if ext, ok := AcceptedVideoExtGated(f, false); ok {
			t.Errorf("AcceptedVideoExtGated(%q, false) = (%q, true), want refused (additional set off)", f, ext)
		}
	}
	// Junk is refused either way.
	if _, ok := AcceptedVideoExtGated("x.exe", true); ok {
		t.Error("AcceptedVideoExtGated(x.exe, true) accepted a non-video container")
	}
}

// TestAttachOriginalAdditionalExtGateRuntimeFlip proves the gate is consulted
// per call on ONE service instance: flipping it changes acceptance without
// reconstruction, and base containers are unaffected.
func TestAttachOriginalAdditionalExtGateRuntimeFlip(t *testing.T) {
	owner := uuid.New()
	repo := newFakeRepo(owner)
	blobs, _ := storage.NewLocal(t.TempDir())
	allowed := false
	svc := NewService(repo, blobs, WithAdditionalExtGate(func() bool { return allowed }))
	ctx := context.Background()

	// Gate off: an additional-set container is refused…
	v1, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "private"})
	if _, _, err := svc.AttachOriginal(ctx, owner, v1.ID, UploadInput{Filename: "clip.avi", Reader: strings.NewReader("x")}); !errors.Is(err, ErrUnsupportedMedia) {
		t.Fatalf("gate off .avi err = %v, want ErrUnsupportedMedia", err)
	}
	// …the content-type fallback can't smuggle one in…
	if _, _, err := svc.AttachOriginal(ctx, owner, v1.ID, UploadInput{Filename: "noext", ContentType: "video/x-msvideo", Reader: strings.NewReader("x")}); !errors.Is(err, ErrUnsupportedMedia) {
		t.Fatalf("gate off content-type fallback err = %v, want ErrUnsupportedMedia", err)
	}
	// …and a base container still lands.
	if _, _, err := svc.AttachOriginal(ctx, owner, v1.ID, UploadInput{Filename: "clip.mp4", Reader: strings.NewReader("x")}); err != nil {
		t.Fatalf("gate off .mp4 err = %v, want ok", err)
	}

	// Runtime flip — same service instance.
	allowed = true
	v2, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t2", Privacy: "private"})
	if _, file, err := svc.AttachOriginal(ctx, owner, v2.ID, UploadInput{Filename: "clip.avi", Reader: strings.NewReader("x")}); err != nil {
		t.Fatalf("gate on .avi err = %v, want ok", err)
	} else if !strings.HasSuffix(file.StorageKey, ".avi") {
		t.Errorf("stored key = %q, want .avi original", file.StorageKey)
	}

	// Default (no gate wired) keeps the full pre-W10 allow-list.
	def := NewService(newFakeRepo(owner), blobs)
	v3, _ := def.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t3", Privacy: "private"})
	if _, _, err := def.AttachOriginal(ctx, owner, v3.ID, UploadInput{Filename: "clip.mkv", Reader: strings.NewReader("x")}); err != nil {
		t.Fatalf("default gate .mkv err = %v, want ok (pre-W10 behavior)", err)
	}
}

// The Count fakes delegate to their List so the pair can never disagree — the
// same invariant the real SQL pairs must hold.
func (f *fakeRepo) CountAdminVideos(ctx context.Context, a sqlcgen.CountAdminVideosParams) (int64, error) {
	rows, err := f.ListAdminVideos(ctx, sqlcgen.ListAdminVideosParams{
		Query: a.Query, States: a.States, Privacies: a.Privacies, Scope: a.Scope,
		Channel: a.Channel, PublishedAfter: a.PublishedAfter, PublishedBefore: a.PublishedBefore,
		HasOriginal: a.HasOriginal, HasHls: a.HasHls, HasWebFiles: a.HasWebFiles,
		ResultLimit: 1 << 30,
	})
	return int64(len(rows)), err
}

func (f *fakeRepo) CountQuarantinedVideos(ctx context.Context) (int64, error) {
	rows, err := f.ListQuarantinedVideos(ctx, sqlcgen.ListQuarantinedVideosParams{ResultLimit: 1 << 30})
	return int64(len(rows)), err
}

func (f *fakeRepo) CountPublicVideosSorted(ctx context.Context, a sqlcgen.CountPublicVideosSortedParams) (int64, error) {
	rows, err := f.ListPublicVideosSorted(ctx, sqlcgen.ListPublicVideosSortedParams{
		IncludeRemote: a.IncludeRemote, ViewerID: a.ViewerID, Tag: a.Tag,
		Category: a.Category, Language: a.Language, HideSensitive: a.HideSensitive,
		Sort: "recent", ResultLimit: 1 << 30,
	})
	return int64(len(rows)), err
}

func (f *fakeRepo) CountSubscriptionVideos(ctx context.Context, followerID uuid.UUID) (int64, error) {
	rows, err := f.ListSubscriptionVideos(ctx, sqlcgen.ListSubscriptionVideosParams{FollowerID: followerID, ResultLimit: 1 << 30})
	return int64(len(rows)), err
}

func (f *fakeRepo) CountSavedVideos(ctx context.Context, userID uuid.UUID) (int64, error) {
	rows, err := f.ListSavedVideos(ctx, sqlcgen.ListSavedVideosParams{UserID: userID, ResultLimit: 1 << 30})
	return int64(len(rows)), err
}

func (f *fakeRepo) CountWatchHistory(ctx context.Context, userID uuid.UUID) (int64, error) {
	rows, err := f.ListWatchHistory(ctx, sqlcgen.ListWatchHistoryParams{UserID: userID, ResultLimit: 1 << 30})
	return int64(len(rows)), err
}

func (f *fakeRepo) CountWatchHistoryInProgress(ctx context.Context, userID uuid.UUID) (int64, error) {
	rows, err := f.ListWatchHistoryInProgress(ctx, sqlcgen.ListWatchHistoryInProgressParams{UserID: userID, ResultLimit: 1 << 30})
	return int64(len(rows)), err
}

func (f *fakeRepo) CountSearchPublicVideos(ctx context.Context, a sqlcgen.CountSearchPublicVideosParams) (int64, error) {
	rows, err := f.SearchPublicVideos(ctx, sqlcgen.SearchPublicVideosParams{
		Query: a.Query, ViewerID: a.ViewerID, Tag: a.Tag, Category: a.Category,
		Language: a.Language, HideSensitive: a.HideSensitive, ResultLimit: 1 << 30,
	})
	return int64(len(rows)), err
}

// TestAttachOriginalBillsOncePerStoredOriginal is the daily-quota ledger's
// retry contract.
//
// AttachOriginal is the choke point every stored original passes through, and it
// appends a rolling-24h ledger event. Two of its callers RETRY it — the URL-import
// worker and the upload-finalize worker both re-run AttachOriginal → Process up
// to five times when a later stage fails — so a Process failure after the bytes
// had already landed billed the owner again on every attempt, up to 5x for one
// upload. The synchronous paths never retried, which is why this went unnoticed.
//
// Billing therefore follows the STORED ORIGINAL, not the call: re-storing the
// same bytes at the same key is the same original and bills once. Genuinely new
// content at that key is a new original and bills again.
func TestAttachOriginalBillsOncePerStoredOriginal(t *testing.T) {
	owner := uuid.New()
	repo := newFakeRepo(owner)
	blobs, _ := storage.NewLocal(t.TempDir())

	var billed []int64
	svc := NewService(repo, blobs, WithUploadUsageRecorder(func(_ context.Context, _ uuid.UUID, bytes int64) error {
		billed = append(billed, bytes)
		return nil
	}))
	ctx := context.Background()
	v, err := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "Clip"})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}

	const content = "the original bytes"
	attach := func(body string) {
		t.Helper()
		if _, _, err := svc.AttachOriginal(ctx, owner, v.ID, UploadInput{
			Filename: "clip.mp4", Reader: strings.NewReader(body),
		}); err != nil {
			t.Fatalf("AttachOriginal(%q): %v", body, err)
		}
	}

	attach(content)
	if len(billed) != 1 || billed[0] != int64(len(content)) {
		t.Fatalf("first attach billed %v, want one event of %d bytes", billed, len(content))
	}

	// The retry: the worker re-runs AttachOriginal with the same bytes after a
	// later stage failed. Same key, same digest — the same original.
	attach(content)
	if len(billed) != 1 {
		t.Errorf("a retry billed again (%v) — one upload must not bill the owner twice", billed)
	}

	// A genuinely different file at the same key is a new original.
	const replacement = "completely different content here"
	attach(replacement)
	if len(billed) != 2 || billed[1] != int64(len(replacement)) {
		t.Fatalf("re-upload of new content billed %v, want a second event of %d bytes", billed, len(replacement))
	}
}

// TestAttachOriginalFailsWhenThePriorDigestCannotBeRead pins the error posture
// of the read the billing guard depends on.
//
// The guard skips the ledger event when the bytes just stored match the original
// they replaced. That decision is only as good as the read behind it, and the
// two ways it can fail are not interchangeable: a MISS means there is genuinely
// no prior original (so this upload is new and bills), while any other error
// means we could not find out. Treating "don't know" as "no prior" would bill a
// second time on exactly the retry attempt the guard exists to protect — a
// transient database blip would quietly restore the bug. So the attempt fails
// instead, and the retry re-reads with nothing charged in the meantime.
func TestAttachOriginalFailsWhenThePriorDigestCannotBeRead(t *testing.T) {
	owner := uuid.New()
	repo := newFakeRepo(owner)
	blobs, _ := storage.NewLocal(t.TempDir())

	var billed []int64
	svc := NewService(repo, blobs, WithUploadUsageRecorder(func(_ context.Context, _ uuid.UUID, bytes int64) error {
		billed = append(billed, bytes)
		return nil
	}))
	ctx := context.Background()
	v, err := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "Clip"})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}

	boom := errors.New("connection reset by peer")
	repo.fileByKindErr = boom
	_, _, err = svc.AttachOriginal(ctx, owner, v.ID, UploadInput{
		Filename: "clip.mp4", Reader: strings.NewReader("the original bytes"),
	})
	if !errors.Is(err, boom) {
		t.Fatalf("AttachOriginal with an unreadable prior digest = %v, want the underlying error", err)
	}
	if len(billed) != 0 {
		t.Errorf("a failed attempt billed %v — nothing was stored, so nothing may be charged", billed)
	}

	// With the read working again the upload proceeds and bills exactly once.
	repo.fileByKindErr = nil
	if _, _, err := svc.AttachOriginal(ctx, owner, v.ID, UploadInput{
		Filename: "clip.mp4", Reader: strings.NewReader("the original bytes"),
	}); err != nil {
		t.Fatalf("AttachOriginal after recovery: %v", err)
	}
	if len(billed) != 1 {
		t.Errorf("billed %v after recovery, want exactly one event", billed)
	}
}

// TestReplaceSourceFailsWhenThePriorDigestCannotBeRead: the same posture on the
// replace path, where the read also decides the source VERSION — falling back to
// version 1 on an error we cannot interpret would write over the generation the
// video is currently serving.
func TestReplaceSourceFailsWhenThePriorDigestCannotBeRead(t *testing.T) {
	owner := uuid.New()
	repo := newFakeRepo(owner)
	blobs, _ := storage.NewLocal(t.TempDir())

	var billed []int64
	svc := NewService(repo, blobs, WithUploadUsageRecorder(func(_ context.Context, _ uuid.UUID, bytes int64) error {
		billed = append(billed, bytes)
		return nil
	}))
	ctx := context.Background()
	id := publishWithOriginal(t, svc, owner, "old source bytes")
	billed = nil

	boom := errors.New("connection reset by peer")
	repo.fileByKindErr = boom
	_, _, err := svc.ReplaceSource(ctx, owner, id, UploadInput{
		Filename: "v2.mp4", Reader: strings.NewReader("brand new source!"),
	}, false)
	if !errors.Is(err, boom) {
		t.Fatalf("ReplaceSource with an unreadable prior digest = %v, want the underlying error", err)
	}
	if len(billed) != 0 {
		t.Errorf("a failed replacement billed %v", billed)
	}
	// The video keeps serving the source it had.
	repo.fileByKindErr = nil
	if cur, cerr := svc.OriginalFileKey(ctx, id); cerr != nil || cur != "web-videos/"+id.String()+".mp4" {
		t.Errorf("original after a failed replacement = %q, %v; want the untouched source", cur, cerr)
	}
}
