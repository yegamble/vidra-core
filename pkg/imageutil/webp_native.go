//go:build webp

package imageutil

import (
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"reflect"

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

	// Use default options; callers can adjust quality later if needed.
	enc := nativewebp.NewEncoder()
	if err := enc.Encode(out, img); err != nil {
		return err
	}
	return nil
}

// EncodeFileToWebPWithQuality encodes with a quality hint when supported.
// If the underlying encoder ignores the value, the default is used.
func EncodeFileToWebPWithQuality(srcPath, dstPath string, quality int) error {
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

	enc := nativewebp.NewEncoder()
	// Best-effort: set quality via reflection if method exists
	if m := reflect.ValueOf(enc).MethodByName("SetQuality"); m.IsValid() {
		t := m.Type()
		if t.NumIn() == 1 {
			// Try int first, then float
			switch t.In(0).Kind() {
			case reflect.Int, reflect.Int32, reflect.Int64:
				m.Call([]reflect.Value{reflect.ValueOf(quality)})
			case reflect.Float32, reflect.Float64:
				m.Call([]reflect.Value{reflect.ValueOf(float64(quality))})
			}
		}
	}
	if err := enc.Encode(out, img); err != nil {
		return err
	}
	return nil
}
