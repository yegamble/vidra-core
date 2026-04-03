package analytics

import (
	"context"
	"fmt"
	"net/http"
	"time"

	ucanalytics "vidra-core/internal/usecase/analytics"

	"vidra-core/internal/domain"
	"vidra-core/internal/httpapi/shared"
	"vidra-core/internal/middleware"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// AnalyticsExportService defines the interface for analytics export operations.
type AnalyticsExportService interface {
	ValidateVideoOwnership(ctx context.Context, videoID, userID uuid.UUID) error
	ValidateChannelOwnership(ctx context.Context, channelID, userID uuid.UUID) error
	GenerateCSV(ctx context.Context, params ucanalytics.ExportParams) ([]byte, error)
	GenerateJSON(ctx context.Context, params ucanalytics.ExportParams) ([]byte, error)
	GeneratePDF(ctx context.Context, params ucanalytics.ExportParams) ([]byte, error)
}

// ExportHandler handles analytics export HTTP requests.
type ExportHandler struct {
	exportService AnalyticsExportService
}

// NewExportHandler creates a new ExportHandler.
func NewExportHandler(exportService AnalyticsExportService) *ExportHandler {
	return &ExportHandler{
		exportService: exportService,
	}
}

// RegisterRoutes registers the analytics export routes.
func (h *ExportHandler) RegisterRoutes(r chi.Router, jwtSecret string) {
	r.Route("/api/v1/analytics/export", func(r chi.Router) {
		r.Use(middleware.Auth(jwtSecret))
		r.Get("/csv", h.ExportCSV)
		r.Get("/json", h.ExportJSON)
		r.Get("/pdf", h.ExportPDF)
	})
}

// ExportCSV handles CSV export requests.
func (h *ExportHandler) ExportCSV(w http.ResponseWriter, r *http.Request) {
	h.handleExport(w, r, "text/csv", "csv", h.exportService.GenerateCSV)
}

// ExportJSON handles JSON export requests.
func (h *ExportHandler) ExportJSON(w http.ResponseWriter, r *http.Request) {
	h.handleExport(w, r, "application/json", "json", h.exportService.GenerateJSON)
}

// ExportPDF handles PDF export requests.
func (h *ExportHandler) ExportPDF(w http.ResponseWriter, r *http.Request) {
	h.handleExport(w, r, "application/pdf", "pdf", h.exportService.GeneratePDF)
}

type generateFunc func(ctx context.Context, params ucanalytics.ExportParams) ([]byte, error)

func (h *ExportHandler) handleExport(w http.ResponseWriter, r *http.Request, contentType, ext string, generate generateFunc) {
	params, authMissing, err := parseExportRequest(r)
	if err != nil {
		if authMissing {
			shared.WriteError(w, http.StatusUnauthorized, domain.ErrUnauthorized)
			return
		}
		shared.WriteError(w, http.StatusBadRequest, err)
		return
	}

	if err := validateOwnership(r.Context(), h.exportService, params); err != nil {
		status := shared.MapDomainErrorToHTTP(err)
		shared.WriteError(w, status, err)
		return
	}

	data, err := generate(r.Context(), params)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to generate %s export", ext))
		return
	}

	filename := exportFilename(params, ext)
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.WriteHeader(http.StatusOK)
	w.Write(data) //nolint:errcheck
}

func parseExportRequest(r *http.Request) (ucanalytics.ExportParams, bool, error) {
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		return ucanalytics.ExportParams{}, true, fmt.Errorf("user not authenticated")
	}

	params := ucanalytics.ExportParams{
		UserID: userID,
	}

	if videoIDStr := r.URL.Query().Get("videoId"); videoIDStr != "" {
		videoID, err := uuid.Parse(videoIDStr)
		if err != nil {
			return ucanalytics.ExportParams{}, false, fmt.Errorf("invalid videoId: must be a valid UUID")
		}
		params.VideoID = &videoID
	}

	if channelIDStr := r.URL.Query().Get("channelId"); channelIDStr != "" {
		channelID, err := uuid.Parse(channelIDStr)
		if err != nil {
			return ucanalytics.ExportParams{}, false, fmt.Errorf("invalid channelId: must be a valid UUID")
		}
		params.ChannelID = &channelID
	}

	startDate, endDate, err := parseDateRange(r)
	if err != nil {
		return ucanalytics.ExportParams{}, false, err
	}
	params.StartDate = startDate
	params.EndDate = endDate

	return params, false, nil
}

func parseDateRange(r *http.Request) (time.Time, time.Time, error) {
	var startDate, endDate time.Time
	var err error

	startDateStr := r.URL.Query().Get("start_date")
	endDateStr := r.URL.Query().Get("end_date")

	if startDateStr != "" {
		startDate, err = time.Parse("2006-01-02", startDateStr)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid start_date format (use YYYY-MM-DD)")
		}
	} else {
		startDate = time.Now().AddDate(0, 0, -30)
	}

	if endDateStr != "" {
		endDate, err = time.Parse("2006-01-02", endDateStr)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid end_date format (use YYYY-MM-DD)")
		}
	} else {
		endDate = time.Now()
	}

	if endDate.Sub(startDate) > 365*24*time.Hour {
		return time.Time{}, time.Time{}, fmt.Errorf("date range exceeds maximum of 365 days")
	}

	return startDate, endDate, nil
}

func validateOwnership(ctx context.Context, svc AnalyticsExportService, params ucanalytics.ExportParams) error {
	if params.VideoID != nil {
		return svc.ValidateVideoOwnership(ctx, *params.VideoID, params.UserID)
	}
	if params.ChannelID != nil {
		return svc.ValidateChannelOwnership(ctx, *params.ChannelID, params.UserID)
	}
	return nil
}

func exportFilename(params ucanalytics.ExportParams, ext string) string {
	if params.VideoID != nil {
		return fmt.Sprintf("analytics-%s.%s", params.VideoID.String(), ext)
	}
	if params.ChannelID != nil {
		return fmt.Sprintf("analytics-channel-%s.%s", params.ChannelID.String(), ext)
	}
	return fmt.Sprintf("analytics-all-channels.%s", ext)
}
