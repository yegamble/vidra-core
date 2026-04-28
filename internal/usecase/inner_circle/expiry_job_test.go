package inner_circle

import (
	"context"
	"errors"
	"testing"
	"time"

	"vidra-core/internal/domain"

	"github.com/google/uuid"
)

func TestExpiryJob_Sweep_AggregatesStats(t *testing.T) {
	memRepo := &fakeMembershipRepo{expireRet: [2]int{4, 2}}
	svc := NewMembershipService(memRepo, &fakeTierRepo{}, &fakeChannelLookup{}, &fakeBTCPay{})
	job := NewExpiryJob(svc, time.Minute, time.Hour)

	a, p, err := job.Sweep(context.Background())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if a != 4 || p != 2 {
		t.Fatalf("sweep returned (%d, %d), want (4, 2)", a, p)
	}
	gotA, gotP := job.Stats()
	if gotA != 4 || gotP != 2 {
		t.Fatalf("stats = (%d, %d), want (4, 2)", gotA, gotP)
	}
}

func TestExpiryJob_FailureIsolation(t *testing.T) {
	memRepo := &fakeMembershipRepo{expireErr: errors.New("db blip")}
	svc := NewMembershipService(memRepo, &fakeTierRepo{}, &fakeChannelLookup{}, &fakeBTCPay{})
	job := NewExpiryJob(svc, time.Minute, time.Hour)

	if _, _, err := job.Sweep(context.Background()); err == nil {
		t.Fatalf("expected error from failing sweep")
	}
	// The job should not panic, abort the goroutine, or affect other state.
	a, p := job.Stats()
	if a != 0 || p != 0 {
		t.Fatalf("stats after failure = (%d, %d), want (0, 0)", a, p)
	}
}

func TestExpiryJob_RunStopsOnContextDone(t *testing.T) {
	memRepo := &fakeMembershipRepo{}
	svc := NewMembershipService(memRepo, &fakeTierRepo{}, &fakeChannelLookup{}, &fakeBTCPay{})
	job := NewExpiryJob(svc, 50*time.Millisecond, time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		job.Run(ctx)
		close(done)
	}()
	time.Sleep(120 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("Run did not exit after context cancel")
	}
}

func TestExpiryJob_NewWithZeros_UsesDefaults(t *testing.T) {
	job := NewExpiryJob(NewMembershipService(&fakeMembershipRepo{}, &fakeTierRepo{}, &fakeChannelLookup{}, &fakeBTCPay{}), 0, 0)
	if job.interval != 5*time.Minute {
		t.Fatalf("interval default = %v, want 5m", job.interval)
	}
	if job.pendingTimeout != time.Hour {
		t.Fatalf("pendingTimeout default = %v, want 1h", job.pendingTimeout)
	}
}

// Ensure the membership domain helpers compile with the fake fixture types in
// this file (e.g. that fakeMembershipRepo continues to satisfy MembershipRepo).
var _ MembershipRepo = (*fakeMembershipRepo)(nil)

func TestFakeMembershipRepo_SatisfiesInterface(t *testing.T) {
	memRepo := &fakeMembershipRepo{listMineResult: []domain.InnerCircleMembership{{ID: uuid.New(), TierID: "vip"}}}
	if rows, err := memRepo.ListMine(context.Background(), uuid.New(), false); err != nil || len(rows) != 1 {
		t.Fatalf("listMine sanity check failed: err=%v rows=%d", err, len(rows))
	}
}
