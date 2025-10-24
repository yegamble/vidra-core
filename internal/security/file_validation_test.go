package security

import (
	"testing"
)

func TestValidateMagicBytes(t *testing.T) {
	tests := []struct {
		name    string
		content []byte
		ext     string
		wantErr bool
	}{
		{
			name:    "Valid JPEG",
			content: []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46, 0x49, 0x46},
			ext:     ".jpg",
			wantErr: false,
		},
		{
			name:    "Valid JPEG with .jpeg extension",
			content: []byte{0xFF, 0xD8, 0xFF, 0xE1},
			ext:     ".jpeg",
			wantErr: false,
		},
		{
			name:    "Valid PNG",
			content: []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00},
			ext:     ".png",
			wantErr: false,
		},
		{
			name:    "Valid GIF87a",
			content: []byte{0x47, 0x49, 0x46, 0x38, 0x37, 0x61},
			ext:     ".gif",
			wantErr: false,
		},
		{
			name:    "Valid GIF89a",
			content: []byte{0x47, 0x49, 0x46, 0x38, 0x39, 0x61},
			ext:     ".gif",
			wantErr: false,
		},
		{
			name:    "Valid WebP",
			content: []byte{0x52, 0x49, 0x46, 0x46, 0x00, 0x00, 0x00, 0x00, 0x57, 0x45, 0x42, 0x50},
			ext:     ".webp",
			wantErr: false,
		},
		{
			name:    "Valid TIFF Little Endian",
			content: []byte{0x49, 0x49, 0x2A, 0x00},
			ext:     ".tiff",
			wantErr: false,
		},
		{
			name:    "Valid TIFF Big Endian",
			content: []byte{0x4D, 0x4D, 0x00, 0x2A},
			ext:     ".tif",
			wantErr: false,
		},
		{
			name:    "Invalid - PNG with .jpg extension",
			content: []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A},
			ext:     ".jpg",
			wantErr: true,
		},
		{
			name:    "Invalid - JPEG with .png extension",
			content: []byte{0xFF, 0xD8, 0xFF, 0xE0},
			ext:     ".png",
			wantErr: true,
		},
		{
			name:    "Invalid - Random bytes",
			content: []byte{0x00, 0x00, 0x00, 0x00},
			ext:     ".jpg",
			wantErr: true,
		},
		{
			name:    "Invalid - Empty content",
			content: []byte{},
			ext:     ".jpg",
			wantErr: true,
		},
		{
			name:    "Invalid - File too small for WebP",
			content: []byte{0x52, 0x49, 0x46, 0x46},
			ext:     ".webp",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateMagicBytes(tt.content, tt.ext)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateMagicBytes() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestGetMimeTypeFromMagicBytes(t *testing.T) {
	tests := []struct {
		name    string
		content []byte
		want    string
	}{
		{
			name:    "JPEG",
			content: []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46, 0x49, 0x46, 0x00, 0x00},
			want:    "image/jpeg",
		},
		{
			name:    "PNG",
			content: []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x00},
			want:    "image/png",
		},
		{
			name:    "GIF",
			content: []byte{0x47, 0x49, 0x46, 0x38, 0x39, 0x61, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
			want:    "image/gif",
		},
		{
			name:    "WebP",
			content: []byte{0x52, 0x49, 0x46, 0x46, 0x00, 0x00, 0x00, 0x00, 0x57, 0x45, 0x42, 0x50},
			want:    "image/webp",
		},
		{
			name:    "Unknown",
			content: []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
			want:    "",
		},
		{
			name:    "Too short",
			content: []byte{0xFF, 0xD8},
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetMimeTypeFromMagicBytes(tt.content)
			if got != tt.want {
				t.Errorf("GetMimeTypeFromMagicBytes() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsAllowedImageExtension(t *testing.T) {
	tests := []struct {
		name string
		ext  string
		want bool
	}{
		{".jpg", ".jpg", true},
		{".jpeg", ".jpeg", true},
		{".png", ".png", true},
		{".gif", ".gif", true},
		{".webp", ".webp", true},
		{".heic", ".heic", true},
		{".tiff", ".tiff", true},
		{".exe", ".exe", false},
		{".php", ".php", false},
		{".js", ".js", false},
		{"jpg without dot", "jpg", true},
		{"JPG uppercase", "JPG", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsAllowedImageExtension(tt.ext)
			if got != tt.want {
				t.Errorf("IsAllowedImageExtension(%q) = %v, want %v", tt.ext, got, tt.want)
			}
		})
	}
}
