package ipfsmirror

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/vidra/vidra-core/internal/ipfs"
	"github.com/vidra/vidra-core/internal/ipfscontrol"
	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

type gatewayControlRepo struct {
	ipfscontrol.Repository
	active bool
	err    error
}

func (r gatewayControlRepo) GetIPFSControlConfig(context.Context) (sqlcgen.IpfsControlConfig, error) {
	b, _ := json.Marshal(ipfscontrol.Config{Provider: "internal", BudgetBytes: 20 << 30, MinFreeBytes: 20 << 30, CopyBytesPerSecond: 2 << 20, Workers: 1})
	return sqlcgen.IpfsControlConfig{Config: b, Revision: 1, PolicyActive: r.active}, r.err
}

func TestPublicGatewayLegacyHLSBeforePolicyAdoption(t *testing.T) {
	id := uuid.New()
	cid := ipfs.RawLeafCIDv1([]byte("legacy HLS"))
	master := "streaming-playlists/hls/imported/hash-master.m3u8"
	for _, tc := range []struct {
		name    string
		control *gatewayControlRepo
		change  func(*sqlcgen.MediaIpfsPin, *masterLookups)
		want    bool
	}{
		{name: "legacy without control", want: true},
		{name: "policy not adopted", control: &gatewayControlRepo{}, want: true},
		{name: "adopted policy", control: &gatewayControlRepo{active: true}},
		{name: "unavailable policy", control: &gatewayControlRepo{err: errors.New("unavailable")}},
		{name: "managed intent", change: func(r *sqlcgen.MediaIpfsPin, _ *masterLookups) { r.PolicyReason = "demand" }},
		{name: "superseded generation", change: func(r *sqlcgen.MediaIpfsPin, _ *masterLookups) { r.CommittedGeneration = "old/master.m3u8" }},
		{name: "wrong root", change: func(r *sqlcgen.MediaIpfsPin, _ *masterLookups) { r.CarRoot = "different" }},
		{name: "wrong key", change: func(r *sqlcgen.MediaIpfsPin, _ *masterLookups) { r.ObjectKey = "different/" }},
		{name: "no current master", change: func(_ *sqlcgen.MediaIpfsPin, l *masterLookups) { l.master = "" }},
		{name: "private", change: func(_ *sqlcgen.MediaIpfsPin, l *masterLookups) { l.videoPrivacy = "private" }},
		{name: "current managed generation", control: &gatewayControlRepo{active: true}, change: func(r *sqlcgen.MediaIpfsPin, _ *masterLookups) { r.CommittedGeneration = master }, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := sqlcgen.MediaIpfsPin{ObjectKey: media.HLSKeyPrefix(id) + "/", Cid: cid, CarRoot: cid, State: "pinned", Network: "public", MediaClass: string(ClassHLS), VideoID: pgUUID(id), PolicyReason: "legacy"}
			l := &masterLookups{fakeLookups: &fakeLookups{videoOK: true, videoPrivacy: "public", videoState: "published", userOK: true, userActive: true}, master: master}
			if tc.change != nil {
				tc.change(&row, l)
			}
			r := &gatewayRepo{fakeRepo: newFakeRepo(), roots: []sqlcgen.MediaIpfsPin{row}}
			r.rows[row.ObjectKey] = &row
			s := New(r, l, newBlobs(t), ipfs.NewFakeIPFSClient(), testConfig())
			if tc.control != nil {
				s.ConfigureControl(ipfscontrol.NewService(tc.control, nil, ipfscontrol.Config{}))
			}
			got, err := s.PublicGatewayRootAllowed(context.Background(), cid)
			if got != tc.want {
				t.Fatalf("allowed=%v error=%v want=%v", got, err, tc.want)
			}
			if row.CommittedGeneration == "" {
				if _, allowed, _ := s.PublicPlaybackHLS(context.Background(), id, master); allowed {
					t.Fatal("legacy exception must not enable automatic IPFS preference")
				}
			}
		})
	}
}

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
