# Sprint 8: Monitoring & Observability

**Status**: 🚧 Planned
**Start Date**: TBD
**Target Duration**: 3-4 days
**Dependencies**: Sprint 5 (RTMP) ✅, Sprint 6 (HLS/VOD) ✅, Sprint 7 (Chat/Scheduling) 🚧

## Overview

Sprint 8 implements production-grade monitoring and observability for the Athena platform. This sprint focuses on metrics collection, visualization, alerting, and operational insights to ensure system reliability and performance.

## Goals

1. **Prometheus Metrics** - Instrument all critical services with metrics
2. **Grafana Dashboards** - Visualize system health and performance
3. **Alerting** - Proactive notification of issues
4. **Structured Logging** - Enhanced logging with correlation IDs
5. **Distributed Tracing** - Request flow visibility (optional)

## Architecture Overview

```
┌─────────────────────────────────────────┐
│         Application Services            │
│  - HTTP API                             │
│  - RTMP Server                          │
│  - HLS Transcoder                       │
│  - VOD Converter                        │
│  - Chat Server                          │
│  - Background Workers                   │
└──────┬──────────────────────────────────┘
       │
       ├─────► Prometheus Exporter
       │       ├─ /metrics endpoint
       │       ├─ Business metrics
       │       ├─ System metrics
       │       └─ Custom metrics
       │
       ├─────► Structured Logger
       │       ├─ JSON format
       │       ├─ Correlation IDs
       │       ├─ Error tracking
       │       └─ Performance logs
       │
       └─────► Health Checks
               ├─ /health (liveness)
               ├─ /ready (readiness)
               └─ Component checks

┌─────────────────────────────────────────┐
│         Prometheus Server               │
│  - Scrapes /metrics every 15s           │
│  - Stores time-series data              │
│  - Evaluates alert rules                │
└──────┬──────────────────────────────────┘
       │
       ├─────► Grafana
       │       ├─ Dashboards
       │       ├─ Panels & graphs
       │       └─ Annotations
       │
       └─────► Alertmanager
               ├─ Alert routing
               ├─ Email notifications
               ├─ Slack integration
               └─ PagerDuty integration
```

## Phase 1: Prometheus Metrics (Days 1-2)

### 1.1 Metrics Package

**File**: `internal/metrics/prometheus.go` (~300 lines)

```go
package metrics

import (
    "github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/promauto"
)

var (
    // HTTP Metrics
    HTTPRequestsTotal = promauto.NewCounterVec(
        prometheus.CounterOpts{
            Name: "athena_http_requests_total",
            Help: "Total number of HTTP requests",
        },
        []string{"method", "path", "status"},
    )

    HTTPRequestDuration = promauto.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "athena_http_request_duration_seconds",
            Help:    "HTTP request latency in seconds",
            Buckets: prometheus.DefBuckets,
        },
        []string{"method", "path"},
    )

    // RTMP Metrics
    RTMPActiveConnections = promauto.NewGauge(
        prometheus.GaugeOpts{
            Name: "athena_rtmp_active_connections",
            Help: "Number of active RTMP connections",
        },
    )

    RTMPConnectionsTotal = promauto.NewCounterVec(
        prometheus.CounterOpts{
            Name: "athena_rtmp_connections_total",
            Help: "Total number of RTMP connections",
        },
        []string{"status"}, // accepted, rejected
    )

    RTMPBytesReceived = promauto.NewCounter(
        prometheus.CounterOpts{
            Name: "athena_rtmp_bytes_received_total",
            Help: "Total bytes received via RTMP",
        },
    )

    // HLS Metrics
    HLSActiveTranscodes = promauto.NewGauge(
        prometheus.GaugeOpts{
            Name: "athena_hls_active_transcodes",
            Help: "Number of active HLS transcoding sessions",
        },
    )

    HLSTranscodeErrors = promauto.NewCounterVec(
        prometheus.CounterOpts{
            Name: "athena_hls_transcode_errors_total",
            Help: "Total number of HLS transcoding errors",
        },
        []string{"error_type"},
    )

    HLSSegmentsServed = promauto.NewCounterVec(
        prometheus.CounterOpts{
            Name: "athena_hls_segments_served_total",
            Help: "Total number of HLS segments served",
        },
        []string{"quality"},
    )

    // VOD Metrics
    VODQueueLength = promauto.NewGauge(
        prometheus.GaugeOpts{
            Name: "athena_vod_queue_length",
            Help: "Number of VOD jobs in queue",
        },
    )

    VODActiveJobs = promauto.NewGauge(
        prometheus.GaugeOpts{
            Name: "athena_vod_active_jobs",
            Help: "Number of active VOD conversion jobs",
        },
    )

    VODConversionDuration = promauto.NewHistogram(
        prometheus.HistogramOpts{
            Name:    "athena_vod_conversion_duration_seconds",
            Help:    "VOD conversion duration in seconds",
            Buckets: []float64{10, 30, 60, 120, 300, 600, 1200, 1800, 3600},
        },
    )

    VODConversionsTotal = promauto.NewCounterVec(
        prometheus.CounterOpts{
            Name: "athena_vod_conversions_total",
            Help: "Total number of VOD conversions",
        },
        []string{"status"}, // completed, failed
    )

    // Live Stream Metrics
    LiveStreamsActive = promauto.NewGauge(
        prometheus.GaugeOpts{
            Name: "athena_live_streams_active",
            Help: "Number of active live streams",
        },
    )

    LiveStreamViewers = promauto.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "athena_live_stream_viewers",
            Help: "Number of viewers per live stream",
        },
        []string{"stream_id", "stream_title"},
    )

    LiveStreamDuration = promauto.NewHistogram(
        prometheus.HistogramOpts{
            Name:    "athena_live_stream_duration_seconds",
            Help:    "Live stream duration in seconds",
            Buckets: []float64{300, 600, 1800, 3600, 7200, 10800, 14400, 21600},
        },
    )

    // Chat Metrics
    ChatActiveConnections = promauto.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "athena_chat_active_connections",
            Help: "Number of active chat WebSocket connections",
        },
        []string{"stream_id"},
    )

    ChatMessagesTotal = promauto.NewCounterVec(
        prometheus.CounterOpts{
            Name: "athena_chat_messages_total",
            Help: "Total number of chat messages",
        },
        []string{"stream_id", "type"},
    )

    ChatRateLimitExceeded = promauto.NewCounterVec(
        prometheus.CounterOpts{
            Name: "athena_chat_rate_limit_exceeded_total",
            Help: "Total number of rate limit violations",
        },
        []string{"user_id"},
    )

    // Database Metrics
    DBConnectionsActive = promauto.NewGauge(
        prometheus.GaugeOpts{
            Name: "athena_db_connections_active",
            Help: "Number of active database connections",
        },
    )

    DBConnectionsIdle = promauto.NewGauge(
        prometheus.GaugeOpts{
            Name: "athena_db_connections_idle",
            Help: "Number of idle database connections",
        },
    )

    DBQueryDuration = promauto.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "athena_db_query_duration_seconds",
            Help:    "Database query duration in seconds",
            Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1.0, 2.0},
        },
        []string{"query_type"}, // select, insert, update, delete
    )

    // Redis Metrics
    RedisCommandsTotal = promauto.NewCounterVec(
        prometheus.CounterOpts{
            Name: "athena_redis_commands_total",
            Help: "Total number of Redis commands",
        },
        []string{"command", "status"},
    )

    RedisCommandDuration = promauto.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "athena_redis_command_duration_seconds",
            Help:    "Redis command duration in seconds",
            Buckets: []float64{0.0001, 0.0005, 0.001, 0.005, 0.01, 0.05, 0.1},
        },
        []string{"command"},
    )

    // IPFS Metrics
    IPFSUploadDuration = promauto.NewHistogram(
        prometheus.HistogramOpts{
            Name:    "athena_ipfs_upload_duration_seconds",
            Help:    "IPFS upload duration in seconds",
            Buckets: []float64{1, 5, 10, 30, 60, 120, 300, 600},
        },
    )

    IPFSUploadsTotal = promauto.NewCounterVec(
        prometheus.CounterOpts{
            Name: "athena_ipfs_uploads_total",
            Help: "Total number of IPFS uploads",
        },
        []string{"status"}, // success, failure
    )

    IPFSUploadBytes = promauto.NewCounter(
        prometheus.CounterOpts{
            Name: "athena_ipfs_upload_bytes_total",
            Help: "Total bytes uploaded to IPFS",
        },
    )

    // System Metrics
    SystemCPUUsage = promauto.NewGauge(
        prometheus.GaugeOpts{
            Name: "athena_system_cpu_usage_percent",
            Help: "System CPU usage percentage",
        },
    )

    SystemMemoryUsage = promauto.NewGauge(
        prometheus.GaugeOpts{
            Name: "athena_system_memory_usage_bytes",
            Help: "System memory usage in bytes",
        },
    )

    SystemDiskUsage = promauto.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "athena_system_disk_usage_bytes",
            Help: "System disk usage in bytes",
        },
        []string{"mount_point"},
    )
)
```

### 1.2 Metrics Middleware

**File**: `internal/middleware/metrics.go` (~100 lines)

```go
package middleware

import (
    "net/http"
    "strconv"
    "time"

    "athena/internal/metrics"
)

// Metrics middleware records HTTP metrics
func Metrics(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        start := time.Now()

        // Wrap response writer to capture status code
        rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

        // Handle request
        next.ServeHTTP(rw, r)

        // Record metrics
        duration := time.Since(start).Seconds()
        status := strconv.Itoa(rw.statusCode)

        metrics.HTTPRequestsTotal.WithLabelValues(
            r.Method,
            r.URL.Path,
            status,
        ).Inc()

        metrics.HTTPRequestDuration.WithLabelValues(
            r.Method,
            r.URL.Path,
        ).Observe(duration)
    })
}

type responseWriter struct {
    http.ResponseWriter
    statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
    rw.statusCode = code
    rw.ResponseWriter.WriteHeader(code)
}
```

### 1.3 Metrics Endpoint

**File**: `internal/httpapi/metrics_handlers.go` (~50 lines)

```go
package httpapi

import (
    "net/http"

    "github.com/prometheus/client_golang/prometheus/promhttp"
)

// RegisterMetricsRoutes registers the /metrics endpoint
func RegisterMetricsRoutes(r chi.Router) {
    r.Handle("/metrics", promhttp.Handler())
}
```

### 1.4 Instrumentation

**Files to Modify**:

1. `internal/livestream/rtmp_server.go` - Add RTMP metrics
2. `internal/livestream/hls_transcoder.go` - Add HLS metrics
3. `internal/livestream/vod_converter.go` - Add VOD metrics
4. `internal/chat/websocket_server.go` - Add chat metrics
5. `internal/repository/*.go` - Add database metrics

## Phase 2: Grafana Dashboards (Day 2)

### 2.1 Dashboard Definitions

**File**: `deployments/grafana/dashboards/athena-overview.json`

**Panels**:

1. **System Overview**
   - Total requests/sec (HTTP + RTMP)
   - Active live streams
   - Total viewers across all streams
   - CPU and memory usage

2. **Live Streaming**
   - Active RTMP connections
   - Active HLS transcodes
   - Viewers per stream (top 10)
   - Stream duration distribution

3. **VOD Conversion**
   - Queue length
   - Active jobs
   - Conversion duration (p50, p95, p99)
   - Success/failure rate

4. **Chat**
   - Active WebSocket connections per stream
   - Messages per second
   - Rate limit violations

5. **Performance**
   - HTTP request latency (p50, p95, p99)
   - Database query latency
   - Redis command latency
   - IPFS upload duration

6. **Errors**
   - HTTP errors (4xx, 5xx) rate
   - RTMP connection rejections
   - HLS transcoding errors
   - VOD conversion failures
   - IPFS upload failures

**File**: `deployments/grafana/dashboards/athena-live-streams.json`

**Panels**:

1. **Stream Health**
   - Bitrate over time
   - Dropped frames
   - Audio/video sync issues

2. **Viewer Engagement**
   - Viewer count over time
   - Peak concurrent viewers
   - Average watch time
   - Chat activity

3. **Resource Usage**
   - CPU per transcode
   - Memory per transcode
   - Disk I/O for HLS segments

### 2.2 Grafana Provisioning

**File**: `deployments/grafana/provisioning/datasources/prometheus.yml`

```yaml
apiVersion: 1

datasources:
  - name: Prometheus
    type: prometheus
    access: proxy
    url: http://prometheus:9090
    isDefault: true
    editable: false
```

**File**: `deployments/grafana/provisioning/dashboards/athena.yml`

```yaml
apiVersion: 1

providers:
  - name: 'Athena Dashboards'
    orgId: 1
    folder: 'Athena'
    type: file
    disableDeletion: false
    updateIntervalSeconds: 10
    allowUiUpdates: true
    options:
      path: /etc/grafana/dashboards
```

## Phase 3: Alerting (Day 3)

### 3.1 Prometheus Alert Rules

**File**: `deployments/prometheus/alerts/athena.yml`

```yaml
groups:
  - name: athena_critical
    interval: 30s
    rules:
      # Service Health
      - alert: ServiceDown
        expr: up{job="athena"} == 0
        for: 1m
        labels:
          severity: critical
        annotations:
          summary: "Athena service is down"
          description: "Athena service has been down for more than 1 minute"

      # High Error Rate
      - alert: HighHTTPErrorRate
        expr: |
          sum(rate(athena_http_requests_total{status=~"5.."}[5m]))
          /
          sum(rate(athena_http_requests_total[5m]))
          > 0.05
        for: 2m
        labels:
          severity: critical
        annotations:
          summary: "High HTTP 5xx error rate"
          description: "More than 5% of requests are failing ({{ $value | humanizePercentage }})"

      # Database Issues
      - alert: DatabaseConnectionPoolExhausted
        expr: athena_db_connections_active >= 25
        for: 1m
        labels:
          severity: critical
        annotations:
          summary: "Database connection pool exhausted"
          description: "All database connections are in use ({{ $value }})"

      - alert: SlowDatabaseQueries
        expr: |
          histogram_quantile(0.95,
            sum(rate(athena_db_query_duration_seconds_bucket[5m])) by (le, query_type)
          ) > 1.0
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "Slow database queries detected"
          description: "95th percentile of {{ $labels.query_type }} queries is {{ $value }}s"

  - name: athena_streaming
    interval: 30s
    rules:
      # HLS Transcoding
      - alert: HLSTranscodingFailures
        expr: |
          sum(rate(athena_hls_transcode_errors_total[5m]))
          > 0.1
        for: 2m
        labels:
          severity: warning
        annotations:
          summary: "HLS transcoding failures detected"
          description: "HLS transcoding error rate is {{ $value }} errors/sec"

      - alert: HighTranscodingLoad
        expr: athena_hls_active_transcodes > 8
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "High transcoding load"
          description: "{{ $value }} concurrent transcoding sessions (limit: 10)"

      # VOD Conversion
      - alert: VODQueueBacklog
        expr: athena_vod_queue_length > 50
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "VOD conversion queue backlog"
          description: "{{ $value }} jobs waiting in VOD queue"

      - alert: VODConversionFailures
        expr: |
          sum(rate(athena_vod_conversions_total{status="failed"}[10m]))
          /
          sum(rate(athena_vod_conversions_total[10m]))
          > 0.1
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "High VOD conversion failure rate"
          description: "{{ $value | humanizePercentage }} of VOD conversions are failing"

      - alert: SlowVODConversion
        expr: |
          histogram_quantile(0.95,
            sum(rate(athena_vod_conversion_duration_seconds_bucket[10m])) by (le)
          ) > 1800
        for: 10m
        labels:
          severity: warning
        annotations:
          summary: "Slow VOD conversions"
          description: "95th percentile VOD conversion time is {{ $value }}s (30+ minutes)"

  - name: athena_resources
    interval: 60s
    rules:
      # CPU
      - alert: HighCPUUsage
        expr: athena_system_cpu_usage_percent > 80
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "High CPU usage"
          description: "CPU usage is {{ $value }}%"

      # Memory
      - alert: HighMemoryUsage
        expr: athena_system_memory_usage_bytes / 1024 / 1024 / 1024 > 14
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "High memory usage"
          description: "Memory usage is {{ $value | humanize }}GB (limit: 16GB)"

      # Disk
      - alert: LowDiskSpace
        expr: |
          athena_system_disk_usage_bytes{mount_point="/storage"}
          / 1024 / 1024 / 1024
          > 900
        for: 10m
        labels:
          severity: critical
        annotations:
          summary: "Low disk space on /storage"
          description: "Disk usage is {{ $value | humanize }}GB (limit: 1TB)"

  - name: athena_chat
    interval: 30s
    rules:
      - alert: HighChatRateLimitViolations
        expr: |
          sum(rate(athena_chat_rate_limit_exceeded_total[5m]))
          > 10
        for: 2m
        labels:
          severity: warning
        annotations:
          summary: "High rate of chat rate limit violations"
          description: "{{ $value }} rate limit violations per second"
```

### 3.2 Alertmanager Configuration

**File**: `deployments/prometheus/alertmanager.yml`

```yaml
global:
  resolve_timeout: 5m
  smtp_smarthost: 'smtp.gmail.com:587'
  smtp_from: 'alerts@athena.example.com'
  smtp_auth_username: 'alerts@athena.example.com'
  smtp_auth_password: '${SMTP_PASSWORD}'

route:
  group_by: ['alertname', 'severity']
  group_wait: 30s
  group_interval: 5m
  repeat_interval: 12h
  receiver: 'email-notifications'

  routes:
    - match:
        severity: critical
      receiver: 'pagerduty'
      continue: true

    - match:
        severity: warning
      receiver: 'slack'

receivers:
  - name: 'email-notifications'
    email_configs:
      - to: 'ops-team@athena.example.com'
        headers:
          Subject: '[Athena Alert] {{ .GroupLabels.alertname }}'

  - name: 'slack'
    slack_configs:
      - api_url: '${SLACK_WEBHOOK_URL}'
        channel: '#athena-alerts'
        title: '{{ .GroupLabels.alertname }}'
        text: '{{ range .Alerts }}{{ .Annotations.description }}{{ end }}'

  - name: 'pagerduty'
    pagerduty_configs:
      - service_key: '${PAGERDUTY_SERVICE_KEY}'
```

## Phase 4: Structured Logging (Day 3)

### 4.1 Enhanced Logger

**File**: `internal/obs/logger.go` (~150 lines)

```go
package obs

import (
    "context"
    "os"

    "github.com/google/uuid"
    "github.com/sirupsen/logrus"
)

type contextKey string

const (
    RequestIDKey contextKey = "request_id"
    UserIDKey    contextKey = "user_id"
    StreamIDKey  contextKey = "stream_id"
)

// NewLogger creates a structured logger
func NewLogger(level string) *logrus.Logger {
    logger := logrus.New()

    // JSON format for production
    logger.SetFormatter(&logrus.JSONFormatter{
        TimestampFormat: "2006-01-02T15:04:05.000Z07:00",
        FieldMap: logrus.FieldMap{
            logrus.FieldKeyTime:  "timestamp",
            logrus.FieldKeyLevel: "level",
            logrus.FieldKeyMsg:   "message",
        },
    })

    logger.SetOutput(os.Stdout)

    // Parse log level
    lvl, err := logrus.ParseLevel(level)
    if err != nil {
        lvl = logrus.InfoLevel
    }
    logger.SetLevel(lvl)

    return logger
}

// WithRequestID adds request ID to context
func WithRequestID(ctx context.Context) context.Context {
    return context.WithValue(ctx, RequestIDKey, uuid.New().String())
}

// LoggerFromContext extracts logger with context fields
func LoggerFromContext(ctx context.Context, logger *logrus.Logger) *logrus.Entry {
    entry := logger.WithFields(logrus.Fields{})

    if reqID := ctx.Value(RequestIDKey); reqID != nil {
        entry = entry.WithField("request_id", reqID)
    }
    if userID := ctx.Value(UserIDKey); userID != nil {
        entry = entry.WithField("user_id", userID)
    }
    if streamID := ctx.Value(StreamIDKey); streamID != nil {
        entry = entry.WithField("stream_id", streamID)
    }

    return entry
}
```

### 4.2 Request ID Middleware

**File**: `internal/middleware/request_id.go` (~50 lines)

```go
package middleware

import (
    "context"
    "net/http"

    "athena/internal/obs"
)

// RequestID middleware adds a unique request ID to each request
func RequestID(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        ctx := obs.WithRequestID(r.Context())

        // Add to response header for client tracking
        reqID := ctx.Value(obs.RequestIDKey).(string)
        w.Header().Set("X-Request-ID", reqID)

        next.ServeHTTP(w, r.WithContext(ctx))
    })
}
```

## Phase 5: Docker Compose Integration (Day 4)

### 5.1 Docker Compose for Monitoring

**File**: `docker-compose.monitoring.yml`

```yaml
version: '3.8'

services:
  prometheus:
    image: prom/prometheus:latest
    container_name: athena-prometheus
    ports:
      - "9090:9090"
    volumes:
      - ./deployments/prometheus/prometheus.yml:/etc/prometheus/prometheus.yml
      - ./deployments/prometheus/alerts:/etc/prometheus/alerts
      - prometheus-data:/prometheus
    command:
      - '--config.file=/etc/prometheus/prometheus.yml'
      - '--storage.tsdb.path=/prometheus'
      - '--web.enable-lifecycle'
    networks:
      - athena-monitoring

  grafana:
    image: grafana/grafana:latest
    container_name: athena-grafana
    ports:
      - "3000:3000"
    environment:
      - GF_SECURITY_ADMIN_USER=admin
      - GF_SECURITY_ADMIN_PASSWORD=admin
      - GF_INSTALL_PLUGINS=
    volumes:
      - ./deployments/grafana/provisioning:/etc/grafana/provisioning
      - ./deployments/grafana/dashboards:/etc/grafana/dashboards
      - grafana-data:/var/lib/grafana
    depends_on:
      - prometheus
    networks:
      - athena-monitoring

  alertmanager:
    image: prom/alertmanager:latest
    container_name: athena-alertmanager
    ports:
      - "9093:9093"
    volumes:
      - ./deployments/prometheus/alertmanager.yml:/etc/alertmanager/alertmanager.yml
      - alertmanager-data:/alertmanager
    command:
      - '--config.file=/etc/alertmanager/alertmanager.yml'
      - '--storage.path=/alertmanager'
    networks:
      - athena-monitoring

volumes:
  prometheus-data:
  grafana-data:
  alertmanager-data:

networks:
  athena-monitoring:
    driver: bridge
```

### 5.2 Prometheus Configuration

**File**: `deployments/prometheus/prometheus.yml`

```yaml
global:
  scrape_interval: 15s
  evaluation_interval: 15s
  external_labels:
    cluster: 'athena-production'

alerting:
  alertmanagers:
    - static_configs:
        - targets:
            - alertmanager:9093

rule_files:
  - /etc/prometheus/alerts/*.yml

scrape_configs:
  - job_name: 'athena'
    static_configs:
      - targets: ['host.docker.internal:8080']
    metrics_path: /metrics
    scrape_interval: 15s
    scrape_timeout: 10s

  - job_name: 'prometheus'
    static_configs:
      - targets: ['localhost:9090']

  - job_name: 'postgres'
    static_configs:
      - targets: ['postgres-exporter:9187']

  - job_name: 'redis'
    static_configs:
      - targets: ['redis-exporter:9121']
```

## Configuration

```bash
# Metrics
ENABLE_METRICS=true
METRICS_PORT=8080
METRICS_PATH=/metrics

# Logging
LOG_LEVEL=info
LOG_FORMAT=json
ENABLE_REQUEST_LOGGING=true

# Monitoring
PROMETHEUS_URL=http://localhost:9090
GRAFANA_URL=http://localhost:3000

# Alerting
ALERTMANAGER_URL=http://localhost:9093
SLACK_WEBHOOK_URL=https://hooks.slack.com/services/YOUR/WEBHOOK/URL
PAGERDUTY_SERVICE_KEY=your-pagerduty-key
SMTP_PASSWORD=your-smtp-password
```

## Files Created

### Production Code (~800 lines)

1. `internal/metrics/prometheus.go` (~300 lines)
2. `internal/middleware/metrics.go` (~100 lines)
3. `internal/middleware/request_id.go` (~50 lines)
4. `internal/obs/logger.go` (~150 lines)
5. `internal/httpapi/metrics_handlers.go` (~50 lines)

### Configuration (~600 lines)

6. `deployments/prometheus/prometheus.yml` (~50 lines)
7. `deployments/prometheus/alerts/athena.yml` (~300 lines)
8. `deployments/prometheus/alertmanager.yml` (~50 lines)
9. `deployments/grafana/provisioning/datasources/prometheus.yml` (~20 lines)
10. `deployments/grafana/provisioning/dashboards/athena.yml` (~20 lines)
11. `deployments/grafana/dashboards/athena-overview.json` (~800 lines)
12. `deployments/grafana/dashboards/athena-live-streams.json` (~400 lines)
13. `docker-compose.monitoring.yml` (~80 lines)

### Documentation

14. `SPRINT8_PLAN.md` - This file
15. `SPRINT8_PROGRESS.md` - Progress tracking
16. `SPRINT8_COMPLETE.md` - Completion summary

**Total**: ~2,200 lines (800 code + 1,400 config/dashboards)

## Success Criteria

- ✓ Prometheus scrapes metrics every 15 seconds
- ✓ All critical services instrumented with metrics
- ✓ Grafana dashboards visualize system health
- ✓ Alerts fire correctly for critical conditions
- ✓ Structured logging with correlation IDs
- ✓ Request tracing across services
- ✓ Monitoring stack runs in Docker Compose
- ✓ Documentation complete
- ✓ Runbooks created for common alerts

## Next Steps

After Sprint 8 completion:

- Performance tuning based on metrics
- Capacity planning using historical data
- Advanced alerting rules (anomaly detection)
- Distributed tracing with Jaeger/Tempo
- Log aggregation with Loki/ELK

---

*Athena PeerTube Backend - Video Platform in Go*
