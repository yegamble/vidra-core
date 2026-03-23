package obs

import (
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestNewMetrics(t *testing.T) {
	metrics := NewMetrics()

	if metrics == nil {
		t.Fatal("NewMetrics returned nil")
	}

	// Verify all metric collectors are initialized
	if metrics.HTTPRequestsTotal == nil {
		t.Error("HTTPRequestsTotal not initialized")
	}
	if metrics.HTTPRequestDuration == nil {
		t.Error("HTTPRequestDuration not initialized")
	}
	if metrics.HTTPRequestSize == nil {
		t.Error("HTTPRequestSize not initialized")
	}
	if metrics.HTTPResponseSize == nil {
		t.Error("HTTPResponseSize not initialized")
	}
	if metrics.DBConnections == nil {
		t.Error("DBConnections not initialized")
	}
	if metrics.DBQueryDuration == nil {
		t.Error("DBQueryDuration not initialized")
	}
	if metrics.DBQueryErrors == nil {
		t.Error("DBQueryErrors not initialized")
	}
	if metrics.IPFSPinDuration == nil {
		t.Error("IPFSPinDuration not initialized")
	}
	if metrics.IPFSGatewayDuration == nil {
		t.Error("IPFSGatewayDuration not initialized")
	}
	if metrics.IPFSErrors == nil {
		t.Error("IPFSErrors not initialized")
	}
	if metrics.IPFSPinnedSize == nil {
		t.Error("IPFSPinnedSize not initialized")
	}
	if metrics.IOTAPaymentIntents == nil {
		t.Error("IOTAPaymentIntents not initialized")
	}
	if metrics.IOTAConfirmationDuration == nil {
		t.Error("IOTAConfirmationDuration not initialized")
	}
	if metrics.IOTAWallets == nil {
		t.Error("IOTAWallets not initialized")
	}
	if metrics.IOTAErrors == nil {
		t.Error("IOTAErrors not initialized")
	}
	if metrics.VirusScanDuration == nil {
		t.Error("VirusScanDuration not initialized")
	}
	if metrics.MalwareDetections == nil {
		t.Error("MalwareDetections not initialized")
	}
	if metrics.VirusScanErrors == nil {
		t.Error("VirusScanErrors not initialized")
	}
	if metrics.VideoEncodingDuration == nil {
		t.Error("VideoEncodingDuration not initialized")
	}
	if metrics.VideoEncodingQueue == nil {
		t.Error("VideoEncodingQueue not initialized")
	}
	if metrics.VideoProcessingErrors == nil {
		t.Error("VideoProcessingErrors not initialized")
	}
}

func TestHTTPRequestsTotal(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMetrics()
	registry.MustRegister(metrics.HTTPRequestsTotal)

	tests := []struct {
		method string
		path   string
		status string
		count  int
	}{
		{"GET", "/api/v1/videos", "200", 2},
		{"POST", "/api/v1/videos", "201", 1},
		{"GET", "/api/v1/videos", "404", 1},
		{"DELETE", "/api/v1/videos/123", "204", 1},
	}

	for _, tt := range tests {
		for i := 0; i < tt.count; i++ {
			metrics.HTTPRequestsTotal.WithLabelValues(tt.method, tt.path, tt.status).Inc()
		}
	}

	// Verify metrics
	expectedMetrics := `
		# HELP http_requests_total Total number of HTTP requests
		# TYPE http_requests_total counter
		http_requests_total{method="DELETE",path="/api/v1/videos/123",status="204"} 1
		http_requests_total{method="GET",path="/api/v1/videos",status="200"} 2
		http_requests_total{method="GET",path="/api/v1/videos",status="404"} 1
		http_requests_total{method="POST",path="/api/v1/videos",status="201"} 1
	`

	if err := testutil.CollectAndCompare(metrics.HTTPRequestsTotal, strings.NewReader(expectedMetrics)); err != nil {
		t.Errorf("unexpected metric output: %v", err)
	}
}

func TestHTTPRequestDuration(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMetrics()
	registry.MustRegister(metrics.HTTPRequestDuration)

	// Record various request durations
	metrics.HTTPRequestDuration.WithLabelValues("GET", "/api/v1/videos").Observe(0.050) // 50ms
	metrics.HTTPRequestDuration.WithLabelValues("GET", "/api/v1/videos").Observe(0.100) // 100ms
	metrics.HTTPRequestDuration.WithLabelValues("GET", "/api/v1/videos").Observe(0.200) // 200ms
	metrics.HTTPRequestDuration.WithLabelValues("POST", "/api/v1/upload").Observe(2.5)  // 2.5s

	// Verify histogram buckets
	count := testutil.CollectAndCount(metrics.HTTPRequestDuration)
	if count == 0 {
		t.Error("HTTPRequestDuration has no metrics")
	}
}

func TestHTTPRequestSize(t *testing.T) {
	metrics := NewMetrics()

	// Record various request sizes
	metrics.HTTPRequestSize.WithLabelValues("POST", "/api/v1/upload").Observe(1024)     // 1KB
	metrics.HTTPRequestSize.WithLabelValues("POST", "/api/v1/upload").Observe(1048576)  // 1MB
	metrics.HTTPRequestSize.WithLabelValues("POST", "/api/v1/upload").Observe(10485760) // 10MB

	count := testutil.CollectAndCount(metrics.HTTPRequestSize)
	if count == 0 {
		t.Error("HTTPRequestSize has no metrics")
	}
}

func TestHTTPResponseSize(t *testing.T) {
	metrics := NewMetrics()

	// Record various response sizes
	metrics.HTTPResponseSize.WithLabelValues("GET", "/api/v1/videos").Observe(512)   // 512 bytes
	metrics.HTTPResponseSize.WithLabelValues("GET", "/api/v1/videos").Observe(2048)  // 2KB
	metrics.HTTPResponseSize.WithLabelValues("GET", "/api/v1/videos").Observe(10240) // 10KB

	count := testutil.CollectAndCount(metrics.HTTPResponseSize)
	if count == 0 {
		t.Error("HTTPResponseSize has no metrics")
	}
}

func TestDBConnections(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMetrics()
	registry.MustRegister(metrics.DBConnections)

	// Set connection pool stats
	metrics.DBConnections.WithLabelValues("open").Set(25)
	metrics.DBConnections.WithLabelValues("idle").Set(5)
	metrics.DBConnections.WithLabelValues("in_use").Set(20)

	expectedMetrics := `
		# HELP db_connections Number of database connections by state
		# TYPE db_connections gauge
		db_connections{state="idle"} 5
		db_connections{state="in_use"} 20
		db_connections{state="open"} 25
	`

	if err := testutil.CollectAndCompare(metrics.DBConnections, strings.NewReader(expectedMetrics)); err != nil {
		t.Errorf("unexpected metric output: %v", err)
	}
}

func TestDBQueryDuration(t *testing.T) {
	metrics := NewMetrics()

	// Record query durations
	metrics.DBQueryDuration.WithLabelValues("SELECT", "videos").Observe(0.005) // 5ms
	metrics.DBQueryDuration.WithLabelValues("INSERT", "videos").Observe(0.010) // 10ms
	metrics.DBQueryDuration.WithLabelValues("UPDATE", "videos").Observe(0.015) // 15ms
	metrics.DBQueryDuration.WithLabelValues("DELETE", "videos").Observe(0.020) // 20ms

	count := testutil.CollectAndCount(metrics.DBQueryDuration)
	if count == 0 {
		t.Error("DBQueryDuration has no metrics")
	}
}

func TestDBQueryErrors(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMetrics()
	registry.MustRegister(metrics.DBQueryErrors)

	// Record query errors
	metrics.DBQueryErrors.WithLabelValues("connection_failed").Inc()
	metrics.DBQueryErrors.WithLabelValues("timeout").Inc()
	metrics.DBQueryErrors.WithLabelValues("timeout").Inc()
	metrics.DBQueryErrors.WithLabelValues("constraint_violation").Inc()

	expectedMetrics := `
		# HELP db_query_errors_total Total number of database query errors
		# TYPE db_query_errors_total counter
		db_query_errors_total{error_type="connection_failed"} 1
		db_query_errors_total{error_type="constraint_violation"} 1
		db_query_errors_total{error_type="timeout"} 2
	`

	if err := testutil.CollectAndCompare(metrics.DBQueryErrors, strings.NewReader(expectedMetrics)); err != nil {
		t.Errorf("unexpected metric output: %v", err)
	}
}

func TestIPFSPinDuration(t *testing.T) {
	metrics := NewMetrics()

	// Record pin operation durations
	metrics.IPFSPinDuration.WithLabelValues("add").Observe(5.5)    // 5.5s
	metrics.IPFSPinDuration.WithLabelValues("add").Observe(3.2)    // 3.2s
	metrics.IPFSPinDuration.WithLabelValues("remove").Observe(0.5) // 0.5s

	count := testutil.CollectAndCount(metrics.IPFSPinDuration)
	if count == 0 {
		t.Error("IPFSPinDuration has no metrics")
	}
}

func TestIPFSGatewayDuration(t *testing.T) {
	metrics := NewMetrics()

	// Record gateway request durations
	metrics.IPFSGatewayDuration.WithLabelValues("gateway1.example.com").Observe(0.100)
	metrics.IPFSGatewayDuration.WithLabelValues("gateway2.example.com").Observe(0.150)
	metrics.IPFSGatewayDuration.WithLabelValues("gateway1.example.com").Observe(0.120)

	count := testutil.CollectAndCount(metrics.IPFSGatewayDuration)
	if count == 0 {
		t.Error("IPFSGatewayDuration has no metrics")
	}
}

func TestIPFSErrors(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMetrics()
	registry.MustRegister(metrics.IPFSErrors)

	// Record IPFS errors
	metrics.IPFSErrors.WithLabelValues("pin_failed").Inc()
	metrics.IPFSErrors.WithLabelValues("timeout").Inc()
	metrics.IPFSErrors.WithLabelValues("timeout").Inc()
	metrics.IPFSErrors.WithLabelValues("not_found").Inc()

	expectedMetrics := `
		# HELP ipfs_errors_total Total number of IPFS errors
		# TYPE ipfs_errors_total counter
		ipfs_errors_total{error_type="not_found"} 1
		ipfs_errors_total{error_type="pin_failed"} 1
		ipfs_errors_total{error_type="timeout"} 2
	`

	if err := testutil.CollectAndCompare(metrics.IPFSErrors, strings.NewReader(expectedMetrics)); err != nil {
		t.Errorf("unexpected metric output: %v", err)
	}
}

func TestIPFSPinnedSize(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMetrics()
	registry.MustRegister(metrics.IPFSPinnedSize)

	// Set pinned content size (in bytes)
	metrics.IPFSPinnedSize.Set(1073741824) // 1GB

	expectedMetrics := `
		# HELP ipfs_pinned_size_bytes Total size of pinned content in bytes
		# TYPE ipfs_pinned_size_bytes gauge
		ipfs_pinned_size_bytes 1.073741824e+09
	`

	if err := testutil.CollectAndCompare(metrics.IPFSPinnedSize, strings.NewReader(expectedMetrics)); err != nil {
		t.Errorf("unexpected metric output: %v", err)
	}
}

func TestIOTAPaymentIntents(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMetrics()
	registry.MustRegister(metrics.IOTAPaymentIntents)

	// Record payment intent creations
	metrics.IOTAPaymentIntents.WithLabelValues("created").Inc()
	metrics.IOTAPaymentIntents.WithLabelValues("created").Inc()
	metrics.IOTAPaymentIntents.WithLabelValues("confirmed").Inc()
	metrics.IOTAPaymentIntents.WithLabelValues("failed").Inc()

	expectedMetrics := `
		# HELP iota_payment_intents_total Total number of IOTA payment intents
		# TYPE iota_payment_intents_total counter
		iota_payment_intents_total{status="confirmed"} 1
		iota_payment_intents_total{status="created"} 2
		iota_payment_intents_total{status="failed"} 1
	`

	if err := testutil.CollectAndCompare(metrics.IOTAPaymentIntents, strings.NewReader(expectedMetrics)); err != nil {
		t.Errorf("unexpected metric output: %v", err)
	}
}

func TestIOTAConfirmationDuration(t *testing.T) {
	metrics := NewMetrics()

	// Record payment confirmation durations
	metrics.IOTAConfirmationDuration.Observe(30.5) // 30.5 seconds
	metrics.IOTAConfirmationDuration.Observe(45.2) // 45.2 seconds
	metrics.IOTAConfirmationDuration.Observe(60.0) // 60 seconds

	count := testutil.CollectAndCount(metrics.IOTAConfirmationDuration)
	if count == 0 {
		t.Error("IOTAConfirmationDuration has no metrics")
	}
}

func TestIOTAWallets(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMetrics()
	registry.MustRegister(metrics.IOTAWallets)

	// Record wallet creations
	metrics.IOTAWallets.Inc()
	metrics.IOTAWallets.Inc()
	metrics.IOTAWallets.Inc()

	expectedMetrics := `
		# HELP iota_wallets_total Total number of IOTA wallets created
		# TYPE iota_wallets_total counter
		iota_wallets_total 3
	`

	if err := testutil.CollectAndCompare(metrics.IOTAWallets, strings.NewReader(expectedMetrics)); err != nil {
		t.Errorf("unexpected metric output: %v", err)
	}
}

func TestIOTAErrors(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMetrics()
	registry.MustRegister(metrics.IOTAErrors)

	// Record IOTA errors
	metrics.IOTAErrors.WithLabelValues("network_error").Inc()
	metrics.IOTAErrors.WithLabelValues("insufficient_funds").Inc()
	metrics.IOTAErrors.WithLabelValues("network_error").Inc()

	expectedMetrics := `
		# HELP iota_errors_total Total number of IOTA errors
		# TYPE iota_errors_total counter
		iota_errors_total{error_type="insufficient_funds"} 1
		iota_errors_total{error_type="network_error"} 2
	`

	if err := testutil.CollectAndCompare(metrics.IOTAErrors, strings.NewReader(expectedMetrics)); err != nil {
		t.Errorf("unexpected metric output: %v", err)
	}
}

func TestVirusScanDuration(t *testing.T) {
	metrics := NewMetrics()

	// Record scan durations
	metrics.VirusScanDuration.WithLabelValues("clean").Observe(0.500)    // 500ms
	metrics.VirusScanDuration.WithLabelValues("clean").Observe(0.750)    // 750ms
	metrics.VirusScanDuration.WithLabelValues("infected").Observe(1.200) // 1.2s

	count := testutil.CollectAndCount(metrics.VirusScanDuration)
	if count == 0 {
		t.Error("VirusScanDuration has no metrics")
	}
}

func TestMalwareDetections(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMetrics()
	registry.MustRegister(metrics.MalwareDetections)

	// Record malware detections
	metrics.MalwareDetections.WithLabelValues("trojan").Inc()
	metrics.MalwareDetections.WithLabelValues("virus").Inc()
	metrics.MalwareDetections.WithLabelValues("virus").Inc()

	expectedMetrics := `
		# HELP malware_detections_total Total number of malware detections
		# TYPE malware_detections_total counter
		malware_detections_total{type="trojan"} 1
		malware_detections_total{type="virus"} 2
	`

	if err := testutil.CollectAndCompare(metrics.MalwareDetections, strings.NewReader(expectedMetrics)); err != nil {
		t.Errorf("unexpected metric output: %v", err)
	}
}

func TestVirusScanErrors(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMetrics()
	registry.MustRegister(metrics.VirusScanErrors)

	// Record scan errors
	metrics.VirusScanErrors.WithLabelValues("scanner_unavailable").Inc()
	metrics.VirusScanErrors.WithLabelValues("timeout").Inc()
	metrics.VirusScanErrors.WithLabelValues("timeout").Inc()

	expectedMetrics := `
		# HELP virus_scan_errors_total Total number of virus scan errors
		# TYPE virus_scan_errors_total counter
		virus_scan_errors_total{error_type="scanner_unavailable"} 1
		virus_scan_errors_total{error_type="timeout"} 2
	`

	if err := testutil.CollectAndCompare(metrics.VirusScanErrors, strings.NewReader(expectedMetrics)); err != nil {
		t.Errorf("unexpected metric output: %v", err)
	}
}

func TestVideoEncodingDuration(t *testing.T) {
	metrics := NewMetrics()

	// Record encoding durations by resolution
	metrics.VideoEncodingDuration.WithLabelValues("360p").Observe(10.5)
	metrics.VideoEncodingDuration.WithLabelValues("720p").Observe(25.3)
	metrics.VideoEncodingDuration.WithLabelValues("1080p").Observe(45.7)
	metrics.VideoEncodingDuration.WithLabelValues("1080p").Observe(50.2)

	count := testutil.CollectAndCount(metrics.VideoEncodingDuration)
	if count == 0 {
		t.Error("VideoEncodingDuration has no metrics")
	}
}

func TestVideoEncodingQueue(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMetrics()
	registry.MustRegister(metrics.VideoEncodingQueue)

	// Set queue depth
	metrics.VideoEncodingQueue.Set(15)

	expectedMetrics := `
		# HELP video_encoding_queue_depth Current number of videos in encoding queue
		# TYPE video_encoding_queue_depth gauge
		video_encoding_queue_depth 15
	`

	if err := testutil.CollectAndCompare(metrics.VideoEncodingQueue, strings.NewReader(expectedMetrics)); err != nil {
		t.Errorf("unexpected metric output: %v", err)
	}
}

func TestVideoProcessingErrors(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMetrics()
	registry.MustRegister(metrics.VideoProcessingErrors)

	// Record processing errors
	metrics.VideoProcessingErrors.WithLabelValues("encoding_failed").Inc()
	metrics.VideoProcessingErrors.WithLabelValues("invalid_format").Inc()
	metrics.VideoProcessingErrors.WithLabelValues("encoding_failed").Inc()

	expectedMetrics := `
		# HELP video_processing_errors_total Total number of video processing errors
		# TYPE video_processing_errors_total counter
		video_processing_errors_total{error_type="encoding_failed"} 2
		video_processing_errors_total{error_type="invalid_format"} 1
	`

	if err := testutil.CollectAndCompare(metrics.VideoProcessingErrors, strings.NewReader(expectedMetrics)); err != nil {
		t.Errorf("unexpected metric output: %v", err)
	}
}

func TestMetricsRegistration(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMetrics()

	// Register all metrics
	err := RegisterMetrics(registry, metrics)
	if err != nil {
		t.Fatalf("failed to register metrics: %v", err)
	}

	// Record at least one value for each metric to ensure they show up in Gather()
	// Prometheus doesn't include Vec metrics in Gather() until they have values
	metrics.HTTPRequestsTotal.WithLabelValues("GET", "/test", "200").Add(0)
	metrics.HTTPRequestDuration.WithLabelValues("GET", "/test").Observe(0)
	metrics.HTTPRequestSize.WithLabelValues("GET", "/test").Observe(0)
	metrics.HTTPResponseSize.WithLabelValues("GET", "/test").Observe(0)
	metrics.DBConnections.WithLabelValues("open").Add(0)
	metrics.DBQueryDuration.WithLabelValues("SELECT", "test").Observe(0)
	metrics.DBQueryErrors.WithLabelValues("test").Add(0)
	metrics.IPFSPinDuration.WithLabelValues("add").Observe(0)
	metrics.IPFSGatewayDuration.WithLabelValues("test").Observe(0)
	metrics.IPFSErrors.WithLabelValues("test").Add(0)
	metrics.IOTAPaymentIntents.WithLabelValues("created").Add(0)
	metrics.IOTAErrors.WithLabelValues("test").Add(0)
	metrics.VirusScanDuration.WithLabelValues("clean").Observe(0)
	metrics.MalwareDetections.WithLabelValues("test").Add(0)
	metrics.VirusScanErrors.WithLabelValues("test").Add(0)
	metrics.VideoEncodingDuration.WithLabelValues("720p").Observe(0)
	metrics.VideoProcessingErrors.WithLabelValues("test").Add(0)

	// Verify metrics are registered
	metricFamilies, err := registry.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	expectedMetrics := []string{
		"http_requests_total",
		"http_request_duration_seconds",
		"http_request_size_bytes",
		"http_response_size_bytes",
		"db_connections",
		"db_query_duration_seconds",
		"db_query_errors_total",
		"ipfs_pin_duration_seconds",
		"ipfs_gateway_duration_seconds",
		"ipfs_errors_total",
		"ipfs_pinned_size_bytes",
		"iota_payment_intents_total",
		"iota_payment_confirmation_duration_seconds",
		"iota_wallets_total",
		"iota_errors_total",
		"virus_scan_duration_seconds",
		"malware_detections_total",
		"virus_scan_errors_total",
		"video_encoding_duration_seconds",
		"video_encoding_queue_depth",
		"video_processing_errors_total",
	}

	foundMetrics := make(map[string]bool)
	for _, mf := range metricFamilies {
		foundMetrics[mf.GetName()] = true
	}

	for _, expected := range expectedMetrics {
		if !foundMetrics[expected] {
			t.Errorf("expected metric %s not registered", expected)
		}
	}
}

func TestRecordHTTPMetrics(t *testing.T) {
	metrics := NewMetrics()

	// Record HTTP metrics helper
	RecordHTTPMetrics(metrics, "GET", "/api/v1/videos", 200, 150*time.Millisecond, 1024, 2048)

	// Verify counter incremented
	count := testutil.CollectAndCount(metrics.HTTPRequestsTotal)
	if count == 0 {
		t.Error("HTTPRequestsTotal not recorded")
	}

	// Verify histogram recorded
	count = testutil.CollectAndCount(metrics.HTTPRequestDuration)
	if count == 0 {
		t.Error("HTTPRequestDuration not recorded")
	}
}

func BenchmarkMetricsRecording(b *testing.B) {
	metrics := NewMetrics()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		metrics.HTTPRequestsTotal.WithLabelValues("GET", "/api/v1/videos", "200").Inc()
		metrics.HTTPRequestDuration.WithLabelValues("GET", "/api/v1/videos").Observe(0.1)
	}
}

func BenchmarkMetricsWithLabels(b *testing.B) {
	metrics := NewMetrics()

	methods := []string{"GET", "POST", "PUT", "DELETE"}
	paths := []string{"/api/v1/videos", "/api/v1/upload", "/api/v1/users"}
	statuses := []string{"200", "201", "400", "404", "500"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		method := methods[i%len(methods)]
		path := paths[i%len(paths)]
		status := statuses[i%len(statuses)]

		metrics.HTTPRequestsTotal.WithLabelValues(method, path, status).Inc()
		metrics.HTTPRequestDuration.WithLabelValues(method, path).Observe(0.1)
	}
}
