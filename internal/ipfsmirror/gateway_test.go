package ipfsmirror

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/vidra/vidra-core/internal/ipfs"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

type gatewayRepo struct {
	*fakeRepo
	roots []sqlcgen.MediaIpfsPin
	err   error
}

func (r *gatewayRepo) ListPublicIPFSRootPins(context.Context, string) ([]sqlcgen.MediaIpfsPin, error) {
	return r.roots, r.err
}

func TestPublicGatewayCurrentEligibility(t *testing.T) {
	cid := ipfs.RawLeafCIDv1([]byte("public gateway"))
	row := sqlcgen.MediaIpfsPin{ObjectKey: "web-videos/current.mp4", Cid: cid, State: "pinned", Network: "public", MediaClass: string(ClassVideoOriginal), VideoID: pgUUID(uuid.New())}
	for _, tc := range []struct {
		name   string
		change func(*gatewayRepo, *fakeLookups)
		want   bool
	}{
		{"public", func(*gatewayRepo, *fakeLookups) {}, true},
		{"withdrawn", func(r *gatewayRepo, l *fakeLookups) { l.videoPrivacy = "private" }, false},
		{"unlisted", func(r *gatewayRepo, l *fakeLookups) { l.userUnlisted = true }, false},
		{"blocked", func(r *gatewayRepo, l *fakeLookups) { l.videoBlocked = true }, false},
		{"quarantined", func(r *gatewayRepo, l *fakeLookups) { l.videoState = "quarantined" }, false},
		{"deleted video", func(r *gatewayRepo, l *fakeLookups) { l.videoOK = false }, false},
		{"deleted owner", func(r *gatewayRepo, l *fakeLookups) { l.userOK = false }, false},
		{"inactive owner", func(r *gatewayRepo, l *fakeLookups) { l.userActive = false }, false},
		{"unpin pending", func(r *gatewayRepo, l *fakeLookups) { r.roots[0].State = "unpinning" }, false},
		{"private network", func(r *gatewayRepo, l *fakeLookups) { r.roots[0].Network = "private" }, false},
		{"unknown class", func(r *gatewayRepo, l *fakeLookups) { r.roots[0].MediaClass = "dm_attachment" }, false},
		{"replaced source", func(r *gatewayRepo, l *fakeLookups) { l.videoFiles = nil }, false},
		{"unknown root", func(r *gatewayRepo, l *fakeLookups) { r.roots = nil }, false},
		{"wrong root", func(r *gatewayRepo, l *fakeLookups) { r.roots[0].Cid = ipfs.RawLeafCIDv1([]byte("other")) }, false},
		{"shared root", func(r *gatewayRepo, l *fakeLookups) {
			r.roots = append([]sqlcgen.MediaIpfsPin{{ObjectKey: "web-videos/current.mp4", Cid: cid, State: "unpinning"}}, r.roots...)
		}, true},
		{"lookup failure", func(r *gatewayRepo, l *fakeLookups) { r.err = errors.New("db unavailable") }, false},
		{"excess references", func(r *gatewayRepo, l *fakeLookups) { r.roots = make([]sqlcgen.MediaIpfsPin, 129); r.roots[0] = row }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &gatewayRepo{fakeRepo: newFakeRepo(), roots: []sqlcgen.MediaIpfsPin{row}}
			l := &fakeLookups{videoPrivacy: "public", videoState: "published", videoOK: true, userOK: true, userActive: true, videoFiles: []VideoFileRef{{Kind: "original", StorageKey: row.ObjectKey}}}
			tc.change(r, l)
			s := New(r, l, newBlobs(t), ipfs.NewFakeIPFSClient(), testConfig())
			got, err := s.PublicGatewayRootAllowed(context.Background(), cid)
			if got != tc.want {
				t.Fatalf("allowed=%v err=%v want=%v", got, err, tc.want)
			}
		})
	}
}

func TestPublicGatewayIdentityAndCoverProvenance(t *testing.T) {
	id := uuid.New()
	cid := ipfs.RawLeafCIDv1([]byte("identity"))
	for _, class := range []MediaClass{ClassUserAvatar, ClassChannelBanner, ClassPlaylistCover} {
		t.Run(string(class), func(t *testing.T) {
			key := "avatars/current.png"
			if class == ClassPlaylistCover {
				key = "playlist-thumbnails/" + id.String() + ".jpg"
			}
			row := sqlcgen.MediaIpfsPin{ObjectKey: key, MediaClass: string(class), Cid: cid, State: "pinned", Network: "public", OwnerUserID: pgUUID(id)}
			r := &gatewayRepo{fakeRepo: newFakeRepo(), roots: []sqlcgen.MediaIpfsPin{row}}
			l := &fakeLookups{userOK: true, userActive: true, ownerImages: []ImageRef{{Class: class, ObjectKey: key}}, playlistHasCover: true, playlistVis: "public", playlistKey: key}
			s := New(r, l, newBlobs(t), ipfs.NewFakeIPFSClient(), testConfig())
			if ok, err := s.PublicGatewayRootAllowed(context.Background(), cid); !ok || err != nil {
				t.Fatalf("current public reference denied: %v", err)
			}
			l.userUnlisted = true
			l.playlistVis = "private"
			if ok, _ := s.PublicGatewayRootAllowed(context.Background(), cid); ok {
				t.Fatal("withdrawn reference allowed")
			}
			l.userUnlisted = false
			l.playlistVis = "public"
			l.ownerImages = nil
			l.playlistKey = "replaced.jpg"
			if ok, _ := s.PublicGatewayRootAllowed(context.Background(), cid); ok {
				t.Fatal("replaced reference allowed")
			}
		})
	}
}
