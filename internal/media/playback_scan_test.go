package media

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vidra/vidra-core/internal/processedupload"
	"github.com/vidra/vidra-core/internal/storage"
)

type playbackScannerFunc func(context.Context, io.Reader) (bool, error)

func (f playbackScannerFunc) ScanReader(ctx context.Context, r io.Reader) (bool, error) {
	return f(ctx, r)
}

func TestPlaybackScanRefusesOutputBeforeStorage(t *testing.T) {
	for _, test := range []struct {
		name      string
		clean     bool
		failure   error
		permanent bool
	}{
		{name: "denied", permanent: true},
		{name: "inspection limit", failure: ErrScanLimit, permanent: true},
		{name: "scanner unavailable", failure: errors.New("scanner unavailable")},
		{name: "clean", clean: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			blobs, err := storage.NewLocal(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			tc := NewHLSTranscoder(blobs)
			tc.SetOutputScanner(playbackScannerFunc(func(_ context.Context, r io.Reader) (bool, error) {
				_, err := io.Copy(io.Discard, r)
				if err != nil {
					return false, err
				}
				return test.clean, test.failure
			}))
			const key = "720p/video.mp4"
			n, err := tc.outputStore().Put(ctx, key, strings.NewReader("rung"))
			var scanErr *playbackScanError
			if errors.As(err, &scanErr) == test.clean || IsPermanent(err) != test.permanent {
				t.Fatalf("scan error = %v, permanent = %t", err, IsPermanent(err))
			}
			exists, existsErr := blobs.Exists(ctx, key)
			if existsErr != nil || exists != test.clean || (n == 4) != test.clean {
				t.Fatalf("stored = %t, bytes = %d, error = %v", exists, n, existsErr)
			}
		})
	}
}

func TestPlaybackScanRejectsOversizedProgressiveFileBeforeReading(t *testing.T) {
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tc := NewHLSTranscoder(blobs)
	tc.SetOutputScanner(playbackScannerFunc(func(context.Context, io.Reader) (bool, error) {
		t.Fatal("opened scanner for an output known to exceed the whole-file limit")
		return false, nil
	}))
	f, err := os.Create(filepath.Join(t.TempDir(), "long.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Truncate(processedupload.MaxScannedBytes + 1); err != nil {
		t.Fatal(err)
	}
	const key = "1080p/video.mp4"
	ctx := context.Background()
	if _, err := tc.outputStore().Put(ctx, key, f); !IsPermanent(err) || !strings.Contains(err.Error(), processedupload.ErrScanTooLarge.Error()) {
		t.Fatalf("wanted a permanent whole-file limit error, got %v", err)
	}
	if exists, err := blobs.Exists(ctx, key); err != nil || exists {
		t.Fatalf("oversized output stored = %t, error = %v", exists, err)
	}
}

func TestOversizedOptionalRetryDropsOnlyTheOverLimitObject(t *testing.T) {
	ctx := context.Background()
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tools := packageTools{blobs: blobs, outputLimit: processedupload.MaxScannedBytes}
	for _, tc := range []struct {
		name string
		size int64
		over bool
	}{{"at limit", processedupload.MaxScannedBytes, false}, {"over limit", processedupload.MaxScannedBytes + 1, true}} {
		t.Run(tc.name, func(t *testing.T) {
			const key = "r1/720p/video.mp4"
			if _, err := blobs.Put(ctx, key, strings.NewReader("previous generation attempt")); err != nil {
				t.Fatal(err)
			}
			local := filepath.Join(t.TempDir(), "video.mp4")
			if err := os.WriteFile(local, nil, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Truncate(local, tc.size); err != nil {
				t.Fatal(err)
			}
			omitted, err := tools.omitOversized(ctx, local, key)
			if err != nil || omitted != tc.over {
				t.Fatalf("omitted %t, error %v", omitted, err)
			}
			if _, err := os.Stat(local); errors.Is(err, os.ErrNotExist) != tc.over {
				t.Fatalf("scratch file after size check: %v", err)
			}
			if exists, err := blobs.Exists(ctx, key); err != nil || exists == tc.over {
				t.Fatalf("old stored file exists %t, error %v", exists, err)
			}
		})
	}
}
