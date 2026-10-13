package ipfsmirror

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/vidra/vidra-core/internal/ipfs"
	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func TestAdmissionInventoryOnlyCurrentGeneration(t *testing.T) {
	id := uuid.New()
	prefix := "streaming-playlists/hls/imported/"
	l := &masterLookups{fakeLookups: &fakeLookups{videoOK: true, videoPrivacy: "public", videoState: "published", userOK: true, userActive: true, hlsTree: prefix}, master: prefix + "source-master.m3u8"}
	b := newBlobs(t)
	putBlob(t, b, prefix+"source-master.m3u8", "master")
	putBlob(t, b, prefix+"720/seg.ts", "segment")
	putBlob(t, b, prefix+media.VP9WebMFilename, "alternate")
	putBlob(t, b, "streaming-playlists/other/master.m3u8", "other")
	s := New(newFakeRepo(), l, b, ipfs.NewFakeIPFSClient(), testConfig())
	row := sqlcgen.MediaIpfsPin{ObjectKey: media.HLSKeyPrefix(id) + "/", MediaClass: "hls", VideoID: pgUUID(id), Network: "public"}
	files, generation, reserved, err := s.admissionInventory(context.Background(), row)
	if err != nil || generation != l.master || len(files) != 2 || files[prefix+"720/seg.ts"] != 7 || reserved <= 13 {
		t.Fatalf("inventory %+v %q %d %v", files, generation, reserved, err)
	}
	l.videoPrivacy = "private"
	if _, _, _, err = s.admissionInventory(context.Background(), row); err == nil {
		t.Fatal("private source admitted")
	}
	l.videoPrivacy = "public"
	l.master = ""
	if _, _, _, err = s.admissionInventory(context.Background(), row); err == nil {
		t.Fatal("unready generation admitted")
	}
}

// TestReservationBytesIsTheMeasuredFactor pins F1: the reservation is the payload
// plus ~2% plus the per-file and fixed terms — not 2x. The first case is the
// shape that mattered: 1 GiB of segments in 200 files used to reserve ~2.003 GiB.
func TestReservationBytesIsTheMeasuredFactor(t *testing.T) {
	const gib = int64(1) << 30
	got := reservationBytes(gib, 200)
	want := gib + gib/50 + 200*16384 + 1<<20
	if got != want {
		t.Fatalf("reservationBytes = %d, want %d", got, want)
	}
	if got >= gib*21/20 {
		t.Fatalf("reserved %d for %d payload bytes: more than 5%% over, the 2x model is back", got, gib)
	}
	// Still covers the worst overhead ever measured (spike T5, 1.0137x).
	if got < gib+gib*137/10000 {
		t.Fatalf("reservation %d is below the measured worst-case growth", got)
	}
	// The cap the caller enforces (1<<50 payload) must not overflow.
	if r := reservationBytes(1<<50, 100000); r <= 1<<50 {
		t.Fatalf("overflow: %d", r)
	}
}
