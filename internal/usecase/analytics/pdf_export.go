package analytics

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"time"

	"vidra-core/internal/domain"

	"codeberg.org/go-pdf/fpdf"
	"github.com/wcharczuk/go-chart/v2"
	"github.com/wcharczuk/go-chart/v2/drawing"
)

// GeneratePDF generates a PDF export of analytics data.
func (s *ExportService) GeneratePDF(ctx context.Context, params ExportParams) ([]byte, error) {
	if params.VideoID != nil {
		return s.generateVideoPDF(ctx, params)
	}
	if params.ChannelID != nil {
		return s.generateChannelPDF(ctx, params)
	}
	return s.generateAllChannelsPDF(ctx, params)
}

func (s *ExportService) generateVideoPDF(ctx context.Context, params ExportParams) ([]byte, error) {
	summary, err := s.analyticsRepo.GetVideoAnalyticsSummary(ctx, *params.VideoID, params.StartDate, params.EndDate)
	if err != nil {
		return nil, fmt.Errorf("fetching video analytics summary: %w", err)
	}

	retentionData, _ := s.analyticsRepo.GetRetentionData(ctx, *params.VideoID, params.EndDate)

	video, err := s.videoRepo.GetByID(ctx, params.VideoID.String())
	if err != nil {
		return nil, fmt.Errorf("fetching video details: %w", err)
	}

	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.AddPage()

	writeHeader(pdf, video.Title, params.StartDate, params.EndDate)
	writeVideoStatsTable(pdf, summary)

	if len(retentionData) > 0 {
		writeRetentionChart(pdf, retentionData)
		writeRetentionTable(pdf, retentionData)
	} else {
		writeSectionTitle(pdf, "Retention Curve")
		writeNoData(pdf)
	}

	writeGeographyTable(pdf, summary.TopCountries)
	writeDeviceTable(pdf, summary.DeviceBreakdown)
	writeTrafficSourceTable(pdf, summary.TrafficSources)
	writeFooter(pdf)

	return pdfToBytes(pdf)
}

func (s *ExportService) generateChannelPDF(ctx context.Context, params ExportParams) ([]byte, error) {
	daily, err := s.analyticsRepo.GetChannelDailyAnalyticsRange(ctx, *params.ChannelID, params.StartDate, params.EndDate)
	if err != nil {
		return nil, fmt.Errorf("fetching channel daily analytics: %w", err)
	}

	totalViews, _ := s.analyticsRepo.GetTotalViewsForChannel(ctx, *params.ChannelID)

	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.AddPage()

	writeHeader(pdf, "Channel Analytics", params.StartDate, params.EndDate)
	writeChannelStatsTable(pdf, daily, totalViews)
	writeFooter(pdf)

	return pdfToBytes(pdf)
}

func (s *ExportService) generateAllChannelsPDF(ctx context.Context, params ExportParams) ([]byte, error) {
	channels, err := s.channelRepo.GetChannelsByAccountID(ctx, params.UserID)
	if err != nil {
		return nil, fmt.Errorf("fetching user channels: %w", err)
	}

	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.AddPage()

	writeHeader(pdf, "All Channels Analytics", params.StartDate, params.EndDate)

	for _, ch := range channels {
		daily, err := s.analyticsRepo.GetChannelDailyAnalyticsRange(ctx, ch.ID, params.StartDate, params.EndDate)
		if err != nil {
			continue
		}
		totalViews, _ := s.analyticsRepo.GetTotalViewsForChannel(ctx, ch.ID)

		writeSectionTitle(pdf, fmt.Sprintf("Channel: %s", ch.Name))
		writeChannelStatsTable(pdf, daily, totalViews)
	}

	writeFooter(pdf)

	return pdfToBytes(pdf)
}

// --- PDF helper functions ---

func writeHeader(pdf *fpdf.Fpdf, title string, startDate, endDate time.Time) {
	pdf.SetFont("Helvetica", "B", 18)
	pdf.CellFormat(0, 12, title, "", 1, "C", false, 0, "")
	pdf.Ln(4)

	pdf.SetFont("Helvetica", "", 10)
	dateRange := fmt.Sprintf("%s to %s", startDate.Format("2006-01-02"), endDate.Format("2006-01-02"))
	pdf.CellFormat(0, 6, dateRange, "", 1, "C", false, 0, "")
	pdf.Ln(8)
}

func writeSectionTitle(pdf *fpdf.Fpdf, title string) {
	pdf.Ln(6)
	pdf.SetFont("Helvetica", "B", 13)
	pdf.CellFormat(0, 8, title, "", 1, "L", false, 0, "")
	pdf.Ln(2)
}

func writeNoData(pdf *fpdf.Fpdf) {
	pdf.SetFont("Helvetica", "I", 10)
	pdf.CellFormat(0, 6, "No data available", "", 1, "L", false, 0, "")
}

func writeVideoStatsTable(pdf *fpdf.Fpdf, summary *domain.AnalyticsSummary) {
	writeSectionTitle(pdf, "Stats Summary")
	pdf.SetFont("Helvetica", "B", 9)

	colWidths := []float64{45, 45}
	headers := []string{"Metric", "Value"}
	for i, h := range headers {
		pdf.CellFormat(colWidths[i], 7, h, "1", 0, "C", false, 0, "")
	}
	pdf.Ln(-1)

	pdf.SetFont("Helvetica", "", 9)
	rows := [][]string{
		{"Total Views", strconv.Itoa(summary.TotalViews)},
		{"Unique Viewers", strconv.Itoa(summary.TotalUniqueViewers)},
		{"Watch Time (hours)", fmt.Sprintf("%.1f", float64(summary.TotalWatchTimeSeconds)/3600)},
		{"Likes", strconv.Itoa(summary.TotalLikes)},
		{"Dislikes", strconv.Itoa(summary.TotalDislikes)},
		{"Comments", strconv.Itoa(summary.TotalComments)},
		{"Shares", strconv.Itoa(summary.TotalShares)},
	}

	for _, row := range rows {
		pdf.CellFormat(colWidths[0], 6, row[0], "1", 0, "L", false, 0, "")
		pdf.CellFormat(colWidths[1], 6, row[1], "1", 0, "R", false, 0, "")
		pdf.Ln(-1)
	}
}

func writeChannelStatsTable(pdf *fpdf.Fpdf, daily []*domain.ChannelDailyAnalytics, totalViews int) {
	writeSectionTitle(pdf, "Stats Summary")

	var totalWatchTime int64
	var totalLikes, totalComments, totalShares int
	var totalSubsGained, totalSubsLost int
	for _, d := range daily {
		totalWatchTime += d.WatchTimeSeconds
		totalLikes += d.Likes
		totalComments += d.Comments
		totalShares += d.Shares
		totalSubsGained += d.SubscribersGained
		totalSubsLost += d.SubscribersLost
	}

	pdf.SetFont("Helvetica", "B", 9)
	colWidths := []float64{45, 45}
	pdf.CellFormat(colWidths[0], 7, "Metric", "1", 0, "C", false, 0, "")
	pdf.CellFormat(colWidths[1], 7, "Value", "1", 0, "C", false, 0, "")
	pdf.Ln(-1)

	pdf.SetFont("Helvetica", "", 9)
	rows := [][]string{
		{"Total Views", strconv.Itoa(totalViews)},
		{"Watch Time (hours)", fmt.Sprintf("%.1f", float64(totalWatchTime)/3600)},
		{"Likes", strconv.Itoa(totalLikes)},
		{"Comments", strconv.Itoa(totalComments)},
		{"Shares", strconv.Itoa(totalShares)},
		{"Subscribers Gained", strconv.Itoa(totalSubsGained)},
		{"Subscribers Lost", strconv.Itoa(totalSubsLost)},
	}

	for _, row := range rows {
		pdf.CellFormat(colWidths[0], 6, row[0], "1", 0, "L", false, 0, "")
		pdf.CellFormat(colWidths[1], 6, row[1], "1", 0, "R", false, 0, "")
		pdf.Ln(-1)
	}
}

func writeRetentionChart(pdf *fpdf.Fpdf, data []*domain.RetentionData) {
	writeSectionTitle(pdf, "Retention Curve")

	chartImg, err := renderRetentionChart(data)
	if err != nil {
		writeNoData(pdf)
		return
	}

	opts := fpdf.ImageOptions{ImageType: "PNG", ReadDpi: true}
	pdf.RegisterImageOptionsReader("retention_chart", opts, bytes.NewReader(chartImg))
	imgY := pdf.GetY()
	// Width=180mm, aspect ratio 2:1 → height=90mm
	const chartHeight = 90.0
	pdf.ImageOptions("retention_chart", 15, imgY, 180, chartHeight, false, opts, 0, "")
	pdf.SetY(imgY + chartHeight + 4)
}

func renderRetentionChart(data []*domain.RetentionData) ([]byte, error) {
	xValues := make([]float64, len(data))
	yValues := make([]float64, len(data))
	for i, d := range data {
		xValues[i] = float64(d.TimestampSeconds)
		yValues[i] = float64(d.ViewerCount)
	}

	graph := chart.Chart{
		Width:  640,
		Height: 320,
		XAxis: chart.XAxis{
			Name: "Time (seconds)",
			Style: chart.Style{
				FontSize: 8,
			},
		},
		YAxis: chart.YAxis{
			Name: "Viewers",
			Style: chart.Style{
				FontSize: 8,
			},
		},
		Series: []chart.Series{
			chart.ContinuousSeries{
				XValues: xValues,
				YValues: yValues,
				Style: chart.Style{
					StrokeColor: drawing.ColorFromHex("336699"),
					StrokeWidth: 2.0,
				},
			},
		},
	}

	var buf bytes.Buffer
	if err := graph.Render(chart.PNG, &buf); err != nil {
		return nil, fmt.Errorf("rendering chart: %w", err)
	}

	return buf.Bytes(), nil
}

func writeRetentionTable(pdf *fpdf.Fpdf, data []*domain.RetentionData) {
	pdf.Ln(4)
	pdf.SetFont("Helvetica", "B", 9)
	pdf.CellFormat(45, 7, "Time (seconds)", "1", 0, "C", false, 0, "")
	pdf.CellFormat(45, 7, "Viewer Count", "1", 0, "C", false, 0, "")
	pdf.Ln(-1)

	pdf.SetFont("Helvetica", "", 9)
	for _, d := range data {
		pdf.CellFormat(45, 6, strconv.Itoa(d.TimestampSeconds), "1", 0, "R", false, 0, "")
		pdf.CellFormat(45, 6, strconv.Itoa(d.ViewerCount), "1", 0, "R", false, 0, "")
		pdf.Ln(-1)
	}
}

func writeGeographyTable(pdf *fpdf.Fpdf, countries []domain.CountryStat) {
	writeSectionTitle(pdf, "Top Countries")
	if len(countries) == 0 {
		writeNoData(pdf)
		return
	}

	pdf.SetFont("Helvetica", "B", 9)
	pdf.CellFormat(45, 7, "Country", "1", 0, "C", false, 0, "")
	pdf.CellFormat(45, 7, "Views", "1", 0, "C", false, 0, "")
	pdf.Ln(-1)

	pdf.SetFont("Helvetica", "", 9)
	limit := len(countries)
	if limit > 10 {
		limit = 10
	}
	for _, c := range countries[:limit] {
		pdf.CellFormat(45, 6, c.Country, "1", 0, "L", false, 0, "")
		pdf.CellFormat(45, 6, strconv.Itoa(c.Views), "1", 0, "R", false, 0, "")
		pdf.Ln(-1)
	}
}

func writeDeviceTable(pdf *fpdf.Fpdf, devices []domain.DeviceStat) {
	writeSectionTitle(pdf, "Device Breakdown")
	if len(devices) == 0 {
		writeNoData(pdf)
		return
	}

	pdf.SetFont("Helvetica", "B", 9)
	pdf.CellFormat(45, 7, "Device", "1", 0, "C", false, 0, "")
	pdf.CellFormat(45, 7, "Views", "1", 0, "C", false, 0, "")
	pdf.Ln(-1)

	pdf.SetFont("Helvetica", "", 9)
	for _, d := range devices {
		pdf.CellFormat(45, 6, d.Device, "1", 0, "L", false, 0, "")
		pdf.CellFormat(45, 6, strconv.Itoa(d.Views), "1", 0, "R", false, 0, "")
		pdf.Ln(-1)
	}
}

func writeTrafficSourceTable(pdf *fpdf.Fpdf, sources []domain.TrafficSource) {
	writeSectionTitle(pdf, "Traffic Sources")
	if len(sources) == 0 {
		writeNoData(pdf)
		return
	}

	pdf.SetFont("Helvetica", "B", 9)
	pdf.CellFormat(45, 7, "Source", "1", 0, "C", false, 0, "")
	pdf.CellFormat(45, 7, "Views", "1", 0, "C", false, 0, "")
	pdf.Ln(-1)

	pdf.SetFont("Helvetica", "", 9)
	limit := len(sources)
	if limit > 10 {
		limit = 10
	}
	for _, s := range sources[:limit] {
		pdf.CellFormat(45, 6, s.Source, "1", 0, "L", false, 0, "")
		pdf.CellFormat(45, 6, strconv.Itoa(s.Views), "1", 0, "R", false, 0, "")
		pdf.Ln(-1)
	}
}

func writeFooter(pdf *fpdf.Fpdf) {
	pdf.Ln(10)
	pdf.SetFont("Helvetica", "I", 8)
	footer := fmt.Sprintf("Generated by Vidra Core on %s", time.Now().UTC().Format(time.RFC3339))
	pdf.CellFormat(0, 5, footer, "", 1, "C", false, 0, "")
}

func pdfToBytes(pdf *fpdf.Fpdf) ([]byte, error) {
	if err := pdf.Error(); err != nil {
		return nil, fmt.Errorf("PDF generation error: %w", err)
	}

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, fmt.Errorf("writing PDF output: %w", err)
	}

	return buf.Bytes(), nil
}
