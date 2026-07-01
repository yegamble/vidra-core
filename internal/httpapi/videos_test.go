package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/admin"
	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/channel"
	"github.com/vidra/vidra-core/internal/comment"
	"github.com/vidra/vidra-core/internal/config"
	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/moderation"
	"github.com/vidra/vidra-core/internal/mute"
	"github.com/vidra/vidra-core/internal/notification"
	"github.com/vidra/vidra-core/internal/playlist"
	"github.com/vidra/vidra-core/internal/rating"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
	"github.com/vidra/vidra-core/internal/video"
	"github.com/vidra/vidra-core/internal/watchword"
)

// videoFakeRepo is an in-memory video.Repository. It resolves a new video's
// owner from the shared channelFakeRepo so GetVideoByID can return owner_id.
type videoFakeRepo struct {
	channels *channelFakeRepo
	mutes    *muteFakeRepo
	blocks   *moderationFakeRepo
	videos   map[uuid.UUID]sqlcgen.GetVideoByIDRow
	files    map[uuid.UUID][]sqlcgen.VideoFile
	metadata map[uuid.UUID]sqlcgen.VideoMetadatum
	views    map[uuid.UUID]int64
	saved    map[string]time.Time   // "userID|videoID" -> saved-at
	history  map[string]historyMark // "userID|videoID" -> resume position + last-watched
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
			ID: r.ID, ChannelID: r.ChannelID, Title: r.Title, Description: r.Description,
			Privacy: r.Privacy, State: r.State, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
			Views: f.views[r.ID], HasThumbnail: f.hasThumb(r.ID),
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
			ID: r.ID, ChannelID: r.ChannelID, Title: r.Title, Description: r.Description,
			Privacy: r.Privacy, State: r.State, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
			Views: f.views[r.ID], HasThumbnail: f.hasThumb(r.ID),
			ChannelHandle: handle, ChannelDisplayName: name,
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
		muted := f.mutes != nil && f.mutes.isMuted(a.FollowerID, f.channelOwner(r.ChannelID))
		if r.Privacy == "public" && r.State == "published" && follows && !muted {
			rows = append(rows, sqlcgen.ListSubscriptionVideosRow{
				ID: r.ID, ChannelID: r.ChannelID, Title: r.Title, Description: r.Description,
				Privacy: r.Privacy, State: r.State, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
				Views: f.views[r.ID], HasThumbnail: f.hasThumb(r.ID),
			})
		}
	}
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
		ID: uuid.New(), ChannelID: a.ChannelID, Title: a.Title,
		Description: a.Description, Privacy: a.Privacy, State: "draft",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	f.videos[v.ID] = sqlcgen.GetVideoByIDRow{
		ID: v.ID, ChannelID: v.ChannelID, Title: v.Title, Description: v.Description,
		Privacy: v.Privacy, State: v.State, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt,
		OwnerID: owner,
	}
	return v, nil
}

func (f *videoFakeRepo) GetVideoByID(_ context.Context, id uuid.UUID) (sqlcgen.GetVideoByIDRow, error) {
	v, ok := f.videos[id]
	if !ok {
		return sqlcgen.GetVideoByIDRow{}, errors.New("not found")
	}
	return v, nil
}

func vidRowToVideo(r sqlcgen.GetVideoByIDRow) sqlcgen.Video {
	return sqlcgen.Video{
		ID: r.ID, ChannelID: r.ChannelID, Title: r.Title, Description: r.Description,
		Privacy: r.Privacy, State: r.State, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

func (f *videoFakeRepo) ListVideosByChannel(_ context.Context, channelID uuid.UUID) ([]sqlcgen.ListVideosByChannelRow, error) {
	var out []sqlcgen.ListVideosByChannelRow
	for _, r := range f.videos {
		if r.ChannelID == channelID {
			out = append(out, sqlcgen.ListVideosByChannelRow{
				ID: r.ID, ChannelID: r.ChannelID, Title: r.Title, Description: r.Description,
				Privacy: r.Privacy, State: r.State, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
				Views: f.views[r.ID], HasThumbnail: f.hasThumb(r.ID),
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (f *videoFakeRepo) ListPublicVideosByChannel(_ context.Context, channelID uuid.UUID) ([]sqlcgen.ListPublicVideosByChannelRow, error) {
	var out []sqlcgen.ListPublicVideosByChannelRow
	for _, r := range f.videos {
		if r.ChannelID == channelID && r.Privacy == "public" && r.State == "published" {
			out = append(out, sqlcgen.ListPublicVideosByChannelRow{
				ID: r.ID, ChannelID: r.ChannelID, Title: r.Title, Description: r.Description,
				Privacy: r.Privacy, State: r.State, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
				Views: f.views[r.ID], HasThumbnail: f.hasThumb(r.ID),
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
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
		CreatedAt: time.Now(),
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
		return sqlcgen.VideoFile{}, errors.New("not found")
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

func (f *videoFakeRepo) SearchPublicVideos(_ context.Context, a sqlcgen.SearchPublicVideosParams) ([]sqlcgen.SearchPublicVideosRow, error) {
	q := ""
	if a.Query != nil {
		q = strings.ToLower(*a.Query)
	}
	var all []sqlcgen.SearchPublicVideosRow
	for _, r := range f.videos {
		if r.Privacy == "public" && r.State == "published" && strings.Contains(strings.ToLower(r.Title), q) &&
			!f.mutedFromFeed(a.ViewerID, r.ChannelID) {
			all = append(all, sqlcgen.SearchPublicVideosRow{
				ID: r.ID, ChannelID: r.ChannelID, Title: r.Title, Description: r.Description,
				Privacy: r.Privacy, State: r.State, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
				Views: f.views[r.ID], HasThumbnail: f.hasThumb(r.ID),
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

// ListAdminVideos returns all videos (any privacy/state) with the current block
// status, mirroring the real admin overview query. An optional title filter.
func (f *videoFakeRepo) ListAdminVideos(_ context.Context, a sqlcgen.ListAdminVideosParams) ([]sqlcgen.ListAdminVideosRow, error) {
	var rows []sqlcgen.ListAdminVideosRow
	for _, r := range f.videos {
		if a.Query != nil && !strings.Contains(strings.ToLower(r.Title), strings.ToLower(*a.Query)) {
			continue
		}
		blocked := false
		if f.blocks != nil {
			blocked, _ = f.blocks.IsVideoBlocked(context.Background(), r.ID)
		}
		ch, cn := f.channelInfo(r.ChannelID)
		rows = append(rows, sqlcgen.ListAdminVideosRow{
			ID: r.ID, Title: r.Title, Privacy: r.Privacy, State: r.State,
			ChannelHandle: ch, ChannelDisplayName: cn,
			Views: f.views[r.ID], CreatedAt: r.CreatedAt, Blocked: blocked,
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

// mutedFromFeed reports whether an authenticated viewer has muted the owner of
// the given channel — mirrors the feed queries' per-viewer mute filter.
func (f *videoFakeRepo) mutedFromFeed(viewer pgtype.UUID, channelID uuid.UUID) bool {
	return viewer.Valid && f.mutes != nil && f.mutes.isMuted(uuid.UUID(viewer.Bytes), f.channelOwner(channelID))
}

func (f *videoFakeRepo) ListPublicVideosSorted(_ context.Context, a sqlcgen.ListPublicVideosSortedParams) ([]sqlcgen.ListPublicVideosSortedRow, error) {
	var rows []sqlcgen.ListPublicVideosSortedRow
	for _, r := range f.videos {
		if r.Privacy == "public" && r.State == "published" && !f.mutedFromFeed(a.ViewerID, r.ChannelID) {
			ch, cn := f.channelInfo(r.ChannelID)
			rows = append(rows, sqlcgen.ListPublicVideosSortedRow{
				ID: r.ID, ChannelID: r.ChannelID, Title: r.Title, Description: r.Description,
				Privacy: r.Privacy, State: r.State, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
				Views: f.views[r.ID], HasThumbnail: f.hasThumb(r.ID),
				ChannelHandle: ch, ChannelDisplayName: cn,
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
	t.Helper()
	chRepo := newChannelFakeRepo()
	authRepo := newAuthFakeRepo()
	issuer := auth.NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", 15*time.Minute)
	authsvc := auth.NewService(authRepo, issuer, 720*time.Hour)
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
	cmRepo := &commentFakeRepo{users: authRepo, mutes: muteRepo, videos: repo}
	modRepo := &moderationFakeRepo{auth: authRepo, videos: repo, comments: cmRepo}
	repo.blocks = modRepo
	return New(cfg, nil, nil,
		WithAuthService(authsvc, 15*time.Minute),
		WithChannelService(channel.NewService(chRepo)),
		WithVideoService(video.NewService(repo, blobs, opts...)),
		WithCommentService(comment.NewService(cmRepo)),
		WithRatingService(rating.NewService(newRatingFakeRepo())),
		WithNotificationService(notification.NewService(notifRepo)),
		WithPlaylistService(playlist.NewService(plRepo)),
		WithModerationService(moderation.NewService(modRepo)),
		WithMuteService(mute.NewService(muteRepo)),
		WithWatchWordService(watchword.NewService(&watchwordFakeRepo{auth: authRepo})),
		WithAdminService(admin.NewService(authRepo)),
		WithMediaStorage(blobs),
	)
}

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

func TestCreateVideoDefaultsPrivate(t *testing.T) {
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

	if owner := list(ownerTok); len(owner.Videos) != 2 {
		t.Errorf("owner list = %d, want 2", len(owner.Videos))
	}
	if anon := list(""); len(anon.Videos) != 1 || anon.Videos[0].Privacy != "public" {
		t.Errorf("anon list = %+v, want 1 public", anon.Videos)
	}
	if other := list(otherTok); len(other.Videos) != 1 {
		t.Errorf("non-owner list = %d, want 1 (public only)", len(other.Videos))
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
