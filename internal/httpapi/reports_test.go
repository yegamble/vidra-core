package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

type modReportRow struct {
	id         uuid.UUID
	reporterID uuid.UUID
	targetType string
	videoID    pgtype.UUID
	commentID  pgtype.UUID
	reason     string
	status     string
	note       string
	createdAt  time.Time
	resolvedAt pgtype.Timestamptz
}

// moderationFakeRepo is an in-memory moderation.Repository that resolves the
// reporter / target join columns from the sibling fakes, mirroring the real
// query, and enforces the comment foreign key.
type moderationFakeRepo struct {
	auth     *authFakeRepo
	videos   *videoFakeRepo
	comments *commentFakeRepo
	reports  []modReportRow
}

func (f *moderationFakeRepo) CreateVideoReport(_ context.Context, a sqlcgen.CreateVideoReportParams) (int64, error) {
	for _, r := range f.reports {
		if r.reporterID == a.ReporterID && r.videoID == a.VideoID {
			return 0, nil
		}
	}
	f.reports = append(f.reports, modReportRow{
		id: uuid.New(), reporterID: a.ReporterID, targetType: "video",
		videoID: a.VideoID, reason: a.Reason, status: "open", createdAt: time.Now(),
	})
	return 1, nil
}

func (f *moderationFakeRepo) CreateCommentReport(_ context.Context, a sqlcgen.CreateCommentReportParams) (int64, error) {
	if _, err := f.comments.GetComment(context.Background(), uuid.UUID(a.CommentID.Bytes)); err != nil {
		return 0, &pgconn.PgError{Code: "23503"} // foreign-key violation: no such comment
	}
	for _, r := range f.reports {
		if r.reporterID == a.ReporterID && r.commentID == a.CommentID {
			return 0, nil
		}
	}
	f.reports = append(f.reports, modReportRow{
		id: uuid.New(), reporterID: a.ReporterID, targetType: "comment",
		commentID: a.CommentID, reason: a.Reason, status: "open", createdAt: time.Now(),
	})
	return 1, nil
}

func (f *moderationFakeRepo) ListReports(_ context.Context, a sqlcgen.ListReportsParams) ([]sqlcgen.ListReportsRow, error) {
	var rows []sqlcgen.ListReportsRow
	for i := len(f.reports) - 1; i >= 0; i-- {
		r := f.reports[i]
		if a.OpenOnly && r.status != "open" {
			continue
		}
		row := sqlcgen.ListReportsRow{
			ID: r.id, TargetType: r.targetType, VideoID: r.videoID, CommentID: r.commentID,
			Reason: r.reason, Status: r.status, ModeratorNote: r.note,
			ResolvedAt: r.resolvedAt, CreatedAt: r.createdAt,
		}
		if u, err := f.auth.GetUserByID(context.Background(), r.reporterID); err == nil {
			row.ReporterUsername = u.Username
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
		rows = append(rows, row)
	}
	return rows, nil
}

func (f *moderationFakeRepo) ResolveReport(_ context.Context, a sqlcgen.ResolveReportParams) (int64, error) {
	for i := range f.reports {
		if f.reports[i].id == a.ID {
			f.reports[i].status = a.Status
			f.reports[i].note = a.ModeratorNote
			f.reports[i].resolvedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
			return 1, nil
		}
	}
	return 0, nil
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
