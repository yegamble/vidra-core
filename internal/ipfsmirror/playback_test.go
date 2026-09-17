package ipfsmirror

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/vidra/vidra-core/internal/ipfs"
	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

type masterLookups struct {
	*fakeLookups
	master string
}

func (l *masterLookups) VideoHLSMasterKey(context.Context, uuid.UUID) (string, bool, error) {
	return l.master, l.master != "", nil
}

func TestPublicPlaybackHLSFencesGenerationAndImportedBasename(t *testing.T) {
	ctx := context.Background()
	id := uuid.New()
	cid := ipfs.RawLeafCIDv1([]byte("hls"))
	master := "streaming-playlists/hls/source-id/hash-master.m3u8"
	r := newFakeRepo()
	key := media.HLSKeyPrefix(id) + "/"
	_, err := r.UpsertIPFSPinIntent(ctx, sqlcgen.UpsertIPFSPinIntentParams{ObjectKey: key, MediaClass: string(ClassHLS), VideoID: pgUUID(id)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.MarkIPFSPinned(ctx, sqlcgen.MarkIPFSPinnedParams{ObjectKey: key, Cid: cid, CarRoot: cid})
	if err != nil {
		t.Fatal(err)
	}
	l := &masterLookups{fakeLookups: &fakeLookups{videoOK: true, videoPrivacy: "public", videoState: "published", userOK: true, userActive: true, hlsTree: "streaming-playlists/hls/source-id/"}, master: master}
	s := New(r, l, newBlobs(t), ipfs.NewFakeIPFSClient(), testConfig())
	if _, ok, _ := s.PublicPlaybackHLS(ctx, id, master); ok {
		t.Fatal("unknown generation published")
	}
	row := r.rows[key]
	row.CommittedGeneration = master
	r.rows[key] = row
	url, ok, err := s.PublicPlaybackHLS(ctx, id, master)
	if err != nil || !ok || url != s.gatewayURL+"/ipfs/"+cid+"/hash-master.m3u8" {
		t.Fatalf("imported playback: %q %v %v", url, ok, err)
	}
	l.master = "streaming-playlists/changed/r2/master.m3u8"
	if _, ok, _ := s.PublicPlaybackHLS(ctx, id, master); ok {
		t.Fatal("superseded generation published")
	}
	l.master = master
	l.videoPrivacy = "private"
	if _, ok, _ := s.PublicPlaybackHLS(ctx, id, master); ok {
		t.Fatal("private playback published")
	}
}
