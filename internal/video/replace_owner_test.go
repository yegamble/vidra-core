package video

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/storage"
)

// handOverProber stands in for the work between storing a replacement and
// swapping it in (scan, probe): while it runs, the video changes owner.
type handOverProber struct {
	repo  *fakeRepo
	video uuid.UUID
	to    uuid.UUID
	keys  []string
}

func (p *handOverProber) Probe(_ context.Context, key string) (media.Metadata, error) {
	p.keys = append(p.keys, key)
	v := p.repo.videos[p.video]
	v.OwnerID = p.to
	p.repo.videos[p.video] = v
	return media.Metadata{DurationSeconds: 5}, nil
}

// TestReplaceSourceRefusedWhenTheOwnerChangesMidway: the swap re-checks, in the
// same step, that the video still belongs to the owner it had when the
// replacement began. If not, nothing is swapped, the stored replacement is
// dropped, no update hook fires and the caller gets ErrReplaceConflict.
func TestReplaceSourceRefusedWhenTheOwnerChangesMidway(t *testing.T) {
	owner, recipient := uuid.New(), uuid.New()
	repo := newFakeRepo(owner)
	blobs, _ := storage.NewLocal(t.TempDir())
	svc := NewService(repo, blobs)
	id := publishWithOriginal(t, svc, owner, "old source bytes")

	prober := &handOverProber{repo: repo, video: id, to: recipient}
	updates := 0
	svc = NewService(repo, blobs, WithProber(prober), WithUpdateHook(func(context.Context, uuid.UUID, bool) { updates++ }))
	_, _, err := svc.ReplaceSource(context.Background(), owner, id, UploadInput{
		Filename: "v2.mp4", ContentType: "video/mp4", Reader: strings.NewReader("replacement"),
	}, false)
	if !errors.Is(err, ErrReplaceConflict) {
		t.Fatalf("ReplaceSource after a hand-over = %v, want ErrReplaceConflict", err)
	}
	if len(prober.keys) != 1 {
		t.Fatalf("probed %d objects, want 1", len(prober.keys))
	}
	if _, oerr := blobs.Open(context.Background(), prober.keys[0]); oerr == nil {
		t.Error("the refused replacement's object was left in storage")
	}
	orig := ""
	for _, f := range repo.files[id] {
		if f.Kind == "original" {
			orig = blobString(t, blobs, f.StorageKey)
		}
	}
	if orig != "old source bytes" || updates != 0 {
		t.Errorf("after a refused swap: original=%q updates=%d, want the old source and no hook", orig, updates)
	}
}
