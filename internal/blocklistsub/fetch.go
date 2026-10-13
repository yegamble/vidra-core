package blocklistsub

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/vidra/vidra-core/internal/urlsafety"
)

// Fetch bounds.
const (
	// MaxBodyBytes caps one list. The largest public lists are a few MiB; 8 MiB
	// leaves headroom without letting a hostile URL stream unbounded data.
	MaxBodyBytes = 8 << 20
	fetchTimeout = 30 * time.Second
)

// Fetch error classes. Callers store only these class messages, never the
// underlying error text (which can carry resolved addresses).
var (
	ErrBlockedTarget = errors.New("address not allowed")
	ErrTooLarge      = errors.New("response too large")
	ErrTimeout       = errors.New("timed out")
)

// StatusError is a non-2xx, non-304 answer.
type StatusError struct{ Code int }

func (e StatusError) Error() string { return fmt.Sprintf("HTTP %d", e.Code) }

// Result is one conditional fetch.
type Result struct {
	Body        []byte
	ETag        string
	NotModified bool
}

// Fetcher retrieves a list, sending etag as If-None-Match when non-empty.
type Fetcher interface {
	Fetch(ctx context.Context, rawURL, etag string) (Result, error)
}

// HTTPFetcher fetches through the SSRF-guarded client: the dial-time IP check
// covers DNS rebinding AND every redirect hop, so a list that redirects to an
// internal host is refused. The body is read through a hard size cap.
type HTTPFetcher struct {
	client *http.Client
	guard  urlsafety.Guard
}

// NewHTTPFetcher builds the guarded fetcher. allowPrivate is the dev/test
// escape hatch shared with the other outbound fetchers (never set in prod).
func NewHTTPFetcher(allowPrivate bool) *HTTPFetcher {
	g := urlsafety.Guard{AllowPrivate: allowPrivate}
	return &HTTPFetcher{client: g.NewClient(fetchTimeout), guard: g}
}

// ValidateURL is the pre-flight shape check used when a subscription is added.
func (f *HTTPFetcher) ValidateURL(raw string) error {
	_, err := f.guard.ValidateURL(raw)
	return err
}

func (f *HTTPFetcher) Fetch(ctx context.Context, rawURL, etag string) (Result, error) {
	u, err := f.guard.ValidateURL(rawURL)
	if err != nil {
		return Result{}, ErrBlockedTarget
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Result{}, ErrBlockedTarget
	}
	req.Header.Set("Accept", "text/csv, application/json, text/plain;q=0.9, */*;q=0.1")
	req.Header.Set("User-Agent", "vidra-blocklist-sync")
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return Result{}, classify(err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotModified:
		return Result{NotModified: true}, nil
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return Result{}, StatusError{Code: resp.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBodyBytes+1))
	if err != nil {
		return Result{}, classify(err)
	}
	if len(body) > MaxBodyBytes {
		return Result{}, ErrTooLarge
	}
	return Result{Body: body, ETag: resp.Header.Get("ETag")}, nil
}

func classify(err error) error {
	switch {
	case errors.Is(err, urlsafety.ErrBlockedAddress), errors.Is(err, urlsafety.ErrInvalidURL),
		errors.Is(err, urlsafety.ErrTooManyRedirects):
		return ErrBlockedTarget
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, urlsafety.ErrIdleTimeout):
		return ErrTimeout
	}
	var to interface{ Timeout() bool }
	if errors.As(err, &to) && to.Timeout() {
		return ErrTimeout
	}
	return errors.New("fetch failed")
}
