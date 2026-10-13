package ipfsmirror

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vidra/vidra-core/internal/ipfs"
)

func budgetService(t *testing.T, budget int64) (*Service, *fakeRepo, *ipfs.FakeIPFSClient, *bytes.Buffer) {
	t.Helper()
	repo, client := newFakeRepo(), ipfs.NewFakeIPFSClient()
	cfg := testConfig()
	cfg.RepoBudgetBytes = budget
	var buf bytes.Buffer
	cfg.Logger = slog.New(slog.NewTextHandler(&buf, nil))
	return New(repo, &fakeLookups{videoOK: true, videoPrivacy: "public", videoState: "published", userOK: true, userActive: true}, newBlobs(t), client, cfg), repo, client, &buf
}

// TestUnmanagedCeilingStopsAddsButNotUnpins: F3. The legacy drain had no ceiling
// at all; at the budget it must stop ADDING and never stop REMOVING.
func TestUnmanagedCeilingStopsAddsButNotUnpins(t *testing.T) {
	ctx := context.Background()
	s, repo, client, logs := budgetService(t, 1000)
	vid := uuid.New()
	capKey := "captions/" + vid.String() + "/en.vtt"
	putBlob(t, s.blobs, capKey, "WEBVTT\n\nhi")
	seedPendingVideo(repo, capKey, string(ClassCaption), vid)
	// A row on its way out, drained in the same pass.
	goneKey := "captions/" + vid.String() + "/fr.vtt"
	seedPinned(repo, goneKey, string(ClassCaption), "bafkreihdwdcefgh4dqkjv67uzcmw7ojee6xedzdetojuzjevtenxquvyku", vid)
	repo.rows[goneKey].State = "unpinning"
	repo.rows[goneKey].NextAttemptAt = repo.rows[capKey].NextAttemptAt

	client.RepoSizeBytes = 1000 // exactly at the budget
	n, err := s.DrainDue(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if client.AddCount != 0 || repo.state(capKey) != "pending" {
		t.Fatalf("add ran over the ceiling: adds=%d state=%q", client.AddCount, repo.state(capKey))
	}
	if repo.state(goneKey) != "unpinned" || n != 1 {
		t.Fatalf("the unpin was gated by the disk ceiling: state=%q n=%d", repo.state(goneKey), n)
	}
	if out := logs.String(); !strings.Contains(out, "ipfs_admission_paused") || !strings.Contains(out, "reason=budget_exhausted") {
		t.Fatalf("no pause line for the F3 ceiling:\n%s", out)
	}
	if used, budget, ok := s.UnmanagedCapacity(ctx); !ok || used != 1000 || budget != 1000 {
		t.Fatalf("UnmanagedCapacity = %d %d %v", used, budget, ok)
	}

	// Space frees (an unpin + gc): the SAME row is pinned on the next pass, by itself.
	client.RepoSizeBytes = 400
	repo.rows[capKey].NextAttemptAt = time.Now().Add(-time.Second) // the claim lease has lapsed
	if n, err = s.DrainDue(ctx, 10); err != nil || n != 1 || repo.state(capKey) != "pinned" {
		t.Fatalf("did not resume under the ceiling: n=%d err=%v state=%q", n, err, repo.state(capKey))
	}
	if !strings.Contains(logs.String(), "ipfs_admission_resumed") {
		t.Fatalf("no resume line:\n%s", logs.String())
	}
}

// TestUnmanagedCeilingIsOffByDefaultAndFailsOpen: no budget, or a node that will
// not say how big it is, changes nothing.
func TestUnmanagedCeilingIsOffByDefaultAndFailsOpen(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name        string
		budget      int64
		noSizeProbe bool
	}{{"no budget", 0, false}, {"node cannot report a size", 1000, true}} {
		t.Run(tc.name, func(t *testing.T) {
			s, repo, client, _ := budgetService(t, tc.budget)
			vid := uuid.New()
			key := "captions/" + vid.String() + "/en.vtt"
			putBlob(t, s.blobs, key, "WEBVTT\n\nhi")
			seedPendingVideo(repo, key, string(ClassCaption), vid)
			client.RepoSizeBytes = 1 << 40
			if tc.noSizeProbe {
				// A client without the RepoSizer capability (or one whose stat fails) must
				// not block adds: a stat failure predicts nothing about the disk.
				s.client = struct{ ipfs.Client }{client}
			}
			if n, err := s.DrainDue(ctx, 10); err != nil || n != 1 || repo.state(key) != "pinned" {
				t.Fatalf("add was blocked: n=%d err=%v state=%q", n, err, repo.state(key))
			}
		})
	}
}

// TestOverBudgetUnpinsDoNotQueueBehindAPendingBacklog: with more pending rows than a
// batch, ordered ahead of the unpin, an over-budget tick must still reach the unpin
// in its FIRST pass, lease none of the pending rows, and probe the node once.
func TestOverBudgetUnpinsDoNotQueueBehindAPendingBacklog(t *testing.T) {
	ctx := context.Background()
	s, repo, client, _ := budgetService(t, 1000)
	vid := uuid.New()
	early := time.Now().Add(-time.Hour)
	var pendingKeys []string
	for i := 0; i < 5; i++ {
		k := fmt.Sprintf("captions/%s/p%d.vtt", vid, i)
		putBlob(t, s.blobs, k, "WEBVTT\n\nx")
		seedPendingVideo(repo, k, string(ClassCaption), vid)
		repo.rows[k].NextAttemptAt = early // sorts ahead of the unpin
		pendingKeys = append(pendingKeys, k)
	}
	goneKey := "captions/" + vid.String() + "/gone.vtt"
	seedPinned(repo, goneKey, string(ClassCaption), "bafkreihdwdcefgh4dqkjv67uzcmw7ojee6xedzdetojuzjevtenxquvyku", vid)
	repo.rows[goneKey].State = "unpinning"
	repo.rows[goneKey].NextAttemptAt = time.Now().Add(-time.Minute)

	client.RepoSizeBytes = 5000
	if _, err := s.DrainDue(ctx, 2); err != nil { // batch (2) < backlog (5)
		t.Fatal(err)
	}
	if repo.state(goneKey) != "unpinned" {
		t.Fatalf("the unpin waited behind the backlog: state=%q", repo.state(goneKey))
	}
	for _, k := range pendingKeys {
		if !repo.rows[k].NextAttemptAt.Equal(early) || repo.state(k) != "pending" {
			t.Fatalf("%s was claimed/leased while over budget (next=%v state=%s)", k, repo.rows[k].NextAttemptAt, repo.state(k))
		}
	}
	if client.AddCount != 0 || client.RepoSizeCalls != 1 {
		t.Fatalf("adds=%d size probes=%d, want 0 and exactly 1 per tick", client.AddCount, client.RepoSizeCalls)
	}
}
