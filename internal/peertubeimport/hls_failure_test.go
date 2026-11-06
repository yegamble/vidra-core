package peertubeimport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/vidra/vidra-core/internal/storage"
)

type hlsHTTPFailure int

func (e hlsHTTPFailure) Error() string       { return "https://media.invalid/?token=fixture-private-detail" }
func (e hlsHTTPFailure) HTTPStatusCode() int { return int(e) }

type hlsTimeoutFailure struct{}

func (hlsTimeoutFailure) Error() string { return "fixture-private-detail" }
func (hlsTimeoutFailure) Timeout() bool { return true }

func TestHLSFailureNotesRetainOnlySafeStageAndClass(t *testing.T) {
	for _, test := range []struct {
		stage hlsCopyStage
		cause error
		want  string
	}{
		{hlsSourceOpen, fmt.Errorf("private key: %w", storage.ErrNotFound), "source_open/not_found"},
		{hlsManifestRead, io.ErrUnexpectedEOF, "manifest_read/truncated"},
		{hlsServerCopy, hlsHTTPFailure(503), "server_copy/http_503"},
		{hlsStreamCopy, context.DeadlineExceeded, "stream_copy/deadline_exceeded"},
		{hlsStreamCopy, context.Canceled, "stream_copy/canceled"},
		{hlsStreamCopy, hlsTimeoutFailure{}, "stream_copy/timeout"},
		{hlsServerCopy, hlsHTTPFailure(999), "server_copy/other"},
		{255, errors.New("fixture-private-detail"), "tree/other"},
	} {
		failure := &hlsCopyFailure{stage: test.stage, cause: test.cause}
		wrapped := fmt.Errorf("fixture-private-detail: %w", failure)
		want := "HLS copy incomplete; rerun required (" + test.want + ")"
		if got := hlsCopyFailureNote(wrapped); got != want || strings.Contains(got, "fixture-private-detail") || failure.Error() != want {
			t.Fatalf("unsafe or unhelpful diagnostic: %q, want %q", got, want)
		}
		if !errors.Is(failure, test.cause) {
			t.Fatal("diagnostic wrapper lost the original error identity")
		}
	}
}

type hlsFailPut struct{ storage.Backend }

func (hlsFailPut) Put(context.Context, string, io.Reader) (int64, error) {
	return 0, hlsHTTPFailure(403)
}

func TestHLSTreeRetainsSourceOpenAndStreamFailureStages(t *testing.T) {
	ctx := context.Background()
	src, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	im := &Importer{srcMedia: src, destMedia: hlsFailPut{}}
	if err := im.copyHLSTree(ctx, "tree", "master.m3u8", 0); err == nil || hlsCopyFailureNote(err) != "HLS copy incomplete; rerun required (source_open/not_found)" {
		t.Fatalf("source failure lost context: %v", err)
	}
	if _, err := src.Put(ctx, "tree/master.m3u8", strings.NewReader("#EXTM3U\n")); err != nil {
		t.Fatal(err)
	}
	if err := im.copyHLSTree(ctx, "tree", "master.m3u8", 0); err == nil || hlsCopyFailureNote(err) != "HLS copy incomplete; rerun required (stream_copy/http_403)" {
		t.Fatalf("stream failure lost context: %v", err)
	}
}
