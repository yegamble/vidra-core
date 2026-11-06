package peertubeimport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/storage"
)

func mixedS3Source(t *testing.T, root string, objects map[string]string) (storage.Backend, func(string) int) {
	t.Helper()
	var mu sync.Mutex
	gets := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.URL.Path, "/source/")
		body, ok := objects[key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.Header().Set("ETag", `"fixture"`)
		w.Header().Set("Last-Modified", time.Unix(1700000000, 0).UTC().Format(http.TimeFormat))
		if r.Method == http.MethodGet {
			mu.Lock()
			gets[key]++
			mu.Unlock()
			_, _ = io.WriteString(w, body)
		}
	}))
	t.Cleanup(srv.Close)
	source, err := OpenSourceStorage(SourceStorageConfig{Backend: "s3", LocalRoot: root,
		S3Endpoint: strings.TrimPrefix(srv.URL, "http://"), S3Bucket: "source", S3Region: "us-east-1",
		S3AccessKey: "test-key", S3SecretKey: "test-secret", S3ForcePathStyle: true})
	if err != nil {
		t.Fatal(err)
	}
	return source, func(key string) int { mu.Lock(); defer mu.Unlock(); return gets[key] }
}

func writeMixedLocal(t *testing.T, root, key, body string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestMixedSourceOptInAndPrivateProgressive(t *testing.T) {
	root := t.TempDir()
	writeMixedLocal(t, root, "web-videos/private/movie.mp4", "private bytes")
	plain, _ := mixedS3Source(t, "", nil)
	if _, ok := plain.(*storage.S3); !ok {
		t.Fatal("ordinary S3 source changed type")
	}
	if _, err := plain.Open(context.Background(), "web-videos/private/movie.mp4"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("unconfigured source read local bytes")
	}
	source, _ := mixedS3Source(t, root, nil)
	dest := newSizeRecordingBackend(t)
	im := &Importer{srcMedia: source, destMedia: dest}
	n, sum, err := im.copyMedia(context.Background(), "web-videos/movie.mp4", "web-videos/copied.mp4")
	want := sha256.Sum256([]byte("private bytes"))
	if err != nil || n != 13 || sum != hex.EncodeToString(want[:]) || string(dest.written["web-videos/copied.mp4"]) != "private bytes" {
		t.Fatalf("private source overlay copy failed: %d %s %v", n, sum, err)
	}
	if _, err := os.Stat(filepath.Join(root, "web-videos/movie.mp4")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("native private source path was flattened")
	}
}

func TestMixedSourceHLSPreservesServerCopyPerObject(t *testing.T) {
	for _, private := range []bool{false, true} {
		t.Run(strconv.FormatBool(private), func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			prefix, actual := "streaming-playlists/hls/mixed", "streaming-playlists/hls/mixed"
			if private {
				actual = "streaming-playlists/hls/private/mixed"
			}
			objects := map[string]string{actual + "/master.m3u8": "#EXTM3U\nlocal.mp4\nremote.mp4\n", actual + "/remote.mp4": "remote"}
			writeMixedLocal(t, root, actual+"/local.mp4", "local")
			source, gets := mixedS3Source(t, root, objects)
			dest, _ := storage.NewLocal(t.TempDir())
			copied := 0
			copier := copyFunc(func(ctx context.Context, primary storage.Backend, from, to string, _ int64) (int64, error) {
				if _, ok := primary.(*storage.S3); !ok {
					t.Fatal("copier lost concrete S3 primary")
				}
				body, ok := objects[from]
				if !ok {
					return 0, storage.ErrNotFound
				}
				copied++
				return dest.Put(ctx, to, strings.NewReader(body))
			})
			im := &Importer{srcMedia: source, destMedia: dest, copyMediaServer: copier}
			if err := im.copyHLSTree(ctx, prefix, "master.m3u8", 0); err != nil {
				t.Fatal(err)
			}
			if copied != 1 || im.copyMediaDisabled.Load() || gets(actual+"/remote.mp4") != 0 {
				t.Fatal("local object disabled server copy or downloaded S3 payload")
			}
			for _, name := range []string{"local", "remote"} {
				rc, err := dest.Open(ctx, prefix+"/"+name+".mp4")
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(rc)
				_ = rc.Close()
				if err != nil || string(body) != name {
					t.Fatal("mixed HLS bytes differ")
				}
			}
		})
	}
}

type mixedPartialDestination struct {
	storage.Backend
	writes int
}

func (d *mixedPartialDestination) Put(ctx context.Context, key string, r io.Reader) (int64, error) {
	if !strings.HasSuffix(key, "/local.mp4") {
		return d.Backend.Put(ctx, key, r)
	}
	d.writes++
	n, _ := d.Backend.Put(ctx, key, io.LimitReader(r, 1))
	return n, storage.ErrCopyUnavailable
}

func TestMixedSourceLocalWriteFailureIsNeverRetried(t *testing.T) {
	root := t.TempDir()
	prefix := "streaming-playlists/hls/partial"
	writeMixedLocal(t, root, prefix+"/local.mp4", "local")
	source, _ := mixedS3Source(t, root, map[string]string{prefix + "/master.m3u8": "#EXTM3U\nlocal.mp4\n"})
	local, _ := storage.NewLocal(t.TempDir())
	dest := &mixedPartialDestination{Backend: local}
	im := &Importer{srcMedia: source, destMedia: dest, copyMediaServer: copyFunc(func(context.Context, storage.Backend, string, string, int64) (int64, error) {
		return 0, storage.ErrNotFound
	})}
	if err := im.copyHLSTree(context.Background(), prefix, "master.m3u8", 0); !errors.Is(err, storage.ErrCopyUnavailable) || dest.writes != 1 || im.copyMediaDisabled.Load() {
		t.Fatalf("uncertain local write retried: writes=%d err=%v", dest.writes, err)
	}
}
