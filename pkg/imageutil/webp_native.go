//go:build webp

package imageutil

import (
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"

	nativewebp "github.com/HugoSmits86/nativewebp"
)

// EncodeFileToWebP converts an image file to WebP and writes it to dst.
func EncodeFileToWebP(srcPath, dstPath string) error {
	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	img, _, err := image.Decode(f)
	if err != nil {
		return err
	}
	out, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()

	// Use default options (nil)
	if err := nativewebp.Encode(out, img, nil); err != nil {
		return err
	}
	return nil
}

// EncodeFileToWebPWithQuality encodes with a quality hint when supported.
// If the underlying encoder ignores the value, the default is used.
// Note: nativewebp is lossless (VP8L) only, so quality is ignored.
func EncodeFileToWebPWithQuality(srcPath, dstPath string, quality int) error {
	// For nativewebp, quality is ignored as it is a lossless encoder.
	// We just delegate to the base function.
	return EncodeFileToWebP(srcPath, dstPath)
}
