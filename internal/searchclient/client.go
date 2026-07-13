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
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/searchevents"
)

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
func (c *Client) authHeader(method, path string) string {
	ts := strconv.FormatInt(c.now().Unix(), 10)
	mac := hmac.New(sha256.New, c.secret)
	mac.Write([]byte(ts + "\n" + method + "\n" + path))
	return "v1:" + ts + ":" + hex.EncodeToString(mac.Sum(nil))
}

// do performs one request under the group's breaker + deadline. A transport
// error or 5xx trips the breaker and returns an error; a 4xx returns *HTTPError
// (service healthy); a 2xx decodes into out (when non-nil) and closes the breaker.
func (c *Client) do(ctx context.Context, g group, method, path string, q url.Values, body, out any) error {
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
	target := c.baseURL + path
	if len(q) > 0 {
		target += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(rctx, method, target, reader)
	if err != nil {
		return err
	}
	req.Header.Set(authHeaderName, c.authHeader(method, path))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if cid := observability.CorrelationFromContext(ctx).CorrelationID; cid != "" {
		req.Header.Set("X-Correlation-ID", cid)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		b.failure()
		return fmt.Errorf("searchclient: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 500 {
		b.failure()
		return fmt.Errorf("searchclient: %s %s: status %d", method, path, resp.StatusCode)
	}
	b.success()
	if resp.StatusCode >= 400 {
		return &HTTPError{Status: resp.StatusCode}
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("searchclient: decode %s: %w", path, err)
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
func (c *Client) DeleteUserHistoryQuery(ctx context.Context, userID uuid.UUID, normalizedQuery string) error {
	path := "/internal/v1/users/" + userID.String() + "/search-history/" + url.PathEscape(normalizedQuery)
	return c.do(ctx, groupHistory, http.MethodDelete, path, nil, nil, nil)
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
