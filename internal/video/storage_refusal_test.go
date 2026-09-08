package video

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"

	"github.com/vidra/vidra-core/internal/storage"
)

// refusingStore is a backend that refuses every write with a real, classified
// storage failure — the read-only S3 credential and the full bucket A24 ran
// against MinIO, without the MinIO.
type refusingStore struct {
	storage.Backend
	err error
}

func (r refusingStore) Put(context.Context, string, io.Reader) (int64, error) { return 0, r.err }

func (r refusingStore) PutSized(context.Context, string, io.Reader, int64) (int64, error) {
	return 0, r.err
}

// A24 measured this and it must not regress: a refused write leaves the video a
// draft with no file row, no state change and nothing billed against the owner's
// daily quota. The refusal has to reach the caller CLASSIFIED — that is what
// turns the bare 500 into a 503 with an instruction — while the row accounting
// stays exactly where it was.
func TestAttachOriginalOnARefusedWriteLeavesNoHalfWrittenRow(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cause error
		class storage.ErrorClass
	}{
		{
			"write denied",
			minio.ErrorResponse{Code: "AccessDenied", Message: "Access Denied.", StatusCode: http.StatusForbidden},
			storage.ClassWriteDenied,
		},
		{
			"bucket quota",
			minio.ErrorResponse{Code: "XMinioAdminBucketQuotaExceeded", Message: "Bucket quota exceeded", StatusCode: http.StatusBadRequest},
			storage.ClassQuotaExceeded,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			owner := uuid.New()
			repo := newFakeRepo(owner)
			local, err := storage.NewLocal(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			var billed int
			svc := NewService(repo, refusingStore{
				Backend: local,
				// The shape the S3 backend produces: its own wrapped sentence,
				// classified at the boundary.
				err: &storage.Error{Class: tc.class, Op: "put", Err: tc.cause},
			}, WithUploadUsageRecorder(func(context.Context, uuid.UUID, int64) error {
				billed++
				return nil
			}))
			ctx := context.Background()
			v, err := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "private"})
			if err != nil {
				t.Fatal(err)
			}

			_, _, err = svc.AttachOriginal(ctx, owner, v.ID, UploadInput{
				Filename: "clip.mp4", Reader: strings.NewReader("bytes that will never land"),
			})
			if err == nil {
				t.Fatal("AttachOriginal succeeded against a store that refuses every write")
			}
			class, ok := storage.ClassOf(err)
			if !ok || class != tc.class {
				t.Fatalf("class = (%q, %v), want %q — an unclassified refusal is the bare 500 this closes", class, ok, tc.class)
			}
			if got := len(repo.files[v.ID]); got != 0 {
				t.Errorf("video_files rows = %d, want 0: a refused write must not leave a row pointing at bytes that are not there", got)
			}
			if got := repo.videos[v.ID].State; got != "draft" {
				t.Errorf("state = %q, want draft: the video must not advance to processing on a write that never happened", got)
			}
			if billed != 0 {
				t.Errorf("daily-quota ledger appended %d times, want 0: nothing was stored", billed)
			}
		})
	}
}

// The sentinels must stay unclassified through the service, or every missing
// object becomes a 503.
func TestAttachOriginalDoesNotClassifyAnOrdinaryFailure(t *testing.T) {
	owner := uuid.New()
	repo := newFakeRepo(owner)
	local, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(repo, refusingStore{Backend: local, err: errors.New("something else entirely")})
	ctx := context.Background()
	v, err := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "private"})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = svc.AttachOriginal(ctx, owner, v.ID, UploadInput{Filename: "clip.mp4", Reader: strings.NewReader("x")})
	if err == nil {
		t.Fatal("AttachOriginal succeeded")
	}
	if _, ok := storage.ClassOf(err); ok {
		t.Errorf("an unrelated failure was given a storage class: %v", err)
	}
}
