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
	putBlob(t, b, prefix+"source-720-fragmented.mp4", "segment")
	putBlob(t, b, prefix+media.VP9WebMFilename, "alternate")
	putBlob(t, b, prefix+"nested/seg.ts", "not-served-by-the-flat-peertube-route")
	putBlob(t, b, "streaming-playlists/other/master.m3u8", "other")
	s := New(newFakeRepo(), l, b, ipfs.NewFakeIPFSClient(), testConfig())
	row := sqlcgen.MediaIpfsPin{ObjectKey: media.HLSKeyPrefix(id) + "/", MediaClass: "hls", VideoID: pgUUID(id), Network: "public"}
	files, generation, reserved, err := s.admissionInventory(context.Background(), row)
	if err != nil || generation != l.master || len(files) != 2 || files[prefix+"source-720-fragmented.mp4"] != 7 || reserved <= 13 {
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

// TestAdmissionInventoryReservesOnlyServedFiles: the reservation is sized from
// the same set pinDirectory adds (F6), so a native tree's downloads are neither
// reserved for nor copied.
func TestAdmissionInventoryReservesOnlyServedFiles(t *testing.T) {
	id := uuid.New()
	gen := media.HLSPrefixForGeneration(id, 1) + "/"
	l := &masterLookups{fakeLookups: &fakeLookups{videoOK: true, videoPrivacy: "public", videoState: "published", userOK: true, userActive: true, hlsTree: gen}, master: gen + "master.m3u8"}
	b := newBlobs(t)
	putBlob(t, b, gen+"master.m3u8", "master")
	putBlob(t, b, gen+"720p/playlist.m3u8", "playlist")
	putBlob(t, b, gen+"720p/seg_00000.ts", "segment")
	putBlob(t, b, gen+"720p/"+media.HLSMuxedDownloadFilename, "muxed-download-mp4")
	putBlob(t, b, gen+"720p/"+media.HLSVideoOnlyDownloadFilename, "video-only-download-mp4")
	putBlob(t, b, gen+media.HLSAudioDownloadFilename, "audio-download")
	putBlob(t, b, gen+media.VP9WebMFilename, "alternate")
	s := New(newFakeRepo(), l, b, ipfs.NewFakeIPFSClient(), testConfig())
	row := sqlcgen.MediaIpfsPin{ObjectKey: media.HLSKeyPrefix(id) + "/", MediaClass: "hls", VideoID: pgUUID(id), Network: "public"}
	files, _, _, err := s.admissionInventory(context.Background(), row)
	if err != nil || len(files) != 3 {
		t.Fatalf("inventory %+v %v, want master + playlist + segment only", files, err)
	}
	for key := range files {
		if !hlsServed(gen, l.master, key) {
			t.Errorf("reserved for %s, which the HLS routes do not serve", key)
		}
	}
}
