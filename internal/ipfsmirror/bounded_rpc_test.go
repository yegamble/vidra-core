package ipfsmirror

import (
	"context"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/ipfs"
)

type boundedProbeClient struct {
	ipfs.Client
	t *testing.T
}

func (c boundedProbeClient) bounded(ctx context.Context) {
	c.t.Helper()
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > time.Minute {
		c.t.Error("routine node probe has no short RPC deadline")
	}
}
func (c boundedProbeClient) Version(ctx context.Context) (string, error) {
	c.bounded(ctx)
	return "test", nil
}
func (c boundedProbeClient) ListPins(ctx context.Context, _ int) (map[string]struct{}, error) {
	c.bounded(ctx)
	return map[string]struct{}{}, nil
}

func TestRoutineRPCBoundedWithLongLivedCopyClient(t *testing.T) {
	s := New(newFakeRepo(), &fakeLookups{}, nil, boundedProbeClient{t: t}, testConfig())
	if _, err := s.Status(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.VerifyPins(context.Background())
}
