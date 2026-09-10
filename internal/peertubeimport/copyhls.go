package peertubeimport

import (
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
var hlsURI = regexp.MustCompile(`URI="([^"]*)"`)

// copyHLSTree follows only same-directory object names, never a URL or traversal.
// PeerTube's flat HLS layout includes byte-range MP4s, audio and init segments.
// Keys are stable across retries; the DB publishes nothing until this succeeds.
func (im *Importer) copyHLSTree(ctx context.Context, prefix, master string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	queue := []string{master}
	seen := map[string]bool{}
	var total int64
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
		if strings.HasSuffix(name, ".m3u8") {
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
			for _, line := range strings.Split(string(body), "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				if !strings.HasPrefix(line, "#") {
					queue = append(queue, line)
				} else {
					refs := hlsURI.FindAllStringSubmatch(line, -1)
					if strings.Count(line, "URI=") != len(refs) {
						return fmt.Errorf("invalid HLS URI attribute")
					}
					for _, m := range refs {
						queue = append(queue, m[1])
					}
				}
			}
			if len(queue) > 10000 {
				return fmt.Errorf("HLS manifest exceeds reference limit")
			}
		} else {
			_ = rc.Close()
		}
		n, _, err := im.copyMedia(ctx, srcKey, key)
		if err != nil {
			return err
		}
		total += n
		if total > 64<<30 {
			return fmt.Errorf("HLS tree exceeds 64 GiB")
		}
	}
	return nil
}
