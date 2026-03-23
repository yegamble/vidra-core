//go:build !webp

package imageutil

// EncodeFileToWebP converts an image file to WebP and writes it to dst.
// In the default build, this is a stub that returns ErrWebPUnavailable.
func EncodeFileToWebP(srcPath, dstPath string) error {
	return ErrWebPUnavailable
}

// EncodeFileToWebPWithQuality allows specifying a quality hint; stubbed here.
func EncodeFileToWebPWithQuality(srcPath, dstPath string, quality int) error {
	return ErrWebPUnavailable
}
