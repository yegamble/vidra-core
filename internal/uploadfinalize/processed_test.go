package uploadfinalize

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/processedupload"
	"github.com/vidra/vidra-core/internal/upload"
	"github.com/vidra/vidra-core/internal/video"
)

func TestPrivateInputOnlyAttachesScannedOutput(t *testing.T) {
	for _, tc := range []struct {
		name         string
		normalizeErr error
		disabled     bool
		permanent    bool
	}{
		{name: "accepted"},
		{name: "unavailable", disabled: true},
		{name: "scanner offline", normalizeErr: errors.New("offline")},
		{name: "infected", normalizeErr: processedupload.ErrInfected, permanent: true},
		{name: "inspection limit", normalizeErr: media.ErrScanLimit, permanent: true},
		{name: "output too large", normalizeErr: processedupload.ErrScanTooLarge, permanent: true},
		{name: "invalid input", normalizeErr: processedupload.ErrRejected, permanent: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			const raw = "private input bytes"
			sess := h.openAndFill(t, raw, upload.PurposeProcessed)
			if _, ok := video.AcceptedVideoExtGated(sess.Filename, true); ok {
				t.Fatal("staging filename can be attached by an older worker")
			}
			if !tc.disabled {
				h.svc.normalize = func(_ context.Context, r io.Reader, size int64) (*os.File, error) {
					b, err := io.ReadAll(r)
					if err != nil || string(b) != raw || size != int64(len(raw)) {
						t.Errorf("normalizer input mismatch: %d, %v", size, err)
					}
					if tc.normalizeErr != nil {
						return nil, tc.normalizeErr
					}
					file, err := os.CreateTemp(t.TempDir(), "processed")
					if err != nil {
						return nil, err
					}
					if _, err = file.WriteString("scanned output"); err == nil {
						_, err = file.Seek(0, io.SeekStart)
					}
					if err != nil {
						file.Close()
						return nil, err
					}
					return file, nil
				}
			}
			ctx := t.Context()
			job, err := h.svc.Enqueue(ctx, sess.ID, h.video, upload.PurposeProcessed, false)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = h.svc.DrainJobs(ctx, 1); err != nil {
				t.Fatal(err)
			}
			if tc.disabled || tc.normalizeErr != nil {
				state := h.jobs.get(job.ID).State
				if h.pipeline.attached != nil || h.pipeline.processedKey != "" || state == StateDone || (state == StateFailed) != tc.permanent {
					t.Fatalf("failed processing returned state %s, permanent %v", state, tc.permanent)
				}
				return
			}
			if !h.pipeline.reconstructed || string(h.pipeline.attached) != "scanned output" || h.jobs.get(job.ID).State != StateDone {
				t.Fatal("scanned output did not finish")
			}
			if exists, err := h.blobs.Exists(ctx, "uploads/"+sess.ID.String()+"/0"); err != nil || exists {
				t.Fatal("completed private input still in staging")
			}
		})
	}
}
