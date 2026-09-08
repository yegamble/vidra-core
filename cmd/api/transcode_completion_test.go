package main

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// TestComposeTranscodeCompletionOrder pins the load-bearing order of the
// transcode completion hook: the publish-after-transcode hold release MUST run
// before the IPFS mirror sync. The mirror routes pin eligibility on committed
// state (non-published → NetworkNone), so a mirror sync against a still-held
// video would skip the HLS-tree pin permanently — the later publish-hook
// SyncVideo only re-pins single-file refs, never the 'hls' directory row.
//
// The federation Update comes LAST for the same class of reason (A29
// remediation): the outbound AS Video advertises a playable HLS master only
// when a ready ladder exists, and UpdateVideo reads the video's CURRENT
// privacy/state — so it must not run before the hold that keeps the video
// unpublished has been released, or the video is either skipped or actively
// unfederated.
func TestComposeTranscodeCompletionOrder(t *testing.T) {
	var calls []string
	hook := composeTranscodeCompletion(
		func(context.Context, uuid.UUID) { calls = append(calls, "release") },
		func(context.Context, uuid.UUID) { calls = append(calls, "mirror") },
		func(context.Context, uuid.UUID) { calls = append(calls, "federate") },
	)
	hook(context.Background(), uuid.New())
	want := []string{"release", "mirror", "federate"}
	if len(calls) != len(want) {
		t.Fatalf("completion hook calls = %v, want %v", calls, want)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Fatalf("completion hook order = %v, want %v", calls, want)
		}
	}

	// Nil components are tolerated (e.g. a build with no mirror and no
	// federation wired).
	composeTranscodeCompletion(nil, nil, nil)(context.Background(), uuid.New())
}
