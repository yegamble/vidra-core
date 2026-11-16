package media

import (
	"context"
	"errors"
	"strings"

	"github.com/vidra/vidra-core/internal/storage"
)

// PeerTube's byte-range HLS stores a complete fragmented MP4 per rendition.
// Decode the cheapest video object directly, using the ordinary authenticated
// storage resolver. Never hand a manifest (and its untrusted URLs) to ffmpeg.
// Segmented, encrypted and live playlists require a different input path. The
// manifest reading and the "one complete MP4" proof are shared with the download
// resolver (hls_download_sources.go) so there is a single, strict parser.
func storyboardInputKey(ctx context.Context, blobs storage.Backend, key string) (string, error) {
	if !strings.HasSuffix(key, ".m3u8") {
		return key, nil
	}
	master, err := parseHLSMaster(ctx, blobs, key)
	if err != nil {
		return "", err
	}
	var selected string
	var cheapest int64
	for _, v := range master.variants {
		if v.width > 0 && v.height > 0 && v.bandwidth > 0 && (selected == "" || v.bandwidth < cheapest) {
			selected, cheapest = v.uri, v.bandwidth
		}
	}
	if selected == "" {
		return "", errors.New("media: storyboard HLS has no local video variant")
	}
	return hlsSingleMP4(ctx, blobs, key, selected)
}
