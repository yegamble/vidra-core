package security

import (
	"archive/zip"
	"bytes"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFileTypeBlocker_RejectExecutables tests executable file blocking
func TestFileTypeBlocker_RejectExecutables(t *testing.T) {
	blocker := NewFileTypeBlocker()

	executables := []string{
		".exe", ".msi", ".com", ".scr", ".dll",
		".bin", ".elf", ".dylib", ".so",
	}

	for _, ext := range executables {
		t.Run("block_"+ext, func(t *testing.T) {
			allowed, reason := blocker.IsAllowed("test"+ext, []byte{})
			assert.False(t, allowed, "Executable %s should be blocked", ext)
			assert.Contains(t, reason, "executable")
		})
	}
}

// TestFileTypeBlocker_RejectScripts tests script file blocking
func TestFileTypeBlocker_RejectScripts(t *testing.T) {
	blocker := NewFileTypeBlocker()

	scripts := []string{
		".bat", ".cmd", ".ps1", ".psm1", ".vbs",
		".js", ".jar", ".sh", ".bash", ".zsh",
		".py", ".pl", ".rb", ".php",
	}

	for _, ext := range scripts {
		t.Run("block_"+ext, func(t *testing.T) {
			allowed, reason := blocker.IsAllowed("script"+ext, []byte{})
			assert.False(t, allowed, "Script %s should be blocked", ext)
			assert.Contains(t, reason, "script")
		})
	}
}

// TestFileTypeBlocker_RejectMacroDocuments tests macro-enabled Office blocking
func TestFileTypeBlocker_RejectMacroDocuments(t *testing.T) {
	blocker := NewFileTypeBlocker()

	macroFormats := []string{
		".docm", ".dotm", ".xlsm", ".xltm",
		".pptm", ".ppam",
	}

	for _, ext := range macroFormats {
		t.Run("block_"+ext, func(t *testing.T) {
			allowed, reason := blocker.IsAllowed("document"+ext, []byte{})
			assert.False(t, allowed, "Macro document %s should be blocked", ext)
			assert.Contains(t, reason, "macro")
		})
	}
}

// TestFileTypeBlocker_RejectDangerousFormats tests dangerous format blocking
func TestFileTypeBlocker_RejectDangerousFormats(t *testing.T) {
	blocker := NewFileTypeBlocker()

	dangerous := []string{
		".svg",     // Active content
		".swf",     // Flash
		".iso",     // Disk image
		".img",     // Disk image
		".vhd",     // Virtual disk
		".vhdx",    // Virtual disk
		".apk",     // Android package
		".ipa",     // iOS package
		".app",     // macOS app
		".pkg",     // Package
		".dmg",     // macOS disk image
		".lnk",     // Shortcut
		".url",     // Internet shortcut
		".webloc",  // macOS web location
		".desktop", // Linux desktop entry
		".reg",     // Registry file
		".cpl",     // Control panel
		".hta",     // HTML application
		".chm",     // Compiled HTML
		".scf",     // Shell command file
	}

	for _, ext := range dangerous {
		t.Run("block_"+ext, func(t *testing.T) {
			allowed, reason := blocker.IsAllowed("file"+ext, []byte{})
			assert.False(t, allowed, "Dangerous format %s should be blocked", ext)
			assert.NotEmpty(t, reason)
		})
	}
}

// TestFileTypeBlocker_AllowLegitimateVideos tests legitimate video formats
func TestFileTypeBlocker_AllowLegitimateVideos(t *testing.T) {
	blocker := NewFileTypeBlocker()

	videos := []struct {
		ext   string
		magic []byte
	}{
		{".mp4", []byte{0x00, 0x00, 0x00, 0x18, 0x66, 0x74, 0x79, 0x70}}, // ftyp
		{".mov", []byte{0x00, 0x00, 0x00, 0x14, 0x66, 0x74, 0x79, 0x70}}, // ftyp
		{".webm", []byte{0x1A, 0x45, 0xDF, 0xA3}},                        // EBML
		{".avi", []byte{0x52, 0x49, 0x46, 0x46}},                         // RIFF
		{".mkv", []byte{0x1A, 0x45, 0xDF, 0xA3}},                         // EBML
	}

	for _, v := range videos {
		t.Run("allow_"+v.ext, func(t *testing.T) {
			content := make([]byte, 512)
			copy(content, v.magic)

			allowed, reason := blocker.IsAllowed("video"+v.ext, content)
			assert.True(t, allowed, "Video %s should be allowed", v.ext)
			assert.Empty(t, reason)
		})
	}
}

// TestFileTypeBlocker_AllowLegitimateImages tests legitimate image formats
func TestFileTypeBlocker_AllowLegitimateImages(t *testing.T) {
	blocker := NewFileTypeBlocker()

	images := []struct {
		ext   string
		magic []byte
	}{
		{".jpg", []byte{0xFF, 0xD8, 0xFF}},
		{".png", []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}},
		{".gif", []byte{0x47, 0x49, 0x46, 0x38, 0x39, 0x61}},
		{".webp", []byte{0x52, 0x49, 0x46, 0x46, 0x00, 0x00, 0x00, 0x00, 0x57, 0x45, 0x42, 0x50}},
	}

	for _, img := range images {
		t.Run("allow_"+img.ext, func(t *testing.T) {
			content := make([]byte, 512)
			copy(content, img.magic)

			allowed, reason := blocker.IsAllowed("image"+img.ext, content)
			assert.True(t, allowed, "Image %s should be allowed", img.ext)
			assert.Empty(t, reason)
		})
	}
}

// TestFileTypeBlocker_AllowLegitimateDocuments tests legitimate document formats
func TestFileTypeBlocker_AllowLegitimateDocuments(t *testing.T) {
	blocker := NewFileTypeBlocker()

	documents := []struct {
		ext   string
		magic []byte
	}{
		{".pdf", []byte{0x25, 0x50, 0x44, 0x46}},  // %PDF
		{".docx", []byte{0x50, 0x4B, 0x03, 0x04}}, // PK (ZIP)
		{".xlsx", []byte{0x50, 0x4B, 0x03, 0x04}}, // PK (ZIP)
		{".pptx", []byte{0x50, 0x4B, 0x03, 0x04}}, // PK (ZIP)
		{".txt", []byte("This is plain text")},
	}

	for _, doc := range documents {
		t.Run("allow_"+doc.ext, func(t *testing.T) {
			content := make([]byte, 512)
			copy(content, doc.magic)

			allowed, reason := blocker.IsAllowed("document"+doc.ext, content)
			assert.True(t, allowed, "Document %s should be allowed", doc.ext)
			assert.Empty(t, reason)
		})
	}
}

// TestFileTypeBlocker_AllowLegitimateAudio tests legitimate audio formats
func TestFileTypeBlocker_AllowLegitimateAudio(t *testing.T) {
	blocker := NewFileTypeBlocker()

	audio := []struct {
		ext   string
		magic []byte
	}{
		{".mp3", []byte{0xFF, 0xFB}},                                     // MP3 frame sync
		{".wav", []byte{0x52, 0x49, 0x46, 0x46}},                         // RIFF
		{".m4a", []byte{0x00, 0x00, 0x00, 0x20, 0x66, 0x74, 0x79, 0x70}}, // ftyp
		{".aac", []byte{0xFF, 0xF1}},                                     // AAC ADTS
		{".flac", []byte{0x66, 0x4C, 0x61, 0x43}},                        // fLaC
		{".ogg", []byte{0x4F, 0x67, 0x67, 0x53}},                         // OggS
	}

	for _, aud := range audio {
		t.Run("allow_"+aud.ext, func(t *testing.T) {
			content := make([]byte, 512)
			copy(content, aud.magic)

			allowed, reason := blocker.IsAllowed("audio"+aud.ext, content)
			assert.True(t, allowed, "Audio %s should be allowed", aud.ext)
			assert.Empty(t, reason)
		})
	}
}

// TestFileTypeBlocker_ExtensionMagicMismatch tests extension/magic byte mismatch
func TestFileTypeBlocker_ExtensionMagicMismatch(t *testing.T) {
	blocker := NewFileTypeBlocker()

	tests := []struct {
		name     string
		filename string
		content  []byte
	}{
		{
			name:     "mp4 claimed but zip magic",
			filename: "video.mp4",
			content:  []byte{0x50, 0x4B, 0x03, 0x04}, // ZIP
		},
		{
			name:     "jpg claimed but exe magic",
			filename: "image.jpg",
			content:  []byte{0x4D, 0x5A}, // MZ (EXE)
		},
		{
			name:     "txt claimed but pdf magic",
			filename: "document.txt",
			content:  []byte{0x25, 0x50, 0x44, 0x46}, // %PDF
		},
		{
			name:     "png claimed but elf magic",
			filename: "image.png",
			content:  []byte{0x7F, 0x45, 0x4C, 0x46}, // ELF
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content := make([]byte, 512)
			copy(content, tt.content)

			allowed, reason := blocker.IsAllowed(tt.filename, content)
			assert.False(t, allowed, "Mismatched file should be blocked")
			assert.Contains(t, reason, "mismatch")
		})
	}
}

// TestFileTypeBlocker_PolyglotDetection tests polyglot file detection
func TestFileTypeBlocker_PolyglotDetection(t *testing.T) {
	blocker := NewFileTypeBlocker()

	// Polyglot: valid GIF + JAR
	polyglot := make([]byte, 1024)
	copy(polyglot[0:], []byte{0x47, 0x49, 0x46, 0x38, 0x39, 0x61}) // GIF89a
	copy(polyglot[512:], []byte{0x50, 0x4B, 0x03, 0x04})           // ZIP/JAR at offset

	allowed, reason := blocker.IsAllowed("image.gif", polyglot)
	assert.False(t, allowed, "Polyglot file should be detected and blocked")
	assert.Contains(t, reason, "polyglot")
}

// TestFileTypeBlocker_ZIPNestingDepth tests ZIP nesting depth limits
func TestFileTypeBlocker_ZIPNestingDepth(t *testing.T) {
	blocker := NewFileTypeBlocker()

	// Create nested ZIP
	nestedZIP := createNestedZIP(t, 5) // 5 levels deep

	allowed, reason := blocker.ValidateArchive("archive.zip", nestedZIP)
	assert.False(t, allowed, "Deeply nested ZIP should be blocked")
	assert.Contains(t, reason, "nesting")
}

// TestFileTypeBlocker_ZIPFileCountLimit tests ZIP file count limits
func TestFileTypeBlocker_ZIPFileCountLimit(t *testing.T) {
	blocker := NewFileTypeBlocker()

	// Create ZIP with just enough files to exceed the limit.
	largeZIP := createZIPWithManyFiles(t, 10001)

	allowed, reason := blocker.ValidateArchive("archive.zip", largeZIP)
	assert.False(t, allowed, "ZIP with too many files should be blocked")
	assert.Contains(t, reason, "file count")
}

// TestFileTypeBlocker_ZIPBombDetection tests ZIP bomb (compression ratio) detection
func TestFileTypeBlocker_ZIPBombDetection(t *testing.T) {
	blocker := NewFileTypeBlocker()

	// Create ZIP with excessive compression ratio
	zipBomb := createZIPBomb(t)

	allowed, reason := blocker.ValidateArchive("archive.zip", zipBomb)
	assert.False(t, allowed, "ZIP bomb should be detected and blocked")
	assert.Contains(t, reason, "compression ratio")
}

// TestFileTypeBlocker_EncryptedArchiveRejection tests encrypted archive blocking
func TestFileTypeBlocker_EncryptedArchiveRejection(t *testing.T) {
	blocker := NewFileTypeBlocker()

	// Create encrypted ZIP
	encryptedZIP := createEncryptedZIP(t)

	allowed, reason := blocker.ValidateArchive("archive.zip", encryptedZIP)
	assert.False(t, allowed, "Encrypted archive should be blocked")
	assert.Contains(t, reason, "encrypted")
}

// TestFileTypeBlocker_ArchiveWithBlockedTypes tests archive containing blocked types
func TestFileTypeBlocker_ArchiveWithBlockedTypes(t *testing.T) {
	blocker := NewFileTypeBlocker()

	// Create ZIP containing .exe
	dangerousZIP := createZIPWithFiles(t, map[string][]byte{
		"readme.txt":   []byte("Hello"),
		"malware.exe":  {0x4D, 0x5A}, // MZ header
		"document.pdf": {0x25, 0x50, 0x44, 0x46},
	})

	allowed, reason := blocker.ValidateArchive("archive.zip", dangerousZIP)
	assert.False(t, allowed, "ZIP containing blocked file types should be rejected")
	assert.Contains(t, reason, "contains blocked file")
}

// TestFileTypeBlocker_ValidArchive tests legitimate archives
func TestFileTypeBlocker_ValidArchive(t *testing.T) {
	blocker := NewFileTypeBlocker()

	// Create valid ZIP with allowed files
	validZIP := createZIPWithFiles(t, map[string][]byte{
		"readme.txt":   []byte("Project documentation"),
		"image.png":    {0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A},
		"document.pdf": {0x25, 0x50, 0x44, 0x46},
	})

	allowed, reason := blocker.ValidateArchive("archive.zip", validZIP)
	assert.True(t, allowed, "Valid archive should be allowed")
	assert.Empty(t, reason)
}

// TestFileTypeBlocker_CaseInsensitiveExtensions tests case-insensitive extension matching
func TestFileTypeBlocker_CaseInsensitiveExtensions(t *testing.T) {
	blocker := NewFileTypeBlocker()

	tests := []struct {
		filename string
		allowed  bool
	}{
		{"malware.EXE", false},
		{"malware.exe", false},
		{"malware.Exe", false},
		{"script.BAT", false},
		{"script.bat", false},
		{"video.MP4", true},
		{"video.mp4", true},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			content := make([]byte, 512)
			if tt.allowed {
				// Add valid MP4 magic
				copy(content, []byte{0x00, 0x00, 0x00, 0x18, 0x66, 0x74, 0x79, 0x70})
			}

			allowed, _ := blocker.IsAllowed(tt.filename, content)
			assert.Equal(t, tt.allowed, allowed)
		})
	}
}

// TestFileTypeBlocker_MultipleExtensions tests double extension tricks
func TestFileTypeBlocker_MultipleExtensions(t *testing.T) {
	blocker := NewFileTypeBlocker()

	malicious := []string{
		"document.pdf.exe",
		"image.jpg.bat",
		"video.mp4.scr",
		"file.txt.ps1",
	}

	for _, filename := range malicious {
		t.Run(filename, func(t *testing.T) {
			allowed, reason := blocker.IsAllowed(filename, []byte{})
			assert.False(t, allowed, "File with malicious extension should be blocked")
			assert.NotEmpty(t, reason)
		})
	}
}

// TestFileTypeBlocker_NoExtension tests files without extensions
func TestFileTypeBlocker_NoExtension(t *testing.T) {
	blocker := NewFileTypeBlocker()

	tests := []struct {
		filename string
		content  []byte
		allowed  bool
	}{
		{
			filename: "Makefile",
			content:  []byte("all:\n\techo 'build'"),
			allowed:  true,
		},
		{
			filename: "README",
			content:  []byte("# Project README"),
			allowed:  true,
		},
		{
			filename: "binary_executable",
			content:  []byte{0x7F, 0x45, 0x4C, 0x46}, // ELF
			allowed:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			allowed, _ := blocker.IsAllowed(tt.filename, tt.content)
			assert.Equal(t, tt.allowed, allowed)
		})
	}
}

// TestFileTypeBlocker_EmptyFile tests empty file handling
func TestFileTypeBlocker_EmptyFile(t *testing.T) {
	blocker := NewFileTypeBlocker()

	allowed, reason := blocker.IsAllowed("empty.txt", []byte{})
	assert.False(t, allowed, "Empty file should be blocked")
	assert.Contains(t, reason, "empty")
}

// TestFileTypeBlocker_NullBytes tests files with null bytes
func TestFileTypeBlocker_NullBytes(t *testing.T) {
	blocker := NewFileTypeBlocker()

	content := []byte{0x00, 0x00, 0x00, 0x00}
	allowed, reason := blocker.IsAllowed("file.txt", content)
	assert.False(t, allowed, "File with only null bytes should be blocked")
	assert.NotEmpty(t, reason)
}

// TestFileTypeBlocker_MaxFileSizeCheck tests file size limits
func TestFileTypeBlocker_MaxFileSizeCheck(t *testing.T) {
	blocker := NewFileTypeBlocker()

	// Create 26MB file (over 25MB limit)
	largeContent := make([]byte, 26*1024*1024)
	copy(largeContent, []byte{0xFF, 0xD8, 0xFF}) // JPEG magic

	allowed, reason := blocker.IsAllowed("large.jpg", largeContent)
	assert.False(t, allowed, "File over size limit should be blocked")
	assert.Contains(t, reason, "size limit")
}

// Helper functions

func createNestedZIP(t *testing.T, depth int) []byte {
	t.Helper()

	if depth == 0 {
		// Base case: simple text file
		return []byte("innermost file")
	}

	// Create a ZIP containing another ZIP
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)

	innerZIP := createNestedZIP(t, depth-1)

	fw, err := zw.Create("nested.zip")
	require.NoError(t, err)

	_, err = fw.Write(innerZIP)
	require.NoError(t, err)

	err = zw.Close()
	require.NoError(t, err)

	return buf.Bytes()
}

func createZIPWithManyFiles(t *testing.T, count int) []byte {
	t.Helper()

	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)

	for i := 0; i < count; i++ {
		filename := filepath.Join("dir", "file_"+string(rune(i))+".txt")
		fw, err := zw.Create(filename)
		require.NoError(t, err)

		_, err = fw.Write([]byte("content"))
		require.NoError(t, err)
	}

	err := zw.Close()
	require.NoError(t, err)

	return buf.Bytes()
}

func createZIPBomb(t *testing.T) []byte {
	t.Helper()

	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)

	// Create highly compressible content that still exceeds the 100x ratio check.
	fw, err := zw.Create("bomb.txt")
	require.NoError(t, err)

	zeros := make([]byte, 2*1024*1024) // 2MB of zeros
	_, err = fw.Write(zeros)
	require.NoError(t, err)

	err = zw.Close()
	require.NoError(t, err)

	return buf.Bytes()
}

func createEncryptedZIP(t *testing.T) []byte {
	t.Helper()

	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)

	// Create encrypted file header
	fh := &zip.FileHeader{
		Name:   "encrypted.txt",
		Method: zip.Deflate,
		Flags:  0x1, // Encrypted flag
	}

	fw, err := zw.CreateHeader(fh)
	require.NoError(t, err)

	_, err = fw.Write([]byte("encrypted content"))
	require.NoError(t, err)

	err = zw.Close()
	require.NoError(t, err)

	return buf.Bytes()
}

func createZIPWithFiles(t *testing.T, files map[string][]byte) []byte {
	t.Helper()

	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)

	for filename, content := range files {
		fw, err := zw.Create(filename)
		require.NoError(t, err)

		_, err = fw.Write(content)
		require.NoError(t, err)
	}

	err := zw.Close()
	require.NoError(t, err)

	return buf.Bytes()
}
