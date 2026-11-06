//go:build integration

package peertubeimport

import (
	"context"
	"os"
	"testing"
	"time"
)

// HLSPlaylist carries the ladder's recorded size so the copy budget can follow it.
func TestSourceHLSPlaylistRecordsLadderBytes(t *testing.T) {
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	src, _ := newScratchDB(t, ctx, base)
	seedPeerTube(t, ctx, src, "fixture-hash", secretPrivKeyAlice)
	hls, ok, err := NewSourceFromPool(src).HLSPlaylist(ctx, 1)
	if err != nil || !ok {
		t.Fatalf("HLSPlaylist(1) = %v, %v", ok, err)
	}
	if want := int64(4096 + 2048 + 64); hls.Bytes != want {
		t.Fatalf("Bytes = %d, want %d (the fixture ladder's videoFile sizes)", hls.Bytes, want)
	}
}
