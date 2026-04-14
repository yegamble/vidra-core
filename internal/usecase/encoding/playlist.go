package encoding

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"vidra-core/internal/domain"
)

// MultiCodecPlaylistGenerator generates HLS master playlists with multiple codec support
type MultiCodecPlaylistGenerator struct {
	service *service
}

func NewMultiCodecPlaylistGenerator(s *service) *MultiCodecPlaylistGenerator {
	return &MultiCodecPlaylistGenerator{service: s}
}

// GenerateMultiCodecMasterPlaylist creates a master playlist that includes all available codec variants
func (g *MultiCodecPlaylistGenerator) GenerateMultiCodecMasterPlaylist(outBaseDir string, resolutions []string, codecs []string) error {
	var b strings.Builder

	// HLS version 7 supports multiple codecs
	b.WriteString("#EXTM3U\n")
	b.WriteString("#EXT-X-VERSION:7\n")
	b.WriteString("\n")

	// Bandwidth estimates per resolution (in bits per second)
	bandwidthMap := map[string]int{
		"240p":  400000,
		"360p":  800000,
		"480p":  1400000,
		"720p":  2800000,
		"1080p": 5000000,
		"1440p": 8000000,
		"2160p": 12000000,
		"4320p": 50000000,
	}

	// For each codec
	for _, codec := range codecs {
		encoder, err := g.service.GetCodecEncoder(codec)
		if err != nil {
			continue // Skip unsupported codecs
		}

		// For each resolution
		for _, res := range resolutions {
			height, ok := domain.HeightForResolution(res)
			if !ok || !encoder.SupportsResolution(res) {
				continue
			}

			bandwidth, ok := bandwidthMap[res]
			if !ok {
				continue
			}

			// Adjust bandwidth based on codec efficiency
			adjustedBandwidth := g.adjustBandwidthForCodec(codec, bandwidth)

			// Path to variant playlist
			playlistPath := fmt.Sprintf("%s/%dp/stream.m3u8", codec, height)

			// Check if the playlist exists
			fullPath := filepath.Join(outBaseDir, playlistPath)
			if _, err := os.Stat(fullPath); os.IsNotExist(err) {
				continue // Skip if playlist doesn't exist
			}

			// Get codec string for this variant
			codecsParam := encoder.GetCodecsString(height)

			// Write stream info
			fmt.Fprintf(&b, "#EXT-X-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%dx%d,CODECS=\"%s\",NAME=\"%s %s\"\n",
				adjustedBandwidth,
				g.calculateWidth(height),
				height,
				codecsParam,
				res,
				strings.ToUpper(codec))
			b.WriteString(playlistPath + "\n")
			b.WriteString("\n")
		}
	}

	// Write master playlist
	masterPath := filepath.Join(outBaseDir, "master.m3u8")
	return os.WriteFile(masterPath, []byte(b.String()), 0o600)
}

// adjustBandwidthForCodec adjusts bandwidth estimates based on codec efficiency
func (g *MultiCodecPlaylistGenerator) adjustBandwidthForCodec(codec string, baseBandwidth int) int {
	switch strings.ToLower(codec) {
	case "h264":
		return baseBandwidth // Baseline
	case "vp9":
		// VP9 is ~30% more efficient than H.264
		return int(float64(baseBandwidth) * 0.7)
	case "av1":
		// AV1 is ~50% more efficient than H.264
		return int(float64(baseBandwidth) * 0.5)
	default:
		return baseBandwidth
	}
}

// calculateWidth estimates width based on 16:9 aspect ratio
func (g *MultiCodecPlaylistGenerator) calculateWidth(height int) int {
	// Assume 16:9 aspect ratio
	return (height * 16) / 9
}

// GenerateLegacyMasterPlaylist generates a simple master playlist for backward compatibility
// This is used when only H.264 is available
func (g *MultiCodecPlaylistGenerator) GenerateLegacyMasterPlaylist(outBaseDir string, resolutions []string) error {
	bandwidthMap := map[string]int{
		"240p":  400000,
		"360p":  800000,
		"480p":  1400000,
		"720p":  2800000,
		"1080p": 5000000,
		"1440p": 8000000,
		"2160p": 12000000,
		"4320p": 50000000,
	}

	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n")

	for _, res := range resolutions {
		bandwidth, ok := bandwidthMap[res]
		if !ok {
			continue
		}

		height, ok := domain.HeightForResolution(res)
		if !ok {
			continue
		}

		// Check if playlist exists
		playlistPath := fmt.Sprintf("%dp/stream.m3u8", height)
		fullPath := filepath.Join(outBaseDir, playlistPath)
		if _, err := os.Stat(fullPath); os.IsNotExist(err) {
			continue
		}

		fmt.Fprintf(&b, "#EXT-X-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%dx%d,NAME=\"%s\"\n",
			bandwidth,
			g.calculateWidth(height),
			height,
			res)
		b.WriteString(playlistPath + "\n")
	}

	masterPath := filepath.Join(outBaseDir, "master.m3u8")
	return os.WriteFile(masterPath, []byte(b.String()), 0o600)
}

// GenerateCodecSpecificMasterPlaylist generates a master playlist for a single codec
// with all its resolution variants
func (g *MultiCodecPlaylistGenerator) GenerateCodecSpecificMasterPlaylist(codecDir string, codec string, resolutions []string) error {
	encoder, err := g.service.GetCodecEncoder(codec)
	if err != nil {
		return err
	}

	bandwidthMap := map[string]int{
		"240p":  400000,
		"360p":  800000,
		"480p":  1400000,
		"720p":  2800000,
		"1080p": 5000000,
		"1440p": 8000000,
		"2160p": 12000000,
		"4320p": 50000000,
	}

	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:7\n\n")

	for _, res := range resolutions {
		if !encoder.SupportsResolution(res) {
			continue
		}

		height, ok := domain.HeightForResolution(res)
		if !ok {
			continue
		}

		bandwidth, ok := bandwidthMap[res]
		if !ok {
			continue
		}

		adjustedBandwidth := g.adjustBandwidthForCodec(codec, bandwidth)

		// Path relative to codec directory
		playlistPath := fmt.Sprintf("%dp/stream.m3u8", height)
		fullPath := filepath.Join(codecDir, playlistPath)
		if _, err := os.Stat(fullPath); os.IsNotExist(err) {
			continue
		}

		codecsParam := encoder.GetCodecsString(height)

		fmt.Fprintf(&b, "#EXT-X-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%dx%d,CODECS=\"%s\",NAME=\"%s\"\n",
			adjustedBandwidth,
			g.calculateWidth(height),
			height,
			codecsParam,
			res)
		b.WriteString(playlistPath + "\n")
		b.WriteString("\n")
	}

	masterPath := filepath.Join(codecDir, "master.m3u8")
	return os.WriteFile(masterPath, []byte(b.String()), 0o600)
}

// DetectAvailableCodecs scans the output directory to detect which codecs are available
func DetectAvailableCodecs(outBaseDir string) []string {
	var codecs []string

	// Check for common codec directories
	possibleCodecs := []string{"h264", "vp9", "av1"}

	for _, codec := range possibleCodecs {
		codecDir := filepath.Join(outBaseDir, codec)
		if info, err := os.Stat(codecDir); err == nil && info.IsDir() {
			codecs = append(codecs, codec)
		}
	}

	// If no codec directories found, assume legacy structure (h264 only)
	if len(codecs) == 0 {
		codecs = []string{"h264"}
	}

	return codecs
}
