package analytics

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"vidra-core/internal/domain"
	"vidra-core/internal/middleware"
	ucanalytics "vidra-core/internal/usecase/analytics"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Mock service ---

type mockExportService struct {
	csvData  []byte
	jsonData []byte
	pdfData  []byte
	err      error
	ownerErr error
}

func (m *mockExportService) ValidateVideoOwnership(_ context.Context, _, _ uuid.UUID) error {
	return m.ownerErr
}

func (m *mockExportService) ValidateChannelOwnership(_ context.Context, _, _ uuid.UUID) error {
	return m.ownerErr
}

func (m *mockExportService) GenerateCSV(_ context.Context, _ ucanalytics.ExportParams) ([]byte, error) {
	return m.csvData, m.err
}

func (m *mockExportService) GenerateJSON(_ context.Context, _ ucanalytics.ExportParams) ([]byte, error) {
	return m.jsonData, m.err
}

func (m *mockExportService) GeneratePDF(_ context.Context, _ ucanalytics.ExportParams) ([]byte, error) {
	return m.pdfData, m.err
}

// --- Helper ---

func newAuthRequest(method, url string, userID uuid.UUID) *http.Request {
	req := httptest.NewRequest(method, url, nil)
	ctx := middleware.WithUserID(req.Context(), userID)
	return req.WithContext(ctx)
}

func newUnauthRequest(method, url string) *http.Request {
	return httptest.NewRequest(method, url, nil)
}

// --- Tests ---

func TestExportCSV_VideoLevel(t *testing.T) {
	videoID := uuid.New()
	userID := uuid.New()

	csvContent := "date,views,unique_viewers,watch_time_seconds,likes,comments,shares\n2026-04-01,100,80,5000,10,5,2\n"
	handler := NewExportHandler(&mockExportService{csvData: []byte(csvContent)})

	req := newAuthRequest("GET", fmt.Sprintf("/api/v1/analytics/export/csv?videoId=%s", videoID), userID)
	rec := httptest.NewRecorder()
	handler.ExportCSV(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "text/csv")
	assert.Contains(t, rec.Header().Get("Content-Disposition"), "attachment")
	assert.Contains(t, rec.Header().Get("Content-Disposition"), videoID.String())

	reader := csv.NewReader(strings.NewReader(rec.Body.String()))
	records, err := reader.ReadAll()
	require.NoError(t, err)
	assert.Len(t, records, 2)
}

func TestExportJSON_VideoLevel(t *testing.T) {
	videoID := uuid.New()
	userID := uuid.New()

	jsonContent := `{"summary":{"total_views":500},"daily":[]}`
	handler := NewExportHandler(&mockExportService{jsonData: []byte(jsonContent)})

	req := newAuthRequest("GET", fmt.Sprintf("/api/v1/analytics/export/json?videoId=%s", videoID), userID)
	rec := httptest.NewRecorder()
	handler.ExportJSON(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "application/json")
	assert.Contains(t, rec.Header().Get("Content-Disposition"), "attachment")

	var result map[string]interface{}
	err := json.Unmarshal(rec.Body.Bytes(), &result)
	require.NoError(t, err)
	assert.Contains(t, result, "summary")
}

func TestExportPDF_VideoLevel(t *testing.T) {
	videoID := uuid.New()
	userID := uuid.New()

	pdfContent := []byte("%PDF-1.4 fake pdf content")
	handler := NewExportHandler(&mockExportService{pdfData: pdfContent})

	req := newAuthRequest("GET", fmt.Sprintf("/api/v1/analytics/export/pdf?videoId=%s", videoID), userID)
	rec := httptest.NewRecorder()
	handler.ExportPDF(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "application/pdf")
	assert.Contains(t, rec.Header().Get("Content-Disposition"), "attachment")
	assert.NotEmpty(t, rec.Body.Bytes())
}

func TestExportCSV_ChannelLevel(t *testing.T) {
	channelID := uuid.New()
	userID := uuid.New()

	csvContent := "date,views\n2026-04-01,200\n"
	handler := NewExportHandler(&mockExportService{csvData: []byte(csvContent)})

	req := newAuthRequest("GET", fmt.Sprintf("/api/v1/analytics/export/csv?channelId=%s", channelID), userID)
	rec := httptest.NewRecorder()
	handler.ExportCSV(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Disposition"), "channel")
}

func TestExportCSV_AllChannels(t *testing.T) {
	userID := uuid.New()

	csvContent := "date,views\n"
	handler := NewExportHandler(&mockExportService{csvData: []byte(csvContent)})

	req := newAuthRequest("GET", "/api/v1/analytics/export/csv", userID)
	rec := httptest.NewRecorder()
	handler.ExportCSV(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Disposition"), "all-channels")
}

func TestExport_OwnershipForbidden(t *testing.T) {
	videoID := uuid.New()
	userID := uuid.New()

	handler := NewExportHandler(&mockExportService{ownerErr: domain.ErrForbidden})

	req := newAuthRequest("GET", fmt.Sprintf("/api/v1/analytics/export/csv?videoId=%s", videoID), userID)
	rec := httptest.NewRecorder()
	handler.ExportCSV(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestExport_InvalidVideoID(t *testing.T) {
	userID := uuid.New()
	handler := NewExportHandler(&mockExportService{})

	req := newAuthRequest("GET", "/api/v1/analytics/export/csv?videoId=not-a-uuid", userID)
	rec := httptest.NewRecorder()
	handler.ExportCSV(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestExport_InvalidDateFormat(t *testing.T) {
	userID := uuid.New()
	handler := NewExportHandler(&mockExportService{})

	req := newAuthRequest("GET", "/api/v1/analytics/export/csv?start_date=invalid", userID)
	rec := httptest.NewRecorder()
	handler.ExportCSV(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestExport_DateDefaultsToLast30Days(t *testing.T) {
	userID := uuid.New()

	var capturedParams ucanalytics.ExportParams
	svc := &capturingMockService{csvData: []byte("date,views\n"), capturedParams: &capturedParams}
	handler := NewExportHandler(svc)

	req := newAuthRequest("GET", "/api/v1/analytics/export/csv", userID)
	rec := httptest.NewRecorder()
	handler.ExportCSV(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	daysDiff := capturedParams.EndDate.Sub(capturedParams.StartDate).Hours() / 24
	assert.InDelta(t, 30, daysDiff, 1)
}

func TestExport_DateRangeExceeds365Days(t *testing.T) {
	userID := uuid.New()
	handler := NewExportHandler(&mockExportService{})

	req := newAuthRequest("GET", "/api/v1/analytics/export/csv?start_date=2020-01-01&end_date=2026-04-03", userID)
	rec := httptest.NewRecorder()
	handler.ExportCSV(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestExport_ServiceError(t *testing.T) {
	userID := uuid.New()
	handler := NewExportHandler(&mockExportService{err: fmt.Errorf("internal error")})

	req := newAuthRequest("GET", "/api/v1/analytics/export/csv", userID)
	rec := httptest.NewRecorder()
	handler.ExportCSV(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestExport_NoAuth(t *testing.T) {
	handler := NewExportHandler(&mockExportService{csvData: []byte("data")})

	req := newUnauthRequest("GET", "/api/v1/analytics/export/csv")
	rec := httptest.NewRecorder()
	handler.ExportCSV(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

// capturingMockService captures params for assertion
type capturingMockService struct {
	csvData        []byte
	capturedParams *ucanalytics.ExportParams
}

func (m *capturingMockService) ValidateVideoOwnership(_ context.Context, _, _ uuid.UUID) error {
	return nil
}

func (m *capturingMockService) ValidateChannelOwnership(_ context.Context, _, _ uuid.UUID) error {
	return nil
}

func (m *capturingMockService) GenerateCSV(_ context.Context, params ucanalytics.ExportParams) ([]byte, error) {
	*m.capturedParams = params
	return m.csvData, nil
}

func (m *capturingMockService) GenerateJSON(_ context.Context, _ ucanalytics.ExportParams) ([]byte, error) {
	return nil, nil
}

func (m *capturingMockService) GeneratePDF(_ context.Context, _ ucanalytics.ExportParams) ([]byte, error) {
	return nil, nil
}
