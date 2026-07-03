package live

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/google/uuid"
)

// ErrNoRecording means no finished recording exists for a stream (the media
// server did not record the session, or it has not been flushed to disk yet).
// RunReplay treats it as a benign no-op rather than a failure.
var ErrNoRecording = errors.New("live: no recording")

// RecordingStore resolves the finished-session recording for a live stream. The
// media server writes it (keyed by stream ID so the raw stream key never touches
// the filesystem); the store hands the api a reader to feed the replay pipeline.
type RecordingStore interface {
	// OpenRecording returns a reader for the newest recording of the given stream
	// (the caller closes it) and its filename (for the container extension), or
	// ErrNoRecording when none exists.
	OpenRecording(streamID uuid.UUID) (io.ReadCloser, string, error)
}

// recordingSubdir is the subdirectory of LIVE_HLS_ROOT the media server writes
// session recordings into (segments/playlists live at the root; recordings are
// namespaced so they are not mistaken for HLS output).
const recordingSubdir = "rec"

// recordingExts are the container extensions a recorded session may have. FLV is
// the RTMP recorder's native output; MP4 covers a media server configured to
// remux on close. Both are accepted originals by the video pipeline.
var recordingExts = []string{".flv", ".mp4"}

// DirRecordingStore reads session recordings from a directory tree the media
// server shares with the api (rooted at LIVE_HLS_ROOT). Recordings live under
// <root>/rec and are named by stream ID (optionally with a media-server suffix,
// e.g. "<id>-1720000000.flv"); the newest match wins so a permanent stream's
// latest session is the one republished.
type DirRecordingStore struct{ root string }

// NewDirRecordingStore builds a store rooted at dir (LIVE_HLS_ROOT). A zero-value
// (empty) root yields a store whose OpenRecording always reports ErrNoRecording,
// so wiring is unconditional and replay simply stays dormant until configured.
func NewDirRecordingStore(dir string) *DirRecordingStore {
	return &DirRecordingStore{root: strings.TrimRight(dir, string(os.PathSeparator))}
}

// OpenRecording finds the newest recording file for streamID under <root>/rec and
// opens it. Only files whose base name is the stream ID (optionally followed by a
// media-server suffix) and whose extension is a known container are considered —
// the id gate keeps one stream's stop from ever serving another's recording, and
// the fixed extension set plus the id prefix prevent path traversal.
func (d *DirRecordingStore) OpenRecording(streamID uuid.UUID) (io.ReadCloser, string, error) {
	if d.root == "" {
		return nil, "", ErrNoRecording
	}
	dir := filepath.Join(d.root, recordingSubdir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		// Missing rec dir (nothing recorded yet) is not an error.
		if errors.Is(err, os.ErrNotExist) {
			return nil, "", ErrNoRecording
		}
		return nil, "", err
	}
	id := streamID.String()

	type candidate struct {
		name    string
		modTime int64
	}
	var matches []candidate
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !recordingBelongsToStream(e.Name(), id) {
			continue
		}
		info, ierr := e.Info()
		if ierr != nil {
			continue
		}
		matches = append(matches, candidate{name: e.Name(), modTime: info.ModTime().UnixNano()})
	}
	if len(matches) == 0 {
		return nil, "", ErrNoRecording
	}
	// Newest first (tie-break on name for determinism).
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].modTime != matches[j].modTime {
			return matches[i].modTime > matches[j].modTime
		}
		return matches[i].name > matches[j].name
	})

	path := filepath.Join(dir, matches[0].name)
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, "", ErrNoRecording
		}
		return nil, "", err
	}
	return f, matches[0].name, nil
}

// recordingBelongsToStream reports whether a recording filename belongs to the
// stream id: the base name (extension stripped) is exactly the id or the id
// followed by a media-server suffix ("<id>-..."), and the extension is a known
// container. A plain filename (no separators) is required, so it can never encode
// a path escape.
func recordingBelongsToStream(name, id string) bool {
	if name == "" || strings.ContainsAny(name, "/\\") {
		return false
	}
	ext := strings.ToLower(filepath.Ext(name))
	known := false
	for _, e := range recordingExts {
		if ext == e {
			known = true
			break
		}
	}
	if !known {
		return false
	}
	base := strings.TrimSuffix(name, filepath.Ext(name))
	return base == id || strings.HasPrefix(base, id+"-")
}
