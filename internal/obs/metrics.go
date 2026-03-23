package obs

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics holds Prometheus collectors used across the app.
type Metrics struct {
	// HTTP
	HTTPRequestsTotal   *prometheus.CounterVec
	HTTPRequestDuration *prometheus.HistogramVec
	HTTPRequestSize     *prometheus.HistogramVec
	HTTPResponseSize    *prometheus.HistogramVec

	// DB
	DBConnections   *prometheus.GaugeVec
	DBQueryDuration *prometheus.HistogramVec
	DBQueryErrors   *prometheus.CounterVec

	// IPFS
	IPFSPinDuration     *prometheus.HistogramVec
	IPFSGatewayDuration *prometheus.HistogramVec
	IPFSErrors          *prometheus.CounterVec
	IPFSPinnedSize      prometheus.Gauge

	// IOTA
	IOTAPaymentIntents       *prometheus.CounterVec
	IOTAConfirmationDuration prometheus.Histogram
	IOTAWallets              prometheus.Counter
	IOTAErrors               *prometheus.CounterVec

	// Security / processing
	VirusScanDuration     *prometheus.HistogramVec
	MalwareDetections     *prometheus.CounterVec
	VirusScanErrors       *prometheus.CounterVec
	VideoEncodingDuration *prometheus.HistogramVec
	VideoEncodingQueue    prometheus.Gauge
	VideoProcessingErrors *prometheus.CounterVec
}

// NewMetrics initializes metric collectors with reasonable defaults.
func NewMetrics() *Metrics {
	m := &Metrics{
		HTTPRequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: "http_requests_total", Help: "Total number of HTTP requests"},
			[]string{"method", "path", "status"},
		),
		HTTPRequestDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{Name: "http_request_duration_seconds", Help: "HTTP request duration in seconds", Buckets: prometheus.DefBuckets},
			[]string{"method", "path"},
		),
		HTTPRequestSize: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{Name: "http_request_size_bytes", Help: "HTTP request size in bytes", Buckets: prometheus.ExponentialBuckets(100, 10, 8)},
			[]string{"method", "path"},
		),
		HTTPResponseSize: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{Name: "http_response_size_bytes", Help: "HTTP response size in bytes", Buckets: prometheus.ExponentialBuckets(100, 10, 8)},
			[]string{"method", "path"},
		),

		DBConnections: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{Name: "db_connections", Help: "Number of database connections by state"},
			[]string{"state"},
		),
		DBQueryDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{Name: "db_query_duration_seconds", Help: "Database query duration in seconds", Buckets: prometheus.DefBuckets},
			[]string{"operation", "table"},
		),
		DBQueryErrors: prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: "db_query_errors_total", Help: "Total number of database query errors"},
			[]string{"error_type"},
		),

		IPFSPinDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{Name: "ipfs_pin_duration_seconds", Help: "IPFS pin operation duration", Buckets: prometheus.DefBuckets},
			[]string{"operation"},
		),
		IPFSGatewayDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{Name: "ipfs_gateway_duration_seconds", Help: "IPFS gateway request duration", Buckets: prometheus.DefBuckets},
			[]string{"gateway"},
		),
		IPFSErrors: prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: "ipfs_errors_total", Help: "Total number of IPFS errors"},
			[]string{"error_type"},
		),
		IPFSPinnedSize: prometheus.NewGauge(prometheus.GaugeOpts{Name: "ipfs_pinned_size_bytes", Help: "Total size of pinned content in bytes"}),

		IOTAPaymentIntents: prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: "iota_payment_intents_total", Help: "Total number of IOTA payment intents"},
			[]string{"status"},
		),
		IOTAConfirmationDuration: prometheus.NewHistogram(prometheus.HistogramOpts{Name: "iota_payment_confirmation_duration_seconds", Help: "IOTA payment confirmation duration", Buckets: prometheus.DefBuckets}),
		IOTAWallets:              prometheus.NewCounter(prometheus.CounterOpts{Name: "iota_wallets_total", Help: "Total number of IOTA wallets created"}),
		IOTAErrors: prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: "iota_errors_total", Help: "Total number of IOTA errors"},
			[]string{"error_type"},
		),

		VirusScanDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{Name: "virus_scan_duration_seconds", Help: "Virus scan duration", Buckets: prometheus.DefBuckets},
			[]string{"result"},
		),
		MalwareDetections: prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: "malware_detections_total", Help: "Total number of malware detections"},
			[]string{"type"},
		),
		VirusScanErrors: prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: "virus_scan_errors_total", Help: "Total number of virus scan errors"},
			[]string{"error_type"},
		),
		VideoEncodingDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{Name: "video_encoding_duration_seconds", Help: "Video encoding duration", Buckets: prometheus.DefBuckets},
			[]string{"resolution"},
		),
		VideoEncodingQueue: prometheus.NewGauge(prometheus.GaugeOpts{Name: "video_encoding_queue_depth", Help: "Current number of videos in encoding queue"}),
		VideoProcessingErrors: prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: "video_processing_errors_total", Help: "Total number of video processing errors"},
			[]string{"error_type"},
		),
	}
	return m
}

// RegisterMetrics registers all collectors in a custom registry.
func RegisterMetrics(reg *prometheus.Registry, m *Metrics) error {
	collectors := []prometheus.Collector{
		m.HTTPRequestsTotal, m.HTTPRequestDuration, m.HTTPRequestSize, m.HTTPResponseSize,
		m.DBConnections, m.DBQueryDuration, m.DBQueryErrors,
		m.IPFSPinDuration, m.IPFSGatewayDuration, m.IPFSErrors, m.IPFSPinnedSize,
		m.IOTAPaymentIntents, m.IOTAConfirmationDuration, m.IOTAWallets, m.IOTAErrors,
		m.VirusScanDuration, m.MalwareDetections, m.VirusScanErrors,
		m.VideoEncodingDuration, m.VideoEncodingQueue, m.VideoProcessingErrors,
	}
	for _, c := range collectors {
		if err := reg.Register(c); err != nil {
			return err
		}
	}
	return nil
}

// RecordHTTPMetrics is a helper to record common HTTP metrics.
func RecordHTTPMetrics(m *Metrics, method, path string, status int, duration time.Duration, reqSizeBytes int64, respSizeBytes int64) {
	if m == nil {
		return
	}
	if m.HTTPRequestsTotal != nil {
		m.HTTPRequestsTotal.WithLabelValues(method, path, itoa(status)).Inc()
	}
	if m.HTTPRequestDuration != nil {
		m.HTTPRequestDuration.WithLabelValues(method, path).Observe(duration.Seconds())
	}
	if reqSizeBytes > 0 && m.HTTPRequestSize != nil {
		m.HTTPRequestSize.WithLabelValues(method, path).Observe(float64(reqSizeBytes))
	}
	if respSizeBytes > 0 && m.HTTPResponseSize != nil {
		m.HTTPResponseSize.WithLabelValues(method, path).Observe(float64(respSizeBytes))
	}
}

func itoa(i int) string {
	// tiny helper avoiding fmt import
	if i == 0 {
		return "0"
	}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	var b [12]byte
	bp := len(b)
	for i > 0 {
		bp--
		b[bp] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		bp--
		b[bp] = '-'
	}
	return string(b[bp:])
}
