//go:build webp
// +build webp

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
    defer f.Close()
    img, _, err := image.Decode(f)
    if err != nil {
        return err
    }
    out, err := os.Create(dstPath)
    if err != nil {
        return err
    }
    defer out.Close()

    // Use default options; callers can adjust quality later if needed.
    enc := nativewebp.NewEncoder()
    if err := enc.Encode(out, img); err != nil {
        return err
    }
    return nil
}

