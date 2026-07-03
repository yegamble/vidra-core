// Package linkpreview fetches OpenGraph link-preview metadata for a URL found in
// a plaintext direct message (product-decisions.md §14). Every fetch is
// SSRF-guarded (the injected http.Client dials through internal/urlsafety),
// bounded to a small HTML response, and best-effort — the caller never blocks or
// fails a message send on a preview outcome.
package linkpreview

import (
	"context"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/vidra/vidra-core/internal/urlsafety"
)

// DefaultMaxBytes bounds the fetched HTML (1 MiB, per spec). Larger responses
// are truncated at the reader.
const DefaultMaxBytes = 1 << 20

// fetchTimeout is the overall deadline for a single preview fetch.
const fetchTimeout = 15 * time.Second

// ErrNotHTML means the response was not an HTML document (previews are html-only).
var ErrNotHTML = errors.New("linkpreview: response is not html")

// urlPattern matches the first http(s) URL in a block of text. It is deliberately
// permissive on the path and stops at whitespace or angle brackets.
var urlPattern = regexp.MustCompile(`https?://[^\s<>()"']+`)

// FirstURL returns the first http/https URL in text, trimmed of trailing
// punctuation, or "" when there is none.
func FirstURL(text string) string {
	m := urlPattern.FindString(text)
	// Trim trailing sentence punctuation that a URL regex tends to swallow.
	return strings.TrimRight(m, ".,;:!?)]}\"'")
}

// Preview is the parsed OpenGraph metadata of a page. Any field may be empty.
type Preview struct {
	Title       string
	Description string
	Image       string
}

// Fetcher performs SSRF-guarded, size-bounded preview fetches.
type Fetcher struct {
	client   *http.Client
	maxBytes int64
	guard    urlsafety.Guard
}

// NewFetcher builds a Fetcher whose URL pre-flight validation and dial-time
// SSRF policy both come from guard (so they agree — e.g. AllowPrivate for local
// dev/test). maxBytes<=0 uses DefaultMaxBytes.
func NewFetcher(guard urlsafety.Guard, maxBytes int64) *Fetcher {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	return &Fetcher{client: guard.NewClient(fetchTimeout), maxBytes: maxBytes, guard: guard}
}

// Fetch retrieves rawURL and returns its OpenGraph preview. It matches the
// messaging.PreviewFetcher shape (title, description, image, error). A
// non-public address, non-HTML body, oversize response, or any transport error
// yields a non-nil error, which the caller records as a 'failed' preview.
func (f *Fetcher) Fetch(ctx context.Context, rawURL string) (title, description, image string, err error) {
	if _, verr := f.guard.ValidateURL(rawURL); verr != nil {
		return "", "", "", verr
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", "", "", err
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("User-Agent", "vidra-linkpreview/1.0")
	resp, err := f.client.Do(req)
	if err != nil {
		return "", "", "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", "", errors.New("linkpreview: non-2xx status")
	}
	ct := resp.Header.Get("Content-Type")
	if ct != "" && !strings.Contains(strings.ToLower(ct), "html") {
		return "", "", "", ErrNotHTML
	}
	p := parse(io.LimitReader(resp.Body, f.maxBytes))
	return p.Title, p.Description, p.Image, nil
}

// parse extracts OpenGraph title/description/image from an HTML stream, falling
// back to the <title> element for the title. It stops at </head> to bound work.
func parse(r io.Reader) Preview {
	var p Preview
	var htmlTitle string // <title> fallback, used only when og:title is absent
	var inTitle bool
	z := html.NewTokenizer(r)
	for {
		switch z.Next() {
		case html.ErrorToken:
			if p.Title == "" {
				p.Title = htmlTitle
			}
			return p
		case html.StartTagToken, html.SelfClosingTagToken:
			t := z.Token()
			switch t.Data {
			case "meta":
				prop, content := "", ""
				for _, a := range t.Attr {
					switch strings.ToLower(a.Key) {
					case "property", "name":
						prop = strings.ToLower(a.Val)
					case "content":
						content = a.Val
					}
				}
				switch prop {
				case "og:title":
					if p.Title == "" {
						p.Title = content
					}
				case "og:description", "description":
					if p.Description == "" {
						p.Description = content
					}
				case "og:image", "og:image:url":
					if p.Image == "" {
						p.Image = content
					}
				}
			case "title":
				inTitle = true
			case "body":
				// Metadata lives in <head>; stop once content starts.
				if p.Title == "" {
					p.Title = htmlTitle
				}
				return p
			}
		case html.TextToken:
			if inTitle && htmlTitle == "" {
				htmlTitle = strings.TrimSpace(z.Token().Data)
			}
		case html.EndTagToken:
			if z.Token().Data == "title" {
				inTitle = false
			}
		}
	}
}
