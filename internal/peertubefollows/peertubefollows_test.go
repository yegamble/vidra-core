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
	added    []string
	made     int
	followed []string
	pending  int64
}

func (r *countingRepo) CreatePeerTubeFollowImport(context.Context, uuid.UUID) (uuid.UUID, error) {
	r.made++
	return uuid.New(), nil
}
func (r *countingRepo) AddPeerTubeFollowImportTargets(_ context.Context, a sqlcgen.AddPeerTubeFollowImportTargetsParams) (int64, error) {
	r.added = a.Column3
	return int64(len(a.Column3)), nil
}
func (r *countingRepo) ListFollowedRemoteHandles(context.Context, uuid.UUID) ([]string, error) {
	return r.followed, nil
}
func (r *countingRepo) CountPendingPeerTubeFollowTargets(context.Context, uuid.UUID) (int64, error) {
	return r.pending, nil
}

func TestEnqueueNormalisesDedupesAndCaps(t *testing.T) {
	r := &countingRepo{}
	s := &Service{Repo: r}
	in := []string{" @A@X.example. ", "a@x.example", "", "b@y.example:8443", "https://z.example/c/u", "nohost", "a@b@c"}
	for i := 0; i < MaxTargets+50; i++ {
		in = append(in, fmt.Sprintf("u%d@h%d.example", i, i))
	}
	q, err := s.Enqueue(context.Background(), uuid.New(), in)
	if err != nil || q.Queued != MaxTargets || q.JobID == nil || r.added[0] != "a@x.example" || r.added[1] != "b@y.example" {
		t.Fatalf("%+v %v first=%v", q, err, r.added[:2])
	}
	for _, a := range r.added {
		if a == "c@z.example" || a == "u@z.example" {
			t.Fatalf("URL target queued: %s", a)
		}
	}
}

func TestEnqueueCapsPerHostAndSkipsFollowedAndPending(t *testing.T) {
	r := &countingRepo{followed: []string{"known@x.example"}, pending: MaxTargets - 2}
	s := &Service{Repo: r}
	var in []string
	for i := 0; i < MaxPerHost+10; i++ {
		in = append(in, fmt.Sprintf("u%d@big.example", i))
	}
	in = append(in, "known@x.example", "new@x.example")
	q, _ := s.Enqueue(context.Background(), uuid.New(), in)
	if q.Queued != 2 { // pending cap leaves room for two
		t.Fatalf("pending cap: %+v", q)
	}
	r.pending = 0
	q, _ = s.Enqueue(context.Background(), uuid.New(), in)
	if q.Queued != MaxPerHost+1 { // 20 big.example + new@x.example; known@x.example skipped
		t.Fatalf("per-host cap: %+v", q)
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
