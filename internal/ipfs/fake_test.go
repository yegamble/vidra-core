package ipfs

import (
	"context"
	"strings"
	"testing"
)

func TestFakeAddPinLifecycle(t *testing.T) {
	ctx := context.Background()
	f := NewFakeIPFSClient()

	res, err := f.Add(ctx, "avatar.png", strings.NewReader("avatar-bytes"))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := ValidateCID(res.CID); err != nil {
		t.Fatalf("Add returned an invalid CID %q: %v", res.CID, err)
	}
	if res.Size != int64(len("avatar-bytes")) {
		t.Errorf("Size = %d, want %d", res.Size, len("avatar-bytes"))
	}
	if pinned, _ := f.IsPinned(ctx, res.CID); !pinned {
		t.Error("Add did not pin the object")
	}
	if _, ok := f.Content(res.CID); !ok {
		t.Error("content not stored")
	}

	// Unpin then re-check.
	if err := f.Unpin(ctx, res.CID); err != nil {
		t.Fatalf("Unpin: %v", err)
	}
	if pinned, _ := f.IsPinned(ctx, res.CID); pinned {
		t.Error("object still pinned after Unpin")
	}
	// Re-pin is idempotent.
	if err := f.Pin(ctx, res.CID); err != nil {
		t.Fatalf("Pin: %v", err)
	}
	if pinned, _ := f.IsPinned(ctx, res.CID); !pinned {
		t.Error("object not pinned after Pin")
	}
}

// TestFakeAddContentAddressed proves identical bytes dedupe to one CID and
// different bytes produce different CIDs.
func TestFakeAddContentAddressed(t *testing.T) {
	ctx := context.Background()
	f := NewFakeIPFSClient()
	a, _ := f.Add(ctx, "x", strings.NewReader("same"))
	b, _ := f.Add(ctx, "y", strings.NewReader("same"))
	c, _ := f.Add(ctx, "z", strings.NewReader("different"))
	if a.CID != b.CID {
		t.Errorf("identical bytes gave different CIDs: %q vs %q", a.CID, b.CID)
	}
	if a.CID == c.CID {
		t.Errorf("different bytes gave the same CID: %q", a.CID)
	}
}

func TestFakeAddDirectory(t *testing.T) {
	ctx := context.Background()
	f := NewFakeIPFSClient()
	entries := []DirEntry{
		{Path: "master.m3u8", Data: strings.NewReader("#EXTM3U")},
		{Path: "720p/playlist.m3u8", Data: strings.NewReader("#EXTM3U 720")},
		{Path: "720p/seg_00001.ts", Data: strings.NewReader("...ts...")},
	}
	res, err := f.AddDirectory(ctx, entries)
	if err != nil {
		t.Fatalf("AddDirectory: %v", err)
	}
	if err := ValidateCID(res.CID); err != nil {
		t.Fatalf("AddDirectory root %q invalid: %v", res.CID, err)
	}
	if pinned, _ := f.IsPinned(ctx, res.CID); !pinned {
		t.Error("directory root not pinned")
	}

	// Same tree ⇒ same root (order-independent).
	f2 := NewFakeIPFSClient()
	res2, _ := f2.AddDirectory(ctx, []DirEntry{
		{Path: "720p/seg_00001.ts", Data: strings.NewReader("...ts...")},
		{Path: "master.m3u8", Data: strings.NewReader("#EXTM3U")},
		{Path: "720p/playlist.m3u8", Data: strings.NewReader("#EXTM3U 720")},
	})
	if res.CID != res2.CID {
		t.Errorf("same tree, different root CID: %q vs %q", res.CID, res2.CID)
	}
}

// TestFakeNodeDown proves the node-unreachable simulation: every RPC fails while
// Down, and recovery restores service (the outage-then-backfill path).
func TestFakeNodeDown(t *testing.T) {
	ctx := context.Background()
	f := NewFakeIPFSClient()
	f.Down = true

	if _, err := f.Version(ctx); err == nil {
		t.Error("Version succeeded while down")
	}
	if _, err := f.Add(ctx, "x", strings.NewReader("data")); err == nil {
		t.Error("Add succeeded while down")
	}
	cid := RawLeafCIDv1([]byte("data"))
	if err := f.Pin(ctx, cid); err == nil {
		t.Error("Pin succeeded while down")
	}
	if err := f.Unpin(ctx, cid); err == nil {
		t.Error("Unpin succeeded while down")
	}

	// Recover.
	f.Down = false
	if v, err := f.Version(ctx); err != nil || v == "" {
		t.Errorf("Version after recovery = %q, %v", v, err)
	}
	if _, err := f.Add(ctx, "x", strings.NewReader("data")); err != nil {
		t.Errorf("Add after recovery: %v", err)
	}
}

// TestFakePinRejectsInvalidCID guards that even the fake enforces the CID contract
// on Pin/Unpin.
func TestFakePinRejectsInvalidCID(t *testing.T) {
	ctx := context.Background()
	f := NewFakeIPFSClient()
	if err := f.Pin(ctx, "QmNotACIDv1"); err == nil {
		t.Error("Pin accepted a CIDv0")
	}
	if err := f.Unpin(ctx, "../etc/passwd"); err == nil {
		t.Error("Unpin accepted a traversal string")
	}
}
