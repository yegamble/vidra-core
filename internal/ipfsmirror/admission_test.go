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
