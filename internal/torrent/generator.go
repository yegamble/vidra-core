package torrent

import (
	"context"
	"crypto/sha1" // #nosec G505 - BitTorrent v1 requires SHA1
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"vidra-core/internal/domain"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/google/uuid"
)

// Generator creates torrent files for video content
type Generator struct {
	config *GeneratorConfig
}

// GeneratorConfig holds configuration for torrent generation
type GeneratorConfig struct {
	// PieceLength is the size of each piece in bytes (default: 256KB for WebTorrent)
	PieceLength int64
	// Trackers is a list of tracker announce URLs
	Trackers []string
	// WebSeeds is a list of HTTP URLs for web seeding
	WebSeeds []string
	// Comment to add to the torrent
	Comment string
	// CreatedBy identifies the software that created the torrent
	CreatedBy string
	// Private indicates if this is a private torrent
	Private bool
	// BaseURL for constructing web seeds
	BaseURL string
}

// DefaultGeneratorConfig returns a default configuration for WebTorrent compatibility
func DefaultGeneratorConfig() *GeneratorConfig {
	return &GeneratorConfig{
		PieceLength: 262144, // 256KB - optimal for WebTorrent
		CreatedBy:   "Vidra Core/1.0",
		Private:     false,
		Trackers: []string{
			"wss://tracker.openwebtorrent.com",
			"wss://tracker.btorrent.xyz",
			"wss://tracker.fastcast.nz",
		},
	}
}

// NewGenerator creates a new torrent generator
func NewGenerator(config *GeneratorConfig) *Generator {
	if config == nil {
		config = DefaultGeneratorConfig()
	}
	if config.PieceLength == 0 {
		config.PieceLength = 262144 // 256KB default
	}
	if config.CreatedBy == "" {
		config.CreatedBy = "Vidra Core/1.0"
	}
	return &Generator{config: config}
}

// TorrentInfo contains information about a generated torrent
type TorrentInfo struct {
	InfoHash    string
	MagnetURI   string
	TorrentFile []byte
	TotalSize   int64
	FileCount   int
	PieceCount  int
}

// GenerateFromVideo creates a torrent from video files
func (g *Generator) GenerateFromVideo(ctx context.Context, videoID uuid.UUID, files []VideoFile) (*TorrentInfo, error) {
	if len(files) == 0 {
		return nil, fmt.Errorf("no files provided for torrent generation")
	}

	// Sort files for consistent torrent generation
	sort.Slice(files, func(i, j int) bool {
		return files[i].Path < files[j].Path
	})

	// Create metainfo
	info := metainfo.Info{
		PieceLength: g.config.PieceLength,
	}

	// Handle single or multiple files
	if len(files) == 1 {
		// Single file torrent
		file := files[0]
		info.Name = filepath.Base(file.Path)
		info.Length = file.Size

		// Generate pieces
		pieces, err := g.generatePieces(ctx, []VideoFile{file})
		if err != nil {
			return nil, fmt.Errorf("failed to generate pieces: %w", err)
		}
		info.Pieces = pieces
	} else {
		// Multi-file torrent
		info.Name = fmt.Sprintf("video_%s", videoID.String())
		info.Files = make([]metainfo.FileInfo, 0, len(files))

		var totalSize int64
		for _, file := range files {
			relPath := filepath.Base(file.Path)
			info.Files = append(info.Files, metainfo.FileInfo{
				Path:   []string{relPath},
				Length: file.Size,
			})
			totalSize += file.Size
		}

		// Generate pieces for all files
		pieces, err := g.generatePieces(ctx, files)
		if err != nil {
			return nil, fmt.Errorf("failed to generate pieces: %w", err)
		}
		info.Pieces = pieces
	}

	// Create the metainfo structure
	mi := metainfo.MetaInfo{
		Announce:     g.config.Trackers[0], // Primary tracker
		AnnounceList: g.buildAnnounceList(),
		Comment:      g.config.Comment,
		CreatedBy:    g.config.CreatedBy,
		CreationDate: time.Now().Unix(),
		UrlList:      g.buildWebSeeds(videoID),
	}

	// Set info bytes by marshaling the info
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal info: %w", err)
	}
	mi.InfoBytes = infoBytes

	// Calculate info hash
	infoHash := mi.HashInfoBytes()

	// Generate torrent file bytes
	torrentBytes, err := bencode.Marshal(mi)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal torrent: %w", err)
	}

	// Calculate total size
	var totalSize int64
	for _, file := range files {
		totalSize += file.Size
	}

	// Generate magnet URI
	magnetURI := g.generateMagnetURI(infoHash.HexString(), info.Name, totalSize)

	return &TorrentInfo{
		InfoHash:    infoHash.HexString(),
		MagnetURI:   magnetURI,
		TorrentFile: torrentBytes,
		TotalSize:   totalSize,
		FileCount:   len(files),
		PieceCount:  len(info.Pieces) / 20, // Each piece hash is 20 bytes
	}, nil
}

// VideoFile represents a file to include in the torrent
type VideoFile struct {
	Path string
	Size int64
}

// flushPiece SHA1-hashes a completed piece and returns the 20-byte hash.
func flushPiece(piece []byte) []byte {
	hash := sha1.Sum(piece)
	return hash[:]
}

// generatePieces generates SHA1 hashes for all pieces
func (g *Generator) generatePieces(ctx context.Context, files []VideoFile) ([]byte, error) {
	var pieces []byte
	currentPiece := make([]byte, 0, g.config.PieceLength)
	var currentPieceSize int64

	for _, file := range files {
		f, err := os.Open(file.Path)
		if err != nil {
			return nil, fmt.Errorf("failed to open file %s: %w", file.Path, err)
		}

		buf := make([]byte, 32*1024) // 32KB buffer for reading
		readErr := func() error {
			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
				}

				n, err := f.Read(buf)
				if err == io.EOF {
					break
				}
				if err != nil {
					return fmt.Errorf("failed to read file %s: %w", file.Path, err)
				}

				data := buf[:n]
				dataOffset := 0

				for dataOffset < len(data) {
					// Calculate how much data to add to current piece
					remainingInPiece := g.config.PieceLength - currentPieceSize
					remainingInData := int64(len(data) - dataOffset)

					toAdd := remainingInPiece
					if remainingInData < remainingInPiece {
						toAdd = remainingInData
					}

					// Add data to current piece
					currentPiece = append(currentPiece, data[dataOffset:dataOffset+int(toAdd)]...)
					currentPieceSize += toAdd
					dataOffset += int(toAdd)

					// If piece is complete, hash it and start new piece
					if currentPieceSize == g.config.PieceLength {
						pieces = append(pieces, flushPiece(currentPiece)...)
						currentPiece = currentPiece[:0]
						currentPieceSize = 0
					}
				}
			}
			return nil
		}()

		// Close file and check both close error and read error
		closeErr := f.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, fmt.Errorf("failed to close file %s: %w", file.Path, closeErr)
		}
	}

	// Hash final partial piece if exists
	if currentPieceSize > 0 {
		pieces = append(pieces, flushPiece(currentPiece)...)
	}

	return pieces, nil
}

// buildAnnounceList builds the announce list for multiple trackers
func (g *Generator) buildAnnounceList() [][]string {
	if len(g.config.Trackers) == 0 {
		return nil
	}

	// Each tracker in its own tier for redundancy
	announceList := make([][]string, 0, len(g.config.Trackers))
	for _, tracker := range g.config.Trackers {
		announceList = append(announceList, []string{tracker})
	}
	return announceList
}

// buildWebSeeds builds web seed URLs for HTTP fallback
func (g *Generator) buildWebSeeds(videoID uuid.UUID) []string {
	if g.config.BaseURL == "" && len(g.config.WebSeeds) == 0 {
		return nil
	}

	seeds := make([]string, 0, len(g.config.WebSeeds)+1)

	// Add configured web seeds
	seeds = append(seeds, g.config.WebSeeds...)

	// Add base URL web seed if configured
	if g.config.BaseURL != "" {
		seedURL := fmt.Sprintf("%s/api/v1/videos/%s/files/",
			strings.TrimRight(g.config.BaseURL, "/"), videoID.String())
		seeds = append(seeds, seedURL)
	}

	return seeds
}

// generateMagnetURI creates a magnet link for the torrent
func (g *Generator) generateMagnetURI(infoHash, name string, size int64) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "magnet:?xt=urn:btih:%s", infoHash)

	// Add display name
	if name != "" {
		fmt.Fprintf(&sb, "&dn=%s", name)
	}

	// Add exact length
	fmt.Fprintf(&sb, "&xl=%d", size)

	// Add trackers
	for _, tracker := range g.config.Trackers {
		fmt.Fprintf(&sb, "&tr=%s", tracker)
	}

	// Add web seeds
	for _, webseed := range g.config.WebSeeds {
		fmt.Fprintf(&sb, "&ws=%s", webseed)
	}

	return sb.String()
}

// GenerateMagnetFromTorrent creates a magnet URI from existing torrent data
func (g *Generator) GenerateMagnetFromTorrent(torrentData []byte) (string, error) {
	var mi metainfo.MetaInfo
	err := bencode.Unmarshal(torrentData, &mi)
	if err != nil {
		return "", fmt.Errorf("failed to unmarshal torrent: %w", err)
	}

	info, err := mi.UnmarshalInfo()
	if err != nil {
		return "", fmt.Errorf("failed to unmarshal info: %w", err)
	}

	infoHash := mi.HashInfoBytes()

	var totalSize int64
	if info.Length > 0 {
		totalSize = info.Length
	} else {
		for _, file := range info.Files {
			totalSize += file.Length
		}
	}

	return g.generateMagnetURI(infoHash.HexString(), info.Name, totalSize), nil
}

// ValidateTorrent validates a torrent file
func ValidateTorrent(torrentData []byte) (*domain.VideoTorrent, error) {
	var mi metainfo.MetaInfo
	err := bencode.Unmarshal(torrentData, &mi)
	if err != nil {
		return nil, fmt.Errorf("invalid torrent file: %w", err)
	}

	info, err := mi.UnmarshalInfo()
	if err != nil {
		return nil, fmt.Errorf("invalid torrent info: %w", err)
	}

	infoHash := mi.HashInfoBytes()

	// Calculate total size
	var totalSize int64
	if info.Length > 0 {
		totalSize = info.Length
	} else {
		for _, file := range info.Files {
			totalSize += file.Length
		}
	}

	// Generate magnet URI
	magnetParts := []string{
		fmt.Sprintf("magnet:?xt=urn:btih:%s", infoHash.HexString()),
		fmt.Sprintf("&dn=%s", info.Name),
		fmt.Sprintf("&xl=%d", totalSize),
	}

	// Add announce list
	for _, tierList := range mi.AnnounceList {
		for _, tracker := range tierList {
			magnetParts = append(magnetParts, fmt.Sprintf("&tr=%s", tracker))
		}
	}

	magnetURI := strings.Join(magnetParts, "")

	return &domain.VideoTorrent{
		ID:                 uuid.New(),
		VideoID:            uuid.New(), // Will be set by caller
		InfoHash:           infoHash.HexString(),
		TorrentFilePath:    "", // Will be set by caller
		MagnetURI:          magnetURI,
		PieceLength:        int(info.PieceLength),
		TotalSizeBytes:     totalSize,
		Seeders:            0,
		Leechers:           0,
		CompletedDownloads: 0,
		IsSeeding:          false,
		CreatedAt:          time.Now(),
		UpdatedAt:          time.Now(),
	}, nil
}

// ParseMagnetURI parses a magnet URI and extracts its components
func ParseMagnetURI(magnetURI string) (infoHash string, trackers []string, err error) {
	if !strings.HasPrefix(magnetURI, "magnet:?") {
		return "", nil, fmt.Errorf("invalid magnet URI format")
	}

	parts := strings.Split(magnetURI[8:], "&")
	for _, part := range parts {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}

		switch kv[0] {
		case "xt":
			if strings.HasPrefix(kv[1], "urn:btih:") {
				infoHash = strings.TrimPrefix(kv[1], "urn:btih:")
			}
		case "tr":
			trackers = append(trackers, kv[1])
		}
	}

	if infoHash == "" {
		return "", nil, fmt.Errorf("no info hash found in magnet URI")
	}

	// Validate info hash format
	if len(infoHash) != 40 {
		// Try to decode if it's base32
		decoded, err := hex.DecodeString(infoHash)
		if err != nil || len(decoded) != 20 {
			return "", nil, fmt.Errorf("invalid info hash format")
		}
	}

	return infoHash, trackers, nil
}
