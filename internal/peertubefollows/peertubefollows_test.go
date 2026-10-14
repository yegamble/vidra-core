package peertubefollows

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

type countingRepo struct {
	Repo
	added []string
	made  int
}

func (r *countingRepo) CreatePeerTubeFollowImport(context.Context, uuid.UUID) (uuid.UUID, error) {
	r.made++
	return uuid.New(), nil
}
func (r *countingRepo) AddPeerTubeFollowImportTargets(_ context.Context, a sqlcgen.AddPeerTubeFollowImportTargetsParams) error {
	r.added = a.Column3
	return nil
}

func TestEnqueueNormalisesDedupesAndCaps(t *testing.T) {
	r := &countingRepo{}
	s := &Service{Repo: r}
	in := []string{" @A@x.example ", "a@x.example", "", "b@y.example"}
	for i := 0; i < MaxTargets+50; i++ {
		in = append(in, fmt.Sprintf("u%d@z.example", i))
	}
	q, err := s.Enqueue(context.Background(), uuid.New(), in)
	if err != nil || q.Queued != MaxTargets || q.JobID == nil || r.added[0] != "a@x.example" || r.added[1] != "b@y.example" {
		t.Fatalf("%+v %v first=%v", q, err, r.added[:2])
	}
}

func TestEnqueueFederationDisabledCreatesNoJob(t *testing.T) {
	r := &countingRepo{}
	s := &Service{Repo: r, Enabled: func() bool { return false }}
	q, err := s.Enqueue(context.Background(), uuid.New(), []string{"a@x.example", "b@y.example"})
	if err != nil || q.JobID != nil || q.Queued != 0 || q.SkippedFederation != 2 || r.made != 0 {
		t.Fatalf("%+v %v made=%d", q, err, r.made)
	}
}
