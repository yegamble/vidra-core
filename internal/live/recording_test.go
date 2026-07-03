package live

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRecordingBelongsToStream(t *testing.T) {
	id := uuid.New().String()
	other := uuid.New().String()
	cases := []struct {
		name string
		file string
		want bool
	}{
		{"exact flv", id + ".flv", true},
		{"exact mp4", id + ".mp4", true},
		{"suffixed unix flv", id + "-1720000000.flv", true},
		{"suffixed mp4", id + "-2.mp4", true},
		{"uppercase ext", id + ".FLV", true},
		{"other stream", other + ".flv", false},
		{"prefix collision (no dash)", id + "x.flv", false},
		{"unknown ext", id + ".ts", false},
		{"playlist not a recording", id + ".m3u8", false},
		{"empty", "", false},
		{"path escape", "../" + id + ".flv", false},
		{"nested path", "sub/" + id + ".flv", false},
		{"bare id no ext", id, false},
	}
	for _, tc := range cases {
		if got := recordingBelongsToStream(tc.file, id); got != tc.want {
			t.Errorf("%s: recordingBelongsToStream(%q) = %v, want %v", tc.name, tc.file, got, tc.want)
		}
	}
}

func TestDirRecordingStoreOpensNewest(t *testing.T) {
	root := t.TempDir()
	recDir := filepath.Join(root, recordingSubdir)
	if err := os.MkdirAll(recDir, 0o755); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	other := uuid.New()

	// Two sessions for the stream (older + newer) plus an unrelated stream's file.
	older := filepath.Join(recDir, id.String()+"-100.flv")
	newer := filepath.Join(recDir, id.String()+"-200.flv")
	writeFile(t, older, "OLD")
	writeFile(t, newer, "NEW")
	writeFile(t, filepath.Join(recDir, other.String()+".flv"), "OTHER")
	// Make newer genuinely newer on disk.
	_ = os.Chtimes(older, time.Now().Add(-2*time.Hour), time.Now().Add(-2*time.Hour))
	_ = os.Chtimes(newer, time.Now(), time.Now())

	store := NewDirRecordingStore(root)
	rc, name, err := store.OpenRecording(id)
	if err != nil {
		t.Fatalf("OpenRecording: %v", err)
	}
	defer func() { _ = rc.Close() }()
	if name != id.String()+"-200.flv" {
		t.Errorf("opened %q, want the newest session file", name)
	}
	b, _ := io.ReadAll(rc)
	if string(b) != "NEW" {
		t.Errorf("recording bytes = %q, want the newest session content", string(b))
	}
}

func TestDirRecordingStoreNoRecording(t *testing.T) {
	id := uuid.New()

	// Empty root → ErrNoRecording (store dormant).
	if _, _, err := NewDirRecordingStore("").OpenRecording(id); !errors.Is(err, ErrNoRecording) {
		t.Errorf("empty root = %v, want ErrNoRecording", err)
	}
	// Configured root but no rec dir yet → ErrNoRecording (nothing recorded).
	if _, _, err := NewDirRecordingStore(t.TempDir()).OpenRecording(id); !errors.Is(err, ErrNoRecording) {
		t.Errorf("missing rec dir = %v, want ErrNoRecording", err)
	}
	// rec dir exists but only another stream's file → ErrNoRecording.
	root := t.TempDir()
	recDir := filepath.Join(root, recordingSubdir)
	_ = os.MkdirAll(recDir, 0o755)
	writeFile(t, filepath.Join(recDir, uuid.New().String()+".flv"), "x")
	if _, _, err := NewDirRecordingStore(root).OpenRecording(id); !errors.Is(err, ErrNoRecording) {
		t.Errorf("no matching file = %v, want ErrNoRecording", err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
