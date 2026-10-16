package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// ListMyReports mirrors the real query's projection: the caller's own local
// reports, most recent activity first, with a message count and target
// context — and no moderator note, which the row type does not even carry. The
// ordering and the private-title rule are proved in
// store.TestListMyReportsOnRealPG.
func (f *moderationFakeRepo) ListMyReports(_ context.Context, a sqlcgen.ListMyReportsParams) ([]sqlcgen.ListMyReportsRow, error) {
	var rows []sqlcgen.ListMyReportsRow
	for i := len(f.reports) - 1; i >= 0; i-- {
		r := f.reports[i]
		if r.remoteActor != "" || r.reporterID != a.ReporterID || (a.ReportID.Valid && r.id != uuid.UUID(a.ReportID.Bytes)) {
			continue
		}
		row := sqlcgen.ListMyReportsRow{ID: r.id, TargetType: r.targetType, Status: r.status, Reason: r.reason,
			CreatedAt: r.createdAt, ResolvedAt: r.resolvedAt, LastActivityAt: r.createdAt, VideoID: r.videoID,
			PredefinedReasons: r.reasons, VideoStartSeconds: r.startSeconds, VideoEndSeconds: r.endSeconds}
		if r.videoID.Valid {
			if v, ok := f.videos.videos[uuid.UUID(r.videoID.Bytes)]; ok {
				row.VideoTitle = v.Title
			}
		}
		for _, m := range f.messages {
			if m.reportID == r.id {
				row.MessageCount++
				row.LastActivityAt = m.createdAt
			}
		}
		rows = append(rows, row)
	}
	rows = rows[min(int(a.ResultOffset), len(rows)):]
	return rows[:min(int(a.ResultLimit), len(rows))], nil
}

func (f *moderationFakeRepo) CountMyReports(ctx context.Context, reporterID uuid.UUID) (int64, error) {
	rows, err := f.ListMyReports(ctx, sqlcgen.ListMyReportsParams{ReporterID: reporterID, ResultLimit: 1 << 30})
	return int64(len(rows)), err
}

// GET /me/reports lists the caller's own reports with their state, target and
// thread activity — never the moderator's internal note — and GET
// /me/reports/{id} returns one of them; another user's report is the same 404
// as an unknown one.
func TestMyReports(t *testing.T) {
	fx := newReportThreadFixture(t) // bob (fx.reporter) has reported ada's "Clip"
	if rec := sendJSONAuth(fx.srv, http.MethodPost, "/api/v1/admin/reports/"+fx.reportID+"/resolve",
		`{"status":"accepted","note":"internal: repeat offender"}`, fx.admin); rec.Code != http.StatusNoContent {
		t.Fatalf("resolve = %d", rec.Code)
	}
	if got := fx.post(fx.mod, `{"body":"thanks, handled"}`); got != http.StatusCreated {
		t.Fatalf("moderator POST = %d", got)
	}

	rec := sendJSONAuth(fx.srv, http.MethodGet, "/api/v1/me/reports", "", fx.reporter)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /me/reports = %d; body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "repeat offender") || strings.Contains(rec.Body.String(), "moderator_note") {
		t.Errorf("my reports leaked the moderator's internal note: %s", rec.Body.String())
	}
	var mine myReportListResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &mine)
	if mine.Total != 1 || len(mine.Reports) != 1 {
		t.Fatalf("my reports = %+v, want exactly bob's one", mine)
	}
	r := mine.Reports[0]
	if r.ID != fx.reportID || r.Status != "accepted" || r.TargetType != "video" || r.VideoTitle != "Clip" ||
		r.Reason != "spam" || r.MessageCount != 1 || r.ResolvedAt == nil || r.LastActivityAt.IsZero() {
		t.Errorf("my report = %+v, want the accepted video report with its one message", r)
	}

	// Others see none of it: an empty list, and a 404 for bob's report id.
	var others myReportListResponse
	_ = json.Unmarshal(sendJSONAuth(fx.srv, http.MethodGet, "/api/v1/me/reports", "", fx.other).Body.Bytes(), &others)
	if others.Total != 0 || len(others.Reports) != 0 || others.Reports == nil {
		t.Errorf("another user's my reports = %+v, want an empty list", others)
	}
	if rec := sendJSONAuth(fx.srv, http.MethodGet, "/api/v1/me/reports/"+fx.reportID, "", fx.reporter); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"id":"`+fx.reportID+`"`) {
		t.Errorf("GET my report = %d %s, want 200 with the report", rec.Code, rec.Body.String())
	}
	for _, tc := range []struct{ name, id, token string }{
		{"another user's report", fx.reportID, fx.other},
		{"a moderator (not the reporter)", fx.reportID, fx.mod},
		{"unknown report", uuid.NewString(), fx.reporter},
		{"malformed id", "nope", fx.reporter},
	} {
		if got := sendJSONAuth(fx.srv, http.MethodGet, "/api/v1/me/reports/"+tc.id, "", tc.token).Code; got != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", tc.name, got)
		}
	}
	if got := sendJSONAuth(fx.srv, http.MethodGet, "/api/v1/me/reports", "", "").Code; got != http.StatusUnauthorized {
		t.Errorf("anonymous GET /me/reports = %d, want 401", got)
	}
}
