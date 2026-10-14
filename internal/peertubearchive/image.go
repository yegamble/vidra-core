package peertubearchive

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/jpeg" // register decoders for image.DecodeConfig
	"image/png"
	"io"
	"net/http"
	"path"
	"strings"

	_ "golang.org/x/image/webp" // register the WebP decoder for image.DecodeConfig

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/profileimage"
)

// Channel image limits. maxImageBytes matches the avatar/banner upload cap
// (httpapi maxIdentityImageBytes); the total bounds what one import reads
// out of the (possibly multi-GB) archive.
const (
	maxImageBytes   = 8 << 20
	maxImageTotal   = 32 << 20
	maxImageSide    = 8192
	maxImagePixels  = 40_000_000
	imageSniffBytes = 512
)

var (
	_ = jpeg.Decode // keep the decoders registered
	_ = png.Decode
)

// ChannelImages is the EXISTING profile-image service, taken as an interface:
// the importer has no storage path of its own.
type ChannelImages interface {
	HasChannelImage(ctx context.Context, channelID uuid.UUID, kind string) bool
	SetChannelImage(ctx context.Context, channelID uuid.UUID, kind string, in profileimage.UploadInput) (profileimage.Image, error)
}

// ImageEntry resolves a channel's exported avatar/banner reference to the name
// of a regular entry under files/ with an accepted image extension, or "".
// The reference comes from attacker-controlled JSON, so it is only ever used as
// a lookup key into the zip's own entry table, never as a path.
func (a *Archive) ImageEntry(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" || len(ref) > maxNameLen {
		return ""
	}
	for _, name := range []string{ref, "files/" + ref} {
		f, ok := a.files[name]
		if !ok || !strings.HasPrefix(name, "files/") || strings.HasSuffix(name, "/") || !f.Mode().IsRegular() {
			continue
		}
		if _, ok := media.ContentTypeForImageExt(path.Base(name)); ok {
			return name
		}
	}
	return ""
}

// ReadImage reads one entry ImageEntry returned, through a hard byte cap
// enforced on the bytes produced (the declared size is not trusted), and
// vets it: the content must match the extension's type and decode within
// the dimension bounds. It returns the entry's base name for the type gate.
func (a *Archive) ReadImage(entry string, budget *int64) (name string, data []byte, err error) {
	f, ok := a.files[entry]
	if !ok || f.UncompressedSize64 > maxImageBytes || int64(f.UncompressedSize64) > *budget {
		return "", nil, errors.New("image entry unavailable or too large")
	}
	rc, err := f.Open()
	if err != nil {
		return "", nil, err
	}
	defer func() { _ = rc.Close() }()
	data, err = io.ReadAll(io.LimitReader(rc, maxImageBytes+1))
	if err != nil || len(data) > maxImageBytes {
		return "", nil, errors.New("image entry unreadable or too large")
	}
	*budget -= int64(len(data))
	name = path.Base(entry)
	if err := vetImage(name, data); err != nil {
		return "", nil, err
	}
	return name, data, nil
}

// vetImage is the sniff-and-bounds gate the plain upload route does not need
// (it trusts the signed-in uploader's own file; here the bytes come out of a
// third-party archive): the sniffed type must equal the extension's type, and
// the header must decode to a bounded size, so a renamed non-image or a
// decompression-bomb header never reaches the store.
func vetImage(name string, data []byte) error {
	want, ok := media.ContentTypeForImageExt(name)
	if !ok {
		return errors.New("unsupported image type")
	}
	head := data
	if len(head) > imageSniffBytes {
		head = head[:imageSniffBytes]
	}
	if http.DetectContentType(head) != want {
		return errors.New("content does not match the image type")
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 ||
		cfg.Width > maxImageSide || cfg.Height > maxImageSide || cfg.Width*cfg.Height > maxImagePixels {
		return errors.New("image dimensions out of bounds")
	}
	return nil
}

// importChannelImages sets a channel's exported avatar/banner when it has none.
// Absent references are skipped silently; a present-but-refused image (type,
// size, bounds, scan, storage) is counted and never aborts the import. An
// image the channel already has is never replaced, so a re-import changes nothing.
func (im *Importer) importChannelImages(ctx context.Context, userID uuid.UUID, a *Archive, ids map[string]uuid.UUID, sum *Summary) error {
	if im.Images == nil {
		return nil
	}
	budget := int64(maxImageTotal)
	for _, c := range a.Channels {
		id, ok := ids[strings.ToLower(strings.TrimSpace(c.Name))]
		if !ok || id == uuid.Nil {
			continue
		}
		for _, one := range [...]struct{ kind, ref string }{
			{profileimage.KindAvatar, c.ArchiveFiles.Avatar}, {profileimage.KindBanner, c.ArchiveFiles.Banner},
		} {
			if strings.TrimSpace(one.ref) == "" {
				continue
			}
			entry := a.ImageEntry(one.ref)
			if entry == "" {
				sum.ChannelImagesSkipped++ // named, but not a usable image entry
				continue
			}
			if im.Images.HasChannelImage(ctx, id, one.kind) {
				continue
			}
			name, data, err := a.ReadImage(entry, &budget)
			if err == nil && im.ScanImage != nil {
				err = im.ScanImage(ctx, "channel_"+one.kind, data)
			}
			if err != nil {
				sum.ChannelImagesSkipped++
				continue
			}
			_, err = im.Images.SetChannelImage(ctx, id, one.kind, profileimage.UploadInput{Filename: name, Reader: bytes.NewReader(data)})
			switch {
			case err == nil:
				sum.ChannelImagesSet++
			case errors.Is(err, profileimage.ErrUnsupportedMedia), errors.Is(err, profileimage.ErrStorageUnavailable):
				sum.ChannelImagesSkipped++
			default:
				return err
			}
		}
	}
	return nil
}
