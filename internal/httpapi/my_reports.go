package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/moderation"
)

// myReportView is one of the caller's own reports ("My reports"). It has no
// moderator_note on purpose: that note is staff-internal. Target context is
// type-dependent and omitted when it does not apply.
type myReportView struct {
	ID               string     `json:"id"`
	TargetType       string     `json:"target_type"`
	Status           string     `json:"status"`
	Reason           string     `json:"reason"`
	CreatedAt        time.Time  `json:"created_at"`
	ResolvedAt       *time.Time `json:"resolved_at,omitempty"`
	LastActivityAt   time.Time  `json:"last_activity_at"`
	MessageCount     int64      `json:"message_count"`
	VideoID          string     `json:"video_id,omitempty"`
	VideoTitle       string     `json:"video_title,omitempty"`
	ReportedUsername string     `json:"reported_username,omitempty"`
	RemoteVideoTitle string     `json:"remote_video_title,omitempty"`
	Reasons          []string   `json:"reasons"`
	StartSeconds     *int32     `json:"start_seconds,omitempty"`
	EndSeconds       *int32     `json:"end_seconds,omitempty"`
}

func newMyReportView(r moderation.MyReport) myReportView {
	return myReportView{ID: r.ID.String(), TargetType: r.TargetType, Status: r.Status, Reason: r.Reason,
		CreatedAt: r.CreatedAt, ResolvedAt: r.ResolvedAt, LastActivityAt: r.LastActivityAt, MessageCount: r.MessageCount,
		VideoID: r.VideoID, VideoTitle: r.VideoTitle, ReportedUsername: r.ReportedUsername,
		RemoteVideoTitle: r.RemoteVideoTitle, Reasons: nonNilStrings(r.Reasons),
		StartSeconds: r.VideoStartSeconds, EndSeconds: r.VideoEndSeconds}
}

type myReportListResponse struct {
	Reports []myReportView `json:"reports"`
	pageMeta
}

// handleListMyReports returns the caller's own reports, most recently active
// first. Behind requireAuth.
func (s *Server) handleListMyReports(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	page := parsePage(c, defaultVideoFeedLimit, maxVideoFeedLimit)
	items, total, err := s.moderationsvc.ListMine(c.Request().Context(), userID, page.Limit32(), page.Offset32())
	if err != nil {
		return err
	}
	views := make([]myReportView, 0, len(items))
	for _, it := range items {
		views = append(views, newMyReportView(it))
	}
	return c.JSON(http.StatusOK, myReportListResponse{Reports: views, pageMeta: page.meta(total)})
}

// handleGetMyReport returns one of the caller's own reports; anyone else's
// (staff included) is the same 404 as an unknown one. Behind requireAuth.
func (s *Server) handleGetMyReport(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	id, err := pathUUID(c, "id", "report not found")
	if err != nil {
		return err
	}
	r, err := s.moderationsvc.GetMine(c.Request().Context(), userID, id)
	if errors.Is(err, moderation.ErrNotFound) {
		return echo.NewHTTPError(http.StatusNotFound, "report not found")
	}
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, newMyReportView(r))
}
