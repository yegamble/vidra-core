package httpapi

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/profileimage"
)

// Instance branding assets (config-parity W1): the instance's own avatar,
// banner, and four typed logo slots, reusing the profileimage pipeline with
// the singleton instance owner. Admin-only pick/delete endpoints mirror
// PeerTube's dedicated asset API (these are NOT registry keys); serving is
// public and referenced from the GET /instance branding block.

// instanceLogoKind maps the /instance-logo/{type} path segment to the stored
// kind. The path enum IS the kind set, so this is a validation gate.
func instanceLogoKind(c echo.Context) (string, error) {
	t := c.Param("type")
	for _, k := range profileimage.InstanceLogoKinds {
		if t == k {
			return t, nil
		}
	}
	return "", echo.NewHTTPError(http.StatusNotFound, "unknown logo type")
}

// handleSetInstanceImage stores an instance avatar/banner (multipart form
// field "file", JPEG/PNG/WebP by extension → otherwise 415 — the same gate as
// user/channel avatars), replacing any previous one. Behind
// requireRole(admin); audited with the asset kind.
func (s *Server) handleSetInstanceImage(kind string) echo.HandlerFunc {
	return func(c echo.Context) error {
		return s.setInstanceImage(c, kind)
	}
}

// handleDeleteInstanceImage removes an instance avatar/banner (row + stored
// object). Behind requireRole(admin); 404 when none is set.
func (s *Server) handleDeleteInstanceImage(kind string) echo.HandlerFunc {
	return func(c echo.Context) error {
		return s.deleteInstanceImage(c, kind)
	}
}

// handleSetInstanceLogo / handleDeleteInstanceLogo are the typed-logo-slot
// variants ({type} ∈ favicon|header-wide|header-square|opengraph).
func (s *Server) handleSetInstanceLogo(c echo.Context) error {
	kind, err := instanceLogoKind(c)
	if err != nil {
		return err
	}
	return s.setInstanceImage(c, kind)
}

func (s *Server) handleDeleteInstanceLogo(c echo.Context) error {
	kind, err := instanceLogoKind(c)
	if err != nil {
		return err
	}
	return s.deleteInstanceImage(c, kind)
}

func (s *Server) setInstanceImage(c echo.Context, kind string) error {
	callerID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	in, cleanup, err := imageUploadInput(c)
	if err != nil {
		return err
	}
	defer cleanup()
	img, err := s.imagesvc.SetInstanceImage(c.Request().Context(), kind, in)
	if err != nil {
		return profileImageError(err, kind)
	}
	s.audit(c, observability.ActionAdminInstanceAssetUpdate, observability.ResultSuccess, callerID.String(), "kind="+kind)
	return c.JSON(http.StatusCreated, newProfileImageView(img))
}

func (s *Server) deleteInstanceImage(c echo.Context, kind string) error {
	callerID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	if err := s.imagesvc.DeleteInstanceImage(c.Request().Context(), kind); err != nil {
		return profileImageError(err, kind)
	}
	s.audit(c, observability.ActionAdminInstanceAssetDelete, observability.ResultSuccess, callerID.String(), "kind="+kind)
	return c.NoContent(http.StatusNoContent)
}

// handleGetInstanceImage serves an instance avatar/banner publicly, with the
// content type derived at upload time. 404 when none is set.
func (s *Server) handleGetInstanceImage(kind string) echo.HandlerFunc {
	return func(c echo.Context) error {
		return s.serveInstanceImage(c, kind)
	}
}

// handleGetInstanceLogo serves a typed logo slot publicly.
func (s *Server) handleGetInstanceLogo(c echo.Context) error {
	kind, err := instanceLogoKind(c)
	if err != nil {
		return err
	}
	return s.serveInstanceImage(c, kind)
}

func (s *Server) serveInstanceImage(c echo.Context, kind string) error {
	img, ok := s.imagesvc.InstanceImage(kind)
	if !ok {
		return echo.NewHTTPError(http.StatusNotFound, kind+" not found")
	}
	return s.serveStoredObjectNamed(c, img.StorageKey, img.ContentType, kind+" not found")
}
