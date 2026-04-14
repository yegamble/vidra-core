package video

import (
	"sort"
	"strings"

	"vidra-core/internal/domain"
)

// BuildVideoFilesResponse constructs PeerTube-compatible files[] and
// streamingPlaylists[] arrays from a video's OutputPaths and S3URLs.
// files[] contains "web video" files (original source).
// streamingPlaylists[] contains HLS playlists with per-resolution file entries.
func BuildVideoFilesResponse(v *domain.Video) ([]domain.VideoFile, []domain.StreamingPlaylist) {
	files := make([]domain.VideoFile, 0)
	playlists := make([]domain.StreamingPlaylist, 0)

	if v.OutputPaths == nil && v.S3URLs == nil {
		return files, playlists
	}

	// Merge OutputPaths and S3URLs, preferring S3 when available.
	resolvedPaths := make(map[string]string)
	for k, v := range v.OutputPaths {
		if v != "" {
			resolvedPaths[k] = v
		}
	}
	for k, v := range v.S3URLs {
		if v != "" {
			resolvedPaths[k] = v // S3 overrides local
		}
	}

	// Source file → files[] (web video)
	if src, ok := resolvedPaths["source"]; ok && src != "" {
		files = append(files, domain.VideoFile{
			Resolution: domain.VideoResolution{ID: 0, Label: "original"},
			FileUrl:    src,
		})
	}

	// HLS resolutions → streamingPlaylists[0].files[]
	masterURL, hasMaster := resolvedPaths["master"]
	hlsFiles := make([]domain.VideoFile, 0)

	for key, url := range resolvedPaths {
		if key == "source" || key == "master" || key == "thumbnail" || key == "preview" {
			continue
		}
		height, ok := domain.HeightForResolution(key)
		if !ok {
			continue
		}
		hlsFiles = append(hlsFiles, domain.VideoFile{
			Resolution: domain.VideoResolution{ID: height, Label: key},
			FileUrl:    url,
		})
	}

	// Sort HLS files by resolution height descending (highest first)
	sort.Slice(hlsFiles, func(i, j int) bool {
		return hlsFiles[i].Resolution.ID > hlsFiles[j].Resolution.ID
	})

	if !hasMaster && len(hlsFiles) > 0 {
		masterURL = deriveMasterPlaylistURL(hlsFiles[0].FileUrl)
		hasMaster = masterURL != ""
	}

	if hasMaster && len(hlsFiles) > 0 {
		playlists = append(playlists, domain.StreamingPlaylist{
			Type:        1, // HLS
			PlaylistUrl: masterURL,
			Files:       hlsFiles,
		})
	}

	return files, playlists
}

func deriveMasterPlaylistURL(fileURL string) string {
	if fileURL == "" {
		return ""
	}

	suffixIndex := strings.IndexAny(fileURL, "?#")
	pathname := fileURL
	suffix := ""
	if suffixIndex >= 0 {
		pathname = fileURL[:suffixIndex]
		suffix = fileURL[suffixIndex:]
	}

	lastSlash := strings.LastIndex(pathname, "/")
	if lastSlash <= 0 {
		return ""
	}
	parentSlash := strings.LastIndex(pathname[:lastSlash], "/")
	if parentSlash <= 0 {
		return ""
	}

	return pathname[:parentSlash] + "/master.m3u8" + suffix
}
