package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/vidra/vidra-core/internal/ipfs"
)

type ipfsGatewayAuthorizer interface {
	PublicGatewayRootAllowed(context.Context, string) (bool, error)
}

// Caddy overwrites the two forwarded headers before this read-only precheck.
// A direct call grants no bearer token and never returns a CID or media bytes.
func (s *Server) handleIPFSGatewayAuthorize(c echo.Context) error {
	c.Response().Header().Set("Cache-Control", "no-store")
	method := c.Request().Header.Get("X-Forwarded-Method")
	uri := c.Request().Header.Get("X-Forwarded-Uri")
	if method != http.MethodGet && method != http.MethodHead {
		return c.NoContent(http.StatusNotFound)
	}
	root, ok := ipfsGatewayRoot(uri)
	gate, wired := s.ipfsmirrorsvc.(ipfsGatewayAuthorizer)
	if !ok || !wired {
		return c.NoContent(http.StatusNotFound)
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), 2*time.Second)
	defer cancel()
	allowed, err := gate.PublicGatewayRootAllowed(ctx, root)
	if err != nil || !allowed {
		return c.NoContent(http.StatusNotFound)
	}
	return c.NoContent(http.StatusNoContent)
}

func ipfsGatewayRoot(uri string) (string, bool) {
	if len(uri) > 2048 || !strings.HasPrefix(uri, "/ipfs/") || strings.ContainsAny(uri, "%\\?#") {
		return "", false
	}
	for _, ch := range uri {
		if ch < 0x21 || ch > 0x7e {
			return "", false
		}
	}
	parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(uri, "/ipfs/"), "/"), "/")
	if ipfs.ValidateCID(parts[0]) != nil {
		return "", false
	}
	for _, part := range parts[1:] {
		if part == "" || part == "." || part == ".." {
			return "", false
		}
	}
	return parts[0], true
}
