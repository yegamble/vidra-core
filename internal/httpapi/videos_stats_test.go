package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// --- videoFakeRepo stats methods (mirror the video_stats queries) ---

func statsDayKey(videoID uuid.UUID, day time.Time) string {
	return videoID.String() + "|" + day.UTC().Format("2006-01-02")
}

func (f *videoFakeRepo) IncrementVideoViewDay(_ context.Context, videoID uuid.UUID) error {
	if f.viewDays == nil {
		f.viewDays = map[string]int64{}
	}
	f.viewDays[statsDayKey(videoID, time.Now())]++
	return nil
}

// splitViewDayKey parses a viewDays map key back into (video id, day).
func splitViewDayKey(k string) (uuid.UUID, time.Time) {
	id := uuid.MustParse(k[:36])
	day, _ := time.Parse("2006-01-02", k[37:])
	return id, day
}

func (f *videoFakeRepo) ListVideoViewDays(_ context.Context, a sqlcgen.ListVideoViewDaysParams) ([]sqlcgen.ListVideoViewDaysRow, error) {
	var rows []sqlcgen.ListVideoViewDaysRow
	for k, n := range f.viewDays {
		id, day := splitViewDayKey(k)
		if id == a.VideoID && !day.Before(a.Since.Time) {
			rows = append(rows, sqlcgen.ListVideoViewDaysRow{Day: pgtype.Date{Time: day, Valid: true}, Views: n})
		}
	}
	return rows, nil
}

func (f *videoFakeRepo) ListChannelViewDays(_ context.Context, a sqlcgen.ListChannelViewDaysParams) ([]sqlcgen.ListChannelViewDaysRow, error) {
	perDay := map[time.Time]int64{}
	for k, n := range f.viewDays {
		id, day := splitViewDayKey(k)
		v, ok := f.videos[id]
		if ok && v.ChannelID == a.ChannelID && !day.Before(a.Since.Time) {
			perDay[day] += n
		}
	}
	var rows []sqlcgen.ListChannelViewDaysRow
	for day, n := range perDay {
		rows = append(rows, sqlcgen.ListChannelViewDaysRow{Day: pgtype.Date{Time: day, Valid: true}, Views: n})
	}
	return rows, nil
}

// commentCount mirrors the stats queries' per-video comment COUNT.
func (f *videoFakeRepo) commentCount(videoID uuid.UUID) int64 {
	if f.commentsRepo == nil {
		return 0
	}
	var n int64
	for _, c := range f.commentsRepo.comments {
		if c.VideoID == videoID {
			n++
		}
	}
	return n
}

func (f *videoFakeRepo) GetVideoEngagementTotals(ctx context.Context, videoID uuid.UUID) (sqlcgen.GetVideoEngagementTotalsRow, error) {
	row := sqlcgen.GetVideoEngagementTotalsRow{Views: f.views[videoID], Comments: f.commentCount(videoID)}
	if f.ratings != nil {
		counts, _ := f.ratings.CountVideoRatings(ctx, videoID)
		row.Likes, row.Dislikes = counts.Likes, counts.Dislikes
	}
	return row, nil
}

func (f *videoFakeRepo) GetChannelEngagementTotals(ctx context.Context, channelID uuid.UUID) (sqlcgen.GetChannelEngagementTotalsRow, error) {
	var row sqlcgen.GetChannelEngagementTotalsRow
	for id, v := range f.videos {
		if v.ChannelID != channelID {
			continue
		}
		vt, _ := f.GetVideoEngagementTotals(ctx, id)
		row.Views += vt.Views
		row.Likes += vt.Likes
		row.Dislikes += vt.Dislikes
		row.Comments += vt.Comments
		row.Videos++
	}
	return row, nil
}

// ownedChannels returns the channels ownerID owns, oldest first (mirrors the
// GROUP BY ... ORDER BY created_at of the account-stats queries).
func (f *videoFakeRepo) ownedChannels(ownerID uuid.UUID) []sqlcgen.Channel {
	var owned []sqlcgen.Channel
	for _, ch := range f.channels.byHandle {
		if ch.OwnerID == ownerID {
			owned = append(owned, ch)
		}
	}
	sort.Slice(owned, func(i, j int) bool { return owned[i].CreatedAt.Before(owned[j].CreatedAt) })
	return owned
}

func (f *videoFakeRepo) GetOwnerChannelStats(ctx context.Context, a sqlcgen.GetOwnerChannelStatsParams) ([]sqlcgen.GetOwnerChannelStatsRow, error) {
	owned := f.ownedChannels(a.OwnerID)
	rows := make([]sqlcgen.GetOwnerChannelStatsRow, 0, len(owned))
	for _, ch := range owned {
		tot, _ := f.GetChannelEngagementTotals(ctx, ch.ID)
		row := sqlcgen.GetOwnerChannelStatsRow{
			ChannelID: ch.ID, Handle: ch.Handle, DisplayName: ch.DisplayName,
			Views: tot.Views, Likes: tot.Likes, Dislikes: tot.Dislikes,
			Comments: tot.Comments, Videos: tot.Videos,
		}
		for k, n := range f.viewDays {
			id, day := splitViewDayKey(k)
			if v, ok := f.videos[id]; ok && v.ChannelID == ch.ID && !day.Before(a.Since28d.Time) {
				row.Views28d += n
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func (f *videoFakeRepo) ListOwnerViewDays(_ context.Context, a sqlcgen.ListOwnerViewDaysParams) ([]sqlcgen.ListOwnerViewDaysRow, error) {
	owned := map[uuid.UUID]bool{}
	for _, ch := range f.ownedChannels(a.OwnerID) {
		owned[ch.ID] = true
	}
	perDay := map[time.Time]int64{}
	for k, n := range f.viewDays {
		id, day := splitViewDayKey(k)
		if v, ok := f.videos[id]; ok && owned[v.ChannelID] && !day.Before(a.Since.Time) {
			perDay[day] += n
		}
	}
	var rows []sqlcgen.ListOwnerViewDaysRow
	for day, n := range perDay {
		rows = append(rows, sqlcgen.ListOwnerViewDaysRow{Day: pgtype.Date{Time: day, Valid: true}, Views: n})
	}
	return rows, nil
}

// --- tests ---

// TestVideoStatsOwnerOnly proves GET /videos/{id}/stats: totals + a 30-day
// zero-filled series for the owner; 404 for everyone else; 401 anonymous.
func TestVideoStatsOwnerOnly(t *testing.T) {
	srv := videoServer(t)
	ownerTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	bobTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	id := createPublishedVideo(t, srv, ownerTok, "ada", `{"title":"clip","privacy":"public"}`)

	// A view (no deduper in the unit harness: every record counts), a like
	// from bob, and a comment from bob.
	if rec := postJSONAuth(srv, "/api/v1/videos/"+id+"/view", "", bobTok); rec.Code != http.StatusNoContent {
		t.Fatalf("record view = %d", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodPut, "/api/v1/videos/"+id+"/rating", `{"rating":"like"}`, bobTok); rec.Code != http.StatusOK {
		t.Fatalf("like = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec := postJSONAuth(srv, "/api/v1/videos/"+id+"/comments", `{"body":"first"}`, bobTok); rec.Code != http.StatusCreated {
		t.Fatalf("comment = %d; body=%s", rec.Code, rec.Body.String())
	}

	rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/videos/"+id+"/stats", "", ownerTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner stats = %d; body=%s", rec.Code, rec.Body.String())
	}
	var stats videoStatsResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &stats)
	if stats.Views != 1 || stats.Likes != 1 || stats.Dislikes != 0 || stats.Comments != 1 {
		t.Fatalf("totals = %+v", stats)
	}
	if len(stats.DailyViews) != 30 {
		t.Fatalf("series length = %d, want 30", len(stats.DailyViews))
	}
	today := time.Now().UTC().Format("2006-01-02")
	last := stats.DailyViews[len(stats.DailyViews)-1]
	if last.Day != today || last.Views != 1 {
		t.Fatalf("last day = %+v, want %s/1", last, today)
	}
	for _, d := range stats.DailyViews[:29] {
		if d.Views != 0 {
			t.Fatalf("day %s = %d, want zero-filled", d.Day, d.Views)
		}
	}

	// Owner-only: non-owner and anon never learn the endpoint exists.
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/videos/"+id+"/stats", "", bobTok); rec.Code != http.StatusNotFound {
		t.Fatalf("non-owner stats = %d, want 404", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/videos/"+id+"/stats", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anon stats = %d, want 401", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/videos/"+uuid.NewString()+"/stats", "", ownerTok); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown video stats = %d, want 404", rec.Code)
	}
}

// TestChannelStatsOwnerOnly proves GET /channels/{handle}/stats aggregates
// across the channel's videos and adds followers + video count — owner only.
func TestChannelStatsOwnerOnly(t *testing.T) {
	srv := videoServer(t)
	ownerTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	bobTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	v1 := createPublishedVideo(t, srv, ownerTok, "ada", `{"title":"one","privacy":"public"}`)
	_ = createVideo(t, srv, ownerTok, "ada", `{"title":"two","privacy":"private"}`)

	// bob follows, views, and likes.
	if rec := postJSONAuth(srv, "/api/v1/channels/ada/follow", "", bobTok); rec.Code != http.StatusNoContent {
		t.Fatalf("follow = %d", rec.Code)
	}
	if rec := postJSONAuth(srv, "/api/v1/videos/"+v1+"/view", "", bobTok); rec.Code != http.StatusNoContent {
		t.Fatalf("view = %d", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodPut, "/api/v1/videos/"+v1+"/rating", `{"rating":"like"}`, bobTok); rec.Code != http.StatusOK {
		t.Fatalf("like = %d", rec.Code)
	}

	rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/channels/ada/stats", "", ownerTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner channel stats = %d; body=%s", rec.Code, rec.Body.String())
	}
	var stats channelStatsResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &stats)
	if stats.Views != 1 || stats.Likes != 1 || stats.Followers != 1 || stats.Videos != 2 {
		t.Fatalf("channel totals = %+v", stats)
	}
	if len(stats.DailyViews) != 30 || stats.DailyViews[29].Views != 1 {
		t.Fatalf("series = len %d last %+v", len(stats.DailyViews), stats.DailyViews[29])
	}

	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/channels/ada/stats", "", bobTok); rec.Code != http.StatusNotFound {
		t.Fatalf("non-owner channel stats = %d, want 404", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/channels/ghost/stats", "", ownerTok); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown channel stats = %d, want 404", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/channels/ada/stats", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anon channel stats = %d, want 401", rec.Code)
	}
}

// TestAccountStatsRollup proves GET /me/stats: account-wide totals + per-channel
// breakdown + a merged 30-day series across every channel the caller owns, with
// follower counts merged from the channel domain. Owner-scoped by construction
// (only the caller's own channels); 401 anonymous.
func TestAccountStatsRollup(t *testing.T) {
	srv := videoServer(t)
	ownerTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	// A second channel for the same owner.
	if rec := postJSONAuth(srv, "/api/v1/channels", `{"handle":"ada_shorts","display_name":"Ada Shorts"}`, ownerTok); rec.Code != http.StatusCreated {
		t.Fatalf("create 2nd channel = %d; body=%s", rec.Code, rec.Body.String())
	}
	bobTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	// Channel "ada": one published video, a view + a like from bob, and bob follows.
	v1 := createPublishedVideo(t, srv, ownerTok, "ada", `{"title":"one","privacy":"public"}`)
	// Channel "ada_shorts": one published video, one view (no follower).
	v2 := createPublishedVideo(t, srv, ownerTok, "ada_shorts", `{"title":"two","privacy":"public"}`)
	if rec := postJSONAuth(srv, "/api/v1/channels/ada/follow", "", bobTok); rec.Code != http.StatusNoContent {
		t.Fatalf("follow = %d", rec.Code)
	}
	if rec := postJSONAuth(srv, "/api/v1/videos/"+v1+"/view", "", bobTok); rec.Code != http.StatusNoContent {
		t.Fatalf("view v1 = %d", rec.Code)
	}
	if rec := postJSONAuth(srv, "/api/v1/videos/"+v2+"/view", "", bobTok); rec.Code != http.StatusNoContent {
		t.Fatalf("view v2 = %d", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodPut, "/api/v1/videos/"+v1+"/rating", `{"rating":"like"}`, bobTok); rec.Code != http.StatusOK {
		t.Fatalf("like = %d", rec.Code)
	}

	rec := getWithAuth(srv, "/api/v1/me/stats", ownerTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("account stats = %d; body=%s", rec.Code, rec.Body.String())
	}
	var stats accountStatsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &stats); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Totals summed across BOTH channels: 2 views, 1 like, 1 follower, 2 videos.
	if stats.Totals.Views != 2 || stats.Totals.Likes != 1 || stats.Totals.Followers != 1 || stats.Totals.Videos != 2 {
		t.Fatalf("account totals = %+v", stats.Totals)
	}
	if len(stats.DailyViews) != 30 || stats.DailyViews[29].Views != 2 {
		t.Fatalf("series = len %d last %+v", len(stats.DailyViews), stats.DailyViews[29])
	}
	if len(stats.Channels) != 2 {
		t.Fatalf("breakdown channels = %d, want 2", len(stats.Channels))
	}
	byHandle := map[string]accountChannelStats{}
	for _, c := range stats.Channels {
		byHandle[c.Handle] = c
	}
	ada, shorts := byHandle["ada"], byHandle["ada_shorts"]
	if ada.Views != 1 || ada.Followers != 1 || ada.Videos != 1 || ada.Views28d != 1 {
		t.Errorf("ada breakdown = %+v", ada)
	}
	if shorts.Views != 1 || shorts.Followers != 0 || shorts.Videos != 1 {
		t.Errorf("ada_shorts breakdown = %+v", shorts)
	}

	// bob owns no channels → empty rollup, not an error.
	bobRec := getWithAuth(srv, "/api/v1/me/stats", bobTok)
	if bobRec.Code != http.StatusOK {
		t.Fatalf("bob account stats = %d", bobRec.Code)
	}
	var bobStats accountStatsResponse
	_ = json.Unmarshal(bobRec.Body.Bytes(), &bobStats)
	if len(bobStats.Channels) != 0 || bobStats.Totals.Views != 0 {
		t.Errorf("bob (no owned channels) should roll up empty: %+v", bobStats)
	}

	// Anonymous is 401.
	if anon := getWithAuth(srv, "/api/v1/me/stats", ""); anon.Code != http.StatusUnauthorized {
		t.Fatalf("anon account stats = %d, want 401", anon.Code)
	}
}
