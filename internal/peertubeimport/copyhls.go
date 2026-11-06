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

	"github.com/vidra/vidra-core/internal/hlsmanifest"
	"github.com/vidra/vidra-core/internal/storage"
)

var hlsLocalName = regexp.MustCompile(`^[a-zA-Z0-9_-][a-zA-Z0-9_.-]*$`)

// copyHLSTree follows only same-directory object names, never a URL or traversal.
// PeerTube's flat HLS layout includes byte-range MP4s, audio and init segments.
// Keys are stable across retries; the DB publishes nothing until this succeeds.
func (im *Importer) copyHLSTree(ctx context.Context, prefix, master string, recordedBytes int64) error {
	ctx, cancel := context.WithTimeout(ctx, hlsTreeTimeout(recordedBytes))
	defer cancel()
	queue := []string{master}
	seen := map[string]bool{}
	// Byte-range HLS names one file per segment line; queue and cap distinct names.
	queued := map[string]bool{master: true}
	enqueue := func(name string) {
		if !queued[name] {
			queued[name] = true
			queue = append(queue, name)
		}
	}
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
			n, localStream, err := im.copyHLSObject(ctx, key, limit)
			if err == nil {
				if n < 0 || n > limit {
					return fmt.Errorf("HLS server copy exceeded the size cap")
				}
				total += n
				if !localStream {
					serverBytes += n
					serverObjects++
				}
				continue
			}
			// Only ineligibility or a refusal before writes permits streaming.
			// Source changes, interrupted copies and verification failures stay
			// failures, so the DB cannot publish an incomplete/mixed HLS tree.
			if localStream || !errors.Is(err, storage.ErrCopyUnavailable) {
				stage := hlsServerCopy
				if localStream {
					stage = hlsStreamCopy
				}
				return &hlsCopyFailure{stage: stage, cause: err}
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
			return &hlsCopyFailure{stage: hlsSourceOpen, cause: err}
		}
		var reader io.Reader = rc
		if manifest {
			body, e := io.ReadAll(io.LimitReader(rc, (1<<20)+1))
			_ = rc.Close()
			if e != nil {
				return &hlsCopyFailure{stage: hlsManifestRead, cause: e}
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
					enqueue(line)
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
							enqueue(attribute.value)
						}
					}
				}
			}
			if len(queued) > 10000 {
				return fmt.Errorf("HLS manifest exceeds reference limit")
			}
			reader = bytes.NewReader(body)
		}
		n, _, err := im.copyMediaReader(ctx, reader, key, limit)
		_ = rc.Close()
		if err != nil {
			return &hlsCopyFailure{stage: hlsStreamCopy, cause: err}
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

// A local-only object must not disable acceleration for the next S3 object.
// The boolean also keeps any failed local destination write on the hard path.
func (im *Importer) copyHLSObject(ctx context.Context, key string, limit int64) (int64, bool, error) {
	for _, sourceKey := range []string{key, strings.Replace(key, ptHLSDir+"/", ptHLSDir+"/private/", 1)} {
		n, err := im.copyMediaServer.Copy(ctx, sourceCopyPrimary(im.srcMedia), sourceKey, key, limit)
		if !errors.Is(err, storage.ErrNotFound) {
			return n, false, err
		}
		if mixed, ok := im.srcMedia.(*sourceLocalFallback); ok {
			rc, err := mixed.openLocal(ctx, sourceKey)
			if err == nil {
				n, _, err := im.copyMediaReader(ctx, rc, key, limit)
				_ = rc.Close()
				return n, true, err
			}
			if !errors.Is(err, storage.ErrNotFound) {
				return 0, false, err
			}
		}
	}
	return 0, false, storage.ErrNotFound
}

type hlsAttribute struct {
	name, value, raw string
	quoted           bool
}

func parseHLSAttributes(list string) ([]hlsAttribute, error) {
	parsed, err := hlsmanifest.ParseAttributes(list)
	if err != nil {
		return nil, err
	}
	attributes := make([]hlsAttribute, 0, len(parsed))
	for _, attribute := range parsed {
		attributes = append(attributes, hlsAttribute{name: attribute.Name, value: attribute.Value, raw: attribute.Raw, quoted: attribute.Quoted})
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

// hlsCopyFloorRate is the slowest a tree may copy before its deadline treats it
// as stuck. Measured streamed copies on the beta ran at 12–24 MB/s per tree.
const hlsCopyFloorRate = 4 << 20

// hlsTreeTimeout bounds one tree's copy by its recorded size so a hung transfer
// still ends without failing a large ladder for its size. 30 minutes alone would
// need 36 MB/s to fill the 64 GiB tree cap; 0 (size unknown) keeps that floor.
func hlsTreeTimeout(recordedBytes int64) time.Duration {
	return max(30*time.Minute, time.Duration(recordedBytes/hlsCopyFloorRate)*time.Second)
}
