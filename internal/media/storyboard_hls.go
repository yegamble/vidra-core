package media

import (
	"context"
	"errors"
	"io"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/vidra/vidra-core/internal/storage"
)

var storyboardHLSLeaf = regexp.MustCompile(`^[a-zA-Z0-9_-][a-zA-Z0-9_.-]*$`)

// PeerTube's byte-range HLS stores a complete fragmented MP4 per rendition.
// Decode the cheapest video object directly, using the ordinary authenticated
// storage resolver. Never hand a manifest (and its untrusted URLs) to ffmpeg.
// Segmented, encrypted and live playlists require a different input path.
func storyboardInputKey(ctx context.Context, blobs storage.Backend, key string) (string, error) {
	if !strings.HasSuffix(key, ".m3u8") {
		return key, nil
	}
	master, err := storyboardManifest(ctx, blobs, key)
	if err != nil {
		return "", err
	}
	var selected string
	var bandwidth, cheapest int64
	for _, line := range master {
		if strings.HasPrefix(line, "#EXT-X-STREAM-INF:") {
			bandwidth = 0
			resolution, _ := m3u8Attr(line, "RESOLUTION")
			w, h, _ := strings.Cut(resolution, "x")
			width, _ := strconv.Atoi(w)
			height, _ := strconv.Atoi(h)
			if width > 0 && height > 0 {
				value, _ := m3u8Attr(line, "BANDWIDTH")
				bandwidth, _ = strconv.ParseInt(value, 10, 64)
			}
		} else if line != "" && !strings.HasPrefix(line, "#") {
			if bandwidth > 0 && (selected == "" || bandwidth < cheapest) {
				selected, cheapest = line, bandwidth
			}
			bandwidth = 0
		}
	}
	if !storyboardHLSLeaf.MatchString(selected) || !strings.HasSuffix(selected, ".m3u8") {
		return "", errors.New("media: storyboard HLS has no local video variant")
	}
	variant, err := storyboardManifest(ctx, blobs, path.Join(path.Dir(key), selected))
	if err != nil {
		return "", err
	}
	var init, object string
	var complete bool
	for _, line := range variant {
		switch {
		case strings.HasPrefix(line, "#EXT-X-KEY:"), strings.HasPrefix(line, "#EXT-X-DISCONTINUITY"):
			return "", errors.New("media: storyboard HLS requires unencrypted continuous media")
		case strings.HasPrefix(line, "#EXT-X-MAP:"):
			value, _ := m3u8Attr(line, "URI")
			if init != "" && value != init {
				return "", errors.New("media: storyboard HLS has multiple init objects")
			}
			init = value
		case line == "#EXT-X-ENDLIST":
			complete = true
		case line != "" && !strings.HasPrefix(line, "#"):
			if object != "" && line != object {
				return "", errors.New("media: storyboard HLS requires a single MP4 object")
			}
			object = line
		}
	}
	if !complete || object != init || !storyboardHLSLeaf.MatchString(object) || !strings.HasSuffix(object, ".mp4") {
		return "", errors.New("media: storyboard HLS requires a complete single-file MP4 variant")
	}
	return path.Join(path.Dir(key), object), nil
}

func storyboardManifest(ctx context.Context, blobs storage.Backend, key string) ([]string, error) {
	r, err := blobs.Open(ctx, key)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	b, err := io.ReadAll(io.LimitReader(r, (1<<20)+1))
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(b) > 1<<20 || strings.TrimSpace(lines[0]) != "#EXTM3U" {
		return nil, errors.New("media: invalid or oversized storyboard HLS manifest")
	}
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	return lines, nil
}
