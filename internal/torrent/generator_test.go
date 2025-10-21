package torrent

import (
	"context"
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGeneratorConfig tests generator configuration
func TestGeneratorConfig(t *testing.T) {
	t.Run("DefaultConfig", func(t *testing.T) {
		config := DefaultGeneratorConfig()
		assert.Equal(t, int64(262144), config.PieceLength)
		assert.Equal(t, "Athena/1.0", config.CreatedBy)
		assert.False(t, config.Private)
		assert.Len(t, config.Trackers, 3)
		assert.Contains(t, config.Trackers, "wss://tracker.openwebtorrent.com")
	})

	t.Run("NewGeneratorWithNilConfig", func(t *testing.T) {
		gen := NewGenerator(nil)
		assert.NotNil(t, gen)
		assert.Equal(t, int64(262144), gen.config.PieceLength)
	})

	t.Run("NewGeneratorWithCustomConfig", func(t *testing.T) {
		config := &GeneratorConfig{
			PieceLength: 524288, // 512KB
			CreatedBy:   "CustomClient/2.0",
			Private:     true,
		}
		gen := NewGenerator(config)
		assert.Equal(t, int64(524288), gen.config.PieceLength)
		assert.Equal(t, "CustomClient/2.0", gen.config.CreatedBy)
		assert.True(t, gen.config.Private)
	})
}

// TestGenerateFromVideo tests torrent generation from video files
func TestGenerateFromVideo(t *testing.T) {
	// Create temporary test files
	tempDir, err := ioutil.TempDir("", "torrent-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	t.Run("SingleFile", func(t *testing.T) {
		// Create a test file
		testFile := filepath.Join(tempDir, "video.mp4")
		testContent := []byte("test video content for torrent generation")
		err := ioutil.WriteFile(testFile, testContent, 0644)
		require.NoError(t, err)

		gen := NewGenerator(DefaultGeneratorConfig())
		videoID := uuid.New()

		info, err := gen.GenerateFromVideo(context.Background(), videoID, []VideoFile{
			{Path: testFile, Size: int64(len(testContent))},
		})

		require.NoError(t, err)
		assert.NotNil(t, info)
		assert.Len(t, info.InfoHash, 40) // SHA1 hex string
		assert.Contains(t, info.MagnetURI, "magnet:?xt=urn:btih:")
		assert.Contains(t, info.MagnetURI, info.InfoHash)
		assert.Equal(t, int64(len(testContent)), info.TotalSize)
		assert.Equal(t, 1, info.FileCount)
		assert.Equal(t, 1, info.PieceCount)
		assert.NotEmpty(t, info.TorrentFile)

		// Verify torrent file can be decoded
		var mi metainfo.MetaInfo
		err = bencode.Unmarshal(info.TorrentFile, &mi)
		require.NoError(t, err)
		assert.Equal(t, "wss://tracker.openwebtorrent.com", mi.Announce)
	})

	t.Run("MultipleFiles", func(t *testing.T) {
		// Create multiple test files
		files := []VideoFile{}
		for i := 0; i < 3; i++ {
			filename := fmt.Sprintf("segment_%d.ts", i)
			testFile := filepath.Join(tempDir, filename)
			content := []byte(fmt.Sprintf("segment %d content with some data to make it longer", i))
			err := ioutil.WriteFile(testFile, content, 0644)
			require.NoError(t, err)
			files = append(files, VideoFile{
				Path: testFile,
				Size: int64(len(content)),
			})
		}

		gen := NewGenerator(DefaultGeneratorConfig())
		videoID := uuid.New()

		info, err := gen.GenerateFromVideo(context.Background(), videoID, files)

		require.NoError(t, err)
		assert.NotNil(t, info)
		assert.Len(t, info.InfoHash, 40)
		assert.Contains(t, info.MagnetURI, "magnet:?xt=urn:btih:")
		assert.Equal(t, 3, info.FileCount)
		assert.Greater(t, info.TotalSize, int64(0))
		assert.NotEmpty(t, info.TorrentFile)
	})

	t.Run("LargeFile", func(t *testing.T) {
		// Create a larger file to test multi-piece torrents
		testFile := filepath.Join(tempDir, "large_video.mp4")
		// Create 1MB of data
		largeContent := make([]byte, 1024*1024)
		for i := range largeContent {
			largeContent[i] = byte(i % 256)
		}
		err := ioutil.WriteFile(testFile, largeContent, 0644)
		require.NoError(t, err)

		// Use smaller piece length to ensure multiple pieces
		config := &GeneratorConfig{
			PieceLength: 262144, // 256KB
			CreatedBy:   "Athena/1.0",
			Trackers: []string{
				"wss://tracker.test.com",
			},
		}
		gen := NewGenerator(config)
		videoID := uuid.New()

		info, err := gen.GenerateFromVideo(context.Background(), videoID, []VideoFile{
			{Path: testFile, Size: int64(len(largeContent))},
		})

		require.NoError(t, err)
		assert.NotNil(t, info)
		assert.Equal(t, int64(len(largeContent)), info.TotalSize)
		assert.Equal(t, 4, info.PieceCount) // 1MB / 256KB = 4 pieces
		assert.Len(t, info.InfoHash, 40)
	})

	t.Run("WithWebSeeds", func(t *testing.T) {
		testFile := filepath.Join(tempDir, "webseed_video.mp4")
		testContent := []byte("video with web seeds")
		err := ioutil.WriteFile(testFile, testContent, 0644)
		require.NoError(t, err)

		config := DefaultGeneratorConfig()
		config.BaseURL = "https://example.com"
		config.WebSeeds = []string{"https://cdn.example.com/videos/"}

		gen := NewGenerator(config)
		videoID := uuid.New()

		info, err := gen.GenerateFromVideo(context.Background(), videoID, []VideoFile{
			{Path: testFile, Size: int64(len(testContent))},
		})

		require.NoError(t, err)
		assert.Contains(t, info.MagnetURI, "&ws=")

		// Decode and verify web seeds are in torrent
		var mi metainfo.MetaInfo
		err = bencode.Unmarshal(info.TorrentFile, &mi)
		require.NoError(t, err)
		assert.Len(t, mi.UrlList, 2) // configured seed + base URL seed
	})

	t.Run("EmptyFiles", func(t *testing.T) {
		gen := NewGenerator(DefaultGeneratorConfig())
		videoID := uuid.New()

		_, err := gen.GenerateFromVideo(context.Background(), videoID, []VideoFile{})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no files provided")
	})

	t.Run("NonExistentFile", func(t *testing.T) {
		gen := NewGenerator(DefaultGeneratorConfig())
		videoID := uuid.New()

		_, err := gen.GenerateFromVideo(context.Background(), videoID, []VideoFile{
			{Path: "/non/existent/file.mp4", Size: 1024},
		})
		assert.Error(t, err)
	})

	t.Run("ContextCancellation", func(t *testing.T) {
		// Create a large file
		testFile := filepath.Join(tempDir, "cancel_test.mp4")
		largeContent := make([]byte, 1024*1024*5) // 5MB
		err := ioutil.WriteFile(testFile, largeContent, 0644)
		require.NoError(t, err)

		gen := NewGenerator(&GeneratorConfig{
			PieceLength: 16384, // Small pieces to make it take longer
			Trackers:    []string{"wss://test.tracker.com"},
		})
		videoID := uuid.New()

		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
		defer cancel()

		_, err = gen.GenerateFromVideo(ctx, videoID, []VideoFile{
			{Path: testFile, Size: int64(len(largeContent))},
		})
		assert.Error(t, err)
	})
}

// TestParseMagnetURI tests magnet URI parsing
func TestParseMagnetURI(t *testing.T) {
	t.Run("ValidMagnetURI", func(t *testing.T) {
		magnetURI := "magnet:?xt=urn:btih:1234567890abcdef1234567890abcdef12345678&dn=test.mp4&tr=wss://tracker1.com&tr=wss://tracker2.com"

		infoHash, trackers, err := ParseMagnetURI(magnetURI)
		require.NoError(t, err)
		assert.Equal(t, "1234567890abcdef1234567890abcdef12345678", infoHash)
		assert.Len(t, trackers, 2)
		assert.Contains(t, trackers, "wss://tracker1.com")
		assert.Contains(t, trackers, "wss://tracker2.com")
	})

	t.Run("MinimalMagnetURI", func(t *testing.T) {
		magnetURI := "magnet:?xt=urn:btih:1234567890abcdef1234567890abcdef12345678"

		infoHash, trackers, err := ParseMagnetURI(magnetURI)
		require.NoError(t, err)
		assert.Equal(t, "1234567890abcdef1234567890abcdef12345678", infoHash)
		assert.Empty(t, trackers)
	})

	t.Run("InvalidFormat", func(t *testing.T) {
		testCases := []string{
			"not-a-magnet-uri",
			"magnet:xt=urn:btih:123", // missing ?
			"magnet:?xt=urn:btih:",   // no info hash
			"magnet:?dn=test.mp4",    // no info hash field
		}

		for _, tc := range testCases {
			_, _, err := ParseMagnetURI(tc)
			assert.Error(t, err, "Expected error for: %s", tc)
		}
	})

	t.Run("InvalidInfoHash", func(t *testing.T) {
		magnetURI := "magnet:?xt=urn:btih:tooshort"

		_, _, err := ParseMagnetURI(magnetURI)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid info hash")
	})
}

// TestValidateTorrent tests torrent file validation
func TestValidateTorrent(t *testing.T) {
	// Create a valid torrent for testing
	tempDir, err := ioutil.TempDir("", "validate-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	testFile := filepath.Join(tempDir, "test.mp4")
	testContent := []byte("test content for validation")
	err = ioutil.WriteFile(testFile, testContent, 0644)
	require.NoError(t, err)

	gen := NewGenerator(DefaultGeneratorConfig())
	videoID := uuid.New()

	info, err := gen.GenerateFromVideo(context.Background(), videoID, []VideoFile{
		{Path: testFile, Size: int64(len(testContent))},
	})
	require.NoError(t, err)

	t.Run("ValidTorrent", func(t *testing.T) {
		torrent, err := ValidateTorrent(info.TorrentFile)
		require.NoError(t, err)
		assert.NotNil(t, torrent)
		assert.Equal(t, info.InfoHash, torrent.InfoHash)
		assert.Equal(t, info.TotalSize, torrent.TotalSizeBytes)
		assert.Equal(t, 262144, torrent.PieceLength)
		assert.Contains(t, torrent.MagnetURI, info.InfoHash)
	})

	t.Run("InvalidBencode", func(t *testing.T) {
		invalidData := []byte("not a valid torrent file")
		_, err := ValidateTorrent(invalidData)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid torrent")
	})

	t.Run("EmptyData", func(t *testing.T) {
		_, err := ValidateTorrent([]byte{})
		assert.Error(t, err)
	})
}

// TestGenerateMagnetFromTorrent tests magnet URI generation from torrent
func TestGenerateMagnetFromTorrent(t *testing.T) {
	// Create a valid torrent
	tempDir, err := ioutil.TempDir("", "magnet-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	testFile := filepath.Join(tempDir, "test.mp4")
	testContent := []byte("test content")
	err = ioutil.WriteFile(testFile, testContent, 0644)
	require.NoError(t, err)

	gen := NewGenerator(DefaultGeneratorConfig())
	videoID := uuid.New()

	info, err := gen.GenerateFromVideo(context.Background(), videoID, []VideoFile{
		{Path: testFile, Size: int64(len(testContent))},
	})
	require.NoError(t, err)

	t.Run("ValidTorrent", func(t *testing.T) {
		magnetURI, err := gen.GenerateMagnetFromTorrent(info.TorrentFile)
		require.NoError(t, err)
		assert.Contains(t, magnetURI, "magnet:?xt=urn:btih:")
		assert.Contains(t, magnetURI, info.InfoHash)
		assert.Contains(t, magnetURI, "&xl=") // exact length
		assert.Contains(t, magnetURI, "&dn=") // display name
		assert.Contains(t, magnetURI, "&tr=") // trackers
	})

	t.Run("InvalidTorrent", func(t *testing.T) {
		_, err := gen.GenerateMagnetFromTorrent([]byte("invalid"))
		assert.Error(t, err)
	})
}

// TestBuildAnnounceList tests announce list building
func TestBuildAnnounceList(t *testing.T) {
	t.Run("MultipleTrackers", func(t *testing.T) {
		config := &GeneratorConfig{
			Trackers: []string{
				"wss://tracker1.com",
				"wss://tracker2.com",
				"wss://tracker3.com",
			},
		}
		gen := NewGenerator(config)

		list := gen.buildAnnounceList()
		assert.Len(t, list, 3)
		assert.Equal(t, []string{"wss://tracker1.com"}, list[0])
		assert.Equal(t, []string{"wss://tracker2.com"}, list[1])
		assert.Equal(t, []string{"wss://tracker3.com"}, list[2])
	})

	t.Run("NoTrackers", func(t *testing.T) {
		config := &GeneratorConfig{
			Trackers: []string{},
		}
		gen := NewGenerator(config)

		list := gen.buildAnnounceList()
		assert.Nil(t, list)
	})
}

// TestBuildWebSeeds tests web seed URL building
func TestBuildWebSeeds(t *testing.T) {
	videoID := uuid.New()

	t.Run("WithBaseURLAndSeeds", func(t *testing.T) {
		config := &GeneratorConfig{
			BaseURL:  "https://example.com",
			WebSeeds: []string{"https://cdn1.com/", "https://cdn2.com/"},
		}
		gen := NewGenerator(config)

		seeds := gen.buildWebSeeds(videoID)
		assert.Len(t, seeds, 3)
		assert.Contains(t, seeds, "https://cdn1.com/")
		assert.Contains(t, seeds, "https://cdn2.com/")
		expectedURL := fmt.Sprintf("https://example.com/api/v1/videos/%s/files/", videoID.String())
		assert.Contains(t, seeds, expectedURL)
	})

	t.Run("OnlyBaseURL", func(t *testing.T) {
		config := &GeneratorConfig{
			BaseURL: "https://example.com/",
		}
		gen := NewGenerator(config)

		seeds := gen.buildWebSeeds(videoID)
		assert.Len(t, seeds, 1)
		assert.True(t, strings.HasPrefix(seeds[0], "https://example.com/api/v1/videos/"))
	})

	t.Run("NoWebSeeds", func(t *testing.T) {
		config := &GeneratorConfig{}
		gen := NewGenerator(config)

		seeds := gen.buildWebSeeds(videoID)
		assert.Nil(t, seeds)
	})
}

// TestPieceGeneration tests the piece generation logic
func TestPieceGeneration(t *testing.T) {
	tempDir, err := ioutil.TempDir("", "piece-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	t.Run("ExactPieceSize", func(t *testing.T) {
		// Create file with exact piece size
		pieceSize := int64(16384) // 16KB
		testFile := filepath.Join(tempDir, "exact.bin")
		content := make([]byte, pieceSize)
		for i := range content {
			content[i] = byte(i % 256)
		}
		err := ioutil.WriteFile(testFile, content, 0644)
		require.NoError(t, err)

		config := &GeneratorConfig{
			PieceLength: pieceSize,
			Trackers:    []string{"wss://test.com"},
		}
		gen := NewGenerator(config)

		pieces, err := gen.generatePieces(context.Background(), []VideoFile{
			{Path: testFile, Size: pieceSize},
		})

		require.NoError(t, err)
		assert.Len(t, pieces, 20) // One SHA1 hash = 20 bytes
	})

	t.Run("PartialLastPiece", func(t *testing.T) {
		pieceSize := int64(16384)
		testFile := filepath.Join(tempDir, "partial.bin")
		// Create file with 1.5 pieces worth of data
		content := make([]byte, pieceSize+pieceSize/2)
		err := ioutil.WriteFile(testFile, content, 0644)
		require.NoError(t, err)

		config := &GeneratorConfig{
			PieceLength: pieceSize,
			Trackers:    []string{"wss://test.com"},
		}
		gen := NewGenerator(config)

		pieces, err := gen.generatePieces(context.Background(), []VideoFile{
			{Path: testFile, Size: int64(len(content))},
		})

		require.NoError(t, err)
		assert.Len(t, pieces, 40) // Two SHA1 hashes = 40 bytes
	})

	t.Run("MultiplePieces", func(t *testing.T) {
		pieceSize := int64(1024) // 1KB pieces
		numPieces := 5
		testFile := filepath.Join(tempDir, "multi.bin")
		content := make([]byte, pieceSize*int64(numPieces))
		err := ioutil.WriteFile(testFile, content, 0644)
		require.NoError(t, err)

		config := &GeneratorConfig{
			PieceLength: pieceSize,
			Trackers:    []string{"wss://test.com"},
		}
		gen := NewGenerator(config)

		pieces, err := gen.generatePieces(context.Background(), []VideoFile{
			{Path: testFile, Size: int64(len(content))},
		})

		require.NoError(t, err)
		assert.Len(t, pieces, numPieces*20) // 5 SHA1 hashes = 100 bytes
	})
}

// BenchmarkGenerateTorrent benchmarks torrent generation
func BenchmarkGenerateTorrent(b *testing.B) {
	// Setup test file
	tempDir, err := ioutil.TempDir("", "bench-*")
	require.NoError(b, err)
	defer os.RemoveAll(tempDir)

	testFile := filepath.Join(tempDir, "bench.mp4")
	content := make([]byte, 1024*1024) // 1MB
	err = ioutil.WriteFile(testFile, content, 0644)
	require.NoError(b, err)

	gen := NewGenerator(DefaultGeneratorConfig())
	videoID := uuid.New()
	files := []VideoFile{{Path: testFile, Size: int64(len(content))}}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := gen.GenerateFromVideo(context.Background(), videoID, files)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkPieceGeneration benchmarks piece hash generation
func BenchmarkPieceGeneration(b *testing.B) {
	tempDir, err := ioutil.TempDir("", "bench-piece-*")
	require.NoError(b, err)
	defer os.RemoveAll(tempDir)

	testFile := filepath.Join(tempDir, "piece.mp4")
	content := make([]byte, 10*1024*1024) // 10MB
	err = ioutil.WriteFile(testFile, content, 0644)
	require.NoError(b, err)

	gen := NewGenerator(DefaultGeneratorConfig())
	files := []VideoFile{{Path: testFile, Size: int64(len(content))}}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := gen.generatePieces(context.Background(), files)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// TestInfoHashConsistency tests that the same files produce the same info hash
func TestInfoHashConsistency(t *testing.T) {
	tempDir, err := ioutil.TempDir("", "consistency-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	testFile := filepath.Join(tempDir, "consistent.mp4")
	testContent := []byte("consistent content for info hash testing")
	err = ioutil.WriteFile(testFile, testContent, 0644)
	require.NoError(t, err)

	gen := NewGenerator(DefaultGeneratorConfig())
	videoID := uuid.New()
	files := []VideoFile{{Path: testFile, Size: int64(len(testContent))}}

	// Generate multiple times
	var infoHashes []string
	for i := 0; i < 5; i++ {
		info, err := gen.GenerateFromVideo(context.Background(), videoID, files)
		require.NoError(t, err)
		infoHashes = append(infoHashes, info.InfoHash)
	}

	// All info hashes should be identical
	for i := 1; i < len(infoHashes); i++ {
		assert.Equal(t, infoHashes[0], infoHashes[i],
			"InfoHash mismatch: generation %d differs from generation 0", i)
	}
}

// TestInfoHashFormat tests that info hashes are properly formatted
func TestInfoHashFormat(t *testing.T) {
	tempDir, err := ioutil.TempDir("", "format-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	testFile := filepath.Join(tempDir, "format.mp4")
	err = ioutil.WriteFile(testFile, []byte("test"), 0644)
	require.NoError(t, err)

	gen := NewGenerator(DefaultGeneratorConfig())
	info, err := gen.GenerateFromVideo(context.Background(), uuid.New(), []VideoFile{
		{Path: testFile, Size: 4},
	})
	require.NoError(t, err)

	// Info hash should be 40 character hex string (20 bytes in hex)
	assert.Len(t, info.InfoHash, 40)

	// Should be valid hex
	_, err = hex.DecodeString(info.InfoHash)
	assert.NoError(t, err)

	// Should be lowercase
	assert.Equal(t, strings.ToLower(info.InfoHash), info.InfoHash)
}
