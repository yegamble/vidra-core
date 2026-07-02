package federation

import (
	"context"
	"errors"
	"testing"
)

type fakeRepo struct {
	users, videos, comments int64
	err                     error
}

func (f fakeRepo) CountUsers(context.Context) (int64, error)        { return f.users, f.err }
func (f fakeRepo) CountPublicVideos(context.Context) (int64, error) { return f.videos, f.err }
func (f fakeRepo) CountComments(context.Context) (int64, error)     { return f.comments, f.err }

func TestNodeInfoUsage(t *testing.T) {
	svc := NewService(fakeRepo{users: 7, videos: 3, comments: 11})
	got, err := svc.NodeInfoUsage(context.Background())
	if err != nil {
		t.Fatalf("NodeInfoUsage: %v", err)
	}
	want := NodeInfoUsage{Users: 7, LocalPosts: 3, LocalComments: 11}
	if got != want {
		t.Errorf("usage = %+v, want %+v", got, want)
	}
}

func TestNodeInfoUsageErrorPropagates(t *testing.T) {
	sentinel := errors.New("db down")
	_, err := NewService(fakeRepo{err: sentinel}).NodeInfoUsage(context.Background())
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want %v", err, sentinel)
	}
}
