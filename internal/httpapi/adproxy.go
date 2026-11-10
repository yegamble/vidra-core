package httpapi

// Generic anti-adblock ad-tag proxy.
//
// Ad networks (HilltopAds, Adsterra, PropellerAds, ...) defeat filter lists by
// shipping a small SERVER-SIDE script the publisher hosts on their own origin.
// The script fetches a short-lived JavaScript "anti-adblock" tag from the
// network's API and re-serves it under an arbitrary, publisher-chosen path, so
// a blocklist keyed on the network's hostname or a well-known path never sees
// the request. That script is almost always PHP; running a PHP interpreter just
// to proxy one JSON call is not worth a second runtime, so this reproduces the
// behaviour in Go.
//
// It is configuration, not a hardcoded integration: AD_ANTIADBLOCK_PROVIDERS is
// a JSON array (see adProxyProvider). Each element maps one local path to one
// upstream fetch, so any network whose anti-adblock endpoint returns the tag
// (as a raw body, or as a string field inside a JSON envelope) is supported with
// no code change. Empty (the default) mounts nothing.
//
// The core (adProxy.handler) is a plain http.Handler so it stays framework-
// agnostic and unit-testable; registerAdProxy wraps it for Echo.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v5"
)

const (
	// adProxyFetchTimeout bounds one upstream fetch. Anti-adblock endpoints are
	// meant to answer in well under a second; a visitor must never wait on a slow
	// network, so a slow or failed fetch falls through to a stale copy or an empty
	// body rather than hanging the page.
	adProxyFetchTimeout = 8 * time.Second
	// adProxyMaxBody caps the upstream read. The tag is a few KiB; this only
	// guards against a misbehaving or hostile upstream.
	adProxyMaxBody = 2 << 20 // 2 MiB
	// adProxyDefaultTTL is how long a fetched tag is reused when a provider does
	// not set cache_ttl_seconds. Matches the reference PHP (300s).
	adProxyDefaultTTL = 300 * time.Second
	// adProxyDefaultContentType is what the tag is served as. We set it ourselves
	// and never echo the upstream's type: the point is to emit script.
	adProxyDefaultContentType = "application/javascript; charset=utf-8"
	adProxyDefaultUserAgent   = "Vidra-AntiAdBlock-Proxy/1.0"
)

// adProxyReservedPaths plus the REST contract namespace (/api/v1) and
// /.well-known/ are refused so a mistyped provider path cannot shadow a real
// route (Echo panics on an exact duplicate, but a reserved prefix could silently
// steal traffic). The rest of /api/ stays open on purpose: see Path.
var adProxyReservedPaths = map[string]bool{
	"/": true, "/healthz": true, "/readyz": true, "/version": true,
	"/schemaz": true, "/metrics": true, "/inbox": true, "/sitemap.xml": true,
}

// adProxyProvider maps one local path to one upstream anti-adblock fetch.
type adProxyProvider struct {
	// Path is the local URL to serve — operator-chosen and deliberately opaque so
	// it is not already on a blocklist. Behind the stock reverse proxy only /api/*
	// and a fixed list of root paths reach this process (everything else goes to
	// the frontend), so in production it has to sit under /api/ but outside the
	// /api/v1 contract namespace, e.g. "/api/x/f2187587a2.js".
	Path string `json:"path"`
	// Upstream is the absolute http(s) endpoint to fetch, without a query string,
	// e.g. "https://api.hilltopads.com/publisher/antiAdBlock".
	Upstream string `json:"upstream"`
	// Params are static query parameters sent on every fetch (the network key,
	// api version, ...). They override any same-named client parameter.
	Params map[string]string `json:"params"`
	// ZoneParam/ZoneDesktop/ZoneMobile carry per-device zone selection: when
	// ZoneParam is set, ZoneDesktop is sent as that parameter, or ZoneMobile when
	// it is non-empty and the client is on a mobile User-Agent.
	ZoneParam   string `json:"zone_param"`
	ZoneDesktop string `json:"zone_desktop"`
	ZoneMobile  string `json:"zone_mobile"`
	// Passthrough forwards the visitor's own query string upstream (static Params
	// and the zone still win), matching the reference script.
	Passthrough bool `json:"passthrough_query"`
	// JSONCodePath extracts the tag from a dotted path inside a JSON envelope,
	// e.g. "result.code". Empty means serve the upstream body verbatim.
	JSONCodePath string `json:"json_code_path"`
	// ContentType overrides the response type (default application/javascript).
	ContentType string `json:"content_type"`
	// UpstreamUserAgent overrides the UA sent upstream.
	UpstreamUserAgent string `json:"upstream_user_agent"`
	// CacheTTLSeconds overrides how long a fetched tag is reused (default 300).
	CacheTTLSeconds int `json:"cache_ttl_seconds"`
}

func (p adProxyProvider) ttl() time.Duration {
	if p.CacheTTLSeconds > 0 {
		return time.Duration(p.CacheTTLSeconds) * time.Second
	}
	return adProxyDefaultTTL
}

// parseAdProxyProviders parses and validates AD_ANTIADBLOCK_PROVIDERS. It is
// strict (unknown keys fail) so an operator typo is loud, and fills defaults so
// a minimal provider object works.
func parseAdProxyProviders(raw string) ([]adProxyProvider, error) {
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	var providers []adProxyProvider
	if err := dec.Decode(&providers); err != nil {
		return nil, fmt.Errorf("adproxy: invalid AD_ANTIADBLOCK_PROVIDERS JSON: %w", err)
	}
	if len(providers) == 0 {
		return nil, errors.New("adproxy: AD_ANTIADBLOCK_PROVIDERS is an empty list")
	}
	seen := make(map[string]bool, len(providers))
	for i := range providers {
		p := &providers[i]
		p.Path = strings.TrimSpace(p.Path)
		p.Upstream = strings.TrimSpace(p.Upstream)
		if !strings.HasPrefix(p.Path, "/") {
			return nil, fmt.Errorf("adproxy: provider %d: path %q must begin with '/'", i, p.Path)
		}
		if adProxyReservedPaths[p.Path] || p.Path == apiBasePath || strings.HasPrefix(p.Path, apiBasePath+"/") || strings.HasPrefix(p.Path, "/.well-known/") {
			return nil, fmt.Errorf("adproxy: provider %d: path %q is reserved", i, p.Path)
		}
		if seen[p.Path] {
			return nil, fmt.Errorf("adproxy: provider %d: duplicate path %q", i, p.Path)
		}
		seen[p.Path] = true
		u, err := url.Parse(p.Upstream)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, fmt.Errorf("adproxy: provider %d: upstream %q must be an absolute http(s) URL", i, p.Upstream)
		}
		if p.ContentType == "" {
			p.ContentType = adProxyDefaultContentType
		}
		if p.UpstreamUserAgent == "" {
			p.UpstreamUserAgent = adProxyDefaultUserAgent
		}
	}
	return providers, nil
}

type adProxyEntry struct {
	body        []byte
	contentType string
	expires     time.Time
}

type adProxy struct {
	client *http.Client
	logger *slog.Logger

	mu    sync.Mutex
	cache map[string]adProxyEntry
}

func newAdProxy(logger *slog.Logger) *adProxy {
	return &adProxy{
		client: &http.Client{
			Timeout: adProxyFetchTimeout,
			// Do not follow redirects: an anti-adblock endpoint answers 200 with
			// the tag, and refusing to chase a 3xx closes an SSRF-via-redirect path
			// to an internal address. ErrUseLastResponse returns the 3xx as the
			// response (treated as a failure below) rather than following it.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		logger: logger,
		cache:  make(map[string]adProxyEntry),
	}
}

// handler serves one provider. On any upstream/parse failure it serves the last
// good tag if one is cached (even expired), otherwise an empty 200 — never an
// error page, so a network outage degrades to "no ad", not a broken page.
func (ap *adProxy) handler(p adProxyProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		zone := p.ZoneDesktop
		if p.ZoneMobile != "" && adProxyIsMobileUA(r.UserAgent()) {
			zone = p.ZoneMobile
		}

		q := url.Values{}
		if p.Passthrough {
			for k, vs := range r.URL.Query() {
				for _, v := range vs {
					q.Add(k, v)
				}
			}
		}
		for k, v := range p.Params { // static params win over client params
			q.Set(k, v)
		}
		if p.ZoneParam != "" && zone != "" {
			q.Set(p.ZoneParam, zone)
		}

		key := p.Path + "\x00" + q.Encode()
		now := time.Now()

		if e, ok := ap.lookup(key); ok && e.expires.After(now) {
			ap.write(w, e.contentType, e.body)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), adProxyFetchTimeout)
		defer cancel()

		body, err := ap.fetch(ctx, p, q)
		if err == nil && p.JSONCodePath != "" {
			var code string
			code, err = adProxyExtractJSON(body, p.JSONCodePath)
			body = []byte(code)
		}
		if err != nil {
			if ap.logger != nil {
				ap.logger.Warn("adproxy: upstream fetch failed, serving fallback",
					"path", p.Path, "upstream", p.Upstream, "error", err)
			}
			if e, ok := ap.lookup(key); ok { // stale-on-error
				ap.write(w, e.contentType, e.body)
				return
			}
			ap.write(w, p.ContentType, nil)
			return
		}

		if len(body) > 0 { // mirror the reference script: only cache a non-empty tag
			ap.store(key, adProxyEntry{body: body, contentType: p.ContentType, expires: now.Add(p.ttl())})
		}
		ap.write(w, p.ContentType, body)
	}
}

func (ap *adProxy) fetch(ctx context.Context, p adProxyProvider, q url.Values) ([]byte, error) {
	target := p.Upstream
	if enc := q.Encode(); enc != "" {
		if strings.Contains(target, "?") {
			target += "&" + enc
		} else {
			target += "?" + enc
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", p.UpstreamUserAgent)
	req.Header.Set("Accept", "application/json, text/javascript, */*")

	resp, err := ap.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, adProxyMaxBody))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("adproxy: upstream returned HTTP %d", resp.StatusCode)
	}
	return body, nil
}

func (ap *adProxy) write(w http.ResponseWriter, contentType string, body []byte) {
	if contentType == "" {
		contentType = adProxyDefaultContentType
	}
	w.Header().Set("Content-Type", contentType)
	// The tag is short-lived and per-device; let the edge revalidate rather than
	// pin a stale script in a shared cache.
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (ap *adProxy) lookup(key string) (adProxyEntry, bool) {
	ap.mu.Lock()
	defer ap.mu.Unlock()
	e, ok := ap.cache[key]
	return e, ok
}

func (ap *adProxy) store(key string, e adProxyEntry) {
	ap.mu.Lock()
	defer ap.mu.Unlock()
	ap.cache[key] = e
}

// adProxyExtractJSON reads a dotted path (e.g. "result.code") from a JSON object
// and returns the string leaf.
func adProxyExtractJSON(body []byte, path string) (string, error) {
	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		return "", fmt.Errorf("adproxy: upstream body is not JSON: %w", err)
	}
	cur := root
	for _, seg := range strings.Split(path, ".") {
		obj, ok := cur.(map[string]any)
		if !ok {
			return "", fmt.Errorf("adproxy: json_code_path %q: %q is not an object", path, seg)
		}
		cur, ok = obj[seg]
		if !ok {
			return "", fmt.Errorf("adproxy: json_code_path %q: key %q not found", path, seg)
		}
	}
	s, ok := cur.(string)
	if !ok {
		return "", fmt.Errorf("adproxy: json_code_path %q: leaf is not a string", path)
	}
	return s, nil
}

// adProxyIsMobileUA mirrors the reference script's device split.
func adProxyIsMobileUA(ua string) bool {
	ua = strings.ToLower(ua)
	for _, token := range []string{"mobi", "ipad", "iphone", "blackberry", "android"} {
		if strings.Contains(ua, token) {
			return true
		}
	}
	return false
}

// registerAdProxy mounts every configured provider at the root, outside the
// /api/v1 OpenAPI contract (like /metrics and the fediverse routes). A bad
// config disables the feature with a logged error rather than refusing to boot.
func (s *Server) registerAdProxy() {
	raw := strings.TrimSpace(s.cfg.AdAntiAdblockProviders)
	if raw == "" {
		return
	}
	providers, err := parseAdProxyProviders(raw)
	if err != nil {
		if s.logger != nil {
			s.logger.Error("adproxy: disabled, invalid AD_ANTIADBLOCK_PROVIDERS", "error", err)
		}
		return
	}
	ap := newAdProxy(s.logger)
	for _, p := range providers {
		s.echo.GET(p.Path, echo.WrapHandler(ap.handler(p)))
		if s.logger != nil {
			s.logger.Info("adproxy: route mounted", "path", p.Path, "upstream", p.Upstream)
		}
	}
}
