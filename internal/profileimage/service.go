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
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Image kinds. They double as the API path segment and the DB kind value.
const (
	KindAvatar = "avatar"
	KindBanner = "banner"
)

// Instance logo slot kinds (config-parity W1). Together with KindAvatar and
// KindBanner they are the valid instance_images.kind values; the logo slots
// mirror PeerTube's LogoType enum and double as the /admin/instance-logo/{type}
// path segment.
const (
	KindLogoFavicon      = "favicon"
	KindLogoHeaderWide   = "header-wide"
	KindLogoHeaderSquare = "header-square"
	KindLogoOpengraph    = "opengraph"
)

// InstanceLogoKinds is the canonical logo-slot order (admin/OpenAPI order).
var InstanceLogoKinds = []string{KindLogoFavicon, KindLogoHeaderWide, KindLogoHeaderSquare, KindLogoOpengraph}

// instanceImageKinds is the full valid kind set for instance images.
var instanceImageKinds = map[string]bool{
	KindAvatar: true, KindBanner: true,
	KindLogoFavicon: true, KindLogoHeaderWide: true, KindLogoHeaderSquare: true, KindLogoOpengraph: true,
}

// IsInstanceImageKind reports whether kind is a valid instance-image kind
// (avatar, banner, or one of the four logo slots).
func IsInstanceImageKind(kind string) bool { return instanceImageKinds[kind] }

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

// InstanceRepository is the data access for the singleton instance branding
// images (config-parity W1; migration 0086). It is a SEPARATE, optional
// interface so existing Repository fakes keep compiling; *sqlcgen.Queries
// satisfies both.
type InstanceRepository interface {
	ListInstanceImages(ctx context.Context) ([]sqlcgen.InstanceImage, error)
	UpsertInstanceImage(ctx context.Context, arg sqlcgen.UpsertInstanceImageParams) (sqlcgen.InstanceImage, error)
	GetInstanceImage(ctx context.Context, kind string) (sqlcgen.InstanceImage, error)
	DeleteInstanceImage(ctx context.Context, kind string) (int64, error)
}

// Option customises the Service.
type Option func(*Service)

// WithMirror wires the optional IPFS-mirror hook.
func WithMirror(m Mirror) Option { return func(s *Service) { s.mirror = m } }

// WithInstanceImages wires the instance branding-image store (avatar, banner,
// and the four logo slots). When unset, the instance-image methods report not
// found / not configured. Call LoadInstanceImages once at boot to populate the
// read cache (GET /instance reads branding state per request, so lookups are
// lock-guarded map reads with no DB round trip — the instancesettings pattern).
func WithInstanceImages(r InstanceRepository) Option {
	return func(s *Service) { s.instRepo = r }
}

// WithVersionBump wires the cross-replica invalidation seam (see
// internal/settingsversion) for the INSTANCE branding cache only. Per-user and
// per-channel images are read from the database per request and need nothing
// here; the instance slots are the ones held in memory, so without this hook a
// new logo appears on the single api replica that served the upload and the
// header flickers between old and new depending on which replica answered. A
// nil bump (single-process wiring, unit fakes) disables the announcement.
func WithVersionBump(bump func(ctx context.Context) error) Option {
	return func(s *Service) { s.bump = bump }
}

// Service holds the profile-image application logic.
type Service struct {
	repo   Repository
	blobs  storage.Backend
	mirror Mirror

	// Instance branding images (optional; nil instRepo disables the surface).
	instRepo InstanceRepository
	instMu   sync.RWMutex
	instance map[string]Image // kind -> stored image metadata
	// bump announces an instance-branding write to the other api replicas.
	bump func(ctx context.Context) error
}

// NewService builds the profile-image service. blobs should be the same
// backend the HTTP layer serves stored media from.
func NewService(repo Repository, blobs storage.Backend, opts ...Option) *Service {
	s := &Service{repo: repo, blobs: blobs, instance: map[string]Image{}}
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

// --- instance branding images (config-parity W1) ---

// fromInstanceImage converts a DB row to the unified Image shape.
func fromInstanceImage(r sqlcgen.InstanceImage) Image {
	return Image{Kind: r.Kind, StorageKey: r.StorageKey, ContentType: r.ContentType,
		SizeBytes: r.SizeBytes, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}

// LoadInstanceImages (re)reads every instance-image row into the in-memory
// cache. Call once at boot; the write paths keep the cache current afterwards.
// A service without an instance repository loads an empty cache.
func (s *Service) LoadInstanceImages(ctx context.Context) error {
	if s.instRepo == nil {
		return nil
	}
	rows, err := s.instRepo.ListInstanceImages(ctx)
	if err != nil {
		return err
	}
	m := make(map[string]Image, len(rows))
	for _, r := range rows {
		if !instanceImageKinds[r.Kind] {
			continue // ignore unknown/legacy kinds defensively
		}
		m[r.Kind] = fromInstanceImage(r)
	}
	s.instMu.Lock()
	s.instance = m
	s.instMu.Unlock()
	return nil
}

// SetInstanceImage stores an instance branding image (avatar, banner, or a
// logo slot), replacing any previous one. Same extension gate and
// replace-evicts-old-blob semantics as the user/channel paths. Instance images
// are intentionally NOT IPFS-mirrored (they are instance-local branding).
func (s *Service) SetInstanceImage(ctx context.Context, kind string, in UploadInput) (Image, error) {
	if !instanceImageKinds[kind] {
		return Image{}, ErrNotFound
	}
	if s.instRepo == nil {
		return Image{}, ErrStorageUnavailable
	}
	if s.blobs == nil {
		return Image{}, ErrStorageUnavailable
	}
	contentType, ok := media.ContentTypeForImageExt(in.Filename)
	if !ok {
		return Image{}, ErrUnsupportedMedia
	}
	key := instanceImageKey(kind, strings.ToLower(filepath.Ext(in.Filename)))
	if old, exists := s.InstanceImage(kind); exists && old.StorageKey != "" && old.StorageKey != key {
		_ = s.blobs.Delete(ctx, old.StorageKey) // best-effort: Delete is idempotent
	}
	size, err := s.blobs.Put(ctx, key, in.Reader)
	if err != nil {
		return Image{}, err
	}
	row, err := s.instRepo.UpsertInstanceImage(ctx, sqlcgen.UpsertInstanceImageParams{
		Kind: kind, StorageKey: key, ContentType: contentType, SizeBytes: size,
	})
	if err != nil {
		return Image{}, err
	}
	img := fromInstanceImage(row)
	s.instMu.Lock()
	s.instance[kind] = img
	s.instMu.Unlock()
	return img, s.announce(ctx)
}

// announce advances the shared settings-version counter so the other api
// replicas reload the branding cache they filled at boot. The error is returned
// rather than swallowed: the row and the object are written either way, but a
// failed announcement leaves the rest of the fleet serving the old branding
// with nothing logged. Re-submitting the upload is idempotent (the storage key
// is deterministic per slot).
func (s *Service) announce(ctx context.Context) error {
	if s.bump == nil {
		return nil
	}
	return s.bump(ctx)
}

// InstanceImage returns the stored instance image for kind from the in-memory
// cache (no DB round trip — GET /instance reads branding state per request).
// ok=false when none is set.
func (s *Service) InstanceImage(kind string) (Image, bool) {
	s.instMu.RLock()
	img, ok := s.instance[kind]
	s.instMu.RUnlock()
	return img, ok
}

// DeleteInstanceImage removes an instance branding image: the row and the
// stored object. ErrNotFound when none is set.
func (s *Service) DeleteInstanceImage(ctx context.Context, kind string) error {
	if !instanceImageKinds[kind] {
		return ErrNotFound
	}
	if s.instRepo == nil {
		return ErrNotFound
	}
	img, ok := s.InstanceImage(kind)
	if !ok {
		return ErrNotFound
	}
	rows, err := s.instRepo.DeleteInstanceImage(ctx, kind)
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	s.instMu.Lock()
	delete(s.instance, kind)
	s.instMu.Unlock()
	if s.blobs == nil {
		return ErrStorageUnavailable
	}
	if err := s.blobs.Delete(ctx, img.StorageKey); err != nil {
		return err
	}
	return s.announce(ctx)
}

// instanceImageKey is the deterministic storage key for an instance image:
// the avatar/banner reuse the per-asset-kind top-level dirs from the user/
// channel layout with the singleton "instance" owner dir; logo slots live
// under logos/instance/<slot><ext>.
func instanceImageKey(kind, ext string) string {
	switch kind {
	case KindAvatar:
		return "avatars/instance/instance" + ext
	case KindBanner:
		return "banners/instance/instance" + ext
	default:
		return "logos/instance/" + kind + ext
	}
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
	contentType, ok := media.ContentTypeForImageExt(in.Filename)
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
