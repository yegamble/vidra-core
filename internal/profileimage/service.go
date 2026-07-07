// Package profileimage implements avatar and banner images for user accounts
// and channels: upload (replace-on-reupload), lookup, and removal, with blobs
// stored behind internal/storage at deterministic PeerTube-style keys (one
// top-level dir per asset kind — avatars/users/<id><ext>,
// banners/channels/<id><ext>; see .ralph/specs/storage-layout.md) and rows
// bookkept in the user_images/channel_images side tables. It mirrors the
// custom video-thumbnail upload gate: the accepted types are JPEG/PNG/WebP by
// extension, and the served Content-Type is derived from the extension —
// never from the client-declared type. HTTP-agnostic and testable without a
// server; ownership of channels is the caller's (handler's) concern.
package profileimage

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Image kinds. They double as the API path segment and the DB kind value.
const (
	KindAvatar = "avatar"
	KindBanner = "banner"
)

// Sentinel errors the HTTP layer maps to status codes.
var (
	// ErrNotFound means no image of that kind is set for the owner.
	ErrNotFound = errors.New("profileimage: not found")
	// ErrUnsupportedMedia means the uploaded file's extension is not an
	// accepted image type (JPEG/PNG/WebP).
	ErrUnsupportedMedia = errors.New("profileimage: unsupported media type")
	// ErrStorageUnavailable means no blob backend is configured (the routes are
	// only mounted when one is, so this is a guard, not a normal path).
	ErrStorageUnavailable = errors.New("profileimage: storage backend not configured")
)

// acceptedImageExts maps an upload extension to the content type served for it
// (mirrors the custom-thumbnail gate in internal/video). The served
// Content-Type is derived here (authoritative), not from the client-declared
// type, so a mislabelled upload can't set a bogus type.
var acceptedImageExts = map[string]string{
	".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png", ".webp": "image/webp",
}

// Repository is the data access the service needs. *sqlcgen.Queries satisfies
// it directly; tests substitute an in-memory fake.
type Repository interface {
	UpsertUserImage(ctx context.Context, arg sqlcgen.UpsertUserImageParams) (sqlcgen.UserImage, error)
	GetUserImage(ctx context.Context, arg sqlcgen.GetUserImageParams) (sqlcgen.UserImage, error)
	DeleteUserImage(ctx context.Context, arg sqlcgen.DeleteUserImageParams) (int64, error)
	UpsertChannelImage(ctx context.Context, arg sqlcgen.UpsertChannelImageParams) (sqlcgen.ChannelImage, error)
	GetChannelImage(ctx context.Context, arg sqlcgen.GetChannelImageParams) (sqlcgen.ChannelImage, error)
	DeleteChannelImage(ctx context.Context, arg sqlcgen.DeleteChannelImageParams) (int64, error)
}

// Mirror is the optional IPFS-mirror hook (fix_plan P19). After an authoritative
// image write or removal, the service calls it BEST-EFFORT so the mirror can
// enqueue an add+pin (or unpin) intent — a mirror error never fails the image
// operation (graceful degradation; the reconciliation scan is the backstop). A
// nil Mirror (the default) disables it. *ipfsmirror.Service satisfies this
// structurally, so this package does not import the mirror.
type Mirror interface {
	EnqueueUserImagePin(ctx context.Context, userID uuid.UUID, kind, objectKey string) error
	EnqueueChannelImagePin(ctx context.Context, channelID uuid.UUID, kind, objectKey string) error
	EnqueueUnpin(ctx context.Context, objectKey string) error
}

// Option customises the Service.
type Option func(*Service)

// WithMirror wires the optional IPFS-mirror hook.
func WithMirror(m Mirror) Option { return func(s *Service) { s.mirror = m } }

// Service holds the profile-image application logic.
type Service struct {
	repo   Repository
	blobs  storage.Backend
	mirror Mirror
}

// NewService builds the profile-image service. blobs should be the same
// backend the HTTP layer serves stored media from.
func NewService(repo Repository, blobs storage.Backend, opts ...Option) *Service {
	s := &Service{repo: repo, blobs: blobs}
	for _, o := range opts {
		o(s)
	}
	return s
}

// UploadInput is an image upload as read from the request: the declared
// filename (its extension is the type gate) and the content stream.
type UploadInput struct {
	Filename string
	Reader   io.Reader
}

// Image is the stored image's metadata, unified across user and channel
// images so callers handle one shape. The storage key is internal and not
// part of any API projection.
type Image struct {
	Kind        string
	StorageKey  string
	ContentType string
	SizeBytes   int64
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func fromUserImage(r sqlcgen.UserImage) Image {
	return Image{Kind: r.Kind, StorageKey: r.StorageKey, ContentType: r.ContentType,
		SizeBytes: r.SizeBytes, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}

func fromChannelImage(r sqlcgen.ChannelImage) Image {
	return Image{Kind: r.Kind, StorageKey: r.StorageKey, ContentType: r.ContentType,
		SizeBytes: r.SizeBytes, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}

// SetUserImage stores an avatar/banner for a user, replacing any previous one.
// A non-image extension → ErrUnsupportedMedia.
func (s *Service) SetUserImage(ctx context.Context, userID uuid.UUID, kind string, in UploadInput) (Image, error) {
	key, contentType, size, err := s.putImage(ctx, kind, "users", userID, in, func() (string, error) {
		old, err := s.repo.GetUserImage(ctx, sqlcgen.GetUserImageParams{UserID: userID, Kind: kind})
		return old.StorageKey, err
	})
	if err != nil {
		return Image{}, err
	}
	row, err := s.repo.UpsertUserImage(ctx, sqlcgen.UpsertUserImageParams{
		UserID: userID, Kind: kind, StorageKey: key, ContentType: contentType, SizeBytes: size,
	})
	if err != nil {
		return Image{}, err
	}
	if s.mirror != nil {
		_ = s.mirror.EnqueueUserImagePin(ctx, userID, kind, key) // best-effort
	}
	return fromUserImage(row), nil
}

// UserImage returns a user's stored avatar/banner metadata, or ErrNotFound.
func (s *Service) UserImage(ctx context.Context, userID uuid.UUID, kind string) (Image, error) {
	row, err := s.repo.GetUserImage(ctx, sqlcgen.GetUserImageParams{UserID: userID, Kind: kind})
	if err != nil {
		return Image{}, ErrNotFound
	}
	return fromUserImage(row), nil
}

// HasUserImage reports whether the user has an image of that kind set.
func (s *Service) HasUserImage(ctx context.Context, userID uuid.UUID, kind string) bool {
	_, err := s.repo.GetUserImage(ctx, sqlcgen.GetUserImageParams{UserID: userID, Kind: kind})
	return err == nil
}

// DeleteUserImage removes a user's avatar/banner: the row and the stored
// object. ErrNotFound when none is set.
func (s *Service) DeleteUserImage(ctx context.Context, userID uuid.UUID, kind string) error {
	img, err := s.UserImage(ctx, userID, kind)
	if err != nil {
		return err
	}
	rows, err := s.repo.DeleteUserImage(ctx, sqlcgen.DeleteUserImageParams{UserID: userID, Kind: kind})
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	return s.deleteBlob(ctx, img.StorageKey)
}

// SetChannelImage stores an avatar/banner for a channel, replacing any
// previous one. Ownership must already be checked by the caller.
func (s *Service) SetChannelImage(ctx context.Context, channelID uuid.UUID, kind string, in UploadInput) (Image, error) {
	key, contentType, size, err := s.putImage(ctx, kind, "channels", channelID, in, func() (string, error) {
		old, err := s.repo.GetChannelImage(ctx, sqlcgen.GetChannelImageParams{ChannelID: channelID, Kind: kind})
		return old.StorageKey, err
	})
	if err != nil {
		return Image{}, err
	}
	row, err := s.repo.UpsertChannelImage(ctx, sqlcgen.UpsertChannelImageParams{
		ChannelID: channelID, Kind: kind, StorageKey: key, ContentType: contentType, SizeBytes: size,
	})
	if err != nil {
		return Image{}, err
	}
	if s.mirror != nil {
		_ = s.mirror.EnqueueChannelImagePin(ctx, channelID, kind, key) // best-effort
	}
	return fromChannelImage(row), nil
}

// ChannelImage returns a channel's stored avatar/banner metadata, or ErrNotFound.
func (s *Service) ChannelImage(ctx context.Context, channelID uuid.UUID, kind string) (Image, error) {
	row, err := s.repo.GetChannelImage(ctx, sqlcgen.GetChannelImageParams{ChannelID: channelID, Kind: kind})
	if err != nil {
		return Image{}, ErrNotFound
	}
	return fromChannelImage(row), nil
}

// HasChannelImage reports whether the channel has an image of that kind set.
func (s *Service) HasChannelImage(ctx context.Context, channelID uuid.UUID, kind string) bool {
	_, err := s.repo.GetChannelImage(ctx, sqlcgen.GetChannelImageParams{ChannelID: channelID, Kind: kind})
	return err == nil
}

// DeleteChannelImage removes a channel's avatar/banner: the row and the stored
// object. ErrNotFound when none is set.
func (s *Service) DeleteChannelImage(ctx context.Context, channelID uuid.UUID, kind string) error {
	img, err := s.ChannelImage(ctx, channelID, kind)
	if err != nil {
		return err
	}
	rows, err := s.repo.DeleteChannelImage(ctx, sqlcgen.DeleteChannelImageParams{ChannelID: channelID, Kind: kind})
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	return s.deleteBlob(ctx, img.StorageKey)
}

// putImage runs the shared upload path: gate the extension, evict a previous
// blob left at a different key (the deterministic key varies with the
// extension, so replacing a .png with a .jpg must not orphan the old object),
// and store the new object. It returns the new key, the derived content type,
// and the stored size. oldKey reports the currently recorded storage key (its
// error means "none recorded" and is not fatal).
func (s *Service) putImage(ctx context.Context, kind, ownerDir string, id uuid.UUID, in UploadInput, oldKey func() (string, error)) (key, contentType string, size int64, err error) {
	if s.blobs == nil {
		return "", "", 0, ErrStorageUnavailable
	}
	contentType, ok := acceptedImageExt(in.Filename)
	if !ok {
		return "", "", 0, ErrUnsupportedMedia
	}
	key = imageKey(kind, ownerDir, id, strings.ToLower(filepath.Ext(in.Filename)))
	if old, oerr := oldKey(); oerr == nil && old != "" && old != key {
		_ = s.blobs.Delete(ctx, old) // best-effort: Delete is idempotent
		if s.mirror != nil {
			_ = s.mirror.EnqueueUnpin(ctx, old) // best-effort: unpin the superseded object
		}
	}
	size, err = s.blobs.Put(ctx, key, in.Reader)
	if err != nil {
		return "", "", 0, err
	}
	return key, contentType, size, nil
}

// deleteBlob removes a stored object; a missing backend is a guard error. It also
// best-effort enqueues an IPFS unpin so a removed avatar/banner is pulled off the
// mirror (this is the automatic unpin path for account deletion too, which routes
// its image removals through here).
func (s *Service) deleteBlob(ctx context.Context, key string) error {
	if s.blobs == nil {
		return ErrStorageUnavailable
	}
	if s.mirror != nil {
		_ = s.mirror.EnqueueUnpin(ctx, key) // best-effort
	}
	return s.blobs.Delete(ctx, key)
}

// acceptedImageExt returns the served content type for filename when it is an
// accepted image, and ok=false otherwise (the upload type gate).
func acceptedImageExt(filename string) (contentType string, ok bool) {
	ct, ok := acceptedImageExts[strings.ToLower(filepath.Ext(filename))]
	return ct, ok
}

// imageKey is the deterministic storage key for an owner's image:
// <kind-dir>/<ownerDir>/<id><ext>, e.g. avatars/users/<uuid>.png or
// banners/channels/<uuid>.webp. One top-level dir per asset kind per the
// storage layout spec; the id-based name is safe because assets are served
// through the API, never as static files.
func imageKey(kind, ownerDir string, id uuid.UUID, ext string) string {
	dir := "avatars"
	if kind == KindBanner {
		dir = "banners"
	}
	return dir + "/" + ownerDir + "/" + id.String() + ext
}
