package peertubeimport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/vidra/vidra-core/internal/storage"
)

var hlsLocalName = regexp.MustCompile(`^[a-zA-Z0-9_-][a-zA-Z0-9_.-]*$`)

// copyHLSTree follows only same-directory object names, never a URL or traversal.
// PeerTube's flat HLS layout includes byte-range MP4s, audio and init segments.
// Keys are stable across retries; the DB publishes nothing until this succeeds.
func (im *Importer) copyHLSTree(ctx context.Context, prefix, master string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	queue := []string{master}
	seen := map[string]bool{}
	var total int64
	var serverBytes int64
	var serverObjects int
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if !hlsLocalName.MatchString(name) {
			return fmt.Errorf("HLS requires a local flat object name")
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		if len(seen) > 10000 {
			return fmt.Errorf("HLS tree exceeds 10000 objects")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		key := prefix + "/" + name
		limit := min(maxSourceFileBytes, (64<<30)-total)
		manifest := strings.HasSuffix(name, ".m3u8")
		if !manifest && im.copyMediaServer != nil && !im.copyMediaDisabled.Load() {
			n, err := im.copyMediaServer.Copy(ctx, im.srcMedia, key, key, limit)
			if errors.Is(err, storage.ErrNotFound) {
				n, err = im.copyMediaServer.Copy(ctx, im.srcMedia, strings.Replace(key, ptHLSDir+"/", ptHLSDir+"/private/", 1), key, limit)
			}
			if err == nil {
				if n < 0 || n > limit {
					return fmt.Errorf("HLS server copy exceeded the size cap")
				}
				total += n
				serverBytes += n
				serverObjects++
				continue
			}
			// Only ineligibility or a refusal before writes permits streaming.
			// Source changes, interrupted copies and verification failures stay
			// failures, so the DB cannot publish an incomplete/mixed HLS tree.
			if !errors.Is(err, storage.ErrCopyUnavailable) {
				return err
			}
			// A bucket/credential mismatch will fail for every next dependency.
			// Keep the read-only source and normal destination keys as the fallback.
			if im.copyMediaDisabled.CompareAndSwap(false, true) && im.logger != nil {
				im.logger.WarnContext(ctx, "peertube import: HLS server copy unavailable; using streamed copies for this run")
			}
		}
		// Private/password source HLS lives below hls/private/<uuid>.
		srcKey := key
		rc, err := im.srcMedia.Open(ctx, srcKey)
		if errors.Is(err, storage.ErrNotFound) {
			srcKey = strings.Replace(key, ptHLSDir+"/", ptHLSDir+"/private/", 1)
			rc, err = im.srcMedia.Open(ctx, srcKey)
		}
		if err != nil {
			return err
		}
		var reader io.Reader = rc
		if manifest {
			body, e := io.ReadAll(io.LimitReader(rc, (1<<20)+1))
			_ = rc.Close()
			if e != nil {
				return e
			}
			if len(body) > 1<<20 {
				return fmt.Errorf("HLS manifest exceeds 1 MiB")
			}
			if !strings.HasPrefix(string(body), "#EXTM3U") {
				return fmt.Errorf("invalid HLS manifest")
			}
			body, e = withoutHLSSubtitleGroups(body)
			if e != nil {
				return e
			}
			for _, line := range strings.Split(string(body), "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				if !strings.HasPrefix(line, "#") {
					queue = append(queue, line)
				} else if strings.Contains(line, "URI=") {
					_, list, _ := strings.Cut(line, ":")
					attributes, err := parseHLSAttributes(list)
					if err != nil {
						return err
					}
					for _, attribute := range attributes {
						if strings.HasSuffix(attribute.name, "URI") {
							if !attribute.quoted {
								return fmt.Errorf("invalid HLS URI attribute")
							}
							queue = append(queue, attribute.value)
						}
					}
				}
			}
			if len(queue) > 10000 {
				return fmt.Errorf("HLS manifest exceeds reference limit")
			}
			reader = bytes.NewReader(body)
		}
		n, _, err := im.copyMediaReader(ctx, reader, key, limit)
		_ = rc.Close()
		if err != nil {
			return err
		}
		total += n
		if total > 64<<30 {
			return fmt.Errorf("HLS tree exceeds 64 GiB")
		}
	}
	if im.logger != nil && serverObjects > 0 {
		im.logger.InfoContext(ctx, "peertube import: HLS tree copied", "server_side_objects", serverObjects, "server_side_bytes", serverBytes, "streamed_bytes", total-serverBytes)
	}
	return nil
}

type hlsAttribute struct {
	name, value, raw string
	quoted           bool
}

// Attribute commas inside quotes (notably CODECS and language names) are not
// separators. Reject ambiguity before removing anything from a source manifest.
func parseHLSAttributes(list string) ([]hlsAttribute, error) {
	var attributes []hlsAttribute
	seen := map[string]bool{}
	invalid := fmt.Errorf("invalid HLS attribute list")
	for list != "" {
		name, rest, found := strings.Cut(list, "=")
		if !found || name == "" || rest == "" || seen[name] {
			return nil, invalid
		}
		for _, c := range name {
			if !(c >= 'A' && c <= 'Z') && !(c >= '0' && c <= '9') && c != '-' {
				return nil, invalid
			}
		}
		seen[name] = true
		attribute := hlsAttribute{name: name, quoted: rest[0] == '"'}
		end := strings.IndexByte(rest, ',')
		if attribute.quoted {
			end = strings.IndexByte(rest[1:], '"')
			if end < 0 {
				return nil, invalid
			}
			end += 2
			attribute.value = rest[1 : end-1]
		} else {
			if end < 0 {
				end = len(rest)
			}
			attribute.value = rest[:end]
			if attribute.value == "" || strings.ContainsAny(attribute.value, "\"= \t") {
				return nil, invalid
			}
		}
		if strings.ContainsAny(attribute.value, "\r\n\x00") || (end < len(rest) && rest[end] != ',') {
			return nil, invalid
		}
		attribute.raw = name + "=" + rest[:end]
		attributes = append(attributes, attribute)
		list = rest[end:]
		if list != "" {
			list = list[1:]
			if list == "" {
				return nil, invalid
			}
		}
	}
	if len(attributes) == 0 {
		return nil, invalid
	}
	return attributes, nil
}

// PeerTube subtitle playlists contain absolute VTT URLs. Vidra carries those
// captions through its caption API and HTML tracks, independently of playback.
// Remove only HLS subtitle groups; audio/video dependencies stay flat and strict.
func withoutHLSSubtitleGroups(body []byte) ([]byte, error) {
	var out strings.Builder
	for _, raw := range strings.SplitAfter(string(body), "\n") {
		line := strings.TrimSpace(raw)
		tag, list, _ := strings.Cut(line, ":")
		if tag != "#EXT-X-MEDIA" && tag != "#EXT-X-STREAM-INF" {
			out.WriteString(raw)
			continue
		}
		attributes, err := parseHLSAttributes(list)
		if err != nil {
			return nil, err
		}
		var mediaType, uri string
		var kept []string
		removed := false
		for _, attribute := range attributes {
			if attribute.name == "TYPE" {
				if attribute.quoted {
					return nil, fmt.Errorf("invalid HLS media type")
				}
				mediaType = attribute.value
			}
			if attribute.name == "URI" {
				if !attribute.quoted || attribute.value == "" {
					return nil, fmt.Errorf("invalid HLS URI attribute")
				}
				uri = attribute.value
			}
			if tag == "#EXT-X-STREAM-INF" && attribute.name == "SUBTITLES" {
				if !attribute.quoted || attribute.value == "" {
					return nil, fmt.Errorf("invalid HLS subtitle group")
				}
				removed = true
				continue
			}
			kept = append(kept, attribute.raw)
		}
		if tag == "#EXT-X-MEDIA" && (mediaType == "" || (mediaType == "SUBTITLES" && uri == "")) {
			return nil, fmt.Errorf("invalid HLS media attributes")
		}
		if tag == "#EXT-X-MEDIA" && mediaType == "SUBTITLES" {
			continue
		}
		if len(kept) == 0 {
			return nil, fmt.Errorf("empty HLS stream attributes")
		}
		if removed {
			raw = strings.Replace(raw, line, tag+":"+strings.Join(kept, ","), 1)
		}
		out.WriteString(raw)
	}
	return []byte(out.String()), nil
}
