package ipfsmirror

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/vidra/vidra-core/internal/ipfs"
	"github.com/vidra/vidra-core/internal/media"
)

type erroringMasterLookups struct{ *fakeLookups }

func (erroringMasterLookups) VideoHLSMasterKey(context.Context, uuid.UUID) (string, bool, error) {
	return "", false, errors.New("lookup down")
}

func hlsMasterFixture(t *testing.T, lk Lookups, putMaster bool) (*Service, *fakeRepo, *ipfs.FakeIPFSClient, string) {
	t.Helper()
	repo, blobs, client := newFakeRepo(), newBlobs(t), ipfs.NewFakeIPFSClient()
	vid := uuid.New()
	gen := media.HLSPrefixForGeneration(vid, 1) + "/"
	if putMaster {
		putBlob(t, blobs, gen+"master.m3u8", "#EXTM3U\n")
	}
	putBlob(t, blobs, gen+"720p/playlist.m3u8", "#EXTM3U\n")
	putBlob(t, blobs, gen+"720p/seg_00000.ts", "seg")
	stable := media.HLSKeyPrefix(vid) + "/"
	seedPendingVideo(repo, stable, string(ClassHLS), vid)
	return New(repo, lk, blobs, client, testConfig()), repo, client, stable
}

// TestPinDirectoryFailsWhenTheMasterIsNotInTheServedTree: a tree without its master
// is not playable; pinning it would publish a CID whose /master.m3u8 is a 404.
func TestPinDirectoryFailsWhenTheMasterIsNotInTheServedTree(t *testing.T) {
	base := &fakeLookups{videoOK: true, videoPrivacy: "public", videoState: "published", userOK: true, userActive: true}
	// The lookup names a master that the store does not hold, while the other files
	// are served ones - so the tree is not merely empty.
	s, repo, client, stable := hlsMasterFixture(t, nil, false)
	gen := media.HLSPrefixForGeneration(uuid.UUID(repo.rows[stable].VideoID.Bytes), 1)
	base.hlsTree = gen
	s.lookups = &masterLookups{fakeLookups: base, master: gen + "/master.m3u8"}
	if n, err := s.DrainDue(context.Background(), 10); err != nil || n != 0 {
		t.Fatalf("DrainDue = %d, %v; want the pin to fail", n, err)
	}
	if repo.state(stable) == "pinned" || client.AddCount != 0 {
		t.Fatalf("a master-less tree was pinned: state=%q adds=%d", repo.state(stable), client.AddCount)
	}
	if repo.rows[stable].LastError == "" {
		t.Fatal("the failure was not recorded on the row")
	}
}

// TestPinDirectoryFailsOnAMasterLookupError: guessing master.m3u8 after a failed
// read would apply the wrong definition of "served".
func TestPinDirectoryFailsOnAMasterLookupError(t *testing.T) {
	base := &fakeLookups{videoOK: true, videoPrivacy: "public", videoState: "published", userOK: true, userActive: true}
	s, repo, client, stable := hlsMasterFixture(t, nil, true)
	base.hlsTree = media.HLSPrefixForGeneration(uuid.UUID(repo.rows[stable].VideoID.Bytes), 1)
	s.lookups = erroringMasterLookups{base}
	if n, err := s.DrainDue(context.Background(), 10); err != nil || n != 0 {
		t.Fatalf("DrainDue = %d, %v; want the pin to fail", n, err)
	}
	if repo.state(stable) == "pinned" || client.AddCount != 0 || repo.rows[stable].LastError == "" {
		t.Fatalf("pinned or unrecorded on a lookup error: state=%q adds=%d err=%q", repo.state(stable), client.AddCount, repo.rows[stable].LastError)
	}
}
