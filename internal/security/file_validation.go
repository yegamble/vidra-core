package security

import (
	"bytes"
	"fmt"
	"strings"
)

// FileSignature represents a file format's magic bytes signature
type FileSignature struct {
	Offset int
	Magic  []byte
	Ext    string
}

// Common image file signatures (magic bytes)
var imageSignatures = []FileSignature{
	// JPEG
	{0, []byte{0xFF, 0xD8, 0xFF}, ".jpg"},
	{0, []byte{0xFF, 0xD8, 0xFF}, ".jpeg"},

	// PNG
	{0, []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}, ".png"},

	// GIF
	{0, []byte{0x47, 0x49, 0x46, 0x38, 0x37, 0x61}, ".gif"}, // GIF87a
	{0, []byte{0x47, 0x49, 0x46, 0x38, 0x39, 0x61}, ".gif"}, // GIF89a

	// WebP
	{8, []byte{0x57, 0x45, 0x42, 0x50}, ".webp"}, // "WEBP" at offset 8

	// TIFF (Little Endian)
	{0, []byte{0x49, 0x49, 0x2A, 0x00}, ".tiff"},
	{0, []byte{0x49, 0x49, 0x2A, 0x00}, ".tif"},

	// TIFF (Big Endian)
	{0, []byte{0x4D, 0x4D, 0x00, 0x2A}, ".tiff"},
	{0, []byte{0x4D, 0x4D, 0x00, 0x2A}, ".tif"},

	// BMP
	{0, []byte{0x42, 0x4D}, ".bmp"},

	// ICO
	{0, []byte{0x00, 0x00, 0x01, 0x00}, ".ico"},

	// HEIC/HEIF (ftyp box with heic/heif brand)
	// Note: HEIC is complex, basic check for ftyp at offset 4
	{4, []byte{0x66, 0x74, 0x79, 0x70, 0x68, 0x65, 0x69, 0x63}, ".heic"}, // ftyp heic
	{4, []byte{0x66, 0x74, 0x79, 0x70, 0x68, 0x65, 0x69, 0x78}, ".heic"}, // ftyp heix
	{4, []byte{0x66, 0x74, 0x79, 0x70, 0x68, 0x65, 0x69, 0x66}, ".heif"}, // ftyp heif
	{4, []byte{0x66, 0x74, 0x79, 0x70, 0x6D, 0x69, 0x66, 0x31}, ".heif"}, // ftyp mif1
}

// ValidateMagicBytes checks if the file content matches expected magic bytes for the given extension
// SECURITY: This provides defense-in-depth against file upload attacks where attackers
// rename malicious files to bypass extension-only validation
func ValidateMagicBytes(content []byte, ext string) error {
	if len(content) == 0 {
		return fmt.Errorf("empty file content")
	}

	// Normalize extension to lowercase with leading dot
	ext = strings.ToLower(ext)
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}

	// Check magic bytes strictly for all formats
	found := false
	for _, sig := range imageSignatures {
		// Check if this is a jpg/jpeg equivalence case
		isJpegMatch := (ext == ".jpeg" && sig.Ext == ".jpg") || (ext == ".jpg" && sig.Ext == ".jpeg")

		// Only check signatures for the claimed extension (allowing jpg/jpeg equivalence)
		if sig.Ext != ext && !isJpegMatch {
			continue
		}

		// Check if file is large enough
		if len(content) < sig.Offset+len(sig.Magic) {
			continue
		}

		// Extract bytes at offset
		fileBytes := content[sig.Offset : sig.Offset+len(sig.Magic)]

		// Compare magic bytes
		if bytes.Equal(fileBytes, sig.Magic) {
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("file content does not match extension %s", ext)
	}

	return nil
}

// GetMimeTypeFromMagicBytes determines the MIME type from magic bytes
// Returns empty string if unable to determine
func GetMimeTypeFromMagicBytes(content []byte) string {
	if len(content) < 12 {
		return ""
	}

	// JPEG
	if bytes.HasPrefix(content, []byte{0xFF, 0xD8, 0xFF}) {
		return "image/jpeg"
	}

	// PNG
	if bytes.HasPrefix(content, []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}) {
		return "image/png"
	}

	// GIF
	if bytes.HasPrefix(content, []byte{0x47, 0x49, 0x46, 0x38}) {
		return "image/gif"
	}

	// WebP (RIFF container + WEBP at offset 8)
	if bytes.HasPrefix(content, []byte{0x52, 0x49, 0x46, 0x46}) && bytes.Equal(content[8:12], []byte{0x57, 0x45, 0x42, 0x50}) {
		return "image/webp"
	}

	// TIFF
	if bytes.HasPrefix(content, []byte{0x49, 0x49, 0x2A, 0x00}) || bytes.HasPrefix(content, []byte{0x4D, 0x4D, 0x00, 0x2A}) {
		return "image/tiff"
	}

	// BMP
	if bytes.HasPrefix(content, []byte{0x42, 0x4D}) {
		return "image/bmp"
	}

	// HEIC/HEIF
	if len(content) >= 12 && bytes.Equal(content[4:8], []byte{0x66, 0x74, 0x79, 0x70}) {
		brand := string(content[8:12])
		if brand == "heic" || brand == "heix" {
			return "image/heic"
		}
		if brand == "heif" || brand == "mif1" {
			return "image/heif"
		}
	}

	return ""
}

// IsAllowedImageExtension checks if the extension is in the allowed list
func IsAllowedImageExtension(ext string) bool {
	ext = strings.ToLower(ext)
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}

	allowed := map[string]bool{
		".jpg":  true,
		".jpeg": true,
		".png":  true,
		".gif":  true,
		".webp": true,
		".heic": true,
		".heif": true,
		".tiff": true,
		".tif":  true,
		".bmp":  true,
	}

	return allowed[ext]
}
