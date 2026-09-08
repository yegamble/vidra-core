package live

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

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
	// RemoveRecording deletes ONE recording by the exact filename OpenRecording
	// returned. By filename and not by stream id, because between the two calls
	// a permanent stream can have started another session — deleting "the newest
	// recording for this stream" could then delete a broadcast that is still
	// being written. A missing file is not an error.
	RemoveRecording(streamID uuid.UUID, filename string) error
	// PruneRecordings deletes recordings last modified before cutoff, at most
	// batch of them, oldest first. Returns how many it removed. It is the
	// LIVE_RECORDING_RETENTION > 0 sweep; see retention.go.
	PruneRecordings(cutoff time.Time, batch int) (int, error)
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

// RemoveRecording deletes one recording by name. The name is re-validated
// against the stream id before anything is unlinked — the caller passes back a
// filename this store produced, but a store that deletes whatever name it is
// handed is one bug away from deleting an arbitrary path, and the id gate plus
// the plain-filename rule is the same check that makes OpenRecording safe.
func (d *DirRecordingStore) RemoveRecording(streamID uuid.UUID, filename string) error {
	if d.root == "" {
		return nil
	}
	if !recordingBelongsToStream(filename, streamID.String()) {
		return ErrNoRecording
	}
	err := os.Remove(filepath.Join(d.root, recordingSubdir, filename))
	if err != nil && errors.Is(err, os.ErrNotExist) {
		// Already gone — the outcome the caller wanted.
		return nil
	}
	return err
}

// PruneRecordings deletes recordings older than cutoff, oldest first, up to
// batch.
//
// Oldest-first and batched for the reason every other prune in this codebase is:
// a volume that has been accumulating recordings since the instance was
// installed must not be swept in one pass that holds a directory listing of tens
// of thousands of entries and unlinks all of them in a single tick. Each pass
// takes the oldest `batch` and the next tick takes the next, so the backlog
// drains at a bounded rate and a steady state costs one directory read.
//
// It deletes by MODIFICATION TIME, not by the unix suffix in the filename: the
// suffix is the moment the recorder OPENED the file, and a long broadcast is
// still being written hours later. mtime is when the last byte landed, which is
// the only timestamp that answers "is this session over".
func (d *DirRecordingStore) PruneRecordings(cutoff time.Time, batch int) (int, error) {
	if d.root == "" || batch <= 0 {
		return 0, nil
	}
	dir := filepath.Join(d.root, recordingSubdir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}

	type stale struct {
		name    string
		modTime time.Time
	}
	var candidates []stale
	for _, e := range entries {
		if e.IsDir() || !recordingFile(e.Name()) {
			// Notably this SKIPS the `.keep` file the compose media service
			// plants: it is not a recording, and deleting it would let the HLS
			// cleanup rmdir() the recording directory out from under the
			// recorder — the A26 defect that meant replay had never once run.
			continue
		}
		info, ierr := e.Info()
		if ierr != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			candidates = append(candidates, stale{name: e.Name(), modTime: info.ModTime()})
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].modTime.Before(candidates[j].modTime) })
	if len(candidates) > batch {
		candidates = candidates[:batch]
	}
	removed := 0
	for _, c := range candidates {
		if rerr := os.Remove(filepath.Join(dir, c.name)); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
			// One unremovable file must not stop the sweep — a permissions
			// problem on a single recording would otherwise pin the whole
			// backlog forever.
			continue
		}
		removed++
	}
	return removed, nil
}

// recordingFile reports whether a directory entry is a session recording at all
// — a plain filename with a known container extension. It is the id-free half of
// recordingBelongsToStream, used by the retention sweep, which does not know (or
// need to know) which stream each file came from.
func recordingFile(name string) bool {
	if name == "" || strings.ContainsAny(name, "/\\") {
		return false
	}
	ext := strings.ToLower(filepath.Ext(name))
	for _, e := range recordingExts {
		if ext == e {
			return true
		}
	}
	return false
}
