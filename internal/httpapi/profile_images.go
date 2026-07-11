package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/profileimage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// profileImageView is the public projection of a stored avatar/banner. The
// storage key is internal and deliberately not exposed.
type profileImageView struct {
	Kind        string    `json:"kind"`
	ContentType string    `json:"content_type"`
	SizeBytes   int64     `json:"size_bytes"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func newProfileImageView(img profileimage.Image) profileImageView {
	return profileImageView{
		Kind:        img.Kind,
		ContentType: img.ContentType,
		SizeBytes:   img.SizeBytes,
		CreatedAt:   img.CreatedAt,
		UpdatedAt:   img.UpdatedAt,
	}
}

// handleSetMyImage stores the caller's own avatar/banner (multipart form field
// "file", JPEG/PNG/WebP by extension → otherwise 415), replacing any previous
// one. Behind requireAuth. The global HTTP body limit bounds the upload (same
// cap as the custom video thumbnail).
func (s *Server) handleSetMyImage(kind string) echo.HandlerFunc {
	return func(c echo.Context) error {
		userID, _, ok := principalFromContext(c)
		if !ok {
			return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
		}
		in, cleanup, err := imageUploadInput(c)
		if err != nil {
			return err
		}
		defer cleanup()
		img, err := s.imagesvc.SetUserImage(c.Request().Context(), userID, kind, in)
		if err != nil {
			return profileImageError(err, kind)
		}
		return c.JSON(http.StatusCreated, newProfileImageView(img))
	}
}

// handleDeleteMyImage removes the caller's own avatar/banner (row + stored
// object). Behind requireAuth; 404 when none is set.
func (s *Server) handleDeleteMyImage(kind string) echo.HandlerFunc {
	return func(c echo.Context) error {
		userID, _, ok := principalFromContext(c)
		if !ok {
			return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
		}
		if err := s.imagesvc.DeleteUserImage(c.Request().Context(), userID, kind); err != nil {
			return profileImageError(err, kind)
		}
		return c.NoContent(http.StatusNoContent)
	}
}

// handleGetUserImage serves a user's avatar/banner publicly, with the content
// type derived at upload time. 404 when the user is unknown or has none set.
func (s *Server) handleGetUserImage(kind string) echo.HandlerFunc {
	return func(c echo.Context) error {
		id, err := uuid.Parse(c.Param("id"))
		if err != nil {
			return echo.NewHTTPError(http.StatusNotFound, kind+" not found")
		}
		img, err := s.imagesvc.UserImage(c.Request().Context(), id, kind)
		if err != nil {
			return profileImageError(err, kind)
		}
		return s.serveStoredObjectNamed(c, img.StorageKey, img.ContentType, kind+" not found")
	}
}

// handleSetChannelImage stores a channel's avatar/banner (owner only). A
// non-owner or unknown handle is 404 so the route does not reveal whether the
// caller could act on it. Same multipart/type gate as the user routes.
func (s *Server) handleSetChannelImage(kind string) echo.HandlerFunc {
	return func(c echo.Context) error {
		ch, err := s.ownedChannel(c)
		if err != nil {
			return err
		}
		in, cleanup, err := imageUploadInput(c)
		if err != nil {
			return err
		}
		defer cleanup()
		img, err := s.imagesvc.SetChannelImage(c.Request().Context(), ch.ID, kind, in)
		if err != nil {
			return profileImageError(err, kind)
		}
		return c.JSON(http.StatusCreated, newProfileImageView(img))
	}
}

// handleDeleteChannelImage removes a channel's avatar/banner (owner only;
// non-owner/unknown handle → 404). 404 when none is set.
func (s *Server) handleDeleteChannelImage(kind string) echo.HandlerFunc {
	return func(c echo.Context) error {
		ch, err := s.ownedChannel(c)
		if err != nil {
			return err
		}
		if err := s.imagesvc.DeleteChannelImage(c.Request().Context(), ch.ID, kind); err != nil {
			return profileImageError(err, kind)
		}
		return c.NoContent(http.StatusNoContent)
	}
}

// handleGetChannelImage serves a channel's avatar/banner publicly. 404 when
// the channel is unknown or has none set.
func (s *Server) handleGetChannelImage(kind string) echo.HandlerFunc {
	return func(c echo.Context) error {
		ch, err := s.channelsvc.GetByHandle(c.Request().Context(), c.Param("handle"))
		if err != nil {
			return channelError(err) // ErrNotFound -> 404
		}
		img, err := s.imagesvc.ChannelImage(c.Request().Context(), ch.ID, kind)
		if err != nil {
			return profileImageError(err, kind)
		}
		return s.serveStoredObjectNamed(c, img.StorageKey, img.ContentType, kind+" not found")
	}
}

// ownedChannel resolves the :handle channel and requires the caller to own it.
// Unknown handle AND non-owner both yield 404 (mirroring the videos convention:
// a mutating route does not reveal whether the caller could act on the target).
func (s *Server) ownedChannel(c echo.Context) (sqlcgen.Channel, error) {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return sqlcgen.Channel{}, echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	ch, err := s.channelsvc.GetByHandle(c.Request().Context(), c.Param("handle"))
	if err != nil || ch.OwnerID != userID {
		return sqlcgen.Channel{}, echo.NewHTTPError(http.StatusNotFound, "channel not found")
	}
	return ch, nil
}

// imageUploadInput reads the multipart "file" part into an UploadInput. The
// returned cleanup closes the part and must be deferred by the caller.
func imageUploadInput(c echo.Context) (profileimage.UploadInput, func(), error) {
	fh, err := c.FormFile("file")
	if err != nil {
		return profileimage.UploadInput{}, nil, echo.NewHTTPError(http.StatusBadRequest, `multipart form field "file" is required`)
	}
	f, err := fh.Open()
	if err != nil {
		return profileimage.UploadInput{}, nil, err
	}
	return profileimage.UploadInput{Filename: fh.Filename, Reader: f}, func() { _ = f.Close() }, nil
}

// profileImageError maps profile-image service sentinels to HTTP error
// envelopes. kind names the asset in the 404 message ("avatar not found").
func profileImageError(err error, kind string) error {
	switch {
	case errors.Is(err, profileimage.ErrNotFound):
		return echo.NewHTTPError(http.StatusNotFound, kind+" not found")
	case errors.Is(err, profileimage.ErrUnsupportedMedia):
		return echo.NewHTTPError(http.StatusUnsupportedMediaType, "unsupported media type")
	case errors.Is(err, profileimage.ErrStorageUnavailable):
		return echo.NewHTTPError(http.StatusServiceUnavailable, "media storage not configured")
	default:
		return err
	}
}

// attachUserImageFlags populates a user view's has_avatar/has_banner when the
// profile-image service is wired (nil-safe: the flags stay omitted otherwise).
func (s *Server) attachUserImageFlags(ctx context.Context, v *userView, userID uuid.UUID) {
	if s.imagesvc == nil {
		return
	}
	avatar := s.imagesvc.HasUserImage(ctx, userID, profileimage.KindAvatar)
	banner := s.imagesvc.HasUserImage(ctx, userID, profileimage.KindBanner)
	v.HasAvatar, v.HasBanner = &avatar, &banner
}

// channelViewFor builds a channel view, decorated with has_avatar/has_banner
// when the profile-image service is wired (the flags stay omitted otherwise).
func (s *Server) channelViewFor(ctx context.Context, ch sqlcgen.Channel, followerCount int64) channelView {
	v := newChannelView(ch, followerCount)
	if s.imagesvc == nil {
		return v
	}
	avatar := s.imagesvc.HasChannelImage(ctx, ch.ID, profileimage.KindAvatar)
	banner := s.imagesvc.HasChannelImage(ctx, ch.ID, profileimage.KindBanner)
	v.HasAvatar, v.HasBanner = &avatar, &banner
	return v
}
