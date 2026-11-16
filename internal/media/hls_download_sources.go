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

// maxHLSManifestBytes caps every manifest this file reads. A hostile or corrupt
// object must not be able to make a request (or a storyboard job) buffer more
// than this.
const maxHLSManifestBytes = 1 << 20

// hlsLeafName is the only shape of reference these resolvers follow: a single
// path element in the master's own directory. It excludes URLs ("https://",
// "//host"), absolute paths, "." / ".." and any separator.
var hlsLeafName = regexp.MustCompile(`^[a-zA-Z0-9_-][a-zA-Z0-9_.-]*$`)

// HLSDownloadRung is one video variant of a master playlist that is provably a
// single complete MP4 object.
type HLSDownloadRung struct {
	Width, Height int
	// VideoKey is the storage key of the rung's MP4.
	VideoKey string
	// AudioKey is the storage key of the audio group's MP4 when the rung's video
	// object carries NO audio (PeerTube's split-audio layout). Empty means the
	// rung's MP4 already contains its audio.
	AudioKey string
}

// HLSDownloadSources is what ResolveHLSDownloadSources proved about a master.
type HLSDownloadSources struct {
	Rungs []HLSDownloadRung
	// AudioKey is the single audio-only MP4 of the playlist's audio group, or ""
	// when there is none (older imports and every muxed ladder).
	AudioKey string
}

// ResolveHLSDownloadSources reads masterKey and each variant playlist once and
// returns, for every variant that PeerTube-style byte-range HLS stores as ONE
// complete fragmented MP4, the key of that object — plus the audio group's
// object when the audio is split out.
//
// Only the master being unreadable or malformed is an error. A variant that is
// encrypted, discontinuous, spread over several objects, incomplete (no
// ENDLIST), or that names anything but a same-directory leaf is skipped: it
// just is not downloadable, and must never fail the whole listing. No URL is
// ever followed; every read goes through blobs.
func ResolveHLSDownloadSources(ctx context.Context, blobs storage.Backend, masterKey string) (HLSDownloadSources, error) {
	var out HLSDownloadSources
	if !strings.HasSuffix(masterKey, ".m3u8") {
		return out, errors.New("media: HLS master key is not a playlist")
	}
	master, err := parseHLSMaster(ctx, blobs, masterKey)
	if err != nil {
		return out, err
	}
	type result struct {
		key string
		err error
	}
	cache := map[string]result{}
	resolve := func(uri string) (string, error) {
		if r, ok := cache[uri]; ok {
			return r.key, r.err
		}
		key, err := hlsSingleMP4(ctx, blobs, masterKey, uri)
		cache[uri] = result{key, err}
		return key, err
	}
	for _, v := range master.variants {
		if v.width <= 0 || v.height <= 0 {
			continue // audio-only or unsized variant: not a video rung
		}
		videoKey, err := resolve(v.uri)
		if err != nil {
			if cerr := ctx.Err(); cerr != nil {
				return HLSDownloadSources{}, cerr
			}
			continue
		}
		rung := HLSDownloadRung{Width: v.width, Height: v.height, VideoKey: videoKey}
		if v.audioGroup != "" {
			uri, separate, ok := master.audioURI(v.audioGroup)
			if !ok {
				continue // the audio is somewhere we cannot prove: do not offer a silent file as complete
			}
			if separate {
				audioKey, err := resolve(uri)
				if err != nil {
					if cerr := ctx.Err(); cerr != nil {
						return HLSDownloadSources{}, cerr
					}
					continue
				}
				rung.AudioKey = audioKey
				if out.AudioKey == "" {
					out.AudioKey = audioKey
				}
			}
		}
		out.Rungs = append(out.Rungs, rung)
	}
	return out, nil
}

type hlsMasterVariant struct {
	width, height int
	bandwidth     int64
	uri           string
	audioGroup    string
}

type hlsAudioMedia struct{ group, uri string }

type hlsMaster struct {
	variants []hlsMasterVariant
	audio    []hlsAudioMedia
}

// audioURI resolves a variant's AUDIO group. separate=false with ok=true means
// the group carries no URI, i.e. the audio is inside the variant itself. ok=false
// means the group is missing or names several distinct objects (a language
// choice this resolver does not make for a download).
func (m hlsMaster) audioURI(group string) (uri string, separate, ok bool) {
	var uris []string
	found := false
	for _, a := range m.audio {
		if a.group != group {
			continue
		}
		found = true
		if a.uri == "" {
			continue
		}
		dup := false
		for _, u := range uris {
			dup = dup || u == a.uri
		}
		if !dup {
			uris = append(uris, a.uri)
		}
	}
	switch {
	case !found:
		return "", false, false
	case len(uris) == 0:
		return "", false, true
	case len(uris) == 1:
		return uris[0], true, true
	}
	return "", false, false
}

func parseHLSMaster(ctx context.Context, blobs storage.Backend, key string) (hlsMaster, error) {
	lines, err := readHLSManifest(ctx, blobs, key)
	if err != nil {
		return hlsMaster{}, err
	}
	var m hlsMaster
	var pending *hlsMasterVariant
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "#EXT-X-STREAM-INF:"):
			v := hlsMasterVariant{}
			resolution, _ := m3u8Attr(line, "RESOLUTION")
			w, h, _ := strings.Cut(resolution, "x")
			v.width, _ = strconv.Atoi(w)
			v.height, _ = strconv.Atoi(h)
			value, _ := m3u8Attr(line, "BANDWIDTH")
			v.bandwidth, _ = strconv.ParseInt(value, 10, 64)
			v.audioGroup, _ = m3u8Attr(line, "AUDIO")
			pending = &v
		case strings.HasPrefix(line, "#EXT-X-MEDIA:"):
			if typ, _ := m3u8Attr(line, "TYPE"); typ == "AUDIO" {
				group, _ := m3u8Attr(line, "GROUP-ID")
				uri, _ := m3u8Attr(line, "URI")
				m.audio = append(m.audio, hlsAudioMedia{group: group, uri: uri})
			}
		case line != "" && !strings.HasPrefix(line, "#"):
			if pending != nil {
				pending.uri = line
				m.variants = append(m.variants, *pending)
				pending = nil
			}
		}
	}
	return m, nil
}

// hlsSingleMP4 proves that the media playlist `uri` (relative to the directory of
// masterKey) is one unencrypted, continuous, complete playlist whose init
// section and every segment name the SAME same-directory .mp4 object, and
// returns that object's storage key. PeerTube's byte-range HLS is exactly this.
func hlsSingleMP4(ctx context.Context, blobs storage.Backend, masterKey, uri string) (string, error) {
	if !hlsLeafName.MatchString(uri) || !strings.HasSuffix(uri, ".m3u8") {
		return "", errors.New("media: HLS playlist reference is not a local playlist")
	}
	dir := path.Dir(masterKey)
	lines, err := readHLSManifest(ctx, blobs, path.Join(dir, uri))
	if err != nil {
		return "", err
	}
	var init, object string
	var complete bool
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "#EXT-X-KEY:"), strings.HasPrefix(line, "#EXT-X-DISCONTINUITY"):
			return "", errors.New("media: HLS requires unencrypted continuous media")
		case strings.HasPrefix(line, "#EXT-X-MAP:"):
			value, _ := m3u8Attr(line, "URI")
			if init != "" && value != init {
				return "", errors.New("media: HLS has multiple init objects")
			}
			init = value
		case line == "#EXT-X-ENDLIST":
			complete = true
		case line != "" && !strings.HasPrefix(line, "#"):
			if object != "" && line != object {
				return "", errors.New("media: HLS requires a single MP4 object")
			}
			object = line
		}
	}
	if !complete || object != init || !hlsLeafName.MatchString(object) || !strings.HasSuffix(object, ".mp4") {
		return "", errors.New("media: HLS requires a complete single-file MP4 variant")
	}
	return path.Join(dir, object), nil
}

func readHLSManifest(ctx context.Context, blobs storage.Backend, key string) ([]string, error) {
	r, err := blobs.Open(ctx, key)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	b, err := io.ReadAll(io.LimitReader(r, maxHLSManifestBytes+1))
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(b) > maxHLSManifestBytes || strings.TrimSpace(lines[0]) != "#EXTM3U" {
		return nil, errors.New("media: invalid or oversized HLS manifest")
	}
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	return lines, nil
}
