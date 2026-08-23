package observability

import (
	"context"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics holds the Prometheus RED (Rate / Errors / Duration) instruments plus a
// durable-queue depth gauge, all registered on a PRIVATE registry.
//
// Why prometheus/client_golang (and not the OTel metrics SDK): the observability
// spec (§2 Metrics) calls for a "/metrics Prometheus scrape" surface exporting
// bounded-cardinality RED metrics. client_golang is the de-facto-standard,
// Apache-2.0, actively-maintained library; it produces the Prometheus text
// exposition format directly and ships a ready http.Handler (promhttp), so a
// pull/scrape endpoint — the conventional Prometheus ops surface — needs no
// always-on exporter connection, unlike an OTLP metrics push. Tracing keeps
// using the OTel SDK (SetupTracing); the two are complementary. The registry is
// private (NOT the process-global default) so each Metrics is self-contained and
// tests build independent instances without leaking global state.
//
// Cardinality is BOUNDED by construction: the only labels are the HTTP method,
// the Echo ROUTE TEMPLATE (c.Path(), e.g. "/api/v1/videos/:id" — never the raw
// URL, an id, a token, or any user data), and the status class (2xx/3xx/4xx/5xx/
// other). The queue gauge labels are the fixed queue name and the fixed job
// state. No unbounded or sensitive value is ever used as a label.
type Metrics struct {
	registry *prometheus.Registry
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
	// searchEnqueueFailures / searchDeadLetters count best-effort search-outbox
	// drops (enqueue-time) and dead-letters (drain-time) by event type
	// (search-service W4). Labels are the fixed event-type set — bounded.
	searchEnqueueFailures *prometheus.CounterVec
	searchDeadLetters     *prometheus.CounterVec
	// qoeDrops counts playback-telemetry events that could not be stored
	// (phase-4 delivery item 4). The label is the four-member QoE event type
	// and nothing else: QoE itself is an event/rollup stream and never becomes
	// Prometheus labels, so the only thing metered here is the health of the
	// pipeline, not the content of a measurement.
	qoeDrops *prometheus.CounterVec
}

// NewMetrics builds the RED instruments on a fresh private registry, together
// with the standard Go-runtime and process collectors so operators also get
// heap/GC/fd visibility. Call Handler() for the scrape endpoint and ObserveRequest
// from the request choke point.
func NewMetrics() *Metrics {
	reg := prometheus.NewRegistry()
	m := &Metrics{
		registry: reg,
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "vidra_http_requests_total",
			Help: "Total HTTP requests, labelled by method, route template, and status class.",
		}, []string{"method", "route", "status_class"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "vidra_http_request_duration_seconds",
			Help:    "HTTP request duration in seconds, labelled by method, route template, and status class.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method", "route", "status_class"}),
		searchEnqueueFailures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "vidra_search_enqueue_failures_total",
			Help: "Search-outbox events dropped at enqueue time (best-effort), by event type.",
		}, []string{"event_type"}),
		searchDeadLetters: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "vidra_search_dead_letters_total",
			Help: "Search-outbox events dead-lettered after the retry cap, by event type.",
		}, []string{"event_type"}),
		qoeDrops: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "vidra_qoe_events_dropped_total",
			Help: "Playback QoE measurements dropped before storage (best-effort), by event type.",
		}, []string{"event_type"}),
	}
	reg.MustRegister(m.requests, m.duration, m.searchEnqueueFailures, m.searchDeadLetters, m.qoeDrops)
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return m
}

// ObserveRequest records one completed HTTP request. route MUST be the Echo route
// template (c.Path()) so cardinality stays bounded; an empty route (no matching
// route, e.g. a 404) is folded to "unmatched" so a scan of random paths cannot
// blow up the label space.
func (m *Metrics) ObserveRequest(method, route string, status int, d time.Duration) {
	if route == "" {
		route = "unmatched"
	}
	class := statusClass(status)
	m.requests.WithLabelValues(method, route, class).Inc()
	m.duration.WithLabelValues(method, route, class).Observe(d.Seconds())
}

// IncSearchEnqueueFailure counts one dropped search event (enqueue-time).
func (m *Metrics) IncSearchEnqueueFailure(eventType string) {
	m.searchEnqueueFailures.WithLabelValues(eventType).Inc()
}

// IncSearchDeadLetter counts one dead-lettered search event (drain-time).
func (m *Metrics) IncSearchDeadLetter(eventType string) {
	m.searchDeadLetters.WithLabelValues(eventType).Inc()
}

// IncQoEDrop counts one playback measurement that could not be stored.
func (m *Metrics) IncQoEDrop(eventType string) {
	m.qoeDrops.WithLabelValues(eventType).Inc()
}

// QueueDepth is one durable-queue depth sample: how many rows sit in a given
// state for a given queue.
type QueueDepth struct {
	Queue string
	State string // pending | running | done | failed
	Count int64
}

// JobQueueHealth contains DB-derived operational gauges. Queue is a fixed
// application queue identifier; no resource/job/worker ids become labels.
type JobQueueHealth struct {
	Queue                  string
	OldestQueuedAgeSeconds int64
	StaleRunning           int64
}

// RegisterQueueDepthSource installs a gauge (vidra_queue_depth{queue,state}) whose
// values are pulled from source at scrape time. Scrape-time collection keeps the
// gauge fresh with no background goroutine; a source error or timeout is swallowed
// so a transient DB hiccup never fails the whole scrape. Call at most once.
func (m *Metrics) RegisterQueueDepthSource(source func(context.Context) ([]QueueDepth, error)) {
	m.registry.MustRegister(&queueDepthCollector{
		desc: prometheus.NewDesc(
			"vidra_queue_depth",
			"Durable job-queue depth by queue and job state.",
			[]string{"queue", "state"}, nil,
		),
		source: source,
	})
}

// RegisterJobQueueHealthSource exposes queue age and stale-running gauges. They
// are gauges rather than retry/failure counters because the Phase-1 projection
// includes a historical backfill and cannot honestly reconstruct monotonic
// process counters.
func (m *Metrics) RegisterJobQueueHealthSource(source func(context.Context) ([]JobQueueHealth, error)) {
	m.registry.MustRegister(&jobQueueHealthCollector{
		age: prometheus.NewDesc(
			"vidra_job_oldest_queued_age_seconds",
			"Age in seconds of the oldest queued or retry-scheduled job.",
			[]string{"queue"}, nil,
		),
		stale: prometheus.NewDesc(
			"vidra_job_stale_running",
			"Number of claimed/running jobs whose lease expired or update heartbeat is stale.",
			[]string{"queue"}, nil,
		),
		source: source,
	})
}

// RegisterSearchServiceHealthSource installs the vidra_search_service_healthy
// gauge (1 = the background prober's last /healthz probe considers vidra-search
// up, 0 = down), sampled from source at scrape time (search-service W9). Like the
// other gauges it is a scrape-time source rather than a pushed value, so it stays
// fresh with no background goroutine and needs no writer coupling. Call at most
// once, only when the search service is configured.
func (m *Metrics) RegisterSearchServiceHealthSource(source func() float64) {
	m.registry.MustRegister(&searchHealthCollector{
		desc: prometheus.NewDesc(
			"vidra_search_service_healthy",
			"Whether the vidra-search service is currently considered healthy by the active health prober (1) or not (0).",
			nil, nil,
		),
		source: source,
	})
}

// DBPoolStats is one sample of the pgx connection pool, taken at scrape time.
// It mirrors the subset of pgxpool.Stat an operator can act on and deliberately
// nothing else: this package must not import the database driver, so cmd/api
// does the (trivial) translation at the wiring seam.
type DBPoolStats struct {
	// The four sizing numbers. Acquired + Idle + Constructing == Total, and
	// Total can never exceed Max — which is what makes "Acquired is pinned at
	// Max" the readable signal that the pool is the bottleneck.
	TotalConns    int32
	IdleConns     int32
	AcquiredConns int32
	MaxConns      int32
	// The acquire counters. EmptyAcquireCount is the one that matters on a
	// multi-node install: it counts the acquires that had to WAIT because every
	// connection was checked out, and it is what distinguishes "the database is
	// slow" from "this process's pool is too small for its own concurrency".
	// AcquireDuration is the cumulative time spent in those waits, exported as a
	// counter of seconds so a rate() over it reads as wait-seconds per second.
	AcquireCount         int64
	EmptyAcquireCount    int64
	CanceledAcquireCount int64
	AcquireDuration      time.Duration
	// NewConnsCount and MaxLifetimeDestroyCount together show churn: a pool that
	// opens and retires connections continuously is paying a TLS handshake per
	// query burst, which usually means MinConns is too low for the workload.
	NewConnsCount           int64
	MaxLifetimeDestroyCount int64
}

// RegisterDBPoolSource installs the vidra_db_pool_* gauges and counters, pulled
// from source at scrape time like every other gauge here — no background
// goroutine, and nothing to keep in step with the pool's real state.
//
// This is the multi-node instrument. PostgreSQL's max_connections is a
// SERVER-WIDE budget and each process holds a slice of it (DB_MAX_CONNS ×
// api and worker processes), so the question an operator has to answer before
// adding a replica — "how much of my pool am I actually using?" — is not
// answerable from the database side alone: the server sees connections, not
// which of them are idle in somebody's pool. Call at most once.
func (m *Metrics) RegisterDBPoolSource(source func() DBPoolStats) {
	m.registry.MustRegister(&dbPoolCollector{
		total: prometheus.NewDesc("vidra_db_pool_total_conns",
			"Connections currently held by this process's PostgreSQL pool (idle + acquired + constructing).", nil, nil),
		idle: prometheus.NewDesc("vidra_db_pool_idle_conns",
			"Pooled PostgreSQL connections currently idle in this process.", nil, nil),
		acquired: prometheus.NewDesc("vidra_db_pool_acquired_conns",
			"Pooled PostgreSQL connections currently checked out in this process. Includes the one the singleton-cron leader elector pins for as long as this instance is the leader.", nil, nil),
		max: prometheus.NewDesc("vidra_db_pool_max_conns",
			"Configured ceiling on this process's PostgreSQL pool (DB_MAX_CONNS). Server-wide demand is this times the number of api and worker processes.", nil, nil),
		acquires: prometheus.NewDesc("vidra_db_pool_acquires_total",
			"Total successful connection acquires from this process's pool.", nil, nil),
		emptyAcquires: prometheus.NewDesc("vidra_db_pool_empty_acquires_total",
			"Acquires that had to wait because every pooled connection was checked out. A rising rate means this process's pool is too small for its own concurrency.", nil, nil),
		canceledAcquires: prometheus.NewDesc("vidra_db_pool_canceled_acquires_total",
			"Acquires abandoned because the caller's context was canceled or its deadline passed while waiting for a connection.", nil, nil),
		acquireWait: prometheus.NewDesc("vidra_db_pool_acquire_wait_seconds_total",
			"Cumulative seconds spent waiting for a pooled connection. rate() over it is wait-seconds per second.", nil, nil),
		newConns: prometheus.NewDesc("vidra_db_pool_new_conns_total",
			"Connections this process has opened to PostgreSQL. Continuous growth against a flat total is pool churn.", nil, nil),
		lifetimeDestroys: prometheus.NewDesc("vidra_db_pool_max_lifetime_destroys_total",
			"Connections retired for reaching DB_CONN_MAX_LIFETIME.", nil, nil),
		source: source,
	})
}

// dbPoolCollector samples the pool on each scrape.
type dbPoolCollector struct {
	total, idle, acquired, max                *prometheus.Desc
	acquires, emptyAcquires, canceledAcquires *prometheus.Desc
	acquireWait, newConns, lifetimeDestroys   *prometheus.Desc
	source                                    func() DBPoolStats
}

func (c *dbPoolCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{
		c.total, c.idle, c.acquired, c.max,
		c.acquires, c.emptyAcquires, c.canceledAcquires,
		c.acquireWait, c.newConns, c.lifetimeDestroys,
	} {
		ch <- d
	}
}

func (c *dbPoolCollector) Collect(ch chan<- prometheus.Metric) {
	s := c.source()
	gauge := func(d *prometheus.Desc, v float64) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.GaugeValue, v)
	}
	counter := func(d *prometheus.Desc, v float64) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.CounterValue, v)
	}
	gauge(c.total, float64(s.TotalConns))
	gauge(c.idle, float64(s.IdleConns))
	gauge(c.acquired, float64(s.AcquiredConns))
	gauge(c.max, float64(s.MaxConns))
	counter(c.acquires, float64(s.AcquireCount))
	counter(c.emptyAcquires, float64(s.EmptyAcquireCount))
	counter(c.canceledAcquires, float64(s.CanceledAcquireCount))
	counter(c.acquireWait, s.AcquireDuration.Seconds())
	counter(c.newConns, float64(s.NewConnsCount))
	counter(c.lifetimeDestroys, float64(s.MaxLifetimeDestroyCount))
}

// Handler returns the Prometheus scrape handler for this registry.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// searchHealthCollector reports the current search-service health (0/1) on each
// scrape.
type searchHealthCollector struct {
	desc   *prometheus.Desc
	source func() float64
}

func (c *searchHealthCollector) Describe(ch chan<- *prometheus.Desc) { ch <- c.desc }

func (c *searchHealthCollector) Collect(ch chan<- prometheus.Metric) {
	ch <- prometheus.MustNewConstMetric(c.desc, prometheus.GaugeValue, c.source())
}

// queueDepthCollector pulls durable-queue depths on each scrape.
type queueDepthCollector struct {
	desc   *prometheus.Desc
	source func(context.Context) ([]QueueDepth, error)
}

type jobQueueHealthCollector struct {
	age    *prometheus.Desc
	stale  *prometheus.Desc
	source func(context.Context) ([]JobQueueHealth, error)
}

func (c *jobQueueHealthCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.age
	ch <- c.stale
}

func (c *jobQueueHealthCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	rows, err := c.source(ctx)
	if err != nil {
		return
	}
	for _, row := range rows {
		ch <- prometheus.MustNewConstMetric(c.age, prometheus.GaugeValue,
			float64(row.OldestQueuedAgeSeconds), row.Queue)
		ch <- prometheus.MustNewConstMetric(c.stale, prometheus.GaugeValue,
			float64(row.StaleRunning), row.Queue)
	}
}

func (c *queueDepthCollector) Describe(ch chan<- *prometheus.Desc) { ch <- c.desc }

func (c *queueDepthCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	depths, err := c.source(ctx)
	if err != nil {
		return // keep the scrape healthy on a transient source error
	}
	for _, d := range depths {
		ch <- prometheus.MustNewConstMetric(c.desc, prometheus.GaugeValue, float64(d.Count), d.Queue, d.State)
	}
}

// statusClass buckets an HTTP status code into a bounded label value.
func statusClass(code int) string {
	switch {
	case code >= 200 && code < 300:
		return "2xx"
	case code >= 300 && code < 400:
		return "3xx"
	case code >= 400 && code < 500:
		return "4xx"
	case code >= 500:
		return "5xx"
	default:
		return "other"
	}
}
