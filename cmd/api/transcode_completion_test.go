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
func TestComposeTranscodeCompletionOrder(t *testing.T) {
	var calls []string
	hook := composeTranscodeCompletion(
		func(context.Context, uuid.UUID) { calls = append(calls, "release") },
		func(context.Context, uuid.UUID) { calls = append(calls, "mirror") },
	)
	hook(context.Background(), uuid.New())
	if len(calls) != 2 || calls[0] != "release" || calls[1] != "mirror" {
		t.Fatalf("completion hook order = %v, want [release mirror] (mirror eligibility reads committed state)", calls)
	}

	// Nil components are tolerated (e.g. a build with no mirror wired).
	composeTranscodeCompletion(nil, nil)(context.Background(), uuid.New())
}
