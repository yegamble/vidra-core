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
	}
	reg.MustRegister(m.requests, m.duration)
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

// QueueDepth is one durable-queue depth sample: how many rows sit in a given
// state for a given queue.
type QueueDepth struct {
	Queue string
	State string // pending | running | done | failed
	Count int64
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

// Handler returns the Prometheus scrape handler for this registry.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// queueDepthCollector pulls durable-queue depths on each scrape.
type queueDepthCollector struct {
	desc   *prometheus.Desc
	source func(context.Context) ([]QueueDepth, error)
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
