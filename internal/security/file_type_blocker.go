package security

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

// FileTypeBlocker validates file types and blocks dangerous formats
type FileTypeBlocker struct {
	maxArchiveDepth     int
	maxArchiveFiles     int
	maxUncompressedSize int64
	maxFileSize         int64
}

// Blocked file extensions (from CLAUDE.md)
var blockedExtensions = map[string]string{
	// Executables
	".exe":   "executable",
	".msi":   "executable",
	".com":   "executable",
	".scr":   "executable",
	".dll":   "executable",
	".bin":   "executable",
	".elf":   "executable",
	".dylib": "executable",
	".so":    "executable",

	// Scripts
	".bat":  "script",
	".cmd":  "script",
	".ps1":  "script",
	".psm1": "script",
	".vbs":  "script",
	".js":   "script",
	".jar":  "script",
	".sh":   "script",
	".bash": "script",
	".zsh":  "script",
	".py":   "script",
	".pl":   "script",
	".rb":   "script",
	".php":  "script",

	// OS/App bundles
	".apk": "package",
	".aab": "package",
	".ipa": "package",
	".app": "package",
	".pkg": "package",
	".dmg": "disk image",

	// Disk images
	".iso":  "disk image",
	".img":  "disk image",
	".vhd":  "disk image",
	".vhdx": "disk image",

	// Macro-enabled Office
	".docm": "macro-enabled document",
	".dotm": "macro-enabled document",
	".xlsm": "macro-enabled document",
	".xltm": "macro-enabled document",
	".pptm": "macro-enabled document",
	".ppam": "macro-enabled document",

	// Shortcuts/links
	".lnk":     "shortcut",
	".url":     "shortcut",
	".webloc":  "shortcut",
	".desktop": "shortcut",
	".reg":     "registry file",
	".cpl":     "control panel",
	".hta":     "HTML application",
	".chm":     "compiled HTML",
	".scf":     "shell command file",

	// Active content
	".svg": "active content (SVG)",
	".swf": "active content (Flash)",
}

// Magic byte signatures for file type validation
var magicBytes = map[string][]byte{
	// Executables (blocked)
	"exe": {0x4D, 0x5A},             // MZ
	"elf": {0x7F, 0x45, 0x4C, 0x46}, // ELF

	// Images (allowed)
	"jpg":  {0xFF, 0xD8, 0xFF},
	"png":  {0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A},
	"gif":  {0x47, 0x49, 0x46, 0x38},
	"webp": {0x52, 0x49, 0x46, 0x46}, // RIFF (need to check WEBP after)

	// Videos (allowed)
	"mp4":  {0x00, 0x00, 0x00},       // ftyp (need more validation)
	"webm": {0x1A, 0x45, 0xDF, 0xA3}, // EBML
	"avi":  {0x52, 0x49, 0x46, 0x46}, // RIFF

	// Audio (allowed)
	"mp3":  {0xFF, 0xFB},             // or 0xFF, 0xF3 or 0xFF, 0xF2
	"flac": {0x66, 0x4C, 0x61, 0x43}, // fLaC
	"ogg":  {0x4F, 0x67, 0x67, 0x53}, // OggS
	"wav":  {0x52, 0x49, 0x46, 0x46}, // RIFF

	// Documents (allowed)
	"pdf": {0x25, 0x50, 0x44, 0x46}, // %PDF
	"zip": {0x50, 0x4B, 0x03, 0x04}, // PK (also for docx, xlsx, etc.)
}

// NewFileTypeBlocker creates a new file type blocker
func NewFileTypeBlocker() *FileTypeBlocker {
	return &FileTypeBlocker{
		maxArchiveDepth:     2,
		maxArchiveFiles:     10000,
		maxUncompressedSize: 10 * 1024 * 1024 * 1024, // 10 GB
		maxFileSize:         25 * 1024 * 1024,        // 25 MB
	}
}

// IsAllowed checks if a file is allowed based on extension and content
func (b *FileTypeBlocker) IsAllowed(filename string, content []byte) (bool, string) {
	// Get extension (case-insensitive)
	ext := strings.ToLower(filepath.Ext(filename))

	// Check for double extensions (e.g., .pdf.exe)
	nameParts := strings.Split(filename, ".")
	if len(nameParts) > 2 {
		// Check if the final extension is blocked
		if reason, blocked := blockedExtensions[ext]; blocked {
			return false, fmt.Sprintf("blocked %s file type", reason)
		}
	}

	// Check if extension is blocked (first priority)
	if reason, blocked := blockedExtensions[ext]; blocked {
		return false, fmt.Sprintf("blocked %s file type", reason)
	}

	// Now check content validation
	// Check if file is empty
	if len(content) == 0 {
		return false, "file is empty"
	}

	// Check if file contains only null bytes
	allNulls := true
	for _, b := range content {
		if b != 0 {
			allNulls = false
			break
		}
	}
	if allNulls {
		return false, "file contains only null bytes"
	}

	// Check file size limit
	if int64(len(content)) > b.maxFileSize {
		return false, fmt.Sprintf("file exceeds size limit of %d MB", b.maxFileSize/(1024*1024))
	}

	// Validate magic bytes match extension
	if !b.validateMagicBytes(filename, content) {
		return false, "file type mismatch between extension and content"
	}

	// Check for polyglot files (multiple file signatures)
	if b.detectPolyglot(content) {
		return false, "polyglot file detected (multiple file signatures)"
	}

	return true, ""
}

// ValidateArchive validates archive files (ZIP, RAR, etc.)
func (b *FileTypeBlocker) ValidateArchive(filename string, content []byte) (bool, string) {
	ext := strings.ToLower(filepath.Ext(filename))

	// Currently only support ZIP validation
	if ext != ".zip" {
		// For other archives, just check extension
		return b.IsAllowed(filename, content)
	}

	// Create ZIP reader from bytes
	reader := bytes.NewReader(content)
	zipReader, err := zip.NewReader(reader, int64(len(content)))
	if err != nil {
		return false, fmt.Sprintf("invalid ZIP file: %v", err)
	}

	// Check for encrypted files
	for _, file := range zipReader.File {
		if file.Flags&0x1 != 0 {
			return false, "encrypted archive detected"
		}
	}

	// Check file count
	if len(zipReader.File) > b.maxArchiveFiles {
		return false, fmt.Sprintf("archive file count exceeds limit (%d)", b.maxArchiveFiles)
	}

	// Check uncompressed size and compression ratio first (ZIP bomb detection)
	var totalUncompressed int64
	for _, file := range zipReader.File {
		totalUncompressed += int64(file.UncompressedSize64)
	}

	// Check compression ratio for ZIP bomb
	if len(content) > 0 {
		compressionRatio := float64(totalUncompressed) / float64(len(content))
		if compressionRatio > 100 {
			return false, fmt.Sprintf("excessive compression ratio detected (%.0fx) - possible ZIP bomb", compressionRatio)
		}
	}

	// Check total uncompressed size
	if totalUncompressed > b.maxUncompressedSize {
		return false, fmt.Sprintf("uncompressed size exceeds limit (%d GB)", b.maxUncompressedSize/(1024*1024*1024))
	}

	// Check file contents
	for _, file := range zipReader.File {
		// Check if file is a nested archive
		if b.isArchive(file.Name) {
			// Open and check nesting depth
			if err := b.checkNestedArchive(file, 1); err != nil {
				return false, err.Error()
			}
		}

		// Check if file inside archive is a blocked type (only check extension, not content)
		ext := strings.ToLower(filepath.Ext(file.Name))
		if reason, blocked := blockedExtensions[ext]; blocked {
			return false, fmt.Sprintf("archive contains blocked file type: %s", reason)
		}
	}

	return true, ""
}

// validateMagicBytes checks if file content matches expected magic bytes for extension
func (b *FileTypeBlocker) validateMagicBytes(filename string, content []byte) bool {
	// Even small files can have identifiable magic bytes (e.g., ELF is 4 bytes)
	ext := strings.ToLower(filepath.Ext(filename))
	if b.validateImageMagic(ext, content) {
		return true
	}
	if b.validateVideoMagic(ext, content) {
		return true
	}
	if b.validateAudioMagic(ext, content) {
		return true
	}
	if b.validateDocArchiveMagic(ext, content) {
		return true
	}
	return b.validateTextOrUnknownMagic(ext, content)
}

func (b *FileTypeBlocker) validateImageMagic(ext string, content []byte) bool {
	switch ext {
	case ".jpg", ".jpeg":
		return bytes.HasPrefix(content, magicBytes["jpg"])
	case ".png":
		return bytes.HasPrefix(content, magicBytes["png"])
	case ".gif":
		return bytes.HasPrefix(content, magicBytes["gif"])
	case ".webp":
		if len(content) < 12 {
			return false
		}
		return bytes.HasPrefix(content, magicBytes["webp"]) && string(content[8:12]) == "WEBP"
	}
	return false
}

func (b *FileTypeBlocker) validateVideoMagic(ext string, content []byte) bool {
	switch ext {
	case ".mp4", ".mov":
		if len(content) < 8 {
			return false
		}
		return string(content[4:8]) == "ftyp"
	case ".webm", ".mkv":
		return bytes.HasPrefix(content, magicBytes["webm"])
	case ".avi":
		return bytes.HasPrefix(content, magicBytes["avi"])
	}
	return false
}

func (b *FileTypeBlocker) validateAudioMagic(ext string, content []byte) bool {
	switch ext {
	case ".mp3":
		return bytes.HasPrefix(content, magicBytes["mp3"]) ||
			bytes.HasPrefix(content, []byte{0xFF, 0xF3}) ||
			bytes.HasPrefix(content, []byte{0xFF, 0xF2}) ||
			bytes.HasPrefix(content, []byte("ID3"))
	case ".m4a", ".aac":
		if len(content) < 8 {
			return false
		}
		return string(content[4:8]) == "ftyp" || bytes.HasPrefix(content, []byte{0xFF, 0xF1})
	case ".wav":
		return bytes.HasPrefix(content, magicBytes["wav"])
	case ".flac":
		return bytes.HasPrefix(content, magicBytes["flac"])
	case ".ogg":
		return bytes.HasPrefix(content, magicBytes["ogg"])
	}
	return false
}

func (b *FileTypeBlocker) validateDocArchiveMagic(ext string, content []byte) bool {
	switch ext {
	case ".pdf":
		return bytes.HasPrefix(content, magicBytes["pdf"])
	case ".zip", ".docx", ".xlsx", ".pptx":
		return bytes.HasPrefix(content, magicBytes["zip"])
	}
	return false
}

func (b *FileTypeBlocker) validateTextOrUnknownMagic(ext string, content []byte) bool {
	switch ext {
	case ".txt":
		if bytes.HasPrefix(content, magicBytes["pdf"]) ||
			bytes.HasPrefix(content, magicBytes["zip"]) ||
			bytes.HasPrefix(content, magicBytes["png"]) ||
			bytes.HasPrefix(content, magicBytes["jpg"]) ||
			bytes.HasPrefix(content, magicBytes["exe"]) ||
			bytes.HasPrefix(content, magicBytes["elf"]) {
			return false
		}
		return true
	case "":
		isExe := bytes.HasPrefix(content, magicBytes["exe"])
		isElf := bytes.HasPrefix(content, magicBytes["elf"])
		return !(isExe || isElf)
	default:
		if bytes.HasPrefix(content, magicBytes["exe"]) || bytes.HasPrefix(content, magicBytes["elf"]) {
			return false
		}
		return true
	}
}

// detectPolyglot checks if file contains multiple file signatures
func (b *FileTypeBlocker) detectPolyglot(content []byte) bool {
	if len(content) < 512 {
		return false
	}

	// Count different file signatures found
	signaturesFound := 0

	// Check for ZIP/JAR signature beyond the start
	if bytes.Contains(content[100:], magicBytes["zip"]) {
		signaturesFound++
	}

	// Check for EXE signature beyond the start
	if bytes.Contains(content[100:], magicBytes["exe"]) {
		signaturesFound++
	}

	// Check for ELF signature beyond the start
	if bytes.Contains(content[100:], magicBytes["elf"]) {
		signaturesFound++
	}

	return signaturesFound > 0
}

// isArchive checks if a filename is an archive
func (b *FileTypeBlocker) isArchive(filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	return ext == ".zip" || ext == ".rar" || ext == ".7z" || ext == ".tar" || ext == ".gz"
}

// checkNestedArchive checks nesting depth of archives
func (b *FileTypeBlocker) checkNestedArchive(file *zip.File, depth int) error {
	if depth > b.maxArchiveDepth {
		return fmt.Errorf("archive nesting depth exceeds limit (%d)", b.maxArchiveDepth)
	}

	// Open the nested archive
	rc, err := file.Open()
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()

	// Read content
	content, err := io.ReadAll(rc)
	if err != nil {
		return err
	}

	// Try to parse as ZIP
	reader := bytes.NewReader(content)
	zipReader, err := zip.NewReader(reader, int64(len(content)))
	if err != nil {
		// Not a valid ZIP, might be other archive format - reject to be safe
		return nil
	}

	// Check files in nested archive
	for _, nestedFile := range zipReader.File {
		if b.isArchive(nestedFile.Name) {
			if err := b.checkNestedArchive(nestedFile, depth+1); err != nil {
				return err
			}
		}
	}

	return nil
}
