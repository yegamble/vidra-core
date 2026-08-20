// Package searchclient is vidra-core's typed HTTP client for the vidra-search
// internal API (MASTER-PLAN §1.4, §2.4). Every call is HMAC-authenticated
// (X-Vidra-Internal-Auth), carries the request's X-Correlation-ID downstream,
// runs under a tight per-endpoint deadline, and is guarded by a per-endpoint-group
// circuit breaker so a slow or failing search service degrades fast instead of
// stalling core. All failures are the CALLER's cue to fall back silently — the
// client never makes search authoritative.
package searchclient

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/searchevents"
)

// defaultJitterRand is the prober's default ±jitter source (concurrency-safe).
func defaultJitterRand() float64 { return rand.Float64() }

// authHeaderName is the internal S2S auth header (MASTER-PLAN §1.4).
const authHeaderName = "X-Vidra-Internal-Auth"

// group identifies an endpoint group for breaker + timeout selection.
type group int

const (
	groupSuggest group = iota
	groupSearch
	groupRecs
	groupHistory
	groupEvents
)

// Default per-group deadlines (MASTER-PLAN §2.4).
const (
	defaultSuggestTimeout = 250 * time.Millisecond
	defaultSearchTimeout  = 800 * time.Millisecond
	defaultRecsTimeout    = 800 * time.Millisecond
	defaultHistoryTimeout = 2 * time.Second
	defaultEventsTimeout  = 5 * time.Second
)

// ErrCircuitOpen is returned when a group's breaker is open (fail-fast). The
// caller degrades exactly as it would on any other error.
var ErrCircuitOpen = fmt.Errorf("searchclient: circuit open")

// HTTPError is a non-2xx, non-5xx (i.e. 4xx) response — the service is up but
// rejected the request. Distinct from transport/5xx errors so a 404 on
// delete-history can be told from an outage.
type HTTPError struct {
	Status int
}

func (e *HTTPError) Error() string { return "searchclient: status " + strconv.Itoa(e.Status) }

// Client talks to the vidra-search internal API.
type Client struct {
	baseURL  string
	secret   []byte
	http     *http.Client
	now      func() time.Time
	breakers map[group]*breaker
	timeouts map[group]time.Duration
	// prober is the active health detector behind Healthy(). It is always
	// constructed (optimistic) so Healthy() is usable even before RunHealthProbe
	// is started; cmd/api starts the loop only when the service is configured.
	prober *prober
}

// Option customises the Client.
type Option func(*Client)

// WithHTTPClient overrides the underlying *http.Client (test seam). The
// per-request deadline is still applied via context.
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

// WithClock overrides the time source (HMAC timestamp + breaker cooldown; tests).
func WithClock(now func() time.Time) Option { return func(c *Client) { c.now = now } }

// WithTimeouts overrides the per-group deadlines (SEARCH_*_TIMEOUT_MS env). A
// zero value keeps the default for that group.
func WithTimeouts(suggest, search, recs time.Duration) Option {
	return func(c *Client) {
		if suggest > 0 {
			c.timeouts[groupSuggest] = suggest
		}
		if search > 0 {
			c.timeouts[groupSearch] = search
		}
		if recs > 0 {
			c.timeouts[groupRecs] = recs
		}
	}
}

// WithHealthInterval overrides the health prober's base cadence (SEARCH_HEALTH_
// INTERVAL env). A non-positive value keeps the 15s default.
func WithHealthInterval(d time.Duration) Option {
	return func(c *Client) {
		if d > 0 {
			c.prober.interval = d
		}
	}
}

// WithLogger sets the logger the health prober uses for its transition lines
// (nil keeps slog.Default()).
func WithLogger(l *slog.Logger) Option {
	return func(c *Client) {
		if l != nil {
			c.prober.logger = l
		}
	}
}

// WithProberRand overrides the prober's [0,1) jitter source (test seam for the
// ±20% interval-bounds assertions).
func WithProberRand(fn func() float64) Option {
	return func(c *Client) {
		if fn != nil {
			c.prober.rand = fn
		}
	}
}

// New builds a Client for baseURL (the search service origin, no trailing slash)
// authenticated with secret. It is safe for concurrent use.
func New(baseURL, secret string, opts ...Option) *Client {
	c := &Client{
		baseURL: baseURL,
		secret:  []byte(secret),
		http:    &http.Client{},
		now:     time.Now,
		timeouts: map[group]time.Duration{
			groupSuggest: defaultSuggestTimeout,
			groupSearch:  defaultSearchTimeout,
			groupRecs:    defaultRecsTimeout,
			groupHistory: defaultHistoryTimeout,
			groupEvents:  defaultEventsTimeout,
		},
		prober: newProber(baseURL),
	}
	for _, opt := range opts {
		opt(c)
	}
	c.breakers = map[group]*breaker{
		groupSuggest: newBreaker(c.now),
		groupSearch:  newBreaker(c.now),
		groupRecs:    newBreaker(c.now),
		groupHistory: newBreaker(c.now),
		groupEvents:  newBreaker(c.now),
	}
	return c
}

// authHeader builds the v1 HMAC header binding the timestamp, method, and path
// (MASTER-PLAN §1.4): v1:{ts}:{hex(hmac_sha256(secret, ts "\n" METHOD "\n" PATH))}.
// PATH is the DECODED path — vidra-search recomputes the signature over
// r.URL.Path, which Go decodes — so callers pass the unescaped form even when
// the wire path carries percent-escapes.
func (c *Client) authHeader(method, path string) string {
	ts := strconv.FormatInt(c.now().Unix(), 10)
	mac := hmac.New(sha256.New, c.secret)
	mac.Write([]byte(ts + "\n" + method + "\n" + path))
	return "v1:" + ts + ":" + hex.EncodeToString(mac.Sum(nil))
}

// do performs one request under the group's breaker + deadline. A transport
// error or 5xx trips the breaker and returns an error; a 4xx returns *HTTPError
// (service healthy); a 2xx decodes into out (when non-nil) and closes the breaker.
// path must contain no characters needing escaping (wire path == sign path);
// the one endpoint embedding a caller-supplied path segment uses doPaths.
func (c *Client) do(ctx context.Context, g group, method, path string, q url.Values, body, out any) error {
	return c.doPaths(ctx, g, method, path, path, q, body, out)
}

// doPaths is do with the wire path (the escaped form placed in the request URI)
// split from the sign path (the DECODED path the HMAC covers). The server
// verifies over r.URL.Path — Go's decoded form — so a path segment carrying
// percent-escapes on the wire MUST be signed unescaped or the signature will
// never match (401).
func (c *Client) doPaths(ctx context.Context, g group, method, wirePath, signPath string, q url.Values, body, out any) error {
	b := c.breakers[g]
	if !b.allow() {
		return ErrCircuitOpen
	}
	rctx, cancel := context.WithTimeout(ctx, c.timeouts[g])
	defer cancel()

	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(buf)
	}
	target := c.baseURL + wirePath
	if len(q) > 0 {
		target += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(rctx, method, target, reader)
	if err != nil {
		return err
	}
	req.Header.Set(authHeaderName, c.authHeader(method, signPath))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if cid := observability.CorrelationFromContext(ctx).CorrelationID; cid != "" {
		req.Header.Set("X-Correlation-ID", cid)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		b.failure()
		return fmt.Errorf("searchclient: %s %s: %w", method, wirePath, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 500 {
		b.failure()
		return fmt.Errorf("searchclient: %s %s: status %d", method, wirePath, resp.StatusCode)
	}
	b.success()
	if resp.StatusCode >= 400 {
		return &HTTPError{Status: resp.StatusCode}
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("searchclient: decode %s: %w", wirePath, err)
		}
	}
	return nil
}

// Suggestions fetches autocomplete rows for a prefix.
func (c *Client) Suggestions(ctx context.Context, p SuggestParams) (SuggestionsResponse, error) {
	q := url.Values{}
	q.Set("q", p.Query)
	q.Set("limit", strconv.Itoa(p.Limit))
	if p.UserID != nil {
		q.Set("user_id", p.UserID.String())
	}
	if p.SessionID != "" {
		q.Set("session_id", p.SessionID)
	}
	q.Set("personalized", strconv.FormatBool(p.Personalized))
	q.Set("include_history", strconv.FormatBool(p.IncludeHistory))
	q.Set("hide_sensitive", strconv.FormatBool(p.HideSensitive))
	if p.Lang != "" {
		q.Set("lang", p.Lang)
	}
	var out SuggestionsResponse
	err := c.do(ctx, groupSuggest, http.MethodGet, "/internal/v1/suggestions", q, nil, &out)
	return out, err
}

// Search returns a ranked, visibility-safe id list for a query.
func (c *Client) Search(ctx context.Context, p SearchParams) (SearchResponse, error) {
	q := url.Values{}
	q.Set("q", p.Query)
	q.Set("limit", strconv.Itoa(p.Limit))
	q.Set("offset", strconv.Itoa(p.Offset))
	if p.Tag != "" {
		q.Set("tag", p.Tag)
	}
	if p.Category != "" {
		q.Set("category", p.Category)
	}
	if p.Language != "" {
		q.Set("language", p.Language)
	}
	if p.UserID != nil {
		q.Set("user_id", p.UserID.String())
	}
	if p.SessionID != "" {
		q.Set("session_id", p.SessionID)
	}
	q.Set("personalized", strconv.FormatBool(p.Personalized))
	q.Set("hide_sensitive", strconv.FormatBool(p.HideSensitive))
	if p.Mode != "" {
		q.Set("mode", p.Mode)
	}
	var out SearchResponse
	err := c.do(ctx, groupSearch, http.MethodGet, "/internal/v1/search", q, nil, &out)
	return out, err
}

// recsQuery builds the shared recommendation query values.
func recsQuery(p RecsParams) url.Values {
	q := url.Values{}
	if p.VideoID != nil {
		q.Set("video_id", p.VideoID.String())
	}
	if p.UserID != nil {
		q.Set("user_id", p.UserID.String())
	}
	if p.SessionID != "" {
		q.Set("session_id", p.SessionID)
	}
	q.Set("limit", strconv.Itoa(p.Limit))
	q.Set("personalized", strconv.FormatBool(p.Personalized))
	q.Set("hide_sensitive", strconv.FormatBool(p.HideSensitive))
	if p.Lang != "" {
		q.Set("lang", p.Lang)
	}
	return q
}

// RecommendationsHome fetches the personalized/trending home rail id list.
func (c *Client) RecommendationsHome(ctx context.Context, p RecsParams) (RecommendationsResponse, error) {
	var out RecommendationsResponse
	err := c.do(ctx, groupRecs, http.MethodGet, "/internal/v1/recommendations/home", recsQuery(p), nil, &out)
	return out, err
}

// RecommendationsRelated fetches the related-videos id list for a watch page.
func (c *Client) RecommendationsRelated(ctx context.Context, p RecsParams) (RecommendationsResponse, error) {
	var out RecommendationsResponse
	err := c.do(ctx, groupRecs, http.MethodGet, "/internal/v1/recommendations/related", recsQuery(p), nil, &out)
	return out, err
}

// GetUserHistory reads a user's stored search history.
func (c *Client) GetUserHistory(ctx context.Context, p HistoryParams) (HistoryResponse, error) {
	q := url.Values{}
	q.Set("limit", strconv.Itoa(p.Limit))
	q.Set("offset", strconv.Itoa(p.Offset))
	path := "/internal/v1/users/" + p.UserID.String() + "/search-history"
	var out HistoryResponse
	err := c.do(ctx, groupHistory, http.MethodGet, path, q, nil, &out)
	return out, err
}

// DeleteUserHistory clears a user's entire search history.
func (c *Client) DeleteUserHistory(ctx context.Context, userID uuid.UUID) error {
	path := "/internal/v1/users/" + userID.String() + "/search-history"
	return c.do(ctx, groupHistory, http.MethodDelete, path, nil, nil, nil)
}

// DeleteUserHistoryQuery removes one normalized query from a user's history.
// The query segment is percent-escaped on the wire but the HMAC covers the
// DECODED path (the server verifies over r.URL.Path), so the two forms are
// passed separately.
func (c *Client) DeleteUserHistoryQuery(ctx context.Context, userID uuid.UUID, normalizedQuery string) error {
	base := "/internal/v1/users/" + userID.String() + "/search-history/"
	return c.doPaths(ctx, groupHistory, http.MethodDelete,
		base+url.PathEscape(normalizedQuery), base+normalizedQuery, nil, nil, nil)
}

// DeleteUser purges all of a user's search-side data (best-effort account-delete
// companion to the enqueued user.suppress + user.history_deleted events).
func (c *Client) DeleteUser(ctx context.Context, userID uuid.UUID) error {
	path := "/internal/v1/users/" + userID.String()
	return c.do(ctx, groupHistory, http.MethodDelete, path, nil, nil, nil)
}

// SendEvents delivers a batch of events to /internal/v1/events (satisfies
// searchevents.Sender for the outbox worker).
func (c *Client) SendEvents(ctx context.Context, batch searchevents.EventBatch) (searchevents.EventBatchResult, error) {
	var out searchevents.EventBatchResult
	err := c.do(ctx, groupEvents, http.MethodPost, "/internal/v1/events", nil, batch, &out)
	return out, err
}

// RunHealthProbe drives the background health prober until ctx is cancelled.
// Start it once, when the service is configured, with `go c.RunHealthProbe(ctx)`;
// it returns promptly on cancellation and never blocks shutdown.
func (c *Client) RunHealthProbe(ctx context.Context) { c.prober.run(ctx) }

// Healthy reports whether vidra-search should be used right now: the active
// prober currently considers /healthz up AND no endpoint-group breaker is open.
// A false result routes the caller to its backup path with no per-request
// latency. This never wedges: a tripped breaker reports "open" only during its
// cooldown window, after which it self-clears (admitting a half-open probe on the
// next real call), and the prober independently re-forgives the service on the
// first successful /healthz.
func (c *Client) Healthy() bool {
	return c.prober.healthy.Load() && !c.anyBreakerOpen()
}

// Ready asks the search service's PUBLIC /readyz once, right now, and reports
// what it said. It is the on-demand counterpart to Healthy(): an operator
// opening the admin status page must see the service's current answer, not the
// conclusion the background prober drew up to 15 seconds ago.
//
// /readyz sits outside the HMAC group on the search service, so the probe
// carries no auth header, and it runs on the CALLER's deadline rather than a
// group timeout — it is not a search, and it must not trip a breaker that would
// then route real searches to their backup path.
func (c *Client) Ready(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/readyz", nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	// Drain a little so the connection can be reused.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("searchclient: readyz answered %d", resp.StatusCode)
	}
	return nil
}

// ServiceProbeHealthy is the prober's view of the service (/healthz) alone,
// independent of breaker state — the signal behind the vidra_search_service_healthy
// gauge and the transition logs.
func (c *Client) ServiceProbeHealthy() bool { return c.prober.healthy.Load() }

// anyBreakerOpen reports whether any endpoint-group breaker is currently refusing
// calls (a service-wide 5xx/transport symptom; 4xx never trips a breaker).
func (c *Client) anyBreakerOpen() bool {
	for _, b := range c.breakers {
		if b.isOpen() {
			return true
		}
	}
	return false
}
