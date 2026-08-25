package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/instancesettings"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

type modReportRow struct {
	id             uuid.UUID
	reporterID     uuid.UUID
	targetType     string
	videoID        pgtype.UUID
	commentID      pgtype.UUID
	reportedUserID pgtype.UUID
	remoteVideoID  pgtype.UUID
	messageID      pgtype.UUID
	reason         string
	status         string
	note           string
	createdAt      time.Time
	resolvedAt     pgtype.Timestamptz
}

// moderationFakeRepo is an in-memory moderation.Repository that resolves the
// reporter / target join columns from the sibling fakes, mirroring the real
// query, and enforces the comment foreign key.
type moderationFakeRepo struct {
	auth       *authFakeRepo
	videos     *videoFakeRepo
	comments   *commentFakeRepo
	reports    []modReportRow
	blocks     map[uuid.UUID]modBlockMark
	blockOrder []uuid.UUID // block order (oldest first)
	// remote-video moderation (remote-content §8). remoteVideos is the FK
	// target: reporting/blocking an id absent from it is a violation.
	remoteVideos     remoteVideoFakeRepo
	remoteBlocks     map[uuid.UUID]modBlockMark
	remoteBlockOrder []uuid.UUID
}

// modBlockMark records a video_blocks row in the fake repo.
type modBlockMark struct {
	reason string
	by     uuid.UUID
	at     time.Time
}

func (f *moderationFakeRepo) CreateVideoReport(_ context.Context, a sqlcgen.CreateVideoReportParams) (uuid.UUID, error) {
	for _, r := range f.reports {
		if r.reporterID == a.ReporterID && r.videoID == a.VideoID {
			return uuid.Nil, pgx.ErrNoRows // ON CONFLICT DO NOTHING yields no row
		}
	}
	id := uuid.New()
	f.reports = append(f.reports, modReportRow{
		id: id, reporterID: a.ReporterID, targetType: "video",
		videoID: a.VideoID, reason: a.Reason, status: "open", createdAt: time.Now(),
	})
	return id, nil
}

func (f *moderationFakeRepo) CreateCommentReport(_ context.Context, a sqlcgen.CreateCommentReportParams) (uuid.UUID, error) {
	if _, err := f.comments.GetComment(context.Background(), uuid.UUID(a.CommentID.Bytes)); err != nil {
		return uuid.Nil, &pgconn.PgError{Code: "23503"} // foreign-key violation: no such comment
	}
	for _, r := range f.reports {
		if r.reporterID == a.ReporterID && r.commentID == a.CommentID {
			return uuid.Nil, pgx.ErrNoRows // ON CONFLICT DO NOTHING yields no row
		}
	}
	id := uuid.New()
	f.reports = append(f.reports, modReportRow{
		id: id, reporterID: a.ReporterID, targetType: "comment",
		commentID: a.CommentID, reason: a.Reason, status: "open", createdAt: time.Now(),
	})
	return id, nil
}

func (f *moderationFakeRepo) CreateAccountReport(_ context.Context, a sqlcgen.CreateAccountReportParams) (uuid.UUID, error) {
	if _, err := f.auth.GetUserByID(context.Background(), uuid.UUID(a.ReportedUserID.Bytes)); err != nil {
		return uuid.Nil, &pgconn.PgError{Code: "23503"} // foreign-key violation: no such user
	}
	for _, r := range f.reports {
		if r.reporterID == a.ReporterID && r.reportedUserID == a.ReportedUserID {
			return uuid.Nil, pgx.ErrNoRows // ON CONFLICT DO NOTHING yields no row
		}
	}
	id := uuid.New()
	f.reports = append(f.reports, modReportRow{
		id: id, reporterID: a.ReporterID, targetType: "account",
		reportedUserID: a.ReportedUserID, reason: a.Reason, status: "open", createdAt: time.Now(),
	})
	return id, nil
}

func (f *moderationFakeRepo) CreateMessageReport(_ context.Context, a sqlcgen.CreateMessageReportParams) (uuid.UUID, error) {
	for _, r := range f.reports {
		if r.reporterID == a.ReporterID && r.messageID == a.MessageID {
			return uuid.Nil, pgx.ErrNoRows // ON CONFLICT DO NOTHING yields no row
		}
	}
	id := uuid.New()
	f.reports = append(f.reports, modReportRow{
		id: id, reporterID: a.ReporterID, targetType: "message",
		messageID: a.MessageID, reason: a.Reason, status: "open", createdAt: time.Now(),
	})
	return id, nil
}

func (f *moderationFakeRepo) ListReports(_ context.Context, a sqlcgen.ListReportsParams) ([]sqlcgen.ListReportsRow, error) {
	var rows []sqlcgen.ListReportsRow
	for i := len(f.reports) - 1; i >= 0; i-- {
		r := f.reports[i]
		if a.Status != nil {
			if *a.Status == "resolved" {
				if r.status == "open" {
					continue
				}
			} else if r.status != *a.Status {
				continue
			}
		}
		row := sqlcgen.ListReportsRow{
			ID: r.id, TargetType: r.targetType, VideoID: r.videoID, CommentID: r.commentID,
			ReportedUserID: r.reportedUserID, RemoteVideoID: r.remoteVideoID,
			Reason: r.reason, Status: r.status,
			ModeratorNote: r.note, ResolvedAt: r.resolvedAt, CreatedAt: r.createdAt,
		}
		if u, err := f.auth.GetUserByID(context.Background(), r.reporterID); err == nil {
			row.ReporterUsername = u.Username
		}
		if r.reportedUserID.Valid {
			if u, err := f.auth.GetUserByID(context.Background(), uuid.UUID(r.reportedUserID.Bytes)); err == nil {
				un := u.Username
				row.ReportedUsername = &un
			}
		}
		if r.videoID.Valid {
			if v, ok := f.videos.videos[uuid.UUID(r.videoID.Bytes)]; ok {
				tt := v.Title
				row.VideoTitle = &tt
			}
		}
		if r.commentID.Valid {
			if cm, err := f.comments.GetComment(context.Background(), uuid.UUID(r.commentID.Bytes)); err == nil {
				b := cm.Body
				row.CommentBody = &b
			}
		}
		if r.remoteVideoID.Valid {
			if rv, ok := f.remoteVideos[uuid.UUID(r.remoteVideoID.Bytes)]; ok {
				tt, dom := rv.Title, rv.Domain
				row.RemoteVideoTitle = &tt
				row.RemoteVideoDomain = &dom
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func (f *moderationFakeRepo) CreateRemoteVideoReport(_ context.Context, a sqlcgen.CreateRemoteVideoReportParams) (uuid.UUID, error) {
	if _, ok := f.remoteVideos[uuid.UUID(a.RemoteVideoID.Bytes)]; !ok {
		return uuid.Nil, &pgconn.PgError{Code: "23503"} // foreign-key violation: no such remote video
	}
	for _, r := range f.reports {
		if r.reporterID == a.ReporterID && r.remoteVideoID == a.RemoteVideoID {
			return uuid.Nil, pgx.ErrNoRows // ON CONFLICT DO NOTHING yields no row
		}
	}
	id := uuid.New()
	f.reports = append(f.reports, modReportRow{
		id: id, reporterID: a.ReporterID, targetType: "remote_video",
		remoteVideoID: a.RemoteVideoID, reason: a.Reason, status: "open", createdAt: time.Now(),
	})
	return id, nil
}

func (f *moderationFakeRepo) BlockRemoteVideo(_ context.Context, a sqlcgen.BlockRemoteVideoParams) (int64, error) {
	if _, ok := f.remoteVideos[a.RemoteVideoID]; !ok {
		return 0, &pgconn.PgError{Code: "23503"} // foreign-key violation: no such remote video
	}
	if f.remoteBlocks == nil {
		f.remoteBlocks = map[uuid.UUID]modBlockMark{}
	}
	if _, ok := f.remoteBlocks[a.RemoteVideoID]; !ok {
		f.remoteBlockOrder = append(f.remoteBlockOrder, a.RemoteVideoID)
	}
	f.remoteBlocks[a.RemoteVideoID] = modBlockMark{reason: a.Reason, by: uuid.UUID(a.BlockedBy.Bytes), at: time.Now()}
	return 1, nil
}

func (f *moderationFakeRepo) UnblockRemoteVideo(_ context.Context, id uuid.UUID) (int64, error) {
	if _, ok := f.remoteBlocks[id]; !ok {
		return 0, nil
	}
	delete(f.remoteBlocks, id)
	for i, v := range f.remoteBlockOrder {
		if v == id {
			f.remoteBlockOrder = append(f.remoteBlockOrder[:i], f.remoteBlockOrder[i+1:]...)
			break
		}
	}
	return 1, nil
}

func (f *moderationFakeRepo) ListBlockedRemoteVideos(_ context.Context, a sqlcgen.ListBlockedRemoteVideosParams) ([]sqlcgen.ListBlockedRemoteVideosRow, error) {
	var rows []sqlcgen.ListBlockedRemoteVideosRow
	for i := len(f.remoteBlockOrder) - 1; i >= 0; i-- { // newest block first
		id := f.remoteBlockOrder[i]
		mark := f.remoteBlocks[id]
		rv := f.remoteVideos[id]
		row := sqlcgen.ListBlockedRemoteVideosRow{
			RemoteVideoID: id, Title: rv.Title, ObjectUrl: rv.ObjectUrl,
			WatchUrl: rv.WatchUrl, Domain: rv.Domain, Reason: mark.reason, BlockedAt: mark.at,
		}
		if u, err := f.auth.GetUserByID(context.Background(), mark.by); err == nil {
			un := u.Username
			row.BlockedByUsername = &un
		}
		rows = append(rows, row)
	}
	off := min(int(a.ResultOffset), len(rows))
	rows = rows[off:]
	if a.ResultLimit > 0 && int(a.ResultLimit) < len(rows) {
		rows = rows[:a.ResultLimit]
	}
	return rows, nil
}

func (f *moderationFakeRepo) ResolveReport(_ context.Context, a sqlcgen.ResolveReportParams) (uuid.UUID, error) {
	for i := range f.reports {
		if f.reports[i].id == a.ID {
			f.reports[i].status = a.Status
			f.reports[i].note = a.ModeratorNote
			f.reports[i].resolvedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
			return f.reports[i].reporterID, nil
		}
	}
	return uuid.Nil, pgx.ErrNoRows
}

func (f *moderationFakeRepo) DeleteReport(_ context.Context, id uuid.UUID) (int64, error) {
	for i := range f.reports {
		if f.reports[i].id == id {
			f.reports = append(f.reports[:i], f.reports[i+1:]...)
			return 1, nil
		}
	}
	return 0, nil
}

// reportByID resolves one of the fake's report rows (for the notification
// fake's report-context join).
func (f *moderationFakeRepo) reportByID(id uuid.UUID) (modReportRow, bool) {
	for _, r := range f.reports {
		if r.id == id {
			return r, true
		}
	}
	return modReportRow{}, false
}

func (f *moderationFakeRepo) BlockVideo(_ context.Context, a sqlcgen.BlockVideoParams) (int64, error) {
	if _, ok := f.videos.videos[a.VideoID]; !ok {
		return 0, &pgconn.PgError{Code: "23503"} // FK violation: no such video
	}
	if f.blocks == nil {
		f.blocks = map[uuid.UUID]modBlockMark{}
	}
	if _, exists := f.blocks[a.VideoID]; !exists {
		f.blockOrder = append(f.blockOrder, a.VideoID)
	}
	f.blocks[a.VideoID] = modBlockMark{reason: a.Reason, by: uuid.UUID(a.BlockedBy.Bytes), at: time.Now()}
	return 1, nil
}

func (f *moderationFakeRepo) UnblockVideo(_ context.Context, videoID uuid.UUID) (int64, error) {
	if _, ok := f.blocks[videoID]; ok {
		delete(f.blocks, videoID)
		for i, id := range f.blockOrder {
			if id == videoID {
				f.blockOrder = append(f.blockOrder[:i], f.blockOrder[i+1:]...)
				break
			}
		}
		return 1, nil
	}
	return 0, nil
}

func (f *moderationFakeRepo) IsVideoBlocked(_ context.Context, videoID uuid.UUID) (bool, error) {
	_, ok := f.blocks[videoID]
	return ok, nil
}

func (f *moderationFakeRepo) ListBlockedVideos(_ context.Context, a sqlcgen.ListBlockedVideosParams) ([]sqlcgen.ListBlockedVideosRow, error) {
	var rows []sqlcgen.ListBlockedVideosRow
	for i := len(f.blockOrder) - 1; i >= 0; i-- { // newest block first
		vid := f.blockOrder[i]
		mark, ok := f.blocks[vid]
		if !ok {
			continue
		}
		v, ok := f.videos.videos[vid]
		if !ok {
			continue
		}
		row := sqlcgen.ListBlockedVideosRow{
			VideoID: vid, Title: v.Title, Privacy: v.Privacy, State: v.State,
			Reason: mark.reason, BlockedAt: mark.at,
		}
		for _, ch := range f.videos.channels.byHandle { // resolve owning channel by id
			if ch.ID == v.ChannelID {
				row.ChannelHandle = ch.Handle
				row.ChannelDisplayName = ch.DisplayName
				break
			}
		}
		if u, err := f.auth.GetUserByID(context.Background(), mark.by); err == nil { // resolve blocker
			un := u.Username
			row.BlockedByUsername = &un
		}
		rows = append(rows, row)
	}
	off := min(int(a.ResultOffset), len(rows))
	rows = rows[off:]
	if a.ResultLimit > 0 && int(a.ResultLimit) < len(rows) {
		rows = rows[:a.ResultLimit]
	}
	return rows, nil
}

func listReports(srv *Server, query, token string) *httptest.ResponseRecorder {
	return sendJSONAuth(srv, http.MethodGet, "/api/v1/admin/reports"+query, "", token)
}

func TestReportVideoAndModerate(t *testing.T) {
	srv := videoServer(t)
	// The first registered account ("ada") becomes admin.
	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	vid := createPublishedVideo(t, srv, admin, "ada", `{"title":"Clip","privacy":"public"}`)
	bob := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	// bob reports the video.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+vid+"/report", `{"reason":"spam"}`, bob); rec.Code != http.StatusNoContent {
		t.Fatalf("report = %d; body=%s", rec.Code, rec.Body.String())
	}

	// A regular user cannot see the moderation queue.
	if rec := listReports(srv, "", bob); rec.Code != http.StatusForbidden {
		t.Errorf("non-admin list = %d, want 403", rec.Code)
	}

	// The admin sees the report with the resolved reporter + video context.
	rec := listReports(srv, "?status=open", admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin list = %d; body=%s", rec.Code, rec.Body.String())
	}
	var body reportListResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body.Reports) != 1 {
		t.Fatalf("reports = %d, want 1; body=%s", len(body.Reports), rec.Body.String())
	}
	r := body.Reports[0]
	if r.TargetType != "video" || r.Reason != "spam" || r.Status != "open" ||
		r.Reporter.Username != "bob" || r.VideoTitle != "Clip" || r.VideoID != vid {
		t.Errorf("report = %+v, want video/spam/open/bob/Clip/%s", r, vid)
	}

	// The admin resolves it; it leaves the open queue.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/reports/"+r.ID+"/resolve", `{"status":"accepted","note":"removed"}`, admin); rec.Code != http.StatusNoContent {
		t.Fatalf("resolve = %d; body=%s", rec.Code, rec.Body.String())
	}
	var afterOpen reportListResponse
	_ = json.Unmarshal(listReports(srv, "?status=open", admin).Body.Bytes(), &afterOpen)
	if len(afterOpen.Reports) != 0 {
		t.Errorf("open after resolve = %d, want 0", len(afterOpen.Reports))
	}
	// Resolving an unknown report → 404.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/reports/"+uuid.New().String()+"/resolve", `{"status":"rejected"}`, admin); rec.Code != http.StatusNotFound {
		t.Errorf("resolve unknown = %d, want 404", rec.Code)
	}
}

func TestReportCommentAndUnknown(t *testing.T) {
	srv := videoServer(t)
	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	vid := createPublishedVideo(t, srv, admin, "ada", `{"title":"Clip","privacy":"public"}`)
	bob := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	// bob comments, then reports his own comment id (any user may report).
	crec := postJSONAuth(srv, "/api/v1/videos/"+vid+"/comments", `{"body":"a comment"}`, bob)
	if crec.Code != http.StatusCreated {
		t.Fatalf("comment = %d; body=%s", crec.Code, crec.Body.String())
	}
	var cv commentView
	_ = json.Unmarshal(crec.Body.Bytes(), &cv)

	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/comments/"+cv.ID+"/report", `{"reason":"abuse"}`, bob); rec.Code != http.StatusNoContent {
		t.Fatalf("report comment = %d; body=%s", rec.Code, rec.Body.String())
	}
	var body reportListResponse
	_ = json.Unmarshal(listReports(srv, "", admin).Body.Bytes(), &body)
	if len(body.Reports) != 1 || body.Reports[0].TargetType != "comment" || body.Reports[0].CommentID != cv.ID {
		t.Fatalf("reports = %+v, want one comment report for %s", body.Reports, cv.ID)
	}

	// Reporting a non-existent comment → 404.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/comments/"+uuid.New().String()+"/report", `{"reason":"x"}`, bob); rec.Code != http.StatusNotFound {
		t.Errorf("report unknown comment = %d, want 404", rec.Code)
	}
}

func TestReportAccountAndModerate(t *testing.T) {
	srv := videoServer(t)
	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	bob, bobID := registerAndUser(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	// bob reports the admin's account.
	adminID := ""
	{
		var me userView
		_ = json.Unmarshal(getWithAuth(srv, "/api/v1/auth/me", admin).Body.Bytes(), &me)
		adminID = me.ID
	}
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/users/"+adminID+"/report", `{"reason":"impersonation"}`, bob); rec.Code != http.StatusNoContent {
		t.Fatalf("report account = %d; body=%s", rec.Code, rec.Body.String())
	}

	// The admin sees the account report with the resolved reported username.
	var body reportListResponse
	_ = json.Unmarshal(listReports(srv, "?status=open", admin).Body.Bytes(), &body)
	if len(body.Reports) != 1 {
		t.Fatalf("reports = %d, want 1; body=%+v", len(body.Reports), body)
	}
	r := body.Reports[0]
	if r.TargetType != "account" || r.Reason != "impersonation" || r.Reporter.Username != "bob" ||
		r.ReportedUserID != adminID || r.ReportedUsername != "ada" {
		t.Errorf("report = %+v, want account/impersonation/bob/ada(%s)", r, adminID)
	}

	// Reporting yourself → 422.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/users/"+bobID+"/report", `{"reason":"me"}`, bob); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("self-report = %d, want 422", rec.Code)
	}
	// Reporting an unknown account → 404.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/users/"+uuid.New().String()+"/report", `{"reason":"x"}`, bob); rec.Code != http.StatusNotFound {
		t.Errorf("report unknown account = %d, want 404", rec.Code)
	}
	// Auth required.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/users/"+adminID+"/report", `{"reason":"x"}`, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anon report account = %d, want 401", rec.Code)
	}
}

// TestResolveReportNotifiesReporter proves the reporter is told when a
// moderator resolves their report — for both outcomes — with the report context
// resolved on the notification and no moderator identity leaked.
func TestResolveReportNotifiesReporter(t *testing.T) {
	srv := videoServer(t)
	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	vid := createPublishedVideo(t, srv, admin, "ada", `{"title":"Clip","privacy":"public"}`)
	bob := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	resolveNth := func(status string) string {
		t.Helper()
		var open reportListResponse
		_ = json.Unmarshal(listReports(srv, "?status=open", admin).Body.Bytes(), &open)
		if len(open.Reports) != 1 {
			t.Fatalf("open reports = %d, want 1", len(open.Reports))
		}
		id := open.Reports[0].ID
		if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/reports/"+id+"/resolve", `{"status":"`+status+`","note":"handled"}`, admin); rec.Code != http.StatusNoContent {
			t.Fatalf("resolve %s = %d; body=%s", status, rec.Code, rec.Body.String())
		}
		return id
	}

	// Accepted: bob reports the video, the admin accepts → bob is notified.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+vid+"/report", `{"reason":"spam"}`, bob); rec.Code != http.StatusNoContent {
		t.Fatalf("report video = %d", rec.Code)
	}
	acceptedID := resolveNth("accepted")

	// Rejected: bob reports the admin's account, the admin rejects → notified too.
	var me userView
	_ = json.Unmarshal(getWithAuth(srv, "/api/v1/auth/me", admin).Body.Bytes(), &me)
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/users/"+me.ID+"/report", `{"reason":"impersonation"}`, bob); rec.Code != http.StatusNoContent {
		t.Fatalf("report account = %d", rec.Code)
	}
	rejectedID := resolveNth("rejected")

	rec := listNotifications(srv, bob)
	if rec.Code != http.StatusOK {
		t.Fatalf("list notifications = %d; body=%s", rec.Code, rec.Body.String())
	}
	var body notificationListResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body.Notifications) != 2 {
		t.Fatalf("notifications = %d, want 2; body=%s", len(body.Notifications), rec.Body.String())
	}
	// Newest first: the rejected account report, then the accepted video report.
	first, second := body.Notifications[0], body.Notifications[1]
	if first.Type != "report_resolved" || first.ReportID != rejectedID ||
		first.ReportStatus != "rejected" || first.ReportTargetType != "account" {
		t.Errorf("first = %+v, want report_resolved/%s/rejected/account", first, rejectedID)
	}
	if second.Type != "report_resolved" || second.ReportID != acceptedID ||
		second.ReportStatus != "accepted" || second.ReportTargetType != "video" {
		t.Errorf("second = %+v, want report_resolved/%s/accepted/video", second, acceptedID)
	}
	// The resolving moderator's identity must not be exposed to the reporter.
	if first.Actor != nil || second.Actor != nil {
		t.Errorf("actor leaked on report_resolved notification: %+v / %+v", first.Actor, second.Actor)
	}
}

// TestResolveOwnReportDoesNotSelfNotify covers the self case: a moderator
// resolving a report they filed themselves gets no notification.
func TestResolveOwnReportDoesNotSelfNotify(t *testing.T) {
	srv := videoServer(t)
	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	vid := createPublishedVideo(t, srv, admin, "ada", `{"title":"Clip","privacy":"public"}`)

	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+vid+"/report", `{"reason":"spam"}`, admin); rec.Code != http.StatusNoContent {
		t.Fatalf("report = %d", rec.Code)
	}
	var open reportListResponse
	_ = json.Unmarshal(listReports(srv, "?status=open", admin).Body.Bytes(), &open)
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/reports/"+open.Reports[0].ID+"/resolve", `{"status":"accepted"}`, admin); rec.Code != http.StatusNoContent {
		t.Fatalf("resolve own report = %d", rec.Code)
	}
	if got := unreadCount(t, srv, admin); got != 0 {
		t.Errorf("unread after resolving own report = %d, want 0", got)
	}
}

// TestResolveReportSurvivesNotifyFailure covers the absent case: notification
// persistence failing must never fail the resolve (best-effort side effect).
func TestResolveReportSurvivesNotifyFailure(t *testing.T) {
	srv, _, _, notifRepo := videoServerEnv(t, testConfig())
	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	vid := createPublishedVideo(t, srv, admin, "ada", `{"title":"Clip","privacy":"public"}`)
	bob := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+vid+"/report", `{"reason":"spam"}`, bob); rec.Code != http.StatusNoContent {
		t.Fatalf("report = %d", rec.Code)
	}
	var open reportListResponse
	_ = json.Unmarshal(listReports(srv, "?status=open", admin).Body.Bytes(), &open)

	notifRepo.createErr = errors.New("notification store down")
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/reports/"+open.Reports[0].ID+"/resolve", `{"status":"accepted"}`, admin); rec.Code != http.StatusNoContent {
		t.Fatalf("resolve with failing notifier = %d, want 204 (best-effort)", rec.Code)
	}
	// The report is resolved even though no notification could be written.
	var after reportListResponse
	_ = json.Unmarshal(listReports(srv, "?status=open", admin).Body.Bytes(), &after)
	if len(after.Reports) != 0 {
		t.Errorf("open after resolve = %d, want 0", len(after.Reports))
	}
	if got := unreadCount(t, srv, bob); got != 0 {
		t.Errorf("unread for reporter = %d, want 0 (create failed)", got)
	}
}

func TestReportValidationAndAuth(t *testing.T) {
	srv := videoServer(t)
	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	vid := createPublishedVideo(t, srv, admin, "ada", `{"title":"Clip","privacy":"public"}`)

	// Blank reason → 422.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+vid+"/report", `{"reason":""}`, admin); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("blank reason = %d, want 422", rec.Code)
	}
	// Bad resolve status → 422.
	// (Need a report to exist; report it first.)
	bob := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	_ = sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+vid+"/report", `{"reason":"spam"}`, bob)
	var body reportListResponse
	_ = json.Unmarshal(listReports(srv, "", admin).Body.Bytes(), &body)
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/reports/"+body.Reports[0].ID+"/resolve", `{"status":"maybe"}`, admin); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("bad status = %d, want 422", rec.Code)
	}

	// Auth required on all routes.
	someID := uuid.New().String()
	cases := []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/videos/" + vid + "/report", `{"reason":"x"}`},
		{http.MethodPost, "/api/v1/comments/" + someID + "/report", `{"reason":"x"}`},
		{http.MethodGet, "/api/v1/admin/reports", ""},
		{http.MethodPost, "/api/v1/admin/reports/" + someID + "/resolve", `{"status":"accepted"}`},
	}
	for _, tc := range cases {
		if rec := sendJSONAuth(srv, tc.method, tc.path, tc.body, ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("anon %s %s = %d, want 401", tc.method, tc.path, rec.Code)
		}
	}
}

// TestDeleteReportAdminOnly proves the hard-delete: an admin purges a report
// (it leaves the queue; the delete is idempotent and audited), while a
// moderator — who can resolve — gets 403, and anonymous callers 401.
func TestDeleteReportAdminOnly(t *testing.T) {
	srv := videoServer(t)
	var buf bytes.Buffer
	srv.logger = slog.New(slog.NewJSONHandler(&buf, nil))

	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	vid := createPublishedVideo(t, srv, admin, "ada", `{"title":"Clip","privacy":"public"}`)
	bob := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	modTok, modID := registerAndUser(t, srv, `{"username":"mia","email":"mia@example.test","password":"supersecret"}`)
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/admin/users/"+modID, `{"role":"moderator"}`, admin); rec.Code != http.StatusOK {
		t.Fatalf("promote moderator = %d; body=%s", rec.Code, rec.Body.String())
	}

	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+vid+"/report", `{"reason":"spam"}`, bob); rec.Code != http.StatusNoContent {
		t.Fatalf("report = %d", rec.Code)
	}
	var open reportListResponse
	_ = json.Unmarshal(listReports(srv, "", admin).Body.Bytes(), &open)
	if len(open.Reports) != 1 {
		t.Fatalf("reports = %d, want 1", len(open.Reports))
	}
	id := open.Reports[0].ID

	// A moderator can see the queue but cannot purge.
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/admin/reports/"+id, "", modTok); rec.Code != http.StatusForbidden {
		t.Errorf("moderator delete = %d, want 403", rec.Code)
	}
	// A regular user cannot either; anonymous is 401.
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/admin/reports/"+id, "", bob); rec.Code != http.StatusForbidden {
		t.Errorf("user delete = %d, want 403", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/admin/reports/"+id, "", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anon delete = %d, want 401", rec.Code)
	}

	// The admin purges it: gone from the queue.
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/admin/reports/"+id, "", admin); rec.Code != http.StatusNoContent {
		t.Fatalf("admin delete = %d; body=%s", rec.Code, rec.Body.String())
	}
	var after reportListResponse
	_ = json.Unmarshal(listReports(srv, "", admin).Body.Bytes(), &after)
	if len(after.Reports) != 0 {
		t.Errorf("reports after delete = %d, want 0", len(after.Reports))
	}
	// Idempotent: re-delete (and an unknown id) still 204; malformed id 404.
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/admin/reports/"+id, "", admin); rec.Code != http.StatusNoContent {
		t.Errorf("re-delete = %d, want 204 (idempotent)", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/admin/reports/"+uuid.New().String(), "", admin); rec.Code != http.StatusNoContent {
		t.Errorf("delete unknown = %d, want 204 (idempotent)", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/admin/reports/not-a-uuid", "", admin); rec.Code != http.StatusNotFound {
		t.Errorf("delete malformed id = %d, want 404", rec.Code)
	}

	// The purge left an audit trail carrying the acting admin.
	ev := findAudit(auditEvents(t, &buf), observability.ActionReportDelete, observability.ResultSuccess)
	if ev == nil {
		t.Fatalf("no %s audit event emitted", observability.ActionReportDelete)
	}
	if ev["actor_id"] == nil || ev["actor_id"] == "" {
		t.Errorf("report delete audit missing actor_id: %v", ev)
	}
	if reason, _ := ev["reason"].(string); !strings.Contains(reason, id) {
		t.Errorf("report delete audit reason = %v, want it to name report %s", ev["reason"], id)
	}
}

// TestNewReportNotifiesStaff proves filing a report pushes a new_report
// notification to every active admin and moderator — the moderation queue's
// push signal — while the reporter and uninvolved regular users hear nothing,
// and an idempotent repeat report does not double-notify.
func TestNewReportNotifiesStaff(t *testing.T) {
	srv := videoServer(t)
	// The first registered account ("ada") becomes admin.
	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	vid := createPublishedVideo(t, srv, admin, "ada", `{"title":"Clip","privacy":"public"}`)
	miaTok, miaID := registerAndUser(t, srv, `{"username":"mia","email":"mia@example.test","password":"supersecret"}`)
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/admin/users/"+miaID, `{"role":"moderator"}`, admin); rec.Code != http.StatusOK {
		t.Fatalf("promote mia = %d; body=%s", rec.Code, rec.Body.String())
	}
	bob := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	pat := registerAndToken(t, srv, `{"username":"pat","email":"pat@example.test","password":"supersecret"}`)

	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+vid+"/report", `{"reason":"spam"}`, bob); rec.Code != http.StatusNoContent {
		t.Fatalf("report = %d; body=%s", rec.Code, rec.Body.String())
	}
	var open reportListResponse
	_ = json.Unmarshal(listReports(srv, "?status=open", admin).Body.Bytes(), &open)
	if len(open.Reports) != 1 {
		t.Fatalf("open reports = %d, want 1", len(open.Reports))
	}
	reportID := open.Reports[0].ID

	// Both staff members are told, with the report context and the reporter as
	// the actor (staff see the reporter in the queue anyway).
	for _, staff := range []struct{ name, token string }{{"admin", admin}, {"moderator", miaTok}} {
		var body notificationListResponse
		_ = json.Unmarshal(listNotifications(srv, staff.token).Body.Bytes(), &body)
		if len(body.Notifications) != 1 {
			t.Fatalf("%s notifications = %d, want 1", staff.name, len(body.Notifications))
		}
		n := body.Notifications[0]
		if n.Type != "new_report" || n.ReportID != reportID || n.ReportStatus != "open" || n.ReportTargetType != "video" {
			t.Errorf("%s notification = %+v, want new_report/%s/open/video", staff.name, n, reportID)
		}
		if n.Actor == nil || n.Actor.Username != "bob" {
			t.Errorf("%s notification actor = %+v, want reporter bob", staff.name, n.Actor)
		}
	}
	// Neither the reporter nor an uninvolved regular user hears anything.
	for _, quiet := range []struct{ name, token string }{{"reporter", bob}, {"regular user", pat}} {
		if got := unreadCount(t, srv, quiet.token); got != 0 {
			t.Errorf("unread for %s = %d, want 0", quiet.name, got)
		}
	}

	// Idempotent repeat: same reporter, same video → still 204, no second
	// notification for anyone.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+vid+"/report", `{"reason":"spam again"}`, bob); rec.Code != http.StatusNoContent {
		t.Fatalf("repeat report = %d", rec.Code)
	}
	if got := unreadCount(t, srv, admin); got != 1 {
		t.Errorf("admin unread after repeat report = %d, want 1", got)
	}
	if got := unreadCount(t, srv, miaTok); got != 1 {
		t.Errorf("moderator unread after repeat report = %d, want 1", got)
	}
}

// TestNewReportStaffFanOutEdgeCases: a staff reporter is not self-notified, a
// staff member who turned the new_report type off hears nothing, and a failing
// notification store never fails the report itself (best-effort side effect).
func TestNewReportStaffFanOutEdgeCases(t *testing.T) {
	srv, _, _, notifRepo := videoServerEnv(t, testConfig())
	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	vid := createPublishedVideo(t, srv, admin, "ada", `{"title":"Clip","privacy":"public"}`)
	miaTok, miaID := registerAndUser(t, srv, `{"username":"mia","email":"mia@example.test","password":"supersecret"}`)
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/admin/users/"+miaID, `{"role":"moderator"}`, admin); rec.Code != http.StatusOK {
		t.Fatalf("promote mia = %d; body=%s", rec.Code, rec.Body.String())
	}

	// mia (staff) reports the video: the admin is told, mia is not.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+vid+"/report", `{"reason":"spam"}`, miaTok); rec.Code != http.StatusNoContent {
		t.Fatalf("staff report = %d; body=%s", rec.Code, rec.Body.String())
	}
	if got := unreadCount(t, srv, admin); got != 1 {
		t.Errorf("admin unread = %d, want 1", got)
	}
	if got := unreadCount(t, srv, miaTok); got != 0 {
		t.Errorf("reporting moderator unread = %d, want 0 (no self-notify)", got)
	}

	// The admin turns the new_report type off; the next report reaches only mia.
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/me/notification-prefs", `{"prefs":{"new_report":false}}`, admin); rec.Code != http.StatusOK {
		t.Fatalf("disable new_report pref = %d; body=%s", rec.Code, rec.Body.String())
	}
	bob := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+vid+"/report", `{"reason":"other"}`, bob); rec.Code != http.StatusNoContent {
		t.Fatalf("report = %d; body=%s", rec.Code, rec.Body.String())
	}
	if got := unreadCount(t, srv, admin); got != 1 {
		t.Errorf("opted-out admin unread = %d, want still 1", got)
	}
	if got := unreadCount(t, srv, miaTok); got != 1 {
		t.Errorf("moderator unread = %d, want 1", got)
	}

	// A failing notification store must never fail the report (best-effort).
	notifRepo.createErr = errors.New("notification store down")
	pat := registerAndToken(t, srv, `{"username":"pat","email":"pat@example.test","password":"supersecret"}`)
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+vid+"/report", `{"reason":"late"}`, pat); rec.Code != http.StatusNoContent {
		t.Fatalf("report with failing notifier = %d, want 204 (best-effort)", rec.Code)
	}
	var open reportListResponse
	_ = json.Unmarshal(listReports(srv, "?status=open", admin).Body.Bytes(), &open)
	if len(open.Reports) != 3 {
		t.Errorf("open reports = %d, want 3 (report persisted despite notify failure)", len(open.Reports))
	}
}

// TestNewReportEmailsOperator proves a new report also emails the operator's
// contact address (push, not pull) — once per report, never for an idempotent
// repeat, and not when the report_email_alerts_enabled setting is off.
func TestNewReportEmailsOperator(t *testing.T) {
	cfg := testConfig()
	cfg.InstanceContactEmail = "ops@example.test"
	settingssvc := instancesettings.NewService(newInstanceSettingsFakeRepo(), settingsDefaultsFromConfig(cfg))
	if err := settingssvc.Load(context.Background()); err != nil {
		t.Fatalf("settings load: %v", err)
	}
	capture := auth.NewCaptureMailer()
	srv, _, _, _, _ := videoServerFullWith(t, cfg, []Option{WithSettingsService(settingssvc), WithContactMailer(capture)})
	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	vid := createPublishedVideo(t, srv, admin, "ada", `{"title":"Clip","privacy":"public"}`)
	bob := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+vid+"/report", `{"reason":"spam"}`, bob); rec.Code != http.StatusNoContent {
		t.Fatalf("report = %d; body=%s", rec.Code, rec.Body.String())
	}
	alerts := capture.ReportAlerts()
	if len(alerts) != 1 {
		t.Fatalf("report alerts = %d, want 1", len(alerts))
	}
	if a := alerts[0]; a.To != "ops@example.test" || a.TargetType != "video" || a.Reason != "spam" {
		t.Errorf("alert = %+v, want to=ops@example.test target=video reason=spam", a)
	}

	// Idempotent repeat: no second email.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+vid+"/report", `{"reason":"again"}`, bob); rec.Code != http.StatusNoContent {
		t.Fatalf("repeat report = %d", rec.Code)
	}
	if n := len(capture.ReportAlerts()); n != 1 {
		t.Errorf("alerts after repeat = %d, want 1", n)
	}

	// The kill switch: report_email_alerts_enabled=false stops the email while
	// the in-app staff notification path stays untouched.
	if err := settingssvc.Apply(context.Background(), map[string]instancesettings.Update{
		instancesettings.KeyReportEmailAlertsEnabled: {Value: instancesettings.FormatBool(false)},
	}, uuid.Nil); err != nil {
		t.Fatalf("disable report email alerts: %v", err)
	}
	pat := registerAndToken(t, srv, `{"username":"pat","email":"pat@example.test","password":"supersecret"}`)
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+vid+"/report", `{"reason":"other"}`, pat); rec.Code != http.StatusNoContent {
		t.Fatalf("report with alerts off = %d", rec.Code)
	}
	if n := len(capture.ReportAlerts()); n != 1 {
		t.Errorf("alerts after disabled setting = %d, want still 1", n)
	}
	if got := unreadCount(t, srv, admin); got != 2 {
		t.Errorf("admin unread = %d, want 2 (in-app fan-out unaffected by the email switch)", got)
	}
}

// Each Count delegates to its List so the fake pair cannot disagree — the same
// invariant the real Count/List SQL pairs must hold.
func (f *moderationFakeRepo) CountReports(ctx context.Context, status *string) (int64, error) {
	rows, err := f.ListReports(ctx, sqlcgen.ListReportsParams{Status: status, ResultLimit: 1 << 30})
	return int64(len(rows)), err
}

func (f *moderationFakeRepo) CountBlockedVideos(ctx context.Context) (int64, error) {
	rows, err := f.ListBlockedVideos(ctx, sqlcgen.ListBlockedVideosParams{ResultLimit: 1 << 30})
	return int64(len(rows)), err
}

func (f *moderationFakeRepo) CountBlockedRemoteVideos(ctx context.Context) (int64, error) {
	rows, err := f.ListBlockedRemoteVideos(ctx, sqlcgen.ListBlockedRemoteVideosParams{ResultLimit: 1 << 30})
	return int64(len(rows)), err
}

// TestReportsStatusFilterIsValidated is the regression test for a silent
// filter: handleListReports used to be `c.QueryParam("status") == "open"`, so
// ?status=resolved — the obvious way to ask for the closed queue — meant "no
// filter" and returned EVERY report. Asking for resolved must return resolved.
func TestReportsStatusFilterIsValidated(t *testing.T) {
	srv := videoServer(t)
	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	one := createPublishedVideo(t, srv, admin, "ada", `{"title":"one","privacy":"public"}`)
	two := createPublishedVideo(t, srv, admin, "ada", `{"title":"two","privacy":"public"}`)
	bob := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	for _, vid := range []string{one, two} {
		if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+vid+"/report", `{"reason":"spam"}`, bob); rec.Code != http.StatusNoContent {
			t.Fatalf("report %s = %d", vid, rec.Code)
		}
	}
	list := func(query string) reportListResponse {
		t.Helper()
		rec := listReports(srv, query, admin)
		if rec.Code != http.StatusOK {
			t.Fatalf("list%s = %d; body=%s", query, rec.Code, rec.Body.String())
		}
		var out reportListResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out
	}

	// Resolve exactly one of the two.
	open := list("?status=open")
	if open.Total != 2 {
		t.Fatalf("open total = %d, want 2", open.Total)
	}
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/reports/"+open.Reports[0].ID+"/resolve", `{"status":"accepted"}`, admin); rec.Code != http.StatusNoContent {
		t.Fatalf("resolve = %d", rec.Code)
	}

	for _, tc := range []struct {
		query string
		want  int64
	}{
		{query: "?status=open", want: 1},
		// The bug: this used to return 2.
		{query: "?status=resolved", want: 1},
		{query: "?status=all", want: 2},
		{query: "", want: 2}, // absent still means "everything", as before
	} {
		got := list(tc.query)
		if got.Total != tc.want {
			t.Errorf("reports%s total = %d, want %d", tc.query, got.Total, tc.want)
		}
		if int64(len(got.Reports)) != tc.want {
			t.Errorf("reports%s rows = %d, want %d", tc.query, len(got.Reports), tc.want)
		}
	}

	// An unrecognised status is rejected instead of quietly meaning "all".
	if rec := listReports(srv, "?status=nonsense", admin); rec.Code != http.StatusBadRequest {
		t.Errorf("status=nonsense = %d, want 400", rec.Code)
	}
}
