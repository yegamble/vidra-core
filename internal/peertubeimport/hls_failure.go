package peertubeimport

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/vidra/vidra-core/internal/storage"
)

type hlsCopyStage uint8

const (
	hlsSourceOpen hlsCopyStage = iota
	hlsManifestRead
	hlsServerCopy
	hlsStreamCopy
)

// Backend errors can contain signed URLs or credentials. Preserve their error
// identity for control flow, but expose only closed stage/class vocabularies.
type hlsCopyFailure struct {
	stage hlsCopyStage
	cause error
}

func (e *hlsCopyFailure) Error() string { return hlsCopyFailureNote(e) }
func (e *hlsCopyFailure) Unwrap() error { return e.cause }

func hlsCopyFailureNote(err error) string {
	stage, reason := "tree", "other"
	var failure *hlsCopyFailure
	if errors.As(err, &failure) {
		switch failure.stage {
		case hlsSourceOpen:
			stage = "source_open"
		case hlsManifestRead:
			stage = "manifest_read"
		case hlsServerCopy:
			stage = "server_copy"
		case hlsStreamCopy:
			stage = "stream_copy"
		}
	}
	var status interface{ HTTPStatusCode() int }
	var timeout interface{ Timeout() bool }
	switch {
	case errors.Is(err, context.Canceled):
		reason = "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		reason = "deadline_exceeded"
	case errors.Is(err, storage.ErrNotFound):
		reason = "not_found"
	case errors.Is(err, io.ErrUnexpectedEOF):
		reason = "truncated"
	case errors.As(err, &status) && status.HTTPStatusCode() >= 100 && status.HTTPStatusCode() <= 599:
		reason = fmt.Sprintf("http_%d", status.HTTPStatusCode())
	case errors.As(err, &timeout) && timeout.Timeout():
		reason = "timeout"
	}
	return fmt.Sprintf("HLS copy incomplete; rerun required (%s/%s)", stage, reason)
}
