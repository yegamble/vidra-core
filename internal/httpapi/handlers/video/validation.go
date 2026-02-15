package video

import (
	"strings"
)

func isAllowedVideo(ext string, head []byte, contentType string) bool {
	if isAllowedVideoMime(contentType) {
		return true
	}
	if isAllowedVideoExt(ext) && hasKnownVideoSignature(head, ext) {
		return true
	}
	return false
}

func isAllowedVideoExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ".mp4", ".mov", ".mkv", ".webm", ".avi":
		return true
	default:
		return false
	}
}

func isAllowedVideoMime(ct string) bool {
	ct = strings.ToLower(ct)
	if strings.HasPrefix(ct, "video/") {
		if strings.Contains(ct, "mp4") || strings.Contains(ct, "quicktime") || strings.Contains(ct, "webm") || strings.Contains(ct, "x-msvideo") || strings.Contains(ct, "x-matroska") {
			return true
		}
	}
	return false
}

func hasKnownVideoSignature(head []byte, ext string) bool {
	if len(head) >= 12 && string(head[4:8]) == "ftyp" {
		if ext == ".mp4" || ext == ".mov" {
			return true
		}
		return true
	}
	if len(head) >= 4 && head[0] == 0x1A && head[1] == 0x45 && head[2] == 0xDF && head[3] == 0xA3 {
		return ext == ".mkv" || ext == ".webm" || ext == ""
	}
	if len(head) >= 12 && string(head[0:4]) == "RIFF" && string(head[8:12]) == "AVI " {
		return ext == ".avi" || ext == ""
	}
	return false
}

func extFromContentType(ct string) string {
	ct = strings.ToLower(ct)
	ct = strings.Split(ct, ";")[0]
	ct = strings.TrimSpace(ct)

	exts := map[string]string{
		"video/mp4":        ".mp4",
		"video/x-msvideo":  ".avi",
		"video/x-matroska": ".mkv",
		"video/quicktime":  ".mov",
		"video/webm":       ".webm",
		"video/x-flv":      ".flv",
		"video/x-ms-wmv":   ".wmv",
		"video/mpeg":       ".mpeg",
		"video/3gpp":       ".3gp",
		"video/ogg":        ".ogv",
	}
	if ext, ok := exts[ct]; ok {
		return ext
	}
	return ".mp4"
}
