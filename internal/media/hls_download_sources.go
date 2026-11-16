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

// maxHLSVariants caps how many STREAM-INF variants of one master are
// considered, so a hostile master cannot make a request resolve (and read the
// playlist of) an unbounded number of them.
const maxHLSVariants = 32

// HLSDownloadRung is one video variant of a master playlist that is provably a
// single complete MP4 object.
type HLSDownloadRung struct {
	Width, Height int
	// VideoKey is the storage key of the rung's MP4.
	VideoKey string
	// SplitAudio reports that the rung's video object carries NO audio: its
	// audio is a separate audio-group object (PeerTube's split-audio layout).
	// False means the rung's MP4 already contains its audio.
	SplitAudio bool
	// AudioKey is that audio object's key. It is filled only when the caller
	// asked for it (needAudio), so a video-only request never reads the audio
	// playlist.
	AudioKey string
}

// HLSDownloadResolver lazily proves what a master playlist's objects are. The
// master is read once on first use; each variant (and the audio) playlist is
// read at most once and only when asked for.
type HLSDownloadResolver struct {
	ctx       context.Context
	blobs     storage.Backend
	masterKey string

	loaded bool
	master hlsMaster
	err    error
	cache  map[string]hlsResolved
}

type hlsResolved struct {
	key string
	err error
}

// NewHLSDownloadResolver prepares a resolver; nothing is read yet.
func NewHLSDownloadResolver(ctx context.Context, blobs storage.Backend, masterKey string) *HLSDownloadResolver {
	return &HLSDownloadResolver{ctx: ctx, blobs: blobs, masterKey: masterKey, cache: map[string]hlsResolved{}}
}

func (r *HLSDownloadResolver) load() error {
	if r.loaded {
		return r.err
	}
	r.loaded = true
	if !strings.HasSuffix(r.masterKey, ".m3u8") {
		r.err = errors.New("media: HLS master key is not a playlist")
		return r.err
	}
	r.master, r.err = parseHLSMaster(r.ctx, r.blobs, r.masterKey)
	if len(r.master.variants) > maxHLSVariants {
		r.master.variants = r.master.variants[:maxHLSVariants]
	}
	return r.err
}

func (r *HLSDownloadResolver) resolve(uri string) (string, error) {
	if c, ok := r.cache[uri]; ok {
		return c.key, c.err
	}
	key, err := hlsSingleMP4(r.ctx, r.blobs, r.masterKey, uri)
	r.cache[uri] = hlsResolved{key, err}
	return key, err
}

// Rung finds the variant of the given HEIGHT that is a single complete MP4. Among
// variants of the same height one with an equal width wins, else the first (a
// recorded width can differ from the manifest's). A variant that is encrypted,
// discontinuous, multi-object, incomplete, or names anything but a
// same-directory leaf is skipped, as is one whose audio group cannot be proven;
// nothing here follows a URL. Only an unreadable or malformed master is an
// error. needAudio asks for the audio object's key as well (and skips a
// variant whose audio object is not a single complete MP4).
func (r *HLSDownloadResolver) Rung(height, widthHint int, needAudio bool) (HLSDownloadRung, bool, error) {
	if err := r.load(); err != nil {
		return HLSDownloadRung{}, false, err
	}
	var candidates []hlsMasterVariant
	for _, v := range r.master.variants {
		if v.height == height && v.width > 0 {
			if v.width == widthHint {
				candidates = append([]hlsMasterVariant{v}, candidates...)
			} else {
				candidates = append(candidates, v)
			}
		}
	}
	for _, v := range candidates {
		videoKey, err := r.resolve(v.uri)
		if err != nil {
			if cerr := r.ctx.Err(); cerr != nil {
				return HLSDownloadRung{}, false, cerr
			}
			continue
		}
		rung := HLSDownloadRung{Width: v.width, Height: v.height, VideoKey: videoKey}
		if v.audioGroup != "" {
			uri, separate, ok := r.master.audioURI(v.audioGroup)
			if !ok {
				continue // the audio is somewhere we cannot prove
			}
			rung.SplitAudio = separate
			if separate && needAudio {
				audioKey, err := r.resolve(uri)
				if err != nil {
					if cerr := r.ctx.Err(); cerr != nil {
						return HLSDownloadRung{}, false, cerr
					}
					continue
				}
				rung.AudioKey = audioKey
			}
		}
		return rung, true, nil
	}
	return HLSDownloadRung{}, false, nil
}

// Audio returns the single audio-only MP4 of the playlist's audio group (the
// group the first video variants name), or "" when there is none. It reads the
// master and that one audio playlist only.
func (r *HLSDownloadResolver) Audio() (string, error) {
	if err := r.load(); err != nil {
		return "", err
	}
	for _, v := range r.master.variants {
		if v.audioGroup == "" {
			continue
		}
		uri, separate, ok := r.master.audioURI(v.audioGroup)
		if !ok || !separate {
			continue
		}
		key, err := r.resolve(uri)
		if err != nil {
			if cerr := r.ctx.Err(); cerr != nil {
				return "", cerr
			}
			continue
		}
		return key, nil
	}
	return "", nil
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
