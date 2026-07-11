package httpapi

import (
	"io"

	"github.com/labstack/echo/v4"
)

// dynamicBodyLimit enforces the EFFECTIVE upload_max_size_bytes overlay on the
// direct (non-resumable) upload route. Unlike echo's static middleware.BodyLimit
// — which bakes the boot-time UPLOAD_MAX_SIZE into the route at registration —
// this resolves the cap per request, so an admin raising or lowering the limit
// through the instance-settings overlay takes effect on the next upload without
// a restart (and a GET of the settings never lies about the enforced value).
//
// It mirrors echo's own body-limit semantics: a declared Content-Length over the
// cap is rejected before the body is read, and a missing/under-declared length
// is caught by a reader that fails once the cap is exceeded. A limit <= 0 means
// no cap (pass through unchanged).
func (s *Server) dynamicBodyLimit() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			limit := s.uploadMaxSizeBytes()
			if limit <= 0 {
				return next(c)
			}
			req := c.Request()
			// Fast path: a truthfully declared Content-Length over the cap is a 413
			// before any bytes are read.
			if req.ContentLength > limit {
				return echo.ErrStatusRequestEntityTooLarge
			}
			// Defence against a missing/under-declared Content-Length: cap the bytes
			// actually read from the body.
			req.Body = &limitedReadCloser{r: req.Body, limit: limit}
			return next(c)
		}
	}
}

// limitedReadCloser fails a read once more than limit bytes have been consumed,
// surfacing echo's 413 through the handler's body/multipart parse.
type limitedReadCloser struct {
	r     io.ReadCloser
	limit int64
	read  int64
}

func (l *limitedReadCloser) Read(p []byte) (int, error) {
	if l.read > l.limit {
		return 0, echo.ErrStatusRequestEntityTooLarge
	}
	n, err := l.r.Read(p)
	l.read += int64(n)
	if l.read > l.limit {
		return n, echo.ErrStatusRequestEntityTooLarge
	}
	return n, err
}

func (l *limitedReadCloser) Close() error { return l.r.Close() }
