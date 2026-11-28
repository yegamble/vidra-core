package httpapi

import (
	"context"
	"errors"
	"net"
	"net/http"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v5"
	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/storage"
)

const referencePlaybackRoute = "/videos/:id/ipfs-reference/:generation/*"

var referenceFile = regexp.MustCompile("^[A-Za-z0-9_-][A-Za-z0-9_.-]{0,254}(/[A-Za-z0-9_-][A-Za-z0-9_.-]{0,254})*$")
var referenceRange = regexp.MustCompile("^bytes=([0-9]{1,19}-[0-9]{0,19}|-[0-9]{1,19})$")
var referenceExtensions = regexp.MustCompile(`\.(m3u8|mp4|m4s|m4a|ts|aac|vtt)$`)

type referencePlayback struct {
	currentMaster func(context.Context, uuid.UUID) (string, error)
	client        *http.Client
}

// WithReferencePlayback enables individually approved native generations. The
// bridge checks the publication receipt and manifest on every HTTP operation.
func WithReferencePlayback(socket string, master func(context.Context, uuid.UUID) (string, error)) Option {
	return func(s *Server) {
		dial := &net.Dialer{Timeout: time.Second}
		s.referencePlayback = &referencePlayback{currentMaster: master, client: &http.Client{
			Transport: &http.Transport{
				DialContext:        func(ctx context.Context, _, _ string) (net.Conn, error) { return dial.DialContext(ctx, "unix", socket) },
				DisableCompression: true, MaxConnsPerHost: 32, IdleConnTimeout: 30 * time.Second, ResponseHeaderTimeout: 5 * time.Second,
			},
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}}
	}
}

func referencePath(id uuid.UUID, generation, file string) string {
	return "/api/v1/videos/" + id.String() + "/ipfs-reference/" + generation + "/" + file
}

func (p *referencePlayback) request(ctx context.Context, method, filePath, byteRange string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, "http://reference.local"+filePath, nil)
	if err != nil {
		return nil, err
	}
	if byteRange != "" {
		req.Header.Set("Range", byteRange)
	}
	return p.client.Do(req)
}

func (p *referencePlayback) checkGeneration(ctx context.Context, id uuid.UUID, master string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	current, err := p.currentMaster(ctx, id)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "reference playback unavailable")
	}
	if err != nil || current != master {
		return echo.NewHTTPError(http.StatusNotFound, "reference media not available")
	}
	return nil
}

func (s *Server) referencePlaybackURL(ctx context.Context, id uuid.UUID, master string) string {
	p := s.referencePlayback
	if p == nil || s.webOrigin() == "" {
		return ""
	}
	gen := path.Base(path.Dir(master))
	if len(gen) > 20 || !media.IsHLSGenerationName(gen) || master != media.HLSKeyPrefix(id)+"/"+gen+"/master.m3u8" {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 400*time.Millisecond)
	defer cancel()
	if p.checkGeneration(ctx, id, master) != nil {
		return ""
	}
	urlPath := referencePath(id, gen, "master.m3u8")
	res, err := p.request(ctx, http.MethodHead, urlPath, "")
	if err != nil {
		return ""
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return ""
	}
	return strings.TrimRight(s.webOrigin(), "/") + urlPath
}

func (s *Server) handleReferenceFile(c *echo.Context) error {
	h := c.Response().Header()
	h.Set("Cache-Control", "private, no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	absent := echo.NewHTTPError(http.StatusNotFound, "reference media not available")
	unavailable := echo.NewHTTPError(http.StatusServiceUnavailable, "reference playback unavailable")
	id, err := pathUUID(c, "id", "reference media not available")
	if err != nil {
		return err
	}
	gen, file := c.Param("generation"), c.Param("*")
	mime := storage.ContentTypeForKey(file)
	req := c.Request()
	p := s.referencePlayback
	if p == nil || !referenceExtensions.MatchString(file) || len(file) > 1024 || (len(gen) > 20 || !media.IsHLSGenerationName(gen)) || !referenceFile.MatchString(file) || req.URL.EscapedPath() != referencePath(id, gen, file) {
		return absent
	}
	master := media.HLSKeyPrefix(id) + "/" + gen + "/master.m3u8"
	if err := p.checkGeneration(req.Context(), id, master); err != nil {
		return err
	}
	byteRange := req.Header.Get("Range")
	if len(req.Header.Values("Range")) > 1 || (byteRange != "" && !referenceRange.MatchString(byteRange)) {
		return c.NoContent(http.StatusRequestedRangeNotSatisfiable)
	}
	res, err := p.request(req.Context(), req.Method, referencePath(id, gen, file), byteRange)
	if err != nil {
		return unavailable
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case http.StatusForbidden, http.StatusNotFound, http.StatusGone:
		return absent
	case http.StatusRequestedRangeNotSatisfiable:
		return c.NoContent(res.StatusCode)
	case http.StatusOK, http.StatusPartialContent:
	default:
		return unavailable
	}
	// A withdrawal or replacement may have committed while Kubo opened the file.
	if err := p.checkGeneration(req.Context(), id, master); err != nil {
		return err
	}
	for _, key := range []string{"Content-Length", "Content-Range", "Accept-Ranges"} {
		if value := res.Header.Get(key); value != "" {
			h.Set(key, value)
		}
	}
	h.Set("Content-Type", mime)
	if req.Method == http.MethodHead {
		return c.NoContent(res.StatusCode)
	}
	return c.Stream(res.StatusCode, mime, res.Body)
}
