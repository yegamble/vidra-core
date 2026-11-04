package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vidra/vidra-core/internal/blobverify"
)

func TestFindingsFileIsExclusivePrivateAndPreservesExistingEvidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "findings.jsonl")
	f, err := openFindings(path)
	if err != nil {
		t.Fatal(err)
	}
	want := blobverify.Finding{Root: "streaming-playlists/video/master.m3u8", Key: "missing.mp4", Reason: "missing"}
	if err := writeFinding(f, want); err != nil {
		t.Fatal(err)
	}
	if err := finishFindings(f); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("evidence permissions: %v, %v", info, err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got blobverify.Finding
	if err := json.Unmarshal(bytes.TrimSuffix(before, []byte{'\n'}), &got); err != nil || got != want || bytes.Count(before, []byte{'\n'}) != 1 {
		t.Fatalf("not one complete JSON line: %q, %v", before, err)
	}
	if _, err := openFindings(path); !errors.Is(err, os.ErrExist) {
		t.Fatalf("existing evidence reopened: %v", err)
	}
	link := filepath.Join(filepath.Dir(path), "alias.jsonl")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := openFindings(link); !errors.Is(err, os.ErrExist) {
		t.Fatalf("evidence symlink followed: %v", err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("existing evidence changed")
	}
}

type findingsWriteFunc func([]byte) (int, error)

func (f findingsWriteFunc) Write(p []byte) (int, error) { return f(p) }

func TestFindingWriteFailureAndShortWriteCannotPass(t *testing.T) {
	stop := errors.New("disk full")
	for _, tc := range []struct {
		write findingsWriteFunc
		want  error
	}{
		{func([]byte) (int, error) { return 0, stop }, stop},
		{func(p []byte) (int, error) { return len(p) - 1, nil }, io.ErrShortWrite},
	} {
		if err := writeFinding(tc.write, blobverify.Finding{Reason: "missing"}); !errors.Is(err, tc.want) {
			t.Fatalf("write failure lost: %v", err)
		}
	}
}

type findingsCloseProbe struct {
	syncErr, closeErr error
	closed            bool
}

func (f *findingsCloseProbe) Sync() error  { return f.syncErr }
func (f *findingsCloseProbe) Close() error { f.closed = true; return f.closeErr }

func TestFindingsFinalizationPreservesSyncAndCloseFailures(t *testing.T) {
	syncErr, closeErr := errors.New("sync failed"), errors.New("close failed")
	f := &findingsCloseProbe{syncErr: syncErr, closeErr: closeErr}
	if err := finishFindings(f); !errors.Is(err, syncErr) || !errors.Is(err, closeErr) || !f.closed {
		t.Fatalf("finalization failure lost: %v, closed=%v", err, f.closed)
	}
}

func TestVerifyBlobsRejectsEmptyFindingsPathBeforeConfiguration(t *testing.T) {
	var out, stderr bytes.Buffer
	if code := runVerifyBlobs([]string{"--findings-jsonl="}, &out, &stderr); code != 1 || !strings.Contains(stderr.String(), "nonempty") || out.Len() != 0 {
		t.Fatalf("empty findings path accepted: code=%d stderr=%q", code, stderr.String())
	}
}
